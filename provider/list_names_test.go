package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// No property in the package schema the SDKs are generated from (every resource, data source and
// nested type, inputs and outputs) is a _by field the bridge pluralized into ...ies, as it turned a
// query's group_by into groupBies. A camelCase name ending in By is a Terraform name ending in _by,
// and the bridge's inflector makes it ...Bies.
func TestNoByFieldIsPluralizedIntoIes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("cmd", "pulumi-resource-betterstack", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec interface{}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var bad []string
	var walk func(where string, v interface{})
	walk = func(where string, v interface{}) {
		switch v := v.(type) {
		case map[string]interface{}:
			for key, child := range v {
				if props, ok := child.(map[string]interface{}); ok && (key == "properties" || key == "inputProperties") {
					for name := range props {
						seen[name] = true
						if strings.HasSuffix(name, "Bies") || name == "bies" {
							bad = append(bad, where+"."+name)
						}
					}
				}
				walk(where+"."+key, child)
			}
		case []interface{}:
			for _, child := range v {
				walk(where, child)
			}
		}
	}
	walk("", spec)
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("%s: a _by list named in the plural, want its camelCase Terraform name", b)
	}
	for _, want := range []string{"groupBy", "orderBy"} {
		if !seen[want] {
			t.Errorf("no property is named %s: the check would pass on a schema without the query fields", want)
		}
	}
}

// The provider answers a query's group_by and order_by under the names the SDKs read, groupBy and
// orderBy, so a program's outputs hold the blocks the API returned.
func TestQueryGroupByReachesTheProgramAsGroupBy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/explorations", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		block := map[string]interface{}{"explore_block_type": "field", "value": "level"}
		body["queries"] = []interface{}{map[string]interface{}{
			"query_type": "query_builder", "group_by": []interface{}{block}, "order_by": []interface{}{block},
		}}
		writeExplorationResponse(w, http.StatusCreated, body)
	})
	mock := httptest.NewServer(mux)
	defer mock.Close()

	ctx := context.Background()
	srv, err := NewServer(ctx, nil, providerMetadata(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if _, err := srv.Configure(ctx, &pulumirpc.ConfigureRequest{
		Args:          mustStruct(map[string]interface{}{"apiToken": "test-token", "endpoint": mock.URL, "teamName": "platform"}),
		AcceptSecrets: true, AcceptResources: true,
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	const urn = "urn:pulumi:test::test::betterstack:index/exploration:Exploration::e"
	news := mustStruct(map[string]interface{}{
		"name":    "grouped",
		"chart":   map[string]interface{}{"chartType": "line_chart"},
		"queries": []interface{}{map[string]interface{}{"queryType": "query_builder"}},
	})
	checked, err := srv.Check(ctx, &pulumirpc.CheckRequest{Urn: urn, Olds: mustStruct(nil), News: news})
	if err != nil || len(checked.GetFailures()) > 0 {
		t.Fatalf("Check: failures=%v err=%v", checked.GetFailures(), err)
	}
	created, err := srv.Create(ctx, &pulumirpc.CreateRequest{Urn: urn, Properties: checked.GetInputs()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	query := created.GetProperties().AsMap()["queries"].([]interface{})[0].(map[string]interface{})
	for _, name := range []string{"groupBy", "orderBy"} {
		blocks, _ := query[name].([]interface{})
		if len(blocks) != 1 || blocks[0].(map[string]interface{})["value"] != "level" {
			t.Errorf("queries[0].%s = %v, want the API's one block reading level (query: %v)", name, query[name], query)
		}
	}
}
