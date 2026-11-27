package provider

import (
	"path/filepath"
	"strings"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/shim"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	shimv1 "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim"
)

// apiValueEdit writes the API values in each resource's and data source's docs page so the bridge
// keeps them as written (shim.QuoteAPIValues). The schema's own descriptions get the same treatment
// inside the provider, under Pulumi.
func apiValueEdit(p shimv1.Provider) tfbridge.DocsEdit {
	names := attributeNamesByPage(p)
	return tfbridge.DocsEdit{
		Path: "*",
		Edit: func(path string, content []byte) ([]byte, error) {
			page := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			return []byte(shim.QuoteAPIValues(string(content), names[page])), nil
		},
	}
}

// attributeNamesByPage maps a docs page (the type without the provider prefix) to the attribute
// names of its resource and data source, nested ones included. Type names are not among them: a
// bare one is an API value more often (a step member's type webhook), and pages name a type in full.
func attributeNamesByPage(p shimv1.Provider) map[string]map[string]bool {
	pages := map[string]map[string]bool{}
	var walk func(shimv1.SchemaMap, map[string]bool)
	walk = func(m shimv1.SchemaMap, into map[string]bool) {
		m.Range(func(key string, s shimv1.Schema) bool {
			into[key] = true
			switch elem := s.Elem().(type) {
			case shimv1.Resource:
				walk(elem.Schema(), into)
			case shimv1.Schema:
				if r, ok := elem.Elem().(shimv1.Resource); ok {
					walk(r.Schema(), into)
				}
			}
			return true
		})
	}
	for _, m := range []shimv1.ResourceMap{p.ResourcesMap(), p.DataSourcesMap()} {
		m.Range(func(key string, r shimv1.Resource) bool {
			page := strings.TrimPrefix(key, "betterstack_")
			if pages[page] == nil {
				pages[page] = map[string]bool{}
			}
			walk(r.Schema(), pages[page])
			return true
		})
	}
	return pages
}
