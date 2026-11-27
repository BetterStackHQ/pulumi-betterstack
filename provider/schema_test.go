package provider

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/shim"
	pftfbridge "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfbridge"
)

type packageSchema struct {
	Name      string                     `json:"name"`
	Version   string                     `json:"version"`
	Resources map[string]json.RawMessage `json:"resources"`
	Functions map[string]json.RawMessage `json:"functions"`
	Config    struct {
		Variables map[string]json.RawMessage `json:"variables"`
	} `json:"config"`
}

type resourceSchema struct {
	RequiredInputs []string `json:"requiredInputs"`
}

func generatedSchema(t *testing.T) packageSchema {
	t.Helper()
	var s packageSchema
	if err := json.Unmarshal(providerMetadata(t).PackageSchema, &s); err != nil {
		t.Fatalf("unmarshal package schema: %v", err)
	}
	return s
}

// Every Terraform resource and data source the schema-generated provider exposes must bridge into a
// Pulumi resource/function. A count match is the 100% coverage guarantee for the bridged surface:
// add a resource to the unified-api schema and this fails until it shows up in Pulumi too.
func TestEveryTerraformResourceBridges(t *testing.T) {
	s := generatedSchema(t)
	p := pftfbridge.ShimProvider(shim.New("pulumi-betterstack", Version))

	if want := p.ResourcesMap().Len(); len(s.Resources) != want {
		t.Errorf("Pulumi schema has %d resources, the TF provider exposes %d", len(s.Resources), want)
	}
	if want := p.DataSourcesMap().Len(); len(s.Functions) < want {
		t.Errorf("Pulumi schema has %d functions, expected at least %d data sources", len(s.Functions), want)
	}
}

func TestPackageMetadata(t *testing.T) {
	s := generatedSchema(t)
	if s.Name != "betterstack" {
		t.Errorf("package name = %q, want betterstack", s.Name)
	}
	if len(s.Config.Variables) < 2 {
		t.Errorf("expected at least 2 config variables (api token + endpoint), got %v", configKeys(s))
	}
	if _, ok := s.Config.Variables["endpoint"]; !ok {
		t.Errorf("config missing endpoint override, have %v", configKeys(s))
	}
}

func TestAllResourceTokensWellFormed(t *testing.T) {
	s := generatedSchema(t)
	if len(s.Resources) == 0 {
		t.Fatal("no resources in generated schema")
	}
	for tok := range s.Resources {
		if !strings.HasPrefix(tok, "betterstack:") {
			t.Errorf("resource token %q does not start with betterstack:", tok)
		}
	}
}

func TestMonitorPlaywrightInputsAreNotUniversallyRequired(t *testing.T) {
	assertMonitorInputs := func(t *testing.T, resources map[string]json.RawMessage) {
		t.Helper()
		raw, ok := resources["betterstack:index/monitor:Monitor"]
		if !ok {
			t.Fatal("monitor resource missing")
		}
		var monitor resourceSchema
		if err := json.Unmarshal(raw, &monitor); err != nil {
			t.Fatalf("unmarshal monitor schema: %v", err)
		}
		for _, name := range []string{"url", "scenarioName"} {
			if contains(monitor.RequiredInputs, name) {
				t.Errorf("%s is universally required; monitor create accepts url or scenarioName", name)
			}
		}
		// Monitor create defaults monitor_type to status, so it's optional like url and scenarioName.
		if contains(monitor.RequiredInputs, "monitorType") {
			t.Errorf("monitorType is required; monitor create defaults it to status, got %v", monitor.RequiredInputs)
		}
	}

	assertMonitorInputs(t, generatedSchema(t).Resources)
	raw, err := os.ReadFile(filepath.Join("cmd", "pulumi-resource-betterstack", "schema.json"))
	if err != nil {
		t.Fatalf("reading the committed schema: %v", err)
	}
	var committed packageSchema
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatalf("unmarshal committed schema: %v", err)
	}
	assertMonitorInputs(t, committed.Resources)
}

// The committed schema is the plugin and SDK contract. Descriptions depend on example conversion, but
// every property contract must match the live bridge.
func TestCommittedSchemaMatchesBridgedPropertyContracts(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("cmd", "pulumi-resource-betterstack", "schema.json"))
	if err != nil {
		t.Fatalf("reading the committed schema: %v", err)
	}
	var committed packageSchema
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatalf("unmarshal committed schema: %v", err)
	}
	fresh := generatedSchema(t)
	for _, c := range []struct {
		what             string
		committed, fresh map[string]json.RawMessage
	}{
		{"resources", committed.Resources, fresh.Resources},
		{"functions", committed.Functions, fresh.Functions},
	} {
		for tok := range c.fresh {
			committed, ok := c.committed[tok]
			if !ok {
				t.Errorf("%s: %s is bridged but missing from the committed schema; run `make schema`", c.what, tok)
				continue
			}
			if !bytes.Equal(schemaContract(t, committed), schemaContract(t, c.fresh[tok])) {
				t.Errorf("%s: %s property contract differs from the bridge; run `make schema`", c.what, tok)
			}
		}
		for tok := range c.committed {
			if _, ok := c.fresh[tok]; !ok {
				t.Errorf("%s: %s is in the committed schema but no longer bridged; run `make schema`", c.what, tok)
			}
		}
	}
	for name, fresh := range fresh.Config.Variables {
		committed, ok := committed.Config.Variables[name]
		if !ok || !bytes.Equal(schemaContract(t, committed), schemaContract(t, fresh)) {
			t.Errorf("config: %s property contract differs from the bridge; run `make schema`", name)
		}
	}
	for name := range committed.Config.Variables {
		if _, ok := fresh.Config.Variables[name]; !ok {
			t.Errorf("config: %s is committed but no longer bridged; run `make schema`", name)
		}
	}
}

func TestExplorationGroupIDIsAnOptionalString(t *testing.T) {
	for name, s := range map[string]packageSchema{"fresh": generatedSchema(t), "committed": committedSchema(t)} {
		var resource struct {
			InputProperties map[string]struct {
				Type string `json:"type"`
			} `json:"inputProperties"`
			RequiredInputs []string `json:"requiredInputs"`
		}
		if err := json.Unmarshal(s.Resources["betterstack:index/exploration:Exploration"], &resource); err != nil {
			t.Fatalf("%s exploration schema: %v", name, err)
		}
		if got := resource.InputProperties["explorationGroupId"].Type; got != "string" {
			t.Errorf("%s explorationGroupId type = %q, want string, like the group's id", name, got)
		}
		for _, required := range resource.RequiredInputs {
			if required == "explorationGroupId" {
				t.Errorf("%s explorationGroupId must remain optional", name)
			}
		}
	}
}

func TestGeneratedSDKsExposeOptionalExplorationGroupID(t *testing.T) {
	for _, tc := range []struct {
		path, want string
	}{
		{"../sdk/go/betterstack/exploration.go", "ExplorationGroupId pulumi.StringPtrInput"},
		{"../sdk/nodejs/exploration.ts", "explorationGroupId?: pulumi.Input<string | undefined>;"},
		{"../sdk/python/pulumi_betterstack/exploration.py", "exploration_group_id: pulumi.Input[Optional[_builtins.str]] = None"},
		{"../sdk/dotnet/Exploration.cs", "public Input<string>? ExplorationGroupId"},
	} {
		raw, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if !bytes.Contains(raw, []byte(tc.want)) {
			t.Errorf("%s does not expose the optional string %q", tc.path, tc.want)
		}
	}
}

func committedSchema(t *testing.T) packageSchema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("cmd", "pulumi-resource-betterstack", "schema.json"))
	if err != nil {
		t.Fatalf("reading the committed schema: %v", err)
	}
	var s packageSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal committed schema: %v", err)
	}
	return s
}

func schemaContract(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("unmarshal schema contract: %v", err)
	}
	removeDescriptions(value)
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal schema contract: %v", err)
	}
	return canonical
}

func removeDescriptions(value interface{}) {
	switch value := value.(type) {
	case map[string]interface{}:
		delete(value, "description")
		for _, child := range value {
			removeDescriptions(child)
		}
	case []interface{}:
		for _, child := range value {
			removeDescriptions(child)
		}
	}
}

func configKeys(s packageSchema) []string {
	out := make([]string, 0, len(s.Config.Variables))
	for k := range s.Config.Variables {
		out = append(out, k)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
