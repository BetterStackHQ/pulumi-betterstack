package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// A status page's password turns password protection on, unless the same request sends
// passwordEnabled too. Inputs that set only the password leave that to the API on create and on an
// update that changes the password, and then plan no changes.
func TestStatusPagePasswordAloneTurnsProtectionOn(t *testing.T) {
	var mu sync.Mutex
	attrs := map[string]interface{}{}
	var sentEnabled []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body", http.StatusBadRequest)
				return
			}
			if _, ok := body["password_enabled"]; ok {
				sentEnabled = append(sentEnabled, r.Method)
			}
			// As uptime's API: a password without password_enabled turns protection on, and an
			// empty one turns it off. The password itself is never returned.
			if password, ok := body["password"]; ok {
				if _, sent := body["password_enabled"]; !sent {
					body["password_enabled"] = password != nil && password != ""
				}
				delete(body, "password")
			}
			for key, value := range body {
				attrs[key] = value
			}
			if _, ok := attrs["password_enabled"]; !ok {
				attrs["password_enabled"] = false
			}
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v5/status-pages":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v5/status-pages/9":
			w.WriteHeader(http.StatusNoContent)
			return
		case r.URL.Path == "/api/v5/status-pages/9":
		default:
			http.Error(w, "unexpected status page route "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{"id": "9", "type": "status_page", "attributes": attrs},
		})
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
	page := func(password string) map[string]interface{} {
		return map[string]interface{}{
			"companyName": "Example", "subdomain": "example-password", "timezone": "UTC",
			"password": password, "deletionProtection": false,
		}
	}
	lifecycle := resourceLifecycle{ctx: ctx, srv: srv, urn: "urn:pulumi:test::test::betterstack:index/statusPage:StatusPage::page"}
	if err := lifecycle.run(page("first"), page("second")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attrs["password_enabled"] != true {
		t.Errorf("the API stores password_enabled %v, want true", attrs["password_enabled"])
	}
	if len(sentEnabled) > 0 {
		t.Errorf("password_enabled was sent on %v though the inputs leave it out", sentEnabled)
	}
}
