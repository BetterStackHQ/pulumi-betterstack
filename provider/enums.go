package provider

import (
	"strings"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/shim"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	"github.com/pulumi/pulumi/pkg/v3/codegen/schema"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// applyEnums gives every fixed-value input an enum type, so IDEs complete and document its values.
// The enum is an alternative type, as AWS and DigitalOcean add theirs: a plain string still type
// checks, so the enums break no program, and a value the API adds before the next release still works.
func applyEnums(info *tfbridge.ProviderInfo) {
	if info.ExtraTypes == nil {
		info.ExtraTypes = map[string]schema.ComplexTypeSpec{}
	}
	for _, e := range shim.Enums() {
		res := info.Resources[e.Resource]
		if res == nil {
			continue
		}
		// Named after the resource and the input, without saying the resource twice: a monitor's
		// monitor_type is MonitorType, a source's platform is SourcePlatform.
		resource := strings.TrimPrefix(e.Resource, "betterstack_")
		name := className(e.Resource) + pascal(e.Attribute)
		if strings.HasPrefix(e.Attribute, resource+"_") {
			name = pascal(e.Attribute)
		}
		tok := "betterstack:index/" + lowerFirst(name) + ":" + name
		if _, taken := info.ExtraTypes[tok]; taken {
			panic("two enums named " + name)
		}
		values := make([]schema.EnumValueSpec, len(e.Values))
		for i, v := range e.Values {
			values[i] = schema.EnumValueSpec{Value: v, Name: enumMemberName(v)}
		}
		info.ExtraTypes[tok] = schema.ComplexTypeSpec{ObjectTypeSpec: schema.ObjectTypeSpec{Type: "string"}, Enum: values}
		if res.Fields == nil {
			res.Fields = map[string]*tfbridge.SchemaInfo{}
		}
		field := res.Fields[e.Attribute]
		if field == nil {
			field = &tfbridge.SchemaInfo{}
			res.Fields[e.Attribute] = field
		}
		// A string first, so the input is the union string | enum.
		if field.Type == "" {
			field.Type = "string"
		}
		field.AltTypes = append(field.AltTypes, tokens.Type(tok))
	}
}

// enumMemberName spells a value as an SDK member: expected_status_code is ExpectedStatusCode and
// syslog-ng is SyslogNg, where Pulumi would write Expected_status_code.
func enumMemberName(value string) string {
	return pascal(strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(value))
}
