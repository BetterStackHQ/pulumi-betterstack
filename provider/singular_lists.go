package provider

import (
	"strings"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/shim"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	tfshim "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim"
)

// applySingularLists keeps the Terraform name of a list that already names the whole list. The bridge
// adds an s to every list whose name doesn't end in one, which reads right for a list of things
// (tag_filter is tagFilters) and wrong for a name that is itself the list: an ip_allowlist would be
// ipAllowlists and a series_names_except seriesNamesExcepts.
//
// This is the name schema generation reads, and so the one the SDKs spell. A list inside a nested
// object also needs applyRuntimeSingularLists, or the provider drops its value.
func applySingularLists(info *tfbridge.ProviderInfo) {
	eachSingularList(info, func(obj tfshim.Resource, fields map[string]*tfbridge.SchemaInfo, path string) {
		nameList(obj, fields, path, schemaStep)
	})
}

// applyRuntimeSingularLists names the same lists where the runtime looks when it converts values.
// The two disagree about an object nested in a field: schema generation finds its fields on the
// field's Elem (and refuses them anywhere else), while the plugin-framework conversion reads them
// from the field's own Fields, stepping into Elem only for a list or set element. Only the runtime
// gets this, as with capturingShim, so schema generation sees exactly the info it validates.
func applyRuntimeSingularLists(info *tfbridge.ProviderInfo) {
	eachSingularList(info, func(obj tfshim.Resource, fields map[string]*tfbridge.SchemaInfo, path string) {
		nameList(obj, fields, path, runtimeStep)
	})
}

func eachSingularList(info *tfbridge.ProviderInfo, apply func(tfshim.Resource, map[string]*tfbridge.SchemaInfo, string)) {
	for _, l := range shim.SingularLists() {
		if res := info.Resources[l.Resource]; res != nil {
			if res.Fields == nil {
				res.Fields = map[string]*tfbridge.SchemaInfo{}
			}
			apply(info.P.ResourcesMap().Get(l.Resource), res.Fields, l.Path)
		}
		if ds := info.DataSources[l.Resource]; ds != nil {
			if ds.Fields == nil {
				ds.Fields = map[string]*tfbridge.SchemaInfo{}
			}
			apply(info.P.DataSourcesMap().Get(l.Resource), ds.Fields, l.Path)
		}
	}
}

// A step finds the SchemaInfo that holds the fields of the object a field holds.
type step func(s tfshim.Schema, fi *tfbridge.SchemaInfo) *tfbridge.SchemaInfo

func schemaStep(s tfshim.Schema, fi *tfbridge.SchemaInfo) *tfbridge.SchemaInfo {
	_, objInfo := nestedObject(s, fi)
	return objInfo
}

func runtimeStep(s tfshim.Schema, fi *tfbridge.SchemaInfo) *tfbridge.SchemaInfo {
	if s.Type() == tfshim.TypeList || s.Type() == tfshim.TypeSet {
		if fi.Elem == nil {
			fi.Elem = &tfbridge.SchemaInfo{}
		}
		return fi.Elem
	}
	return fi
}

// nameList names the list at path (field or field.subfield) as its Terraform name in camelCase.
func nameList(obj tfshim.Resource, fields map[string]*tfbridge.SchemaInfo, path string, next step) {
	parts := strings.Split(path, ".")
	for i, part := range parts {
		s, ok := obj.Schema().GetOk(part)
		if !ok {
			panic("pulumi_singular names " + path + ", which has no " + part)
		}
		fi := fields[part]
		if fi == nil {
			fi = &tfbridge.SchemaInfo{}
			fields[part] = fi
		}
		if i == len(parts)-1 {
			fi.Name = lowerFirst(pascal(part))
			return
		}
		nested, _ := nestedObject(s, &tfbridge.SchemaInfo{})
		if nested == nil {
			panic("pulumi_singular names " + path + ", but " + part + " holds no object")
		}
		objInfo := next(s, fi)
		if objInfo.Fields == nil {
			objInfo.Fields = map[string]*tfbridge.SchemaInfo{}
		}
		obj, fields = nested, objInfo.Fields
	}
}
