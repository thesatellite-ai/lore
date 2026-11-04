package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// groupReadBit makes a token file readable by its group: the mode lore
// must refuse.
const groupReadBit = 0o040

// testToken is built at run time so no token-shaped literal sits in the
// source (push protection and the secret scanners flag those).
var testToken = strings.Repeat("0123456789abcdef", 4)

type kindsFake struct{}

func (kindsFake) Kinds(context.Context) ([]Kind, error) {
	return []Kind{{Source: "agency", Def: schema.TableDef{Name: "server", Fields: []schema.FieldDef{
		{Name: "name", Kind: value.KindText, Required: true},
		{Name: "ip", Kind: value.KindIP},
		{Name: "notes", Kind: value.KindText, UI: schema.UI{Widget: schema.WidgetMarkdown}},
	}}}}, nil
}

func newServer(t *testing.T, f *fake) http.Handler {
	t.Helper()
	s, err := New(f.reg, f, Options{Token: testToken, LoreVersion: "0.1.18", Kinds: kindsFake{}})
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler()
}

type reply struct {
	status int
	body   string
	header http.Header
}

func call(h http.Handler, method, target, body, token string, hdr ...string) reply {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return reply{status: rec.Code, body: rec.Body.String(), header: rec.Header()}
}

func code(t *testing.T, r reply) errs.Code {
	t.Helper()
	var env errs.Envelope
	if err := json.Unmarshal([]byte(r.body), &env); err != nil {
		t.Fatalf("error body %q: %v", r.body, err)
	}
	return env.Error.Code
}

// sample is a valid request per operation, for the route walks.
type sample struct {
	method, path, body string
}

func samples(t *testing.T, f *fake) map[string]sample {
	t.Helper()
	bodies := map[string]string{
		"memory.add":  `{"body":"x"}`,
		"trash.purge": `{"yes":true}`,
		opRuleAdd:     `{"pattern":"p"}`,
	}
	out := map[string]sample{}
	for _, e := range f.reg.Entries() {
		rt, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			continue
		}
		out[e.Spec().Name] = sample{rt.Method, strings.ReplaceAll(rt.Path, "{id}", "row_1"), bodies[e.Spec().Name]}
	}
	if len(out) != 4 {
		t.Fatalf("routes %d", len(out))
	}
	return out
}

// TestClosedByDefault walks every route plus the two meta routes and an
// unknown path: no token and a wrong token answer 401 E_UNAUTHORIZED, the
// right token answers 2xx, and no response carries a CORS header.
func TestClosedByDefault(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	h := newServer(t, f)
	routes := samples(t, f)
	routes["openapi"] = sample{http.MethodGet, PathOpenAPI, ""}
	routes["spec"] = sample{http.MethodGet, PathSpec, ""}
	wrong := strings.Repeat("f", len(testToken))
	for name, s := range routes {
		for _, tok := range []string{"", wrong, testToken[:10], testToken + "0"} {
			r := call(h, s.method, s.path, s.body, tok)
			if r.status != http.StatusUnauthorized || code(t, r) != errs.Unauthorized {
				t.Errorf("%s token %q: %d %s", name, tok, r.status, r.body)
			}
		}
		r := call(h, s.method, s.path, s.body, "", "Authorization", "Basic "+testToken)
		if r.status != http.StatusUnauthorized {
			t.Errorf("%s: basic auth accepted", name)
		}
		r = call(h, s.method, s.path, s.body, testToken)
		if r.status < 200 || r.status > 299 {
			t.Errorf("%s: right token: %d %s", name, r.status, r.body)
		}
		for k := range r.header {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s: CORS header %s", name, k)
			}
		}
	}
	if r := call(h, http.MethodGet, "/v1/nosuch", "", ""); r.status != http.StatusUnauthorized {
		t.Errorf("unknown path without token: %d", r.status)
	}
	if r := call(h, http.MethodGet, "/v1/nosuch", "", testToken); r.status != http.StatusNotFound || code(t, r) != errs.NotFound {
		t.Errorf("unknown path: %d %s", r.status, r.body)
	}
	if _, err := New(f.reg, f, Options{}); !errs.Has(err, errs.ConfigInvalid) {
		t.Errorf("empty token accepted: %v", err)
	}
}

// TestEveryRouteThroughRunner: each route reaches the Runner once and no
// run function is entered outside it.
func TestEveryRouteThroughRunner(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	h := newServer(t, f)
	for name, s := range samples(t, f) {
		if r := call(h, s.method, s.path, s.body, testToken); r.status >= 300 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
		if f.viaRunner[name] != 1 {
			t.Errorf("%s: runner calls %d", name, f.viaRunner[name])
		}
	}
	if f.direct != 0 {
		t.Errorf("%d run functions entered outside the Runner", f.direct)
	}
}

// TestConfirm: 428 E_NEEDS_CONFIRM, then success with "yes": true in the
// body or the query; "yes" on an operation that never asks is refused.
func TestConfirm(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	h := newServer(t, f)
	r := call(h, http.MethodPost, "/v1/trashes/row_1/purge", "", testToken)
	if r.status != http.StatusPreconditionRequired || code(t, r) != errs.NeedsConfirm {
		t.Fatalf("no yes: %d %s", r.status, r.body)
	}
	r = call(h, http.MethodPost, "/v1/trashes/row_1/purge", `{"yes":true}`, testToken)
	if r.status != http.StatusOK || r.body != `{"purged":1}`+"\n" || !f.lastCaller().Yes || f.lastCaller().Interactive {
		t.Fatalf("yes: %d %s %+v", r.status, r.body, f.lastCaller())
	}
	if r = call(h, http.MethodPost, "/v1/memories", `{"body":"x","force":true}`, testToken); r.status != http.StatusPreconditionRequired {
		t.Fatalf("force: %d", r.status)
	}
	if r = call(h, http.MethodPost, "/v1/memories", `{"body":"x","force":true,"yes":true}`, testToken); r.status != http.StatusCreated {
		t.Fatalf("force+yes: %d %s", r.status, r.body)
	}
	if r = call(h, http.MethodPost, "/v1/trashes/row_1/purge", `{"yes":"sure"}`, testToken); r.status != http.StatusBadRequest {
		t.Fatalf("yes not bool: %d", r.status)
	}
	if r = call(h, http.MethodGet, "/v1/memories?yes=true", "", testToken); r.status != http.StatusBadRequest {
		t.Fatalf("yes on list: %d", r.status)
	}
	if r = call(h, http.MethodPost, "/v1/secrets/rules", `{"pattern":"p","yes":true}`, testToken); r.status != http.StatusBadRequest {
		t.Fatalf("yes on non-confirming add: %d", r.status)
	}
}

// TestInputs: body and query decoding, unknown fields, path id, body cap.
func TestInputs(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	h := newServer(t, f)
	r := call(h, http.MethodPost, "/v1/memories", `{"body":"hi","title":"T","tags":["a","b"],"limit":3,"wait":60000000000}`, testToken)
	if r.status != http.StatusCreated || !strings.Contains(r.body, `"echo":"hi|T|a,b|3|1m0s|false"`) {
		t.Fatalf("add: %d %s", r.status, r.body)
	}
	if r = call(h, http.MethodGet, "/v1/memories?limit=1", "", testToken); r.status != http.StatusOK || r.body != `{"items":[{"id":"mem_a","title":"first"}]}`+"\n" {
		t.Fatalf("list query: %d %s", r.status, r.body)
	}
	bad := []sample{
		{http.MethodPost, "/v1/memories", `{"body":"x","colour":"red"}`},          // unknown field
		{http.MethodPost, "/v1/memories", `{}`},                                   // missing required
		{http.MethodPost, "/v1/memories", `[1]`},                                  // not an object
		{http.MethodPost, "/v1/memories", `{"body":5}`},                           // wrong type
		{http.MethodPost, "/v1/memories?body=x", `{}`},                            // query on POST
		{http.MethodGet, "/v1/memories?limit=many", ""},                           // bad int
		{http.MethodGet, "/v1/memories?limit=1&limit=2", ""},                      // two values
		{http.MethodGet, "/v1/memories?colour=red", ""},                           // unknown parameter
		{http.MethodGet, "/v1/memories", `{"limit":1}`},                           // body on GET
		{http.MethodPost, "/v1/trashes/row_1/purge", `{"id":"row_2","yes":true}`}, // id mismatch
	}
	for _, s := range bad {
		if got := call(h, s.method, s.path, s.body, testToken); got.status != http.StatusBadRequest {
			t.Errorf("%s %s %s: %d %s", s.method, s.path, s.body, got.status, got.body)
		}
	}
	if r = call(h, http.MethodPost, "/v1/trashes/row_1/purge", `{"id":"row_1","yes":true}`, testToken); r.status != http.StatusOK {
		t.Errorf("matching id: %d %s", r.status, r.body)
	}
	big := `{"body":"` + strings.Repeat("x", MaxBodyBytes) + `"}`
	if r = call(h, http.MethodPost, "/v1/memories", big, testToken); r.status != http.StatusBadRequest || code(t, r) != errs.BodyTooLarge {
		t.Errorf("over cap: %d %.200s", r.status, r.body)
	}
	fits := `{"body":"` + strings.Repeat("x", MaxBodyBytes-len(`{"body":""}`)) + `"}`
	if r = call(h, http.MethodPost, "/v1/memories", fits, testToken); r.status != http.StatusCreated {
		t.Errorf("at cap: %d %.200s", r.status, r.body)
	}
}

// TestCaller: the machine's actor, X-Lore-Agent recorded, bad agent values
// refused.
func TestCaller(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(f.reg, f, Options{Token: testToken, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	if r := call(h, http.MethodGet, "/v1/memories", "", testToken, HeaderAgent, "codex"); r.status != http.StatusOK {
		t.Fatal(r.body)
	}
	if c := f.lastCaller(); c.Agent != "codex" || c.Actor != actor || c.Interactive {
		t.Fatalf("caller %+v", c)
	}
	for _, a := range []string{"bad\x01agent", strings.Repeat("a", MaxAgentLen+1)} {
		if r := call(h, http.MethodGet, "/v1/memories", "", testToken, HeaderAgent, a); r.status != http.StatusBadRequest {
			t.Errorf("agent %q: %d", a, r.status)
		}
	}
}

// TestStatusFromClass (O3, O5): every registered code answers the HTTP
// status of its class through a real route, and none but E_INTERNAL is 500.
func TestStatusFromClass(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	h := newServer(t, f)
	for _, d := range errs.All() {
		r := call(h, http.MethodPost, "/v1/memories", `{"body":"fail:`+string(d.Code)+`"}`, testToken)
		if r.status != d.HTTP || code(t, r) != d.Code {
			t.Errorf("%s: %d want %d", d.Code, r.status, d.HTTP)
		}
		if (r.status == http.StatusInternalServerError) != (d.Code == errs.Internal) {
			t.Errorf("%s answers %d: only E_INTERNAL may be 500", d.Code, r.status)
		}
		if d.Code == errs.Unauthorized && r.status != http.StatusUnauthorized {
			t.Errorf("E_UNAUTHORIZED is %d", r.status)
		}
	}
	if StatusOf(io.EOF) != http.StatusInternalServerError {
		t.Error("a foreign error must be internal")
	}
}

// TestSpecEndpoint: /v1/spec has the version, every non-hidden operation
// with route and resolved hints, and the kinds.
func TestSpecEndpoint(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	r := call(newServer(t, f), http.MethodGet, PathSpec, "", testToken)
	var doc SpecDoc
	if err := json.Unmarshal([]byte(r.body), &doc); err != nil || r.status != http.StatusOK {
		t.Fatalf("%d %s %v", r.status, r.body, err)
	}
	if doc.LoreVersion != "0.1.18" || len(doc.Operations) != 4 {
		t.Fatalf("doc %+v", doc)
	}
	add := doc.Operations[0]
	if add.Name != "memory.add" || add.REST.Method != http.MethodPost || add.REST.Path != "/v1/memories" || add.Confirm.When != "force" || len(add.Errors) != 2 {
		t.Fatalf("add %+v", add)
	}
	body, title, limit, force := add.Input[0], add.Input[1], add.Input[3], add.Input[5]
	if body.Widget != schema.WidgetMarkdown || !body.Required || body.Label != "Body" || body.InList {
		t.Errorf("body %+v", body)
	}
	if title.Widget != schema.WidgetText || title.Label != "Title" || !title.InList {
		t.Errorf("title %+v", title)
	}
	if limit.Widget != schema.WidgetNumber || force.Widget != schema.WidgetBool || force.Type != op.TypeBool {
		t.Errorf("limit %+v force %+v", limit, force)
	}
	if len(doc.Kinds) != 1 || doc.Kinds[0].Source != "agency" || doc.Kinds[0].Fields[2].Widget != schema.WidgetMarkdown || !doc.Kinds[0].Fields[0].InList {
		t.Errorf("kinds %+v", doc.Kinds)
	}
	for _, o := range doc.Operations {
		if o.Name == "merge.driver" {
			t.Error("hidden op in spec")
		}
	}
	empty := BuildSpec(f.reg, "x", nil)
	b, _ := json.Marshal(empty)
	if !bytes.Contains(b, []byte(`"kinds":[]`)) {
		t.Errorf("nil kinds should be []: %s", b)
	}
}

// TestOpenAPIServed: the served document parses and carries the kinds.
func TestOpenAPIServed(t *testing.T) {
	t.Parallel()
	r := call(newServer(t, newFake(t)), http.MethodGet, PathOpenAPI, "", testToken)
	var doc struct {
		OpenAPI    string                     `json:"openapi"`
		Paths      map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(r.body), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" || len(doc.Paths) != 3 || doc.Components.Schemas["kind.server"] == nil {
		t.Fatalf("doc %d paths, schemas %v", len(doc.Paths), doc.Components.Schemas)
	}
}

// TestToken: created 0600 hex, stable on reload, replaced by rotate; a file
// with group/other bits, garbage or a symlink is refused.
func TestToken(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "lore")
	tok, err := LoadOrCreateToken(home)
	if err != nil || len(tok) != 2*TokenBytes {
		t.Fatalf("create: %q %v", tok, err)
	}
	st, _ := os.Stat(TokenPath(home))
	hs, _ := os.Stat(home)
	if st.Mode().Perm() != 0o600 || hs.Mode().Perm() != 0o700 {
		t.Fatalf("modes %v %v", st.Mode().Perm(), hs.Mode().Perm())
	}
	again, err := LoadOrCreateToken(home)
	if err != nil || again != tok {
		t.Fatalf("reload: %v", err)
	}
	rot, err := RotateToken(home)
	if err != nil || rot == tok {
		t.Fatalf("rotate: %v", err)
	}
	if now, _ := LoadOrCreateToken(home); now != rot {
		t.Fatal("rotate not persisted")
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
	if err := os.Chmod(TokenPath(home), tokenMode|groupReadBit); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(home); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("0640 accepted: %v", err)
	}
	if err := os.WriteFile(TokenPath(home), []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(TokenPath(home), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(home); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("garbage accepted: %v", err)
	}
	link := t.TempDir()
	if err := os.Symlink(TokenPath(home), TokenPath(link)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(link); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("symlink accepted: %v", err)
	}
}

// TestServe: Listen(0) binds 127.0.0.1 on a free port, Serve announces it,
// answers a real HTTP request (401 without token, 200 with), and returns
// when the context ends.
func TestServe(t *testing.T) {
	t.Parallel()
	if _, err := Listen(70000); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad port: %v", err)
	}
	f := newFake(t)
	s, err := New(f.reg, f, Options{Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if !strings.HasPrefix(addr, BindHost+":") || strings.HasSuffix(addr, ":0") {
		t.Fatalf("addr %s", addr)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var announce bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln, &announce, "token: ~/.lore/api-token") }()
	get := func(tok string) int {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/v1/memories", nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if get("") != http.StatusUnauthorized || get(testToken) != http.StatusOK {
		t.Fatal("live server statuses")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if announce.String() != "lore api listening on http://"+addr+" (token: ~/.lore/api-token)\n" {
		t.Fatalf("announce %q", announce.String())
	}
}
