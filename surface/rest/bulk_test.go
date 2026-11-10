package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// bulkIn is the input of an operation whose id is optional: one row by id,
// or many by --kind/--where (CLI_REFERENCE 3.2).
type bulkIn struct {
	ID     string   `arg:"0" json:"id,omitempty" desc:"the row"`
	Kind   string   `flag:"kind" json:"kind,omitempty" desc:"the kind"`
	Where  []string `flag:"where" json:"where,omitempty" desc:"a filter"`
	DryRun bool     `flag:"dry-run" json:"dry_run" desc:"show only"`
	Set    []string `flag:"set" json:"set,omitempty" desc:"a field"`
}

// reqIn is the input of an operation whose id is required.
type reqIn struct {
	ID string `arg:"0" json:"id" desc:"the row"`
}

// echoOut returns what the operation received.
type echoOut struct {
	Op string `json:"op"`
	In string `json:"in"`
}

func bulkFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{viaRunner: map[string]int{}}
	echo := func(name string) func(context.Context, bulkIn) (echoOut, error) {
		return func(ctx context.Context, in bulkIn) (echoOut, error) {
			check(ctx, f)
			b, err := json.Marshal(in)
			return echoOut{Op: name, In: string(b)}, err
		}
	}
	del := op.Define(op.Spec{Name: "resource.delete", Summary: "Delete resources", Permission: op.PermWrite, Access: op.AccessWrite}, echo("resource.delete"))
	edit := op.Define(op.Spec{Name: "resource.edit", Summary: "Edit resources", Permission: op.PermWrite, Access: op.AccessWrite}, echo("resource.edit"))
	memDel := op.Define(op.Spec{Name: "memory.delete", Summary: "Delete a memory", Permission: op.PermWrite, Access: op.AccessWrite},
		func(ctx context.Context, in reqIn) (echoOut, error) {
			check(ctx, f)
			return echoOut{Op: "memory.delete", In: in.ID}, nil
		})
	memEdit := op.Define(op.Spec{Name: "memory.edit", Summary: "Edit a memory", Permission: op.PermWrite, Access: op.AccessWrite},
		func(ctx context.Context, in reqIn) (echoOut, error) {
			check(ctx, f)
			return echoOut{Op: "memory.edit", In: in.ID}, nil
		})
	reg, err := op.NewRegistry(del, edit, memDel, memEdit)
	if err != nil {
		t.Fatal(err)
	}
	f.reg = reg
	return f
}

// TestBulkCollectionTwin (P2-RES-1): an edit or delete whose id is
// optional also answers on the collection path, with the bulk filter from
// the query (DELETE) or the body (PATCH), reaching the same operation
// through the Runner; an operation whose id is required gets no twin; the
// by-id route still works for both.
func TestBulkCollectionTwin(t *testing.T) {
	f := bulkFake(t)
	h := newServer(t, f)
	decode := func(r reply) echoOut {
		t.Helper()
		var o echoOut
		if err := json.Unmarshal([]byte(r.body), &o); err != nil {
			t.Fatalf("body %q: %v", r.body, err)
		}
		return o
	}
	r := call(h, http.MethodDelete, "/v1/resources?kind=host&where=env%3Dprod&where=os%3Dlinux&dry_run=true", "", testToken)
	if r.status != http.StatusOK {
		t.Fatalf("bulk delete: %d %s", r.status, r.body)
	}
	if o := decode(r); o.Op != "resource.delete" || !strings.Contains(o.In, `"kind":"host"`) || !strings.Contains(o.In, `"where":["env=prod","os=linux"]`) || !strings.Contains(o.In, `"dry_run":true`) || strings.Contains(o.In, `"id"`) {
		t.Errorf("bulk delete input %+v", o)
	}
	r = call(h, http.MethodPatch, "/v1/resources", `{"kind":"host","where":["env=prod"],"set":["os=bsd"]}`, testToken)
	if o := decode(r); r.status != http.StatusOK || o.Op != "resource.edit" || !strings.Contains(o.In, `"set":["os=bsd"]`) || strings.Contains(o.In, `"id"`) {
		t.Errorf("bulk edit: %d %+v", r.status, o)
	}
	if o := decode(call(h, http.MethodDelete, "/v1/resources/res_1", "", testToken)); !strings.Contains(o.In, `"id":"res_1"`) {
		t.Errorf("by-id delete %+v", o)
	}
	if o := decode(call(h, http.MethodPatch, "/v1/resources/res_1", `{"set":["a=b"]}`, testToken)); !strings.Contains(o.In, `"id":"res_1"`) {
		t.Errorf("by-id edit %+v", o)
	}
	// A DELETE takes its input from the query, never a body.
	if r := call(h, http.MethodPatch, "/v1/resources?kind=host", ``, testToken); code(t, r) != errs.InvalidInput {
		t.Errorf("PATCH with a query: %d %s", r.status, r.body)
	}
	for _, m := range []string{http.MethodDelete, http.MethodPatch} {
		if r := call(h, m, "/v1/memories", ``, testToken); r.status != http.StatusNotFound && r.status != http.StatusMethodNotAllowed {
			t.Errorf("%s /v1/memories (required id) has a twin: %d %s", m, r.status, r.body)
		}
	}
	if o := decode(call(h, http.MethodDelete, "/v1/memories/mem_1", "", testToken)); o.In != "mem_1" {
		t.Errorf("memory delete by id %+v", o)
	}
	// The twin is still behind the token.
	if r := call(h, http.MethodDelete, "/v1/resources?kind=host", "", ""); r.status != http.StatusUnauthorized {
		t.Errorf("bulk delete without a token: %d", r.status)
	}
	if f.direct != 0 || f.viaRunner["resource.delete"] != 2 || f.viaRunner["resource.edit"] != 2 {
		t.Errorf("runner counts %v direct %d", f.viaRunner, f.direct)
	}
}

// TestSecretChoiceHeaders (P2-CTX-5, SECRETS 4 over REST): the secret
// answer travels in X-Lore-Not-A-Secret (reason), repeatable
// X-Lore-Secret-Ref and X-Lore-Secret-Unstored, and reaches the Runner as
// op.Caller.Secret; bad values are refused before the operation runs.
func TestSecretChoiceHeaders(t *testing.T) {
	long := strings.Repeat("r", maxSecretHeaderLen+1)
	cases := []struct {
		name string
		hdr  [][2]string
		want op.SecretChoice
		bad  bool
	}{
		{"none", nil, op.SecretChoice{}, false},
		{"not a secret with reason", [][2]string{{HeaderFalsePositive, "test fixture"}}, op.SecretChoice{NotASecret: true, Reason: "test fixture"}, false},
		{"not a secret, empty reason (engine refuses)", [][2]string{{HeaderFalsePositive, ""}}, op.SecretChoice{NotASecret: true}, false},
		{"unicode reason", [][2]string{{HeaderFalsePositive, "doc sample é"}}, op.SecretChoice{NotASecret: true, Reason: "doc sample é"}, false},
		{"lower-case header name", [][2]string{{"x-lore-not-a-secret", "x"}}, op.SecretChoice{NotASecret: true, Reason: "x"}, false},
		{"one ref", [][2]string{{HeaderVaultRef, "gopass://a/b"}}, op.SecretChoice{Refs: []string{"gopass://a/b"}}, false},
		{"two refs keep order", [][2]string{{HeaderVaultRef, "gopass://a"}, {HeaderVaultRef, "op://v/i/f"}}, op.SecretChoice{Refs: []string{"gopass://a", "op://v/i/f"}}, false},
		{"unstored true", [][2]string{{HeaderUnstoredFinding, "true"}}, op.SecretChoice{Unstored: true}, false},
		{"unstored false", [][2]string{{HeaderUnstoredFinding, "false"}}, op.SecretChoice{}, false},
		{"unstored not a bool", [][2]string{{HeaderUnstoredFinding, "yes"}}, op.SecretChoice{}, true},
		{"reason too long", [][2]string{{HeaderFalsePositive, long}}, op.SecretChoice{}, true},
		{"ref too long", [][2]string{{HeaderVaultRef, long}}, op.SecretChoice{}, true},
		{"control character", [][2]string{{HeaderVaultRef, "gopass://a\x7f"}}, op.SecretChoice{}, true},
	}
	for _, c := range cases {
		f := newFake(t)
		h := newServer(t, f)
		req := newReq(http.MethodPost, "/v1/memories", `{"body":"x"}`)
		for _, kv := range c.hdr {
			req.Header.Add(kv[0], kv[1])
		}
		r := serve(h, req)
		if c.bad {
			if code(t, r) != errs.InvalidInput || f.viaRunner["memory.add"] != 0 {
				t.Errorf("%s: %d %s (runs %d)", c.name, r.status, r.body, f.viaRunner["memory.add"])
			}
			continue
		}
		if r.status != http.StatusCreated {
			t.Errorf("%s: %d %s", c.name, r.status, r.body)
			continue
		}
		got := f.lastCaller().Secret
		if got.NotASecret != c.want.NotASecret || got.Reason != c.want.Reason || got.Unstored != c.want.Unstored || strings.Join(got.Refs, "|") != strings.Join(c.want.Refs, "|") {
			t.Errorf("%s: %+v want %+v", c.name, got, c.want)
		}
	}
}

// newReq builds an authenticated request (call's helper sets headers with
// Set, which cannot repeat a header).
func newReq(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

// serve runs one request through h.
func serve(h http.Handler, req *http.Request) reply {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return reply{status: rec.Code, body: rec.Body.String(), header: rec.Header()}
}
