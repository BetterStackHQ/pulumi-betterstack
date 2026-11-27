package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/shim"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	tfshim "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim/walk"
)

// The runtime converts values by the attribute path, not the path schema generation takes, so every
// singular list must read under its own name there too. A list only the schema renamed would be a
// property the SDKs offer and the provider drops: headersTemplate sent, nothing written.
func TestSingularListsKeepTheirNameAtRuntime(t *testing.T) {
	info := Provider()
	applyRuntimeSingularLists(&info)
	for _, l := range shim.SingularLists() {
		var kinds []bridged
		if r := info.Resources[l.Resource]; r != nil {
			kinds = append(kinds, bridged{"resource", info.P.ResourcesMap().Get(l.Resource), r.Fields})
		}
		if d := info.DataSources[l.Resource]; d != nil {
			kinds = append(kinds, bridged{"data source", info.P.DataSourcesMap().Get(l.Resource), d.Fields})
		}
		if len(kinds) == 0 {
			t.Errorf("%s: no resource or data source", l.Resource)
		}
		for _, k := range kinds {
			sp, want := runtimePath(t, k.res, l.Path)
			got, err := tfbridge.TerraformToPulumiNameAtPath(sp, k.res.Schema(), k.fields)
			if err != nil || got != want {
				t.Errorf("%s %s %s = %q (%v), want %q", k.kind, l.Resource, l.Path, got, err, want)
			}
		}
	}
}

type bridged struct {
	kind   string
	res    tfshim.Resource
	fields map[string]*tfbridge.SchemaInfo
}

// Schema generation refuses fields set where only the runtime reads them, so Provider(), which it
// uses, must not have them: tfgen would stop with ".Fields should be .Elem.Fields".
func TestSingularListsLeaveTheSchemaInfoValid(t *testing.T) {
	info := Provider()
	if err := info.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// runtimePath is the bridge's path to a field or field.subfield: a list or set adds an element step.
func runtimePath(t *testing.T, res tfshim.Resource, path string) (walk.SchemaPath, string) {
	t.Helper()
	sp := walk.NewSchemaPath()
	var last string
	obj := res
	parts := strings.Split(path, ".")
	for i, part := range parts {
		sp = sp.GetAttr(part)
		last = part
		if i == len(parts)-1 {
			break
		}
		s := obj.Schema().Get(part)
		if s.Type() == tfshim.TypeList || s.Type() == tfshim.TypeSet {
			sp = sp.Element()
		}
		nested, _ := nestedObject(s, &tfbridge.SchemaInfo{})
		if nested == nil {
			t.Fatalf("%s: %s holds no object", path, part)
		}
		obj = nested
	}
	return sp, lowerFirst(pascal(last))
}
