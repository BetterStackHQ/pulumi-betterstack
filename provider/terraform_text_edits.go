package provider

import (
	"regexp"

	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
)

// terraformTextRule rewrites one sentence the generated Terraform docs write for Terraform users:
// a migration from the old providers, Terraform state and commands, the removed block. Pulumi has no
// older package to migrate from, and its users act through pulumi up, retainOnDelete and secrets.
type terraformTextRule struct {
	what    string
	pattern *regexp.Regexp
	pulumi  string
}

var terraformTextRules = []terraformTextRule{
	{"migration note", regexp.MustCompile("\n\n-> Replaces [^\n]*Existing state moves over with a `moved \\{\\}` block; see the \\[migration guide\\]\\([^)]*\\)\\."), ""},
	{"deletion protection",
		regexp.MustCompile("so `deletion_protection` is on by default: Terraform refuses to destroy or replace it until you set `deletion_protection = false` and apply\\."),
		"so `deletion_protection` is on by default: a preview, an update or a pulumi destroy that destroys or replaces it fails until you set `deletion_protection` to `false` and run pulumi up."},
	{"retained delete", regexp.MustCompile("Destroying this resource only removes it from Terraform state\\."), "Destroying this resource only removes it from the Pulumi state."},
	{"adopted id", regexp.MustCompile("Changing it replaces this Terraform resource\\."), "Changing it replaces this resource."},
	{"adopted key", regexp.MustCompile("Changing it makes Terraform take over that object instead\\."), "Changing it takes over that object instead."},
	{"endpoint family", regexp.MustCompile("One Terraform resource maps to one endpoint family; use `(?:terraform|pulumi) import` or the data source for objects created elsewhere\\."),
		"One resource maps to one endpoint family; use pulumi import or the matching get function for objects created elsewhere."},
	{"read-only object", regexp.MustCompile("so the data source is its only Terraform surface\\."), "so this function is its only Pulumi surface."},
	{"write-only value", regexp.MustCompile("so Terraform keeps the value you set\\."), "so Pulumi keeps the value you set."},
	{"write-only values",
		regexp.MustCompile("so Terraform keeps the values you set\\. A name added or removed outside Terraform shows in the plan\\."),
		"so Pulumi keeps the values you set. A name added or removed outside Pulumi shows in the preview."},
	{"sensitive value",
		regexp.MustCompile("To print it, declare an output with `sensitive = true` and run `terraform output -raw <name>`\\."),
		"To print it, export it as a stack output and run pulumi stack output with its name and --show-secrets."},
	// Pulumi takes the same map in its own syntax, so only the HCL example goes.
	{"map wrapper example", regexp.MustCompile(", for example `\\{ owner = \\{ values = \\[ \\.\\.\\. \\] \\} \\}`\\."), "."},
	// Pulumi's enum type lists the values, and the page's section the link goes to isn't in its docs.
	{"values section", regexp.MustCompile(" ?See the \\[[0-9]+ valid values\\]\\(#values--[^)]*\\)\\."), ""},
	{"item key", regexp.MustCompile("In Terraform, an item without an id"), "In Pulumi, an item without an id"},
	// Import: Pulumi has its own import section from the shell example, so the Terraform 1.5 block goes,
	// and the lookups are named the way Pulumi names them. The bridge's own rules run first and turn the
	// block's backticks and `terraform import` into Pulumi's, so the pattern takes either.
	{"import block", regexp.MustCompile("With Terraform 1\\.5 or later, use an [`']import[`'] block:(?s:.*?)Or run [`'](?:terraform|pulumi) import[`']:\\n\\n"), ""},
	{"import list resource", regexp.MustCompile(" `terraform query` with the \\[`betterstack_[a-z_]+` list resource\\]\\([^)]*\\) lists them all, ready to import\\."), ""},
	{"import data source", regexp.MustCompile("([Tt])he \\[`betterstack_[a-z_]+` data source\\]\\([^)]*\\)"), "${1}he matching get function"},
}

// pulumiText applies terraformTextRules to a docs page or a schema description.
func pulumiText(text string) string {
	for _, r := range terraformTextRules {
		text = r.pattern.ReplaceAllString(text, r.pulumi)
	}
	return text
}

// terraformTextEdit applies terraformTextRules to every docs page the bridge reads.
func terraformTextEdit() tfbridge.DocsEdit {
	return tfbridge.DocsEdit{
		Path: "*",
		Edit: func(_ string, content []byte) ([]byte, error) { return []byte(pulumiText(string(content))), nil },
	}
}
