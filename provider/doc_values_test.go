package provider

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/shim"
	pftfbridge "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfbridge"
	shimv1 "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim"
)

// The bridge rewrites a lowercase word in backticks, and a snake_case word in double quotes, into the
// property name it would be in each language, and records the original in the span's
// pulumi-lang-hcl attribute. Right for an attribute name; wrong for a value the API takes, which then
// reads reactErrors in TypeScript and Go and Europe in C#.
var renamedWord = regexp.MustCompile(`pulumi-lang-hcl=\\?"([` + "`" + `"])([^"\\` + "`" + `]+)[` + "`" + `"]\\?"`)

// committedSchemaText is the schema `make schema` wrote, the one the plugin and SDKs ship. The bridge
// runs docs edits only there: the in-memory generation the other tests use skips them.
func committedSchemaText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("cmd", "pulumi-resource-betterstack", "schema.json"))
	if err != nil {
		t.Fatalf("reading the committed schema: %v", err)
	}
	return string(raw)
}

// A docs value the bridge renames is a value Pulumi users would copy and the API would refuse.
func TestDocsRenameOnlyAttributeNames(t *testing.T) {
	names := terraformNames()
	var bad []string
	seen := map[string]bool{}
	for _, m := range renamedWord.FindAllStringSubmatch(committedSchemaText(t), -1) {
		word := m[2]
		if names[word] || seen[word] {
			continue
		}
		seen[word] = true
		bad = append(bad, word)
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("the Pulumi docs rename these values as if they were properties: %s. Run `make schema`; if they stay, "+
			"QuoteAPIValues (resolve_go.erb) needs to recognise them as values", strings.Join(bad, ", "))
	}
}

// The bridge's Markdown pass pairs underscores across quoted values, so 'react_errors', 'vue_errors'
// came out as 'react*errors', 'vue*errors'. QuoteAPIValues escapes them; this catches a regression.
var emphasisedValue = regexp.MustCompile(`'[0-9a-z]+\*[0-9a-z*]*'`)

func TestDocsKeepUnderscoresInValues(t *testing.T) {
	if found := emphasisedValue.FindAllString(committedSchemaText(t), 5); len(found) > 0 {
		t.Errorf("the Pulumi docs turned underscores in values into emphasis: %s", strings.Join(found, ", "))
	}
}

// A schema description reaches every SDK's docs as written, so an escape meant for the bridge's
// Markdown pass shows as a backslash there: 'expected\_status\_code' is no value the API takes.
var escapedValue = regexp.MustCompile(`'[0-9a-z]*\\_[0-9a-z\\_]*'`)

func TestDocsShowValuesWithoutEscapes(t *testing.T) {
	if found := escapedValue.FindAllString(strings.ReplaceAll(committedSchemaText(t), `\\`, `\`), 5); len(found) > 0 {
		t.Errorf("the Pulumi docs show values with escaped underscores: %s", strings.Join(found, ", "))
	}
}

func TestQuoteDescriptionValuesDoesNotEscape(t *testing.T) {
	in := "What it checks.\n\n- `expected_status_code`: Checks the status code.\n- `keyword_absence`: Checks the page."
	want := "What it checks.\n\n- 'expected_status_code': Checks the status code.\n- 'keyword_absence': Checks the page."
	if got := shim.QuoteDescriptionValues(in, nil); got != want {
		t.Errorf("QuoteDescriptionValues:\n got %q\nwant %q", got, want)
	}
}

// Every attribute name, nested ones included, and every resource and data source type without the
// provider prefix: the words the bridge is right to rename.
func terraformNames() map[string]bool {
	p := pftfbridge.ShimProvider(shim.New("pulumi-betterstack", Version))
	names := map[string]bool{}
	var walk func(shimv1.SchemaMap)
	walk = func(m shimv1.SchemaMap) {
		m.Range(func(key string, s shimv1.Schema) bool {
			names[key] = true
			switch elem := s.Elem().(type) {
			case shimv1.Resource:
				walk(elem.Schema())
			case shimv1.Schema:
				if r, ok := elem.Elem().(shimv1.Resource); ok {
					walk(r.Schema())
				}
			}
			return true
		})
	}
	for _, m := range []shimv1.ResourceMap{p.ResourcesMap(), p.DataSourcesMap()} {
		m.Range(func(key string, r shimv1.Resource) bool {
			names[strings.TrimPrefix(key, "betterstack_")] = true
			walk(r.Schema())
			return true
		})
	}
	return names
}

func TestQuoteAPIValues(t *testing.T) {
	names := map[string]bool{"platform": true, "data_region": true}
	in := "- `platform` (String) Valid values: `react_errors`, `vue_errors`. Defaults to `true`.\n" +
		"> Use `data_region` \"us_east\" or `europe`.\n```hcl\nx = `a_b`\n```\n"
	want := "- `platform` (String) Valid values: 'react\\_errors', 'vue\\_errors'. Defaults to true.\n" +
		"> Use `data_region` 'us_east' or 'europe'.\n```hcl\nx = `a_b`\n```\n"
	if got := shim.QuoteAPIValues(in, names); got != want {
		t.Errorf("QuoteAPIValues:\n got %q\nwant %q", got, want)
	}
}

// In a list of valid values, or a default, a word is a value even where an attribute has its name.
func TestQuoteAPIValuesInValueLists(t *testing.T) {
	names := map[string]bool{"monitor_type": true, "status": true}
	in := "- `monitor_type` (String) Valid values: `status`, `keyword_absence`. Defaults to `status`. See `status`."
	want := "- `monitor_type` (String) Valid values: 'status', 'keyword\\_absence'. Defaults to 'status'. See `status`."
	if got := shim.QuoteAPIValues(in, names); got != want {
		t.Errorf("QuoteAPIValues:\n got %q\nwant %q", got, want)
	}
}
