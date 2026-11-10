package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestAPIServedKindsAndMasking (SPEC 9.6 dynamic kinds, RES sensitive
// fields): the /openapi.json `lore api` serves carries a route and a
// schema for each resource kind of the repo (with its fields), and none
// for a kind that does not exist; a resource's sensitive field comes back
// masked from GET /v1/resources/<kind> while a plain field is readable.
func TestAPIServedKindsAndMasking(t *testing.T) {
	envScopeRepo(t)
	const pw = "hunter2-plain-value"
	for _, args := range [][]string{
		{"resource", "kind", "add", "host", "--description", "a server", "--yes"},
		{"resource", "field", "add", "host", "ip", "--type", "text", "--yes"},
		{"resource", "field", "add", "host", "admin_pw", "--type", "text", "--sensitive", "--yes"},
		{"resource", "add", "host", "web1", "--set", "ip=10.0.0.1", "--set", "admin_pw=" + pw, "--yes"},
	} {
		if code, out, errOut := runCaptured(t, args...); code != 0 {
			t.Fatalf("lore %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errOut)
		}
	}
	h := apiServer(t)

	status, body := apiDo(t, h, http.MethodGet, "/openapi.json", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /openapi.json: %d %s", status, body)
	}
	var doc struct {
		Paths      map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Paths["/v1/resources/host"] == nil {
		t.Errorf("served openapi has no /v1/resources/host route")
	}
	schema := string(doc.Components.Schemas["kind.host"])
	if !strings.Contains(schema, `"ip"`) || !strings.Contains(schema, `"admin_pw"`) {
		t.Errorf("kind.host schema %s, want the fields ip and admin_pw", schema)
	}
	if doc.Paths["/v1/resources/server"] != nil || doc.Components.Schemas["kind.server"] != nil {
		t.Errorf("served openapi names a kind that does not exist")
	}

	status, body = apiDo(t, h, http.MethodGet, "/v1/resources/host", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/resources/host: %d %s", status, body)
	}
	if strings.Contains(body, pw) || !strings.Contains(body, "10.0.0.1") || !strings.Contains(body, `"admin_pw":"`) {
		t.Errorf("resource list %s: want admin_pw masked and ip readable", body)
	}
}
