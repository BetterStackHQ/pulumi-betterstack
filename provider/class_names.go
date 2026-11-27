package provider

import (
	"strings"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/shim"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	md "github.com/pulumi/pulumi-terraform-bridge/v3/unstable/metadata"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
)

// className is a resource's name in the SDKs: the schema's own spelling where its title capitalises a
// word inside the name (PagerDutyIntegration), else the Terraform name in PascalCase, as the bridge
// derives it.
func className(resource string) string {
	for _, c := range shim.ClassNames() {
		if c.Resource == resource {
			return c.Name
		}
	}
	return pascal(strings.TrimPrefix(resource, "betterstack_"))
}

// applyClassNames re-tokens the resources and data sources the schema spells differently, after the
// bridge derived every token from the Terraform names. Nested types take their names from these
// tokens, and the auto-aliases record the old ones, so existing stacks keep working.
func applyClassNames(info *tfbridge.ProviderInfo) {
	for _, c := range shim.ClassNames() {
		if res := info.Resources[c.Resource]; res != nil {
			res.Tok = tokens.Type("betterstack:index/" + lowerFirst(c.Name) + ":" + c.Name)
		}
		if ds := info.DataSources[c.Resource]; ds != nil {
			ds.Tok = tokens.ModuleMember("betterstack:index/get" + c.Name + ":get" + c.Name)
		}
	}
}

// recordCaseOnlyRenames records a token that changes only its capitals as a plain alias, before the
// bridge's auto-aliasing reads its history. Left to the bridge, the old token would stay in the SDKs
// as a deprecated <resource>_legacy class, and Pulumi rejects two tokens that differ only in case.
// A resource keeps the old type as an alias, so existing stacks move to the new one. A function holds
// no state, so it needs no alias.
func recordCaseOnlyRenames(info *tfbridge.ProviderInfo) {
	const key = "auto-aliasing"
	hist, ok, err := md.Get[map[string]map[string]map[string]any](info.GetMetadata(), key)
	contract.AssertNoErrorf(err, "reading the auto-aliasing history")
	if !ok {
		return
	}
	current := map[string]map[string]string{"resources": {}, "datasources": {}}
	for tf, res := range info.Resources {
		current["resources"][tf] = res.Tok.String()
	}
	for tf, ds := range info.DataSources {
		current["datasources"][tf] = ds.Tok.String()
	}
	for kind, entries := range hist {
		for tf, entry := range entries {
			old, _ := entry["current"].(string)
			tok := current[kind][tf]
			if tok == "" || old == tok || !strings.EqualFold(old, tok) {
				continue
			}
			entry["current"] = tok
			if kind == "resources" {
				past, _ := entry["past"].([]any)
				entry["past"] = append(past, map[string]any{"name": old, "inCodegen": false, "majorVersion": entry["majorVersion"]})
			}
		}
	}
	contract.AssertNoErrorf(md.Set(info.GetMetadata(), key, hist), "writing the auto-aliasing history")
}
