package provider

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim/walk"
)

// pulumiPropertyPath spells a Terraform attribute path the way a Pulumi program writes it, with the
// bridge's own naming: camelCase, plural lists (tag_filter is tagFilters) and the field renames
// in info. The provider's replacement and deletion-protection messages use it under Pulumi.
func pulumiPropertyPath(info *tfbridge.ProviderInfo) func(resourceType string, p path.Path) string {
	return func(resourceType string, p path.Path) string {
		res, ok := info.P.ResourcesMap().GetOk(resourceType)
		if !ok {
			return p.String()
		}
		var fields map[string]*tfbridge.SchemaInfo
		if ri := info.Resources[resourceType]; ri != nil {
			fields = ri.Fields
		}
		sp := walk.NewSchemaPath()
		var b strings.Builder
		for _, step := range p.Steps() {
			switch s := step.(type) {
			case path.PathStepAttributeName:
				sp = sp.GetAttr(string(s))
				name, err := tfbridge.TerraformToPulumiNameAtPath(sp, res.Schema(), fields)
				if err != nil {
					return p.String()
				}
				if b.Len() > 0 {
					b.WriteByte('.')
				}
				b.WriteString(name)
			case path.PathStepElementKeyInt:
				sp = sp.Element()
				fmt.Fprintf(&b, "[%d]", int64(s))
			case path.PathStepElementKeyString:
				sp = sp.Element()
				fmt.Fprintf(&b, "[%q]", string(s))
			default:
				sp = sp.Element()
			}
		}
		return b.String()
	}
}
