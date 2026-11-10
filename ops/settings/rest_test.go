package settings_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/surface/rest"
)

// restToken is the bearer token of the test server (not a credential:
// a fixed string the test server accepts).
const restToken = "test-token-settings"

// TestRESTRouteMatrix: every settings operation has a REST route, none
// answers a caller without the token (closed by default) or with a wrong
// one, and the routes work end to end with it: policy set asks first and
// writes with "yes", the read routes see the row.
func TestRESTRouteMatrix(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := rest.New(h.App.Registry(), h.App, rest.Options{Token: restToken, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	routes := map[string]op.Route{}
	for _, e := range settings.Ops(h.App) {
		rt, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			t.Errorf("%s has no REST route", e.Spec().Name)
			continue
		}
		routes[e.Spec().Name] = rt
		path := strings.ReplaceAll(strings.ReplaceAll(rt.Path, "{id}", "x"), "{", "")
		for _, tok := range []string{"", "Bearer wrong", "Bearer " + restToken + "x", restToken} {
			code, _ := call(t, ts, rt.Method, path, tok, `{}`)
			if code != http.StatusUnauthorized {
				t.Errorf("%s %s with auth %q: %d, want 401", rt.Method, path, tok, code)
			}
		}
	}
	auth := "Bearer " + restToken
	set := routes[settings.OpPolicySet]
	if code, body := call(t, ts, set.Method, set.Path, auth, `{"key":"meta.max_bytes","value":"99"}`); code == http.StatusOK || code == http.StatusCreated || !strings.Contains(body, "E_NEEDS_CONFIRM") {
		t.Errorf("policy set without yes: %d %s", code, body)
	}
	if code, body := call(t, ts, set.Method, set.Path, auth, `{"key":"meta.max_bytes","value":"99","yes":true}`); code != set.Status || !strings.Contains(body, `"pol_`) {
		t.Errorf("policy set: %d %s", code, body)
	}
	show := routes[settings.OpPolicyShow]
	code, body := call(t, ts, show.Method, show.Path, auth, ``)
	var out settings.PolicyOut
	if err := json.Unmarshal([]byte(body), &out); code != http.StatusOK || err != nil {
		t.Fatalf("policy show: %d %s %v", code, body, err)
	}
	for _, r := range out.Data {
		if r.Key == "meta.max_bytes" && (r.Value != "99" || r.Level != "repo") {
			t.Errorf("policy over REST %+v", r)
		}
	}
	vs := routes[settings.OpVarsSet]
	if code, body := call(t, ts, vs.Method, vs.Path, auth, `{"name":"region","value":"eu","yes":true}`); code != vs.Status {
		t.Errorf("vars set: %d %s", code, body)
	}
	vl := routes[settings.OpVarsList]
	if code, body := call(t, ts, vl.Method, vl.Path, auth, ``); code != http.StatusOK || !strings.Contains(body, `"region"`) {
		t.Errorf("vars list: %d %s", code, body)
	}
}

// call sends one request; auth is the Authorization header ("" = none).
func call(t *testing.T, ts *httptest.Server, method, path, auth, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" && method != http.MethodGet {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}
