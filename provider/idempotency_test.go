package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/apitest"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	shim "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"google.golang.org/protobuf/types/known/structpb"
)

// knownNonIdempotent lists the resources whose Pulumi lifecycle still shows a diff with unchanged
// inputs, each with the reason the matrix run surfaced. A listed resource that fails logs the gap
// and passes; one that starts passing fails, so the list can only shrink deliberately. Every entry
// must also be documented in README.md ("Known idempotency gaps").
var knownNonIdempotent = map[string]string{}

// userAgent is what the shim sends for this bridge, built from Version when the server is built.
func userAgent() string { return "pulumi-betterstack/" + Version + " (unified-api " + Version + ")" }

// Every managed resource must converge: after Create, after Read, and after Update, planning the
// same inputs again must be a no-op. This is the Pulumi-level counterpart of the generated
// Terraform smoke matrix and runs through the same gRPC server the plugin serves, against the same
// binding-driven mock API, using the same per-resource fixtures.
func TestIdempotencyMatrix(t *testing.T) {
	info := Provider()
	fixtures := apitest.Fixtures()
	if got, want := len(fixtures), info.P.ResourcesMap().Len(); got != want {
		t.Fatalf("apitest ships %d fixtures but the provider manages %d resources; every resource needs one", got, want)
	}

	// One mock and one configured server for the whole matrix: the mock scopes its records by
	// resource (and parent path), so the subtests cannot see each other's objects, and the
	// subtests run sequentially so LastUserAgent is the agent of this subtest's own request.
	mock := apitest.NewMockAPI()
	t.Cleanup(mock.Close)
	ctx := context.Background()
	srv, err := NewServer(ctx, nil, providerMetadata(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// apiRateLimit 0 switches off the client's throttle (8 requests a second by default), which
	// would otherwise stretch 46 lifecycles against an in-memory mock to about twenty seconds.
	if _, err := srv.Configure(ctx, &pulumirpc.ConfigureRequest{
		Args:            mustStruct(map[string]interface{}{"apiToken": "test-token", "endpoint": mock.URL(), "apiRateLimit": 0}),
		AcceptSecrets:   true,
		AcceptResources: true,
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	for _, fx := range fixtures {
		t.Run(fx.Resource, func(t *testing.T) {
			res, ok := info.Resources[fx.TFType]
			if !ok {
				t.Fatalf("%s is not bridged", fx.TFType)
			}
			tfSchema := info.P.ResourcesMap().Get(fx.TFType).Schema()
			if fx.Seed != nil {
				if err := mock.Seed(fx.Seed.Resource, fx.Seed.Vars, fx.Seed.ID); err != nil {
					t.Fatal(err)
				}
			}
			mock.InjectItemFields(fx.Resource, fx.ComputedItemFields)
			mock.InjectOnCreate(fx.Resource, fx.Settle)

			lifecycle := resourceLifecycle{
				ctx:  ctx,
				srv:  srv,
				mock: mock,
				urn:  "urn:pulumi:test::test::" + res.Tok.String() + "::" + fx.Resource,
			}
			create, err := pulumiInputs(fx.Resource, fx.Create, tfSchema, res.Fields)
			if err != nil {
				t.Fatal(err)
			}
			var update map[string]interface{}
			if fx.Update != nil {
				if update, err = pulumiInputs(fx.Resource, fx.Update, tfSchema, res.Fields); err != nil {
					t.Fatal(err)
				}
			}
			// Every request this lifecycle makes, not only the create, must carry the bridge's agent.
			seen := len(mock.UserAgents())
			err = lifecycle.run(create, update)
			for i, agent := range mock.UserAgents()[seen:] {
				if agent != userAgent() {
					t.Errorf("request %d sent User-Agent %q, want %q", i+1, agent, userAgent())
				}
			}

			reason, known := knownNonIdempotent[fx.Resource]
			switch {
			case err != nil && known:
				t.Logf("known gap: %s\n%v", reason, err)
			case err != nil:
				t.Fatal(err)
			case known:
				t.Fatalf("%s is now idempotent, remove it from knownNonIdempotent", fx.Resource)
			}
		})
	}
}

// Every known gap has to be explained to users of the provider, not only to this test, and the
// two lists must agree both ways: a README entry nobody tests and a tested name no fixture has
// are both stale.
func TestKnownIdempotencyGapsAreDocumented(t *testing.T) {
	resources := map[string]bool{}
	for _, fx := range apitest.Fixtures() {
		resources[fx.Resource] = true
	}
	readme := readmeSection(t, "Known idempotency gaps")
	for name := range knownNonIdempotent {
		if !resources[name] {
			t.Errorf("knownNonIdempotent lists %q, which is not a managed resource", name)
		}
		if !strings.Contains(readme, "`"+name+"`") {
			t.Errorf("README.md section \"Known idempotency gaps\" does not mention `%s`", name)
		}
	}
	// Resource names are lower snake case; other backticked words in the section are prose.
	for _, match := range regexp.MustCompile("`([a-z][a-z0-9_]*)`").FindAllStringSubmatch(readme, -1) {
		if _, ok := knownNonIdempotent[match[1]]; !ok {
			t.Errorf("README.md lists `%s` as a known gap but knownNonIdempotent does not", match[1])
		}
	}
}

type resourceLifecycle struct {
	ctx  context.Context
	srv  pulumirpc.ResourceProviderServer
	mock *apitest.MockAPI
	urn  string
}

// run drives Create, Read and (when the fixture has one) Update, planning the unchanged inputs
// against the state each step produced. Drift is collected rather than fatal so one run reports
// every step that diverges, and the object is deleted whatever happened after Create; a failed
// Delete joins the result, which is why the return is named.
func (l resourceLifecycle) run(create, update map[string]interface{}) (err error) {
	checked, err := l.check(nil, create)
	if err != nil {
		return fmt.Errorf("create Check: %w", err)
	}
	created, err := l.srv.Create(l.ctx, &pulumirpc.CreateRequest{Urn: l.urn, Properties: checked})
	if err != nil {
		return fmt.Errorf("Create: %w", err)
	}
	id := created.GetId()
	state := created.GetProperties()
	var drift []error
	defer func() {
		if _, deleteErr := l.srv.Delete(l.ctx, &pulumirpc.DeleteRequest{Urn: l.urn, Id: id, Properties: state}); deleteErr != nil {
			err = errors.Join(err, fmt.Errorf("Delete: %w", deleteErr))
		}
	}()

	if err := l.expectNoDiff(id, state, create); err != nil {
		drift = append(drift, fmt.Errorf("after Create: %w", err))
	}

	read, err := l.srv.Read(l.ctx, &pulumirpc.ReadRequest{Urn: l.urn, Id: id, Properties: state, Inputs: checked})
	if err != nil {
		drift = append(drift, fmt.Errorf("Read: %w", err))
	} else {
		state = read.GetProperties()
		if err := l.expectNoDiff(id, state, create); err != nil {
			drift = append(drift, fmt.Errorf("after Read: %w", err))
		}
	}

	if update != nil {
		checkedUpdate, err := l.check(state, update)
		if err != nil {
			drift = append(drift, fmt.Errorf("update Check: %w", err))
		} else if updated, err := l.srv.Update(l.ctx, &pulumirpc.UpdateRequest{
			Urn: l.urn, Id: id, Olds: state, News: checkedUpdate,
		}); err != nil {
			drift = append(drift, fmt.Errorf("Update: %w", err))
		} else {
			state = updated.GetProperties()
			// A no-op update would converge trivially: the new values must be in the state.
			if err := expectStateCarries(state, update); err != nil {
				drift = append(drift, fmt.Errorf("after Update: %w", err))
			}
			if err := l.expectNoDiff(id, state, update); err != nil {
				drift = append(drift, fmt.Errorf("after Update: %w", err))
			}
		}
	}
	return errors.Join(drift...)
}

// expectStateCarries checks every input value is present in the state, nested objects compared
// by the keys the input names (the server adds fields of its own), secrets unwrapped and numbers
// compared as JSON numbers.
func expectStateCarries(state *structpb.Struct, inputs map[string]interface{}) error {
	props, err := plugin.UnmarshalProperties(state, plugin.MarshalOptions{KeepUnknowns: true})
	if err != nil {
		return fmt.Errorf("unmarshal state: %w", err)
	}
	var missing []string
	for _, key := range sortedKeys(inputs) {
		actual, ok := props.Mappable()[key]
		if !ok {
			missing = append(missing, fmt.Sprintf("  %s: absent, want %v", key, inputs[key]))
			continue
		}
		if !valueCarries(inputs[key], actual) {
			missing = append(missing, fmt.Sprintf("  %s: state=%v want=%v", key, actual, inputs[key]))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("state does not carry the inputs:\n%s", strings.Join(missing, "\n"))
	}
	return nil
}

func valueCarries(want, got interface{}) bool {
	switch want := want.(type) {
	case map[string]interface{}:
		object, ok := got.(map[string]interface{})
		if !ok {
			return false
		}
		for key, value := range want {
			if !valueCarries(value, object[key]) {
				return false
			}
		}
		return true
	case []interface{}:
		items, ok := got.([]interface{})
		if !ok || len(items) != len(want) {
			return false
		}
		for i := range want {
			if !valueCarries(want[i], items[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(jsonShape(want), jsonShape(got))
	}
}

// jsonShape round-trips a scalar through JSON so fixture ints and state float64s compare equal.
func jsonShape(value interface{}) interface{} {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return value
	}
	return out
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (l resourceLifecycle) check(olds *structpb.Struct, news map[string]interface{}) (*structpb.Struct, error) {
	if olds == nil {
		olds = mustStruct(nil)
	}
	resp, err := l.srv.Check(l.ctx, &pulumirpc.CheckRequest{Urn: l.urn, Olds: olds, News: mustStruct(news)})
	if err != nil {
		return nil, err
	}
	if failures := resp.GetFailures(); len(failures) > 0 {
		return nil, fmt.Errorf("check failures: %v", failures)
	}
	return resp.GetInputs(), nil
}

// expectNoDiff plans the same inputs against the given state. The pf bridge plans from Olds, so
// the state a Create, Read or Update returned is exactly what a later `pulumi up` would plan from.
func (l resourceLifecycle) expectNoDiff(id string, state *structpb.Struct, inputs map[string]interface{}) error {
	checked, err := l.check(state, inputs)
	if err != nil {
		return fmt.Errorf("Check: %w", err)
	}
	diff, err := l.srv.Diff(l.ctx, &pulumirpc.DiffRequest{Urn: l.urn, Id: id, Olds: state, News: checked})
	if err != nil {
		return fmt.Errorf("Diff: %w", err)
	}
	if diff.GetChanges() == pulumirpc.DiffResponse_DIFF_NONE && len(diff.GetReplaces()) == 0 && len(diff.GetDiffs()) == 0 {
		return nil
	}
	return fmt.Errorf("unchanged inputs plan %s (replaces=%v diffs=%v):\n%s",
		diff.GetChanges(), diff.GetReplaces(), diff.GetDiffs(), describeDetailedDiff(diff, state, checked))
}

// describeDetailedDiff lists each differing property path with the value in state and the value
// the plan carries, so a failure says what moved rather than only that something did. The bridge
// does not always fill DetailedDiff, so the plain Diffs list is merged in.
func describeDetailedDiff(diff *pulumirpc.DiffResponse, olds, news *structpb.Struct) string {
	oldProps := resource.NewPropertyMapFromMap(olds.AsMap())
	newProps := resource.NewPropertyMapFromMap(news.AsMap())
	kinds := map[string]string{}
	for path, detail := range diff.GetDetailedDiff() {
		kinds[path] = detail.GetKind().String()
	}
	for _, path := range diff.GetDiffs() {
		if _, ok := kinds[path]; !ok {
			kinds[path] = "listed in diffs"
		}
	}
	paths := make([]string, 0, len(kinds))
	for path := range kinds {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	lines := make([]string, 0, len(paths))
	for _, path := range paths {
		lines = append(lines, fmt.Sprintf("  %s (%s): state=%s plan=%s",
			path, kinds[path], propertyAt(oldProps, path), propertyAt(newProps, path)))
	}
	if len(lines) == 0 {
		return "  (no property paths reported)"
	}
	return strings.Join(lines, "\n")
}

func propertyAt(props resource.PropertyMap, path string) string {
	parsed, err := resource.ParsePropertyPath(path)
	if err != nil {
		return "<unparseable path>"
	}
	value, ok := parsed.Get(resource.NewObjectProperty(props))
	if !ok {
		return "<absent>"
	}
	return fmt.Sprintf("%v", value.Mappable())
}

// pulumiInputs renames a fixture's Terraform attribute names to the bridged Pulumi property names
// with the bridge's own naming, recursing into nested objects the way the bridge does, so the
// matrix never guesses a camelCase the bridge would not produce. An attribute the schema does not
// know is an error: the bridge's encoder would drop it silently and the step would test nothing.
func pulumiInputs(path string, attrs map[string]interface{}, sch shim.SchemaMap, fields map[string]*tfbridge.SchemaInfo) (map[string]interface{}, error) {
	out := make(map[string]interface{}, len(attrs))
	for name, value := range attrs {
		fieldSchema, ok := sch.GetOk(name)
		if !ok {
			return nil, fmt.Errorf("%s: attribute %q is not in the Terraform schema", path, name)
		}
		converted, err := pulumiValue(path+"."+name, value, fieldSchema, fields[name])
		if err != nil {
			return nil, err
		}
		out[tfbridge.TerraformToPulumiNameV2(name, sch, fields)] = converted
	}
	return out, nil
}

func pulumiValue(path string, value interface{}, sch shim.Schema, field *tfbridge.SchemaInfo) (interface{}, error) {
	var elemSchema shim.Schema
	var elemFields map[string]*tfbridge.SchemaInfo
	var elemInfo *tfbridge.SchemaInfo
	if field != nil {
		elemInfo = field.Elem
		if elemInfo != nil {
			elemFields = elemInfo.Fields
		}
	}
	if sch != nil {
		switch elem := sch.Elem().(type) {
		case shim.Resource:
			// A nested object: its attributes are named through its own schema map.
			if object, ok := value.(map[string]interface{}); ok {
				return pulumiInputs(path, object, elem.Schema(), elemFields)
			}
			if items, ok := value.([]interface{}); ok {
				if tfbridge.IsMaxItemsOne(sch, field) && len(items) == 1 {
					return pulumiValue(path, items[0], sch, field)
				}
				converted := make([]interface{}, len(items))
				for i, item := range items {
					object, ok := item.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("%s[%d]: expected an object, got %T", path, i, item)
					}
					nested, err := pulumiInputs(fmt.Sprintf("%s[%d]", path, i), object, elem.Schema(), elemFields)
					if err != nil {
						return nil, err
					}
					converted[i] = nested
				}
				return converted, nil
			}
		case shim.Schema:
			elemSchema = elem
		}
	}
	if items, ok := value.([]interface{}); ok {
		converted := make([]interface{}, len(items))
		for i, item := range items {
			nested, err := pulumiValue(fmt.Sprintf("%s[%d]", path, i), item, elemSchema, elemInfo)
			if err != nil {
				return nil, err
			}
			converted[i] = nested
		}
		return converted, nil
	}
	return value, nil
}

// readmeSection returns the body of one `## ` section of README.md, up to the next heading.
func readmeSection(t *testing.T, heading string) string {
	t.Helper()
	raw, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)
	marker := "## " + heading
	start := strings.Index(readme, marker)
	if start < 0 {
		t.Fatalf("README.md has no \"%s\" section", marker)
	}
	body := readme[start+len(marker):]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	return body
}
