package provider

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/apitest"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	shim "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
)

// dataSourceOnly are the data sources with no resource of the same type whose seeded record takes more
// than its lookup arguments: the mock API's record is scoped by these parent variables, found by these
// arguments and holds these attributes. Any other is looked up by its required arguments, or by its id
// when none is required, all 777; a singleton by nothing.
var dataSourceOnly = map[string]struct {
	vars  map[string]string
	args  map[string]interface{}
	attrs map[string]interface{}
}{
	"ip_list":    {vars: map[string]string{}, attrs: map[string]interface{}{"us": []interface{}{"192.0.2.1"}}},
	"gcp_metric": {vars: map[string]string{"source_id": "5"}, args: map[string]interface{}{"sourceId": 5, "id": "777"}},
}

// TestCLIDataSources reads every data source through the Pulumi CLI, the way a program does: a
// data source of a managed type reads back a resource the same program created, by its id and the
// parent ids that resource has; the others read a record seeded in the mock API. Each read must
// reach the API and return the record's id.
func TestCLIDataSources(t *testing.T) {
	requirePulumiCLI(t)
	info := Provider()
	fixtures := map[string]apitest.SmokeFixture{}
	for _, fx := range apitest.Fixtures() {
		fixtures[fx.TFType] = fx
	}
	var types []string
	info.P.DataSourcesMap().Range(func(key string, _ shim.Resource) bool {
		types = append(types, key)
		return true
	})
	sort.Strings(types)
	if len(types) != len(info.DataSources) {
		t.Fatalf("the provider has %d data sources but bridges %d", len(types), len(info.DataSources))
	}

	var mu sync.Mutex
	read := 0
	t.Run("data_sources", func(t *testing.T) {
		for _, tfType := range types {
			t.Run(strings.TrimPrefix(tfType, "betterstack_"), func(t *testing.T) {
				t.Parallel()
				ds := info.DataSources[tfType]
				if ds == nil {
					t.Fatalf("%s is not bridged", tfType)
				}
				var err error
				if fx, ok := fixtures[tfType]; ok {
					err = readManagedDataSource(t, info, ds, fx)
				} else {
					err = readSeededDataSource(t, info, ds, tfType, strings.TrimPrefix(tfType, "betterstack_"))
				}
				if err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				read++
				mu.Unlock()
			})
		}
	})
	t.Logf("data sources read through the Pulumi CLI: %d of %d", read, len(types))
}

// readManagedDataSource creates the fixture's resource, then reads it back with the data source.
func readManagedDataSource(t *testing.T, info tfbridge.ProviderInfo, ds *tfbridge.DataSourceInfo, fx apitest.SmokeFixture) error {
	res := info.Resources[fx.TFType]
	tfSchema := info.P.ResourcesMap().Get(fx.TFType).Schema()
	props, err := pulumiInputs(fx.Resource, fx.Create, tfSchema, res.Fields)
	if err != nil {
		return err
	}
	mock := apitest.NewMockAPI()
	t.Cleanup(mock.Close)
	if fx.Seed != nil {
		if err := mock.Seed(fx.Seed.Resource, fx.Seed.Vars, fx.Seed.ID); err != nil {
			return err
		}
	}
	mock.InjectItemFields(fx.Resource, fx.ComputedItemFields)
	mock.InjectOnCreate(fx.Resource, fx.Settle)

	// The lookup passes the id, and every other argument the created resource also has (its parent
	// ids), so a nested data source finds the resource where the program created it.
	resourceProps := pulumiPropertyNames(res, tfSchema)
	args := map[string]interface{}{}
	for _, arg := range dataSourceArgs(ds, fx.TFType, info) {
		switch {
		case arg == "id":
			args[arg] = "${subject.id}"
		case resourceProps[arg]:
			args[arg] = "${subject." + arg + "}"
		}
	}
	program := invokeProgram("ds-"+strings.ReplaceAll(fx.Resource, "_", "-"), ds.Tok.String(), "id", args,
		fmt.Sprintf("  subject:\n    type: %s\n    properties: %s\n", res.Tok.String(), jsonText(props)),
		"  subjectId: ${subject.id}\n")
	stack := newCLIStackFromProgram(t, "ds-"+fx.Resource, mock, program, thisBuild(t))
	outputs, requests, err := upOutputs(stack)
	if err != nil {
		return err
	}
	if !hasMethod(requests, "GET") {
		return fmt.Errorf("the data source sent no GET: %v", requests)
	}
	if outputs["readId"] != outputs["subjectId"] {
		return fmt.Errorf("the data source read id %v, the program created %v", outputs["readId"], outputs["subjectId"])
	}
	return nil
}

// readSeededDataSource reads a record seeded in the mock API.
func readSeededDataSource(t *testing.T, info tfbridge.ProviderInfo, ds *tfbridge.DataSourceInfo, tfType, resource string) error {
	seed, ok := dataSourceOnly[resource]
	if !ok {
		seed.args, seed.attrs = map[string]interface{}{}, map[string]interface{}{}
		sch := info.P.DataSourcesMap().Get(tfType).Schema()
		sch.Range(func(key string, s shim.Schema) bool {
			if s.Required() {
				seed.args[tfbridge.TerraformToPulumiNameV2(key, sch, ds.Fields)] = "777"
				if key != "id" {
					seed.attrs[key] = "777"
				}
			}
			return true
		})
		// A singleton takes no arguments: its id is computed.
		if id, ok := sch.GetOk("id"); ok && len(seed.args) == 0 && id.Optional() {
			seed.args["id"] = "777"
		}
	}
	// A plan or version has no id: its lookup argument is its identity.
	key := "id"
	if _, ok := seed.args["id"]; !ok && len(seed.args) > 0 {
		key = sortedNames(boolKeys(seed.args))[0]
	}
	mock := apitest.NewMockAPI()
	t.Cleanup(mock.Close)
	if seed.args != nil || seed.vars != nil {
		attrs := seed.attrs
		if attrs == nil {
			attrs = map[string]interface{}{}
		}
		if err := mock.SeedRecord(resource, seed.vars, "777", attrs); err != nil {
			return err
		}
	}
	outputs := ""
	if resource == "ip_list" {
		outputs = "  ips: ${read.ips}\n"
	}
	program := invokeProgram("ds-"+strings.ReplaceAll(resource, "_", "-"), ds.Tok.String(), key, seed.args, "", outputs)
	stack := newCLIStackFromProgram(t, "ds-"+resource, mock, program, thisBuild(t))
	read, requests, err := upOutputs(stack)
	if err != nil {
		return err
	}
	if !hasMethod(requests, "GET") {
		return fmt.Errorf("the data source sent no GET: %v", requests)
	}
	if resource == "ip_list" && !strings.Contains(fmt.Sprint(read["ips"]), "192.0.2.1") {
		return fmt.Errorf("the IP list read %v, want the seeded 192.0.2.1", read["ips"])
	}
	if want, ok := seed.args[key]; ok && read["readId"] != want {
		return fmt.Errorf("the data source read %s %v, want %v", key, read["readId"], want)
	}
	return nil
}

// dataSourceArgs lists the data source's Pulumi argument names.
func dataSourceArgs(ds *tfbridge.DataSourceInfo, tfType string, info tfbridge.ProviderInfo) []string {
	sch := info.P.DataSourcesMap().Get(tfType).Schema()
	return sortedNames(pulumiPropertyNames(&tfbridge.ResourceInfo{Fields: ds.Fields}, sch, true))
}

// pulumiPropertyNames maps a Terraform schema's attributes (inputs only, when inputsOnly is given)
// to the Pulumi names the program uses.
func pulumiPropertyNames(res *tfbridge.ResourceInfo, sch shim.SchemaMap, inputsOnly ...bool) map[string]bool {
	names := map[string]bool{}
	sch.Range(func(key string, s shim.Schema) bool {
		if len(inputsOnly) > 0 && inputsOnly[0] && !s.Required() && !s.Optional() {
			return true
		}
		names[tfbridge.TerraformToPulumiNameV2(key, sch, res.Fields)] = true
		return true
	})
	return names
}

func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// invokeProgram renders a Pulumi YAML program that reads one data source and exports its key.
func invokeProgram(name, token, key string, args map[string]interface{}, resources, outputs string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\nruntime: yaml\n", name)
	if resources != "" {
		b.WriteString("resources:\n" + resources)
	}
	b.WriteString("variables:\n  read:\n    fn::invoke:\n      function: " + token + "\n      arguments:\n")
	for _, key := range sortedNames(boolKeys(args)) {
		fmt.Fprintf(&b, "        %s: %s\n", key, yamlValue(args[key]))
	}
	if len(args) == 0 {
		b.WriteString("        {}\n")
	}
	b.WriteString("outputs:\n  readId: ${read." + key + "}\n" + outputs)
	return b.String()
}

func boolKeys(m map[string]interface{}) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// yamlValue writes an interpolation as is and anything else as JSON, which YAML reads verbatim.
func yamlValue(v interface{}) string {
	if s, ok := v.(string); ok && strings.HasPrefix(s, "${") {
		return s
	}
	return jsonText(v)
}

func jsonText(v interface{}) string {
	out, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(out)
}

// upOutputs applies the program and returns its stack outputs and the requests it caused.
func upOutputs(s *cliStack) (map[string]interface{}, []mockRequest, error) {
	s.t.Helper()
	seen := len(s.mock.Requests())
	collect, done := diagnostics()
	result, err := s.pt.UpErr(s.t, optup.EventStreams(collect))
	messages := done()
	requests := parseRequests(s.mock.Requests()[seen:])
	if err != nil {
		return nil, requests, fmt.Errorf("up: %w\n%s", err, strings.Join(messages, "\n"))
	}
	outputs := map[string]interface{}{}
	for key, value := range result.Outputs {
		outputs[key] = fmt.Sprint(value.Value)
	}
	return outputs, requests, nil
}
