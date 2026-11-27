package provider

import (
	"strings"

	"github.com/pulumi/inflector"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	shim "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim"
)

// applyNestedTypeNames renames a nested object type whose default name is already a class of the SDKs.
// A dashboard's charts list is typed DashboardChart by default, the same name as the dashboard chart
// resource, so the .NET SDK has two DashboardChartArgs and a program has to spell out which one it
// means. A data source's nested type clashes the same way with a function (getDashboardChart) and
// with the Go SDK's lookup of a resource (GetDashboardChart).
//
// A nested type that holds only some of the resource's fields (a dashboard's charts: their place and
// name, no queries) is a summary of it, <Name>Summary. One that holds fields the resource doesn't (a
// status page's sections with their resources) is an entry of its parent's list, <Name>Item.
func applyNestedTypeNames(info *tfbridge.ProviderInfo) {
	taken := map[string]string{} // class name -> the Terraform resource it describes, or "" for a function
	for tf, res := range info.Resources {
		taken[upperFirst(res.Tok.Name().String())] = tf
		taken["Get"+upperFirst(res.Tok.Name().String())] = tf
	}
	for _, ds := range info.DataSources {
		if _, ok := taken[upperFirst(ds.Tok.Name().String())]; !ok {
			taken[upperFirst(ds.Tok.Name().String())] = ""
		}
	}
	info.P.ResourcesMap().Range(func(tf string, r shim.Resource) bool {
		if res := info.Resources[tf]; res != nil {
			if res.Fields == nil {
				res.Fields = map[string]*tfbridge.SchemaInfo{}
			}
			renameNested(info, taken, res.Tok.Name().String(), r, res.Fields)
		}
		return true
	})
	info.P.DataSourcesMap().Range(func(tf string, r shim.Resource) bool {
		if ds := info.DataSources[tf]; ds != nil {
			if ds.Fields == nil {
				ds.Fields = map[string]*tfbridge.SchemaInfo{}
			}
			renameNested(info, taken, ds.Tok.Name().String(), r, ds.Fields)
		}
		return true
	})
}

// renameNested walks every nested object of an object, at every depth, and names it as the bridge
// would (the parent's type name and the field's Pulumi name, singular for a list or set), unless that
// name is taken.
func renameNested(info *tfbridge.ProviderInfo, taken map[string]string, prefix string, obj shim.Resource, fields map[string]*tfbridge.SchemaInfo) {
	obj.Schema().Range(func(field string, s shim.Schema) bool {
		fi := fields[field]
		if fi == nil {
			fi = &tfbridge.SchemaInfo{}
		}
		nested, objInfo := nestedObject(s, fi)
		if nested == nil {
			return true
		}
		fields[field] = fi
		name := lowerFirst(pascal(field))
		if s.Type() == shim.TypeList || s.Type() == shim.TypeSet {
			name = inflector.Singularize(name)
		}
		typeName := prefix + upperFirst(name)
		if target, clash := taken[upperFirst(typeName)]; clash {
			if target != "" && subsetOf(nested, info.P.ResourcesMap().Get(target)) {
				typeName += "Summary"
			} else {
				typeName += "Item"
			}
			objInfo.TypeName = &typeName
		}
		if objInfo.Fields == nil {
			objInfo.Fields = map[string]*tfbridge.SchemaInfo{}
		}
		renameNested(info, taken, typeName, nested, objInfo.Fields)
		return true
	})
}

// nestedObject finds the object a field holds and the SchemaInfo the bridge reads for it. The bridge
// steps one Elem deeper in the info for each Elem it steps through in the schema: a nested block's
// object is the field's Elem, a nested attribute's is a map schema holding it, and a list of them
// adds the list's element.
func nestedObject(s shim.Schema, fi *tfbridge.SchemaInfo) (shim.Resource, *tfbridge.SchemaInfo) {
	switch elem := s.Elem().(type) {
	case shim.Resource:
		if fi.Elem == nil {
			fi.Elem = &tfbridge.SchemaInfo{}
		}
		return elem, fi.Elem
	case shim.Schema:
		if fi.Elem == nil {
			fi.Elem = &tfbridge.SchemaInfo{}
		}
		obj, objInfo := nestedObject(elem, fi.Elem)
		if obj == nil && fi.Elem.TypeName == nil && fi.Elem.Elem == nil && len(fi.Elem.Fields) == 0 {
			fi.Elem = nil
		}
		return obj, objInfo
	}
	return nil, nil
}

// subsetOf reports whether every field of a nested object is a field (or the id) of the resource.
func subsetOf(nested shim.Resource, resource shim.Resource) bool {
	if resource == nil {
		return false
	}
	subset := true
	nested.Schema().Range(func(field string, _ shim.Schema) bool {
		if _, ok := resource.Schema().GetOk(field); !ok && field != "id" {
			subset = false
		}
		return subset
	})
	return subset
}

func upperFirst(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
