package wire_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/surface/rest"
)

// restToken is the bearer token of the test server (not a credential).
const restToken = "test-token"

// TestRESTResourceHistoryReportKeepAsText (later 510, OV49): the resource
// operations added in OV49 answer through `lore api` on the real registry
// and engine: field retype with keep_as_text (confirmed with yes),
// resource history (sensitive values masked over REST too, --field
// narrowing) and the resource report; retype without yes needs
// confirmation (428), as on the CLI.
func TestRESTResourceHistoryReportKeepAsText(t *testing.T) {
	h := opstest.New(t)
	srv, err := rest.New(h.App.Registry(), h.App, rest.Options{Token: restToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := func(method, path, body string, want int) string {
		t.Helper()
		req, rerr := http.NewRequestWithContext(context.Background(), method, ts.URL+path, strings.NewReader(body))
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set("Authorization", "Bearer "+restToken)
		req.Header.Set("Content-Type", "application/json")
		resp, derr := ts.Client().Do(req)
		if derr != nil {
			t.Fatal(derr)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d %s, want %d", method, path, resp.StatusCode, b, want)
		}
		return string(b)
	}
	call(http.MethodPost, "/v1/resource/kinds", `{"kind":"host","yes":true}`, http.StatusCreated)
	call(http.MethodPost, "/v1/resource/fields", `{"kind":"host","field":"os","type":"text","yes":true}`, http.StatusCreated)
	call(http.MethodPost, "/v1/resource/fields", `{"kind":"host","field":"ip","type":"text","sensitive":true,"yes":true}`, http.StatusCreated)
	call(http.MethodPost, "/v1/resource/fields", `{"kind":"host","field":"cut_over","type":"text","yes":true}`, http.StatusCreated)
	added := call(http.MethodPost, "/v1/resources", `{"kind":"host","name":"hub","set":["os=ubuntu-22.04","ip=203.0.113.10","cut_over=soon"],"yes":true}`, http.StatusCreated)
	var res struct {
		ID string `json:"id"`
	}
	if jerr := json.Unmarshal([]byte(added), &res); jerr != nil || !strings.HasPrefix(res.ID, "res_") {
		t.Fatalf("add: %s %v", added, jerr)
	}
	call(http.MethodPatch, "/v1/resources/hub", `{"set":["os=ubuntu-24.04","ip=203.0.113.11"],"yes":true}`, http.StatusOK)

	hist := call(http.MethodPost, "/v1/resources/hub/history", `{}`, http.StatusOK)
	if !strings.Contains(hist, "ubuntu-22.04") || !strings.Contains(hist, "ubuntu-24.04") {
		t.Fatalf("history over REST: %s", hist)
	}
	if strings.Contains(hist, "203.0.113") {
		t.Fatalf("history over REST leaks a sensitive value: %s", hist)
	}
	if one := call(http.MethodPost, "/v1/resources/hub/history", `{"field":"os"}`, http.StatusOK); strings.Contains(one, `"ip"`) {
		t.Fatalf("history field=os still lists ip: %s", one)
	}
	call(http.MethodPost, "/v1/resource/fields/retype", `{"kind":"host","field":"cut_over","type":"date","keep_as_text":true}`, http.StatusPreconditionRequired)
	call(http.MethodPost, "/v1/resource/fields/retype", `{"kind":"host","field":"cut_over","type":"date","keep_as_text":true,"yes":true}`, http.StatusOK)
	// GET /v1/resources/<name> is the dynamic-kind route (kindroutes.go):
	// one resource is shown by its res_ id.
	if show := call(http.MethodGet, "/v1/resources/"+res.ID, "", http.StatusOK); !strings.Contains(show, "cut_over") || !strings.Contains(show, "quarantin") {
		t.Fatalf("show after retype --keep-as-text over REST: %s", show)
	}
	if rep := call(http.MethodPost, "/v1/resources/report", `{}`, http.StatusOK); !strings.Contains(rep, "quarantin") {
		t.Fatalf("report over REST: %s", rep)
	}
}
