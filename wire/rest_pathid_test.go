package wire_test

// rest_pathid_test.go — every REST route with {id} binds it to an input
// field its operation takes. Before, show, edit and delete always had a
// /{id} path, and an operation with no "id" field (setup.show, setup.edit,
// whoami.show, identity.show, task.template.show, …: 21 routes) answered
// every call with E_INVALID_INPUT "the request body does not fit".

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/surface/docs"
	"github.com/thesatellite-ai/lore/surface/rest"
)

func TestRESTPathIDBindsToAField(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	seen := map[string]string{}
	for _, e := range h.App.Registry().Entries() {
		rt, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			continue
		}
		key := rt.Method + " " + rt.Path
		if prev, dup := seen[key]; dup {
			t.Errorf("%s: both %s and %s", key, prev, e.Spec().Name)
		}
		seen[key] = e.Spec().Name
		if !docs.PathHasID(rt.Path) {
			continue
		}
		name := docs.PathIDName(e.InputFields(), rt.Path)
		var f *op.FieldInfo
		for _, in := range e.InputFields() {
			if in.Name == name {
				f = &in
			}
		}
		if f == nil || f.Type != op.TypeString {
			t.Errorf("%s (%s): the path {id} fills no string input field (got %q)", key, e.Spec().Name, name)
		}
	}
	// The server builds: no route collides on the mux.
	srv, err := rest.New(h.App.Registry(), h.App, rest.Options{Token: restToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := func(method, path string) (int, string) {
		t.Helper()
		req, rerr := http.NewRequestWithContext(context.Background(), method, ts.URL+path, nil)
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set("Authorization", "Bearer "+restToken)
		resp, derr := ts.Client().Do(req)
		if derr != nil {
			t.Fatal(derr)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	// Each reaches its operation (the test App lacks some release-only
	// parts, so an op's own E_UNSUPPORTED is fine; a missing route or the
	// decoder's refusal is not).
	for _, path := range []string{"/v1/whoami/show", "/v1/identities/show", "/v1/stores/show"} {
		if code, body := call(http.MethodGet, path); strings.Contains(body, "no route") || strings.Contains(body, "does not fit") {
			t.Errorf("GET %s: %d %s, want the operation's answer", path, code, body)
		}
	}
	// A path id bound to a name argument reaches the operation: a missing
	// template is the operation's not-found, never the decoder's refusal.
	if code, body := call(http.MethodGet, "/v1/task/templates/no-such-template"); strings.Contains(body, "does not fit") || code == http.StatusOK {
		t.Errorf("GET task template: %d %s, want the op's own answer", code, body)
	}
}
