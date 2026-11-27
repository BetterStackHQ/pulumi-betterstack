package provider

import (
	"strings"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/shim"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// applyIDInputs names the inputs that carry a resource's id. Pulumi reserves id, so the bridge
// renamed each to <resource>Id: onCallRotationId for what is the calendar's id, and
// errorsSlackIntegrationId for the existing integration a resource adopts. They become calendarId
// and integrationId, and the resource's Pulumi id is taken from that input, as Datadog does for teamId.
func applyIDInputs(info *tfbridge.ProviderInfo) {
	for _, in := range shim.IDInputs() {
		res := info.Resources[in.Resource]
		if res == nil {
			continue
		}
		name := lowerFirst(pascal(in.Name))
		// A resource with no id attribute is keyed by the input itself (source_id), which keeps its
		// name: only the Pulumi id comes from it.
		if tf, ok := info.P.ResourcesMap().GetOk(in.Resource); ok {
			if _, hasID := tf.Schema().GetOk("id"); !hasID {
				res.ComputeID = tfbridge.DelegateIDField(resource.PropertyKey(name), "betterstack", info.Repository)
				continue
			}
		}
		if res.Fields == nil {
			res.Fields = map[string]*tfbridge.SchemaInfo{}
		}
		res.Fields["id"] = &tfbridge.SchemaInfo{Name: name}
		res.ComputeID = tfbridge.DelegateIDField(resource.PropertyKey(name), "betterstack", info.Repository)
	}
}

func pascal(snake string) string {
	var b strings.Builder
	for _, part := range strings.Split(snake, "_") {
		if part != "" {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}

func lowerFirst(s string) string { return strings.ToLower(s[:1]) + s[1:] }
