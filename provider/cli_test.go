package provider

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/apitest"
	"github.com/pulumi/providertest/providers"
	"github.com/pulumi/providertest/pulumitest"
	"github.com/pulumi/providertest/pulumitest/opttest"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// These tests run the provider the way users meet it: the Pulumi CLI runs a YAML program against
// the plugin's own gRPC server (attached in process), and the server talks to the binding-driven
// mock API. They see what the gRPC-level tests cannot: the engine's replace order, the preview's
// messages, and whether `pulumi up` followed by `pulumi preview` is empty.
//
// The programs are written from the generator's smoke fixtures (the same ones the Terraform
// acceptance suite and the idempotency matrix use), so a new resource is covered with no edit here.
//
// They need the `pulumi` CLI and its YAML language plugin. Without them the tests skip, unless
// PULUMI_CLI_TESTS=required (CI), which turns a missing CLI into a failure.

var updatePreviewMessages = flag.Bool("update-preview-messages", false,
	"rewrite testdata/cli/preview_messages.txt from this run")

// cliKnownGaps lists resources whose CLI run still fails, each with the reason. A listed resource
// that fails logs the gap and passes; one that starts passing fails, so the list only shrinks on
// purpose.
var cliKnownGaps = map[string]string{}

func requirePulumiCLI(t *testing.T) {
	t.Helper()
	if home, err := os.UserHomeDir(); err == nil {
		bin := filepath.Join(home, ".pulumi", "bin")
		// os.Setenv, not t.Setenv: the lifecycle subtests run in parallel, which t.Setenv forbids.
		if _, err := os.Stat(bin); err == nil && !strings.Contains(os.Getenv("PATH"), bin) {
			_ = os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}
	for _, tool := range []string{"pulumi", "pulumi-language-yaml"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("PULUMI_CLI_TESTS") == "required" {
				t.Fatalf("%s is not on PATH and PULUMI_CLI_TESTS=required", tool)
			}
			t.Skipf("%s is not on PATH; set PULUMI_CLI_TESTS=required to fail instead", tool)
		}
	}
}

// cliStack is one Pulumi project bound to its own mock API and its own in-process provider server.
type cliStack struct {
	t    *testing.T
	pt   *pulumitest.PulumiTest
	mock *apitest.MockAPI
	name string
}

func newCLIStack(t *testing.T, name string, mock *apitest.MockAPI, resources map[string]cliResource) *cliStack {
	t.Helper()
	return newCLIStackWith(t, name, mock, resources, thisBuild(t))
}

// thisBuild attaches the provider built from this tree, served in process.
func thisBuild(t *testing.T) opttest.Option {
	meta := providerMetadata(t)
	return opttest.AttachProviderServer("betterstack", func(pt providers.PulumiTest) (pulumirpc.ResourceProviderServer, error) {
		return NewServer(context.Background(), nil, meta)
	})
}

func newCLIStackWith(t *testing.T, name string, mock *apitest.MockAPI, resources map[string]cliResource, provider opttest.Option) *cliStack {
	t.Helper()
	return newCLIStackFromProgram(t, name, mock, cliProgram(name, resources), provider)
}

// newCLIStackFromProgram starts a stack from a complete Pulumi YAML program.
func newCLIStackFromProgram(t *testing.T, name string, mock *apitest.MockAPI, program string, provider opttest.Option) *cliStack {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Pulumi.yaml"), []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	pt := pulumitest.NewPulumiTest(t, dir, opttest.SkipInstall(), opttest.DisableGrpcLog(), provider)
	pt.SetConfig(t, "betterstack:apiToken", "test-token")
	pt.SetConfig(t, "betterstack:endpoint", mock.URL())
	pt.SetConfig(t, "betterstack:apiRateLimit", "0")
	return &cliStack{t: t, pt: pt, mock: mock, name: name}
}

// cliResource is one resource of a program: its Pulumi type token and its Pulumi-named inputs.
type cliResource struct {
	Type       string
	Properties map[string]interface{}
}

// cliProgram renders a Pulumi YAML program. Property values are written as JSON, which YAML reads
// verbatim, so strings that look like numbers or booleans keep their type.
func cliProgram(name string, resources map[string]cliResource) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\nruntime: yaml\nresources:\n", name)
	keys := make([]string, 0, len(resources))
	for key := range resources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		res := resources[key]
		props, err := json.Marshal(res.Properties)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(&b, "  %s:\n    type: %s\n    properties: %s\n", key, res.Type, props)
	}
	return b.String()
}

func (s *cliStack) write(resources map[string]cliResource) {
	s.t.Helper()
	s.pt.WritePulumiYaml(s.t, cliProgram(s.name, resources))
}

// up applies the program and returns the requests it caused and the diagnostics it printed.
func (s *cliStack) up() ([]mockRequest, []string, error) {
	s.t.Helper()
	seen := len(s.mock.Requests())
	collect, done := diagnostics()
	_, err := s.pt.UpErr(s.t, optup.EventStreams(collect))
	messages := done()
	return parseRequests(s.mock.Requests()[seen:]), messages, err
}

// mockRequest is one request the mock logged, split from its "METHOD /path?query" form.
type mockRequest struct {
	Method string
	Path   string
	Query  string
}

func parseRequests(log []string) []mockRequest {
	out := make([]mockRequest, 0, len(log))
	for _, line := range log {
		method, uri, _ := strings.Cut(line, " ")
		path, query, _ := strings.Cut(uri, "?")
		out = append(out, mockRequest{Method: method, Path: path, Query: query})
	}
	return out
}

// preview previews the program and returns the diagnostics it printed.
func (s *cliStack) preview() (map[string]int, []string, string, error) {
	s.t.Helper()
	collect, done := diagnostics()
	result, err := s.pt.PreviewErr(s.t, optpreview.EventStreams(collect))
	messages := done()
	if err != nil {
		return nil, messages, result.StdOut, err
	}
	return opCounts(result.ChangeSummary), messages, result.StdOut, nil
}

// expectEmptyPreview requires a preview that changes nothing.
func (s *cliStack) expectEmptyPreview(step string) error {
	s.t.Helper()
	ops, messages, out, err := s.preview()
	if err != nil {
		return fmt.Errorf("preview after %s: %w\n%s", step, err, out)
	}
	for op, n := range ops {
		if op != "same" && n > 0 {
			return fmt.Errorf("preview after %s is not empty: %v\n%s", step, ops, out)
		}
	}
	if len(messages) > 0 {
		return fmt.Errorf("preview after %s printed messages:\n%s", step, strings.Join(messages, "\n"))
	}
	return nil
}

func opCounts[T ~string](summary map[T]int) map[string]int {
	out := make(map[string]int, len(summary))
	for op, n := range summary {
		out[string(op)] = n
	}
	return out
}

// diagnostics collects the engine's warning and error messages from an event stream, URNs
// stripped so the text is stable across runs.
func diagnostics() (chan<- events.EngineEvent, func() []string) {
	ch := make(chan events.EngineEvent)
	var (
		mu       sync.Mutex
		messages []string
		wg       sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range ch {
			d := ev.DiagnosticEvent
			if d == nil || (d.Severity != "warning" && d.Severity != "error") {
				continue
			}
			mu.Lock()
			messages = append(messages, d.Severity+": "+normalizeMessage(d.Message))
			mu.Unlock()
		}
	}()
	return ch, func() []string {
		wg.Wait()
		mu.Lock()
		defer mu.Unlock()
		return dedupe(messages)
	}
}

var (
	ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m|<\{%[a-z]+%\}>`)
	urnPrefix  = regexp.MustCompile(`urn:pulumi:[^\s]+`)
)

func normalizeMessage(message string) string {
	message = ansiEscape.ReplaceAllString(message, "")
	message = urnPrefix.ReplaceAllString(message, "<urn>")
	return strings.Join(strings.Fields(message), " ")
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// TestCLILifecycle runs, per managed resource: `pulumi up` from the create fixture and an empty
// preview; the update fixture, `pulumi up` and an empty preview; the replace fixture's preview
// (its messages are pinned in testdata/cli/preview_messages.txt), `pulumi up` with the order of
// the create and delete requests checked, and an empty preview; then `pulumi destroy`.
func TestCLILifecycle(t *testing.T) {
	requirePulumiCLI(t)
	info := Provider()
	fixtures := apitest.Fixtures()
	if got, want := len(fixtures), info.P.ResourcesMap().Len(); got != want {
		t.Fatalf("apitest ships %d fixtures but the provider manages %d resources", got, want)
	}

	var (
		mu       sync.Mutex
		previews = map[string][]string{}
	)
	t.Run("resources", func(t *testing.T) {
		for _, fx := range fixtures {
			t.Run(fx.Resource, func(t *testing.T) {
				t.Parallel()
				messages, err := runCLILifecycle(t, info.Resources[fx.TFType], fx)
				mu.Lock()
				previews[fx.Resource] = messages
				mu.Unlock()
				reason, known := cliKnownGaps[fx.Resource]
				switch {
				case err != nil && known:
					t.Logf("known gap: %s\n%v", reason, err)
				case err != nil:
					t.Fatal(err)
				case known:
					t.Fatalf("%s now passes, remove it from cliKnownGaps", fx.Resource)
				}
			})
		}
	})
	// The pinned text covers every resource, so a filtered run (-run .../monitor) cannot check it.
	if len(previews) != len(fixtures) || (t.Failed() && !*updatePreviewMessages) {
		return
	}
	checkGolden(t, filepath.Join("testdata", "cli", "preview_messages.txt"), renderPreviews(previews))
}

func runCLILifecycle(t *testing.T, res *tfbridge.ResourceInfo, fx apitest.SmokeFixture) ([]string, error) {
	if res == nil {
		return nil, fmt.Errorf("%s is not bridged", fx.TFType)
	}
	info := Provider()
	tfSchema := info.P.ResourcesMap().Get(fx.TFType).Schema()
	inputs := func(attrs map[string]interface{}) (map[string]cliResource, error) {
		props, err := pulumiInputs(fx.Resource, attrs, tfSchema, res.Fields)
		if err != nil {
			return nil, err
		}
		return map[string]cliResource{"subject": {Type: res.Tok.String(), Properties: props}}, nil
	}

	mock := apitest.NewMockAPI()
	t.Cleanup(mock.Close)
	if fx.Seed != nil {
		if err := mock.Seed(fx.Seed.Resource, fx.Seed.Vars, fx.Seed.ID); err != nil {
			return nil, err
		}
	}
	mock.InjectItemFields(fx.Resource, fx.ComputedItemFields)
	mock.InjectOnCreate(fx.Resource, fx.Settle)

	create, err := inputs(fx.Create)
	if err != nil {
		return nil, err
	}
	stack := newCLIStack(t, "cli-"+strings.ReplaceAll(fx.Resource, "_", "-"), mock, create)
	if _, messages, err := stack.up(); err != nil {
		return nil, fmt.Errorf("up (create): %w\n%s", err, strings.Join(messages, "\n"))
	}
	if err := stack.expectEmptyPreview("create"); err != nil {
		return nil, err
	}

	if fx.Update != nil {
		update, err := inputs(fx.Update)
		if err != nil {
			return nil, err
		}
		stack.write(update)
		if _, messages, err := stack.up(); err != nil {
			return nil, fmt.Errorf("up (update): %w\n%s", err, strings.Join(messages, "\n"))
		}
		if err := stack.expectEmptyPreview("update"); err != nil {
			return nil, err
		}
	}

	var previewMessages []string
	forgets := false // the replace showed the resource's delete sends no request
	if fx.Replace != nil {
		replace, err := inputs(fx.Replace)
		if err != nil {
			return nil, err
		}
		stack.write(replace)
		ops, messages, out, err := stack.preview()
		if err != nil {
			return messages, fmt.Errorf("preview (replace): %w\n%s", err, out)
		}
		previewMessages = append([]string{}, messages...) // non-nil: the resource has a replace step
		if ops["replace"] != 1 {
			return messages, fmt.Errorf("preview (replace) plans %v, want one replace\n%s", ops, out)
		}
		requests, upMessages, err := stack.up()
		if err != nil {
			return messages, fmt.Errorf("up (replace): %w\n%s", err, strings.Join(upMessages, "\n"))
		}
		forgets = !hasMethod(requests, "DELETE")
		if err := checkReplaceOrder(requests, res.DeleteBeforeReplace); err != nil {
			return messages, err
		}
		if err := stack.expectEmptyPreview("replace"); err != nil {
			return messages, err
		}
	}

	seen := len(mock.Requests())
	if _, err := stack.pt.DestroyErr(t); err != nil {
		return previewMessages, fmt.Errorf("destroy: %w", err)
	}
	if !hasMethod(parseRequests(mock.Requests()[seen:]), "DELETE") && fx.Seed == nil && !forgets {
		return previewMessages, fmt.Errorf("destroy sent no DELETE: %v", mock.Requests()[seen:])
	}
	return previewMessages, nil
}

// checkReplaceOrder checks the order Pulumi replaced in. Pulumi creates the replacement before it
// deletes the original unless the resource sets DeleteBeforeReplace. When the create and the
// delete hit the same path, the object is the parent's one-per-parent child, so creating first
// and then deleting removes the replacement: such a resource has to delete first. A delete that
// names its object in the query (a team member's ?email=) reaches only that one, so it may follow.
func checkReplaceOrder(requests []mockRequest, deleteBeforeReplace bool) error {
	create, del := -1, -1
	for i, r := range requests {
		switch {
		case r.Method == "DELETE" && del < 0:
			del = i
		case (r.Method == "POST" || r.Method == "PUT") && create < 0:
			create = i
		}
	}
	if create < 0 {
		return fmt.Errorf("replace sent no create: %v", requests)
	}
	// A resource whose delete only forgets the object (a release) sends no DELETE at all.
	if del < 0 {
		return nil
	}
	if requests[create].Path == requests[del].Path && create < del && requests[del].Query == "" {
		return fmt.Errorf("replace created at %s and then deleted the same path, removing the replacement; set DeleteBeforeReplace: %v",
			requests[create].Path, requests)
	}
	if deleteBeforeReplace != (del < create) {
		return fmt.Errorf("replace order: DeleteBeforeReplace=%v but create at %d, delete at %d: %v", deleteBeforeReplace, create, del, requests)
	}
	return nil
}

func hasMethod(requests []mockRequest, method string) bool {
	for _, r := range requests {
		if r.Method == method {
			return true
		}
	}
	return false
}

func renderPreviews(previews map[string][]string) string {
	names := make([]string, 0, len(previews))
	for name := range previews {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Diagnostics `pulumi preview` prints for each resource's replace fixture.\n")
	b.WriteString("# Regenerate: go test ./provider -run TestCLILifecycle -update-preview-messages\n")
	for _, name := range names {
		fmt.Fprintf(&b, "\n%s\n", name)
		switch {
		case previews[name] == nil:
			b.WriteString("  (no replace fixture: no create-only input)\n")
		case len(previews[name]) == 0:
			b.WriteString("  (no messages)\n")
		}
		for _, m := range previews[name] {
			fmt.Fprintf(&b, "  %s\n", m)
		}
	}
	return b.String()
}

func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if *updatePreviewMessages {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run with -update-preview-messages)", path, err)
	}
	if string(want) != got {
		t.Errorf("%s is stale: the preview messages changed. Review the new text and rerun with -update-preview-messages.\n%s",
			path, lineDiff(string(want), got))
	}
}

func lineDiff(want, got string) string {
	wantLines, gotLines := map[string]bool{}, map[string]bool{}
	for _, l := range strings.Split(want, "\n") {
		wantLines[l] = true
	}
	for _, l := range strings.Split(got, "\n") {
		gotLines[l] = true
	}
	var out []string
	for _, l := range strings.Split(want, "\n") {
		if !gotLines[l] {
			out = append(out, "- "+l)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if !wantLines[l] {
			out = append(out, "+ "+l)
		}
	}
	return strings.Join(out, "\n")
}

// TestCLIStatusPageSections runs a status page's sections, whose items are matched by terraform_match_items_by
// (a section by name, a resource by type and id) rather than by the ids the server assigns, through
// `pulumi up`: adding sections, reordering them and adding a resource must each converge to an
// empty preview, and none may plan a replacement.
func TestCLIStatusPageSections(t *testing.T) {
	requirePulumiCLI(t)
	info := Provider()
	const tfType = "betterstack_status_page"
	res := info.Resources[tfType]
	tfSchema := info.P.ResourcesMap().Get(tfType).Schema()
	// Off, so the destroy that ends the test is allowed.
	page := map[string]interface{}{"company_name": "example", "company_url": "https://example.com", "subdomain": "example-page", "timezone": "UTC", "deletion_protection": false}
	resource := func(id int, name string) map[string]interface{} {
		return map[string]interface{}{"resource_type": "Monitor", "resource_id": id, "public_name": name}
	}
	section := func(name string, resources ...map[string]interface{}) map[string]interface{} {
		items := make([]interface{}, len(resources))
		for i, r := range resources {
			items[i] = r
		}
		return map[string]interface{}{"name": name, "resources": items}
	}
	withSections := func(sections ...map[string]interface{}) map[string]cliResource {
		attrs := map[string]interface{}{}
		for k, v := range page {
			attrs[k] = v
		}
		list := make([]interface{}, len(sections))
		for i, s := range sections {
			list[i] = s
		}
		attrs["sections"] = list
		props, err := pulumiInputs("status_page", attrs, tfSchema, res.Fields)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]cliResource{"subject": {Type: res.Tok.String(), Properties: props}}
	}

	mock := apitest.NewMockAPI()
	t.Cleanup(mock.Close)
	mock.AssignItemIDs("status_page", "sections", "sections.resources")
	steps := []struct {
		name    string
		program map[string]cliResource
	}{
		{"two sections", withSections(section("API", resource(1, "Public API")), section("Web"))},
		{"reordered", withSections(section("Web"), section("API", resource(1, "Public API")))},
		{"resource added", withSections(section("Web", resource(2, "Website")), section("API", resource(1, "Public API")))},
	}
	stack := newCLIStack(t, "cli-status-page-sections", mock, steps[0].program)
	var known map[string]string // section and resource ids in state, by item key, after the last step
	for i, step := range steps {
		if i > 0 {
			stack.write(step.program)
			ops, messages, out, err := stack.preview()
			if err != nil {
				t.Fatalf("%s: preview: %v\n%s", step.name, err, out)
			}
			if ops["update"] != 1 || ops["replace"] != 0 {
				t.Fatalf("%s: preview plans %v, want one update and no replace\n%s\n%s", step.name, ops, strings.Join(messages, "\n"), out)
			}
		}
		if _, messages, err := stack.up(); err != nil {
			t.Fatalf("%s: up: %v\n%s", step.name, err, strings.Join(messages, "\n"))
		}
		// An item that was already on the page goes back with the id it got, matched by its key,
		// so the API keeps it (and a resource's subscribers) instead of recreating it.
		sent := sectionIDs(t, mock.LastRequestBody("status_page"))
		for key, id := range known {
			if got, ok := sent[key]; ok && got != id {
				t.Errorf("%s: %s was sent with id %q, want its existing id %q", step.name, key, got, id)
			} else if !ok && i > 0 {
				t.Errorf("%s: %s was sent without its existing id %q (sent %v)", step.name, key, id, sent)
			}
		}
		if err := stack.expectEmptyPreview(step.name); err != nil {
			t.Fatal(err)
		}
		known = stateSectionIDs(t, stack)
		if len(known) == 0 {
			t.Fatalf("%s: the state carries no section ids; the mock should have assigned them", step.name)
		}
	}
	if _, err := stack.pt.DestroyErr(t); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}

// sectionIDs maps each section ("section API") and resource ("resource Monitor/1") in a status page
// body or state to its id, leaving out items without one.
func sectionIDs(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return collectSectionIDs(doc, "sections")
}

func collectSectionIDs(doc map[string]interface{}, sectionsKey string) map[string]string {
	out := map[string]string{}
	sections, _ := doc[sectionsKey].([]interface{})
	for _, raw := range sections {
		section, _ := raw.(map[string]interface{})
		if id, _ := section["id"].(string); id != "" {
			out[fmt.Sprintf("section %v", section["name"])] = id
		}
		resources, _ := section["resources"].([]interface{})
		for _, raw := range resources {
			res, _ := raw.(map[string]interface{})
			typ := res["resource_type"]
			if typ == nil {
				typ = res["resourceType"]
			}
			rid := res["resource_id"]
			if rid == nil {
				rid = res["resourceId"]
			}
			if id, _ := res["id"].(string); id != "" {
				out[fmt.Sprintf("resource %v/%v", typ, rid)] = id
			}
		}
	}
	return out
}

func stateSectionIDs(t *testing.T, stack *cliStack) map[string]string {
	t.Helper()
	var deployment struct {
		Resources []struct {
			Type    string                 `json:"type"`
			Outputs map[string]interface{} `json:"outputs"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(stack.pt.ExportStack(t).Deployment, &deployment); err != nil {
		t.Fatal(err)
	}
	for _, r := range deployment.Resources {
		if strings.HasSuffix(r.Type, ":StatusPage") {
			return collectSectionIDs(r.Outputs, "sections")
		}
	}
	return nil
}
