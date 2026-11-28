package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	pftfbridge "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfbridge"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfgen"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

var (
	metadataOnce sync.Once
	metadataVal  pftfbridge.ProviderMetadata
	metadataErr  error
)

// providerMetadata generates the bridge metadata once per package run: tfgen walks every resource
// schema, which is the slow part of each test, and the result is immutable, so every test shares it.
func providerMetadata(t *testing.T) pftfbridge.ProviderMetadata {
	t.Helper()
	metadataOnce.Do(func() {
		res, err := tfgen.GenerateSchema(context.Background(), tfgen.GenerateSchemaOptions{
			ProviderInfo:    Provider(),
			DiagnosticsSink: diag.DefaultSink(io.Discard, io.Discard, diag.FormatOptions{Color: colors.Never}),
			XInMemoryDocs:   true,
		})
		if err != nil {
			metadataErr = err
			return
		}
		metadataVal = res.ProviderMetadata
	})
	if metadataErr != nil {
		t.Fatalf("GenerateSchema: %v", metadataErr)
	}
	return metadataVal
}

func mustStruct(m map[string]interface{}) *structpb.Struct {
	if m == nil {
		m = map[string]interface{}{}
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(err)
	}
	return s
}

// Drives a full create→delete through the bridged provider's gRPC server against a mock Better Stack
// API, proving the Pulumi runtime path (RPC → bridge → TF resource → JSON:API client) end to end.
// The underlying Terraform provider ships no runtime tests, so this is the only CRUD coverage.
//
// The mock serves the path-versioned public endpoint (/api/v3/monitors) and asserts the write body is
// the flat attributes ({"url":...}), which is what the incidents group's envelope declares.
func TestMonitorLifecycle(t *testing.T) {
	// The shim is built inside NewServer with the Version of that moment, so the override has to
	// be in place before the server exists; restoring it afterwards cannot change a built server.
	// The metadata is generated first so the override never leaks into the package-wide cache.
	meta := providerMetadata(t)
	for _, tc := range []struct {
		name, version string
	}{
		{"default version", Version},
		{"overridden version", "9.9.9-test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := Version
			Version = tc.version
			t.Cleanup(func() { Version = previous })
			runMonitorLifecycle(t, meta, "pulumi-betterstack/"+tc.version+" (unified-api "+tc.version+")")
		})
	}
}

func runMonitorLifecycle(t *testing.T, meta pftfbridge.ProviderMetadata, wantUserAgent string) {
	var sawCreate, sawDelete bool
	var createBody []byte
	var agents []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/monitors", func(w http.ResponseWriter, r *http.Request) {
		sawCreate = true
		agents = append(agents, r.UserAgent())
		createBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"42","type":"monitor","attributes":{"url":"https://example.com","monitor_type":"status"}}}`))
	})
	mux.HandleFunc("/api/v3/monitors/42", func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.UserAgent())
		if r.Method == http.MethodDelete {
			sawDelete = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"42","type":"monitor","attributes":{"url":"https://example.com","monitor_type":"status"}}}`))
	})
	mock := httptest.NewServer(mux)
	defer mock.Close()

	ctx := context.Background()
	srv, err := NewServer(ctx, nil, meta)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if _, err := srv.Configure(ctx, &pulumirpc.ConfigureRequest{
		Args:            mustStruct(map[string]interface{}{"apiToken": "test-token", "endpoint": mock.URL}),
		AcceptSecrets:   true,
		AcceptResources: true,
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	const urn = "urn:pulumi:test::test::betterstack:index/monitor:Monitor::m"
	chk, err := srv.Check(ctx, &pulumirpc.CheckRequest{
		Urn:  urn,
		Olds: mustStruct(nil),
		// Off, so the delete this test checks is allowed.
		News: mustStruct(map[string]interface{}{"url": "https://example.com", "monitorType": "status", "deletionProtection": false}),
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(chk.GetFailures()) > 0 {
		t.Fatalf("Check failures: %v", chk.GetFailures())
	}

	cr, err := srv.Create(ctx, &pulumirpc.CreateRequest{Urn: urn, Properties: chk.GetInputs()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !sawCreate {
		t.Error("mock API never received the create POST")
	}
	if got := string(createBody); !strings.Contains(got, `"url":"https://example.com"`) || strings.Contains(got, `"data":`) {
		t.Errorf("incidents declares request_path [], so the body must be the flat attributes: %s", got)
	}
	if cr.GetId() != "42" {
		t.Errorf("created id = %q, want 42", cr.GetId())
	}

	if _, err := srv.Delete(ctx, &pulumirpc.DeleteRequest{Urn: urn, Id: "42", Properties: cr.GetProperties()}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !sawDelete {
		t.Error("mock API never received the delete")
	}
	if len(agents) == 0 {
		t.Fatal("mock API saw no requests")
	}
	for _, agent := range agents {
		if agent != wantUserAgent {
			t.Errorf("request sent User-Agent %q, want %q", agent, wantUserAgent)
		}
	}
}

// A field the API accepts only on update reaches it through a follow-up PATCH. When that PATCH
// fails the object already exists, so the failure must keep its identity: Pulumi has to learn the
// id and the values the API really holds, or the object is orphaned and the next up duplicates it.
func TestUpdateOnlyPauseIsAppliedAfterCreation(t *testing.T) {
	for _, mode := range []string{"success", "failed patch retains identity", "failed read retains identity"} {
		t.Run(mode, func(t *testing.T) {
			failPatch := mode != "success"
			failRead := mode == "failed read retains identity"
			var methods []string
			paused := false
			mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				respond := func(status int) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
						"id": "42", "type": "monitor_group",
						"attributes": map[string]interface{}{"name": "group", "paused": paused},
					}})
				}
				if r.Method == http.MethodGet {
					if failRead {
						http.Error(w, "read unavailable", http.StatusForbidden)
						return
					}
					// Recovery reads the object back; the rejected pause must not be echoed here.
					if r.URL.Path != "/api/v3/monitor-groups/42" {
						t.Errorf("unexpected read: %s", r.URL.Path)
					}
					respond(http.StatusOK)
					return
				}
				var body map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "invalid body", http.StatusBadRequest)
					return
				}
				// Incidents writes are flat: the attributes are the body.
				attrs := body
				switch r.Method {
				case http.MethodPost:
					if _, present := attrs["paused"]; present {
						t.Error("POST received update-only paused")
					}
					if r.URL.Path != "/api/v3/monitor-groups" || attrs["name"] != "group" {
						t.Errorf("unexpected create: %s %v", r.URL.Path, body)
					}
					respond(http.StatusCreated)
				case http.MethodPatch:
					if r.URL.Path != "/api/v3/monitor-groups/42" || len(attrs) != 1 || attrs["paused"] != true {
						t.Errorf("expected PATCH of paused only, got %s %s %v", r.Method, r.URL.Path, body)
					}
					if failPatch {
						w.WriteHeader(http.StatusUnprocessableEntity)
						_, _ = io.WriteString(w, `{"errors":"pause rejected"}`)
						return
					}
					paused = true
					respond(http.StatusOK)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				}
			}))
			defer mock.Close()

			ctx := context.Background()
			srv, err := NewServer(ctx, nil, providerMetadata(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := srv.Configure(ctx, &pulumirpc.ConfigureRequest{
				Args:          mustStruct(map[string]interface{}{"apiToken": "test", "endpoint": mock.URL}),
				AcceptSecrets: true, AcceptResources: true,
			}); err != nil {
				t.Fatal(err)
			}
			const urn = "urn:pulumi:test::test::betterstack:index/monitorGroup:MonitorGroup::group"
			checked, err := srv.Check(ctx, &pulumirpc.CheckRequest{
				Urn: urn, Olds: mustStruct(nil), News: mustStruct(map[string]interface{}{"name": "group", "paused": true}),
			})
			if err != nil || len(checked.GetFailures()) > 0 {
				t.Fatalf("Check: %v %v", checked.GetFailures(), err)
			}
			created, err := srv.Create(ctx, &pulumirpc.CreateRequest{Urn: urn, Properties: checked.GetInputs()})
			if !failPatch {
				if strings.Join(methods, ",") != "POST,PATCH" {
					t.Fatalf("expected create then update, got %v", methods)
				}
				if err != nil || created.GetId() != "42" || created.GetProperties().AsMap()["paused"] != true {
					t.Fatalf("Create: %v %v", created, err)
				}
				return
			}
			if err == nil {
				t.Fatal("failed PATCH must fail creation")
			}
			// The object exists, so recovery reads it back before reporting the failure.
			if strings.Join(methods, ",") != "POST,PATCH,GET" {
				t.Fatalf("expected create, failed update, then read-back, got %v", methods)
			}
			for _, detail := range status.Convert(err).Details() {
				failure, ok := detail.(*pulumirpc.ErrorResourceInitFailed)
				if !ok {
					continue
				}
				if failure.GetId() != "42" {
					t.Fatalf("failed PATCH lost the created resource identity: %v", failure)
				}
				if failRead {
					if len(failure.GetProperties().AsMap()) != 0 {
						t.Fatal("failed read must not report planned values as observed state")
					}
					return
				}
				// Live values, not the planned ones: a state claiming paused=true would look
				// converged and the rejected field would never be retried.
				props := failure.GetProperties().AsMap()
				if props["paused"] != false || props["name"] != "group" {
					t.Fatalf("partial create must carry the properties the API really holds: %v", props)
				}
				if len(failure.GetReasons()) == 0 || !strings.Contains(strings.Join(failure.GetReasons(), " "), "pause rejected") {
					t.Fatalf("the API error must survive: %v", failure.GetReasons())
				}
				return
			}
			t.Fatalf("failed PATCH lost the created resource identity: %v", err)
		})
	}
}

// Removing an optional Pulumi input must reach the bridged Terraform resource as a null update,
// without requiring a Terraform-only DynamicAttribute value that an SDK cannot represent.
func TestExplorationGroupCanBeClearedByRemovingThePulumiInput(t *testing.T) {
	attrs := map[string]interface{}{}
	sawNull := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/explorations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		// The path API takes flat attributes, and the draft takes a group id only as a string.
		if body["data"] != nil || body["exploration_group_id"] != "17" {
			http.Error(w, "expected a flat body with exploration_group_id \"17\"", http.StatusBadRequest)
			return
		}
		attrs = body
		writeExplorationResponse(w, http.StatusCreated, attrs)
	})
	mux.HandleFunc("/api/v3/explorations/41", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			http.Error(w, "expected PATCH", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid update", http.StatusBadRequest)
			return
		}
		update := body
		value, present := update["exploration_group_id"]
		if !present || value != nil {
			http.Error(w, "expected explicit null exploration_group_id", http.StatusBadRequest)
			return
		}
		sawNull = true
		for key, value := range update {
			attrs[key] = value
		}
		writeExplorationResponse(w, http.StatusOK, attrs)
	})
	mock := httptest.NewServer(mux)
	defer mock.Close()

	ctx := context.Background()
	srv, err := NewServer(ctx, nil, providerMetadata(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if _, err := srv.Configure(ctx, &pulumirpc.ConfigureRequest{
		Args: mustStruct(map[string]interface{}{
			"apiToken": "test-token", "endpoint": mock.URL, "teamName": "platform",
		}),
		AcceptSecrets: true, AcceptResources: true,
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	const urn = "urn:pulumi:test::test::betterstack:index/exploration:Exploration::e"
	inputs := func(group bool) *structpb.Struct {
		values := map[string]interface{}{
			"name":    "nullable-contract",
			"chart":   map[string]interface{}{"chartType": "line_chart"},
			"queries": []interface{}{map[string]interface{}{"queryType": "sql_expression"}},
		}
		if group {
			values["explorationGroupId"] = "17"
		}
		return mustStruct(values)
	}
	checkedCreate, err := srv.Check(ctx, &pulumirpc.CheckRequest{Urn: urn, Olds: mustStruct(nil), News: inputs(true)})
	if err != nil || len(checkedCreate.GetFailures()) > 0 {
		t.Fatalf("create Check: failures=%v err=%v", checkedCreate.GetFailures(), err)
	}
	created, err := srv.Create(ctx, &pulumirpc.CreateRequest{Urn: urn, Properties: checkedCreate.GetInputs()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	checkedUpdate, err := srv.Check(ctx, &pulumirpc.CheckRequest{Urn: urn, Olds: created.GetProperties(), News: inputs(false)})
	if err != nil || len(checkedUpdate.GetFailures()) > 0 {
		t.Fatalf("update Check: failures=%v err=%v", checkedUpdate.GetFailures(), err)
	}
	diff, err := srv.Diff(ctx, &pulumirpc.DiffRequest{
		Urn: urn, Id: "41", Olds: created.GetProperties(), News: checkedUpdate.GetInputs(),
	})
	if err != nil || diff.GetChanges() == pulumirpc.DiffResponse_DIFF_NONE {
		t.Fatalf("removing explorationGroupId must produce an update diff: diff=%v err=%v", diff, err)
	}
	if _, err := srv.Update(ctx, &pulumirpc.UpdateRequest{
		Urn: urn, Id: "41", Olds: created.GetProperties(), News: checkedUpdate.GetInputs(),
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !sawNull {
		t.Fatal("Pulumi input removal did not send exploration_group_id as JSON null")
	}
}

func writeExplorationResponse(w http.ResponseWriter, status int, attrs map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data": map[string]interface{}{"id": "41", "type": "exploration", "attributes": attrs},
	})
}

// Fields the API accepts only on create (`only: ["create"]`) must travel in the POST and never in a
// later PATCH — the mock rejects them outright — and changing one must replace the resource.
func TestCollectorCreateOnlyFieldsNeverReachAnUpdate(t *testing.T) {
	createOnly := []string{"platform", "data_region", "custom_bucket"}
	attrs := map[string]interface{}{}
	var createBody, patchBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/collectors", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		createBody = body
		for key, value := range createBody {
			attrs[key] = value
		}
		writeCollectorResponse(w, http.StatusCreated, attrs)
	})
	mux.HandleFunc("/api/v4/collectors/7", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			http.Error(w, "expected PATCH", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		patchBody = body
		for _, field := range createOnly {
			if _, present := patchBody[field]; present {
				http.Error(w, "create-only field "+field+" rejected on update", http.StatusUnprocessableEntity)
				return
			}
		}
		for key, value := range patchBody {
			attrs[key] = value
		}
		writeCollectorResponse(w, http.StatusOK, attrs)
	})
	mock := httptest.NewServer(mux)
	defer mock.Close()

	ctx := context.Background()
	srv, err := NewServer(ctx, nil, providerMetadata(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if _, err := srv.Configure(ctx, &pulumirpc.ConfigureRequest{
		Args: mustStruct(map[string]interface{}{
			"apiToken": "test-token", "endpoint": mock.URL, "teamName": "platform",
		}),
		AcceptSecrets: true, AcceptResources: true,
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	const urn = "urn:pulumi:test::test::betterstack:index/collector:Collector::c"
	inputs := func(name, platform string) *structpb.Struct {
		return mustStruct(map[string]interface{}{
			"name":       name,
			"platform":   platform,
			"dataRegion": "us-east",
			// Off, so the replacement this test checks for is allowed.
			"deletionProtection": false,
			"customBucket": map[string]interface{}{
				"endpoint":    "https://s3.example.com",
				"accessKeyId": "AKIA", "secretAccessKey": "shh",
			},
		})
	}
	checkedCreate, err := srv.Check(ctx, &pulumirpc.CheckRequest{Urn: urn, Olds: mustStruct(nil), News: inputs("collector", "docker")})
	if err != nil || len(checkedCreate.GetFailures()) > 0 {
		t.Fatalf("create Check: failures=%v err=%v", checkedCreate.GetFailures(), err)
	}
	created, err := srv.Create(ctx, &pulumirpc.CreateRequest{Urn: urn, Properties: checkedCreate.GetInputs()})
	if err != nil || created.GetId() != "7" {
		t.Fatalf("Create: %v %v", created, err)
	}
	if createBody["platform"] != "docker" || createBody["data_region"] != "us-east" || createBody["custom_bucket"] == nil {
		t.Fatalf("create-only fields must travel in the POST: %v", createBody)
	}

	checkedRename, err := srv.Check(ctx, &pulumirpc.CheckRequest{
		Urn: urn, Olds: created.GetProperties(), News: inputs("collector-renamed", "docker"),
	})
	if err != nil || len(checkedRename.GetFailures()) > 0 {
		t.Fatalf("rename Check: failures=%v err=%v", checkedRename.GetFailures(), err)
	}
	renameDiff, err := srv.Diff(ctx, &pulumirpc.DiffRequest{
		Urn: urn, Id: "7", Olds: created.GetProperties(), News: checkedRename.GetInputs(),
	})
	if err != nil || renameDiff.GetChanges() == pulumirpc.DiffResponse_DIFF_NONE || len(renameDiff.GetReplaces()) > 0 {
		t.Fatalf("renaming is an in-place update: diff=%v err=%v", renameDiff, err)
	}
	updated, err := srv.Update(ctx, &pulumirpc.UpdateRequest{
		Urn: urn, Id: "7", Olds: created.GetProperties(), News: checkedRename.GetInputs(),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if patchBody["name"] != "collector-renamed" {
		t.Fatalf("the rename must reach the PATCH: %v", patchBody)
	}

	checkedReplace, err := srv.Check(ctx, &pulumirpc.CheckRequest{
		Urn: urn, Olds: updated.GetProperties(), News: inputs("collector-renamed", "swarm"),
	})
	if err != nil || len(checkedReplace.GetFailures()) > 0 {
		t.Fatalf("replace Check: failures=%v err=%v", checkedReplace.GetFailures(), err)
	}
	replaceDiff, err := srv.Diff(ctx, &pulumirpc.DiffRequest{
		Urn: urn, Id: "7", Olds: updated.GetProperties(), News: checkedReplace.GetInputs(),
	})
	if err != nil {
		t.Fatalf("replace Diff: %v", err)
	}
	if !slices.Contains(replaceDiff.GetReplaces(), "platform") {
		t.Fatalf("changing a create-only field must replace the resource: %v", replaceDiff)
	}
	for _, change := range []struct {
		name  string
		apply func(*structpb.Struct)
	}{
		{"bucket endpoint", func(news *structpb.Struct) {
			news.Fields["customBucket"].GetStructValue().Fields["endpoint"] = structpb.NewStringValue("https://s3.example.com/other-archive")
		}},
		{"previously omitted bucket option", func(news *structpb.Struct) {
			news.Fields["customBucket"].GetStructValue().Fields["keepDataAfterRetention"] = structpb.NewBoolValue(false)
		}},
		{"bucket removal", func(news *structpb.Struct) { delete(news.Fields, "customBucket") }},
	} {
		t.Run(change.name, func(t *testing.T) {
			news := inputs("collector-renamed", "docker")
			change.apply(news)
			checked, err := srv.Check(ctx, &pulumirpc.CheckRequest{Urn: urn, Olds: updated.GetProperties(), News: news})
			if err != nil || len(checked.GetFailures()) > 0 {
				t.Fatalf("bucket Check: failures=%v err=%v", checked.GetFailures(), err)
			}
			diff, err := srv.Diff(ctx, &pulumirpc.DiffRequest{Urn: urn, Id: "7", Olds: updated.GetProperties(), News: checked.GetInputs()})
			bucketReplaced := slices.ContainsFunc(diff.GetReplaces(), func(path string) bool {
				return path == "customBucket" || strings.HasPrefix(path, "customBucket.")
			})
			if err != nil || !bucketReplaced {
				t.Fatalf("changing an immutable bucket must replace the collector: diff=%v err=%v", diff, err)
			}
		})
	}
}

func writeCollectorResponse(w http.ResponseWriter, status int, attrs map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data": map[string]interface{}{"id": "7", "type": "collector", "attributes": attrs},
	})
}

// pulumi destroy calls Delete with no plan step, so the Terraform plan checks never run. The delete
// itself refuses while the state says deletion protection is on, and no DELETE reaches the API.
func TestProtectedCollectorIsNotDeletedByADestroy(t *testing.T) {
	var deletes []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/collectors", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		writeCollectorResponse(w, http.StatusCreated, body)
	})
	mux.HandleFunc("/api/v4/collectors/7", func(w http.ResponseWriter, r *http.Request) {
		deletes = append(deletes, r.Method+" "+r.URL.String())
		w.WriteHeader(http.StatusNoContent)
	})
	mock := httptest.NewServer(mux)
	defer mock.Close()

	ctx := context.Background()
	logs := &recordingSink{}
	srv, err := NewServer(ctx, logs, providerMetadata(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if _, err := srv.Configure(ctx, &pulumirpc.ConfigureRequest{
		Args:          mustStruct(map[string]interface{}{"apiToken": "test-token", "endpoint": mock.URL, "teamName": "platform"}),
		AcceptSecrets: true, AcceptResources: true,
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	const urn = "urn:pulumi:test::test::betterstack:index/collector:Collector::c"
	create := func(news map[string]interface{}) *pulumirpc.CreateResponse {
		t.Helper()
		checked, err := srv.Check(ctx, &pulumirpc.CheckRequest{Urn: urn, Olds: mustStruct(nil), News: mustStruct(news)})
		if err != nil || len(checked.GetFailures()) > 0 {
			t.Fatalf("Check: failures=%v err=%v", checked.GetFailures(), err)
		}
		created, err := srv.Create(ctx, &pulumirpc.CreateRequest{Urn: urn, Properties: checked.GetInputs()})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		return created
	}

	protected := create(map[string]interface{}{"name": "collector", "platform": "docker"})
	if protected.GetProperties().GetFields()["deletionProtection"].GetBoolValue() != true {
		t.Fatalf("a new collector must start protected: %v", protected.GetProperties())
	}
	_, err = srv.Delete(ctx, &pulumirpc.DeleteRequest{Urn: urn, Id: "7", Properties: protected.GetProperties()})
	if err == nil || !strings.Contains(status.Convert(err).Message(), "Deletion protection is on") {
		t.Fatalf("Delete of a protected collector must fail with the deletion protection error, got %v", err)
	}
	// Pulumi's own words for turning it off, not Terraform's.
	for _, want := range []string{"This update destroys collector \"collector\" which is an irreversible action.", "Set deletionProtection: false and run pulumi up to allow destroying it."} {
		if !strings.Contains(status.Convert(err).Message(), want) {
			t.Errorf("the deletion protection error must name %q under Pulumi, got %q", want, status.Convert(err).Message())
		}
	}
	if strings.Contains(status.Convert(err).Message(), "deletion_protection = false") {
		t.Errorf("the deletion protection error gives Terraform's fix under Pulumi: %q", status.Convert(err).Message())
	}
	// Said once: the bridge's own log line of the same error would repeat it in the other order.
	if msg := status.Convert(err).Message(); strings.Count(msg, "Deletion protection is on") != 1 || strings.Count(msg, "irreversible action") != 1 {
		t.Errorf("the deletion protection error must say it once, got %q", msg)
	}
	for _, line := range logs.errors() {
		if strings.Contains(line, "Deletion protection is on") {
			t.Errorf("the deletion protection error is logged as well as returned: %q", line)
		}
	}
	if len(deletes) != 0 {
		t.Fatalf("a protected collector reached the API: %v", deletes)
	}

	unprotected := create(map[string]interface{}{"name": "collector", "platform": "docker", "deletionProtection": false})
	if _, err := srv.Delete(ctx, &pulumirpc.DeleteRequest{Urn: urn, Id: "7", Properties: unprotected.GetProperties()}); err != nil {
		t.Fatalf("Delete with protection off: %v", err)
	}
	if len(deletes) != 1 || !strings.HasPrefix(deletes[0], "DELETE ") {
		t.Fatalf("expected one DELETE once protection is off, got %v", deletes)
	}
}
