package provider

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every rule must still match the generated docs or schema: a rule that matches nothing means the Terraform
// text changed and the Pulumi docs carry it again. After the rules, no Terraform-only instruction is
// left in a resource or data source page, or in the committed Pulumi schema.
func TestTerraformTextRulesMatchAndLeaveNoTerraformInstructions(t *testing.T) {
	resources, _ := filepath.Glob(filepath.Join("..", "..", "generated", "terraform", "docs", "resources", "*.md"))
	dataSources, _ := filepath.Glob(filepath.Join("..", "..", "generated", "terraform", "docs", "data-sources", "*.md"))
	pages := append(resources, dataSources...)
	if len(pages) == 0 {
		t.Fatal("no generated Terraform docs found")
	}
	hits := map[string]int{}
	leftover := regexp.MustCompile("moved \\{\\}|`removed` block|Terraform state|Terraform resource|Terraform surface|Terraform keeps|Terraform take over|Terraform hides|Terraform always|Terraform refuses|With Terraform|terraform output|terraform query|`terraform import`|deletion_protection = false")
	edit := terraformTextEdit()
	for _, page := range pages {
		raw, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range terraformTextRules {
			if r.pattern.Match(raw) {
				hits[r.what]++
			}
		}
		out, _ := edit.Edit(filepath.Base(page), raw)
		prose := regexp.MustCompile("(?ms)^```.*?^```").ReplaceAllString(string(out), "")
		if m := leftover.FindString(prose); m != "" {
			t.Errorf("%s still says %q for Pulumi", strings.TrimPrefix(page, "../../"), m)
		}
	}
	// Descriptions reach the bridge from the Terraform schema as well, rewritten by the same rules.
	sources, _ := filepath.Glob(filepath.Join("..", "..", "generated", "terraform", "internal", "provider", "*.go"))
	for _, src := range sources {
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range terraformTextRules {
			if r.pattern.Match(raw) {
				hits[r.what]++
			}
		}
	}
	if m := leftover.FindString(committedSchemaText(t)); m != "" {
		t.Errorf("the committed Pulumi schema still says %q; run `make schema`", m)
	}
	for _, r := range terraformTextRules {
		if hits[r.what] == 0 {
			t.Errorf("the %q rule matches no generated docs page; update it to the Terraform text", r.what)
		}
	}
}
