//go:build userscen

package userscen

// campaign_query_rest_test.go — the `lore api` REST surface as a whole,
// walked from the served /openapi.json (every documented route, not a
// sample):
//
//   - a caller without the token, with a wrong one, or with a lower-case
//     scheme is 401 E_UNAUTHORIZED on every route, and the refusal body
//     matches the 401 schema the document declares for that route;
//   - every route that takes a body refuses each malformed body (cut
//     JSON, an array, a string, a number, two objects, binary) with 400
//     E_INVALID_INPUT matching the declared 400 schema, and writes nothing;
//   - unknown routes (every method, odd paths) are 404 E_NOT_FOUND with a
//     typed JSON body;
//   - the read operations of the query area answer their declared schema
//     and the same document as the CLI;
//   - many parallel HTTP callers (readers, refused strangers) all get the
//     answer a single caller gets, never a 5xx.

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

// cqRoute is one documented method + path.
type cqRoute struct {
	method, path, opID string
	body               bool // the operation takes a request body
}

// cqRoutes lists every documented route of doc, sorted.
func cqRoutes(doc restOAPI) []cqRoute {
	var out []cqRoute
	for p, ops := range doc.Paths {
		for m, o := range ops {
			out = append(out, cqRoute{method: strings.ToUpper(m), path: p, opID: o.OperationID, body: o.RequestBody != nil})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].method < out[j].method
	})
	return out
}

// cqFill puts a well-formed id in every {id} of a route.
func cqFill(path string) string {
	return strings.ReplaceAll(path, "{id}", "mem_01a1b49cf238767ca425d36a07b5cc1e")
}

// TestCampaignQuery_RESTEveryRouteRefusesStrangers walks every route.
func TestCampaignQuery_RESTEveryRouteRefusesStrangers(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	doc, _, _ := restDocs(t, srv)
	before := cqDataHash(t, rw.repo)
	routes := cqRoutes(doc)
	if len(routes) < 100 {
		t.Fatalf("only %d documented routes", len(routes))
	}
	wrong := strings.Repeat("0", 64)
	callers := []struct {
		name  string
		token *string
		hdr   []string
	}{
		{"no token", restNoToken, nil},
		{"wrong token", &wrong, nil},
		{"lower-case scheme", restNoToken, []string{"Authorization", "bearer " + srv.token}},
		{"token as cookie", restNoToken, []string{"Cookie", "lore_token=" + srv.token}},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, rt := range routes {
		for _, cl := range callers {
			wg.Add(1)
			sem <- struct{}{}
			go func(rt cqRoute, name string, tok *string, hdr []string) {
				defer wg.Done()
				defer func() { <-sem }()
				req := restReq{method: rt.method, path: cqFill(rt.path), token: tok, header: hdr}
				if rt.body {
					req.body = `{"body":"written by a stranger","title":"stranger"}`
				}
				rep, err := srv.try(req)
				mu.Lock()
				defer mu.Unlock()
				what := rt.method + " " + rt.path + " (" + rt.opID + ") " + name
				if err != nil {
					t.Errorf("%s: %v", what, err)
					return
				}
				restRefused(t, what, rep)
				cqCheckReply(t, doc, what, rt.method, rt.path, rep)
			}(rt, cl.name, cl.token, cl.hdr)
		}
	}
	wg.Wait()
	if after := cqDataHash(t, rw.repo); after != before {
		t.Errorf("a refused stranger wrote rows")
	}
	t.Logf("refused %d routes × %d callers", len(routes), len(callers))
}

// TestCampaignQuery_RESTMalformedBodies: every body route refuses every
// malformed body with a typed 400 and writes nothing.
func TestCampaignQuery_RESTMalformedBodies(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	doc, _, _ := restDocs(t, srv)
	before := cqDataHash(t, rw.repo)
	bodies := map[string]string{
		"cut json":    `{"body":"x"`,
		"array":       `[1,2]`,
		"string":      `"hello"`,
		"number":      `42`,
		"two objects": `{}{}`,
		"binary":      "\x00\x01\xff\xfe",
		"trailing":    `{} x`,
		"bare word":   `body=x`,
	}
	n := 0
	for _, rt := range cqRoutes(doc) {
		if !rt.body {
			continue
		}
		for name, b := range bodies {
			rep := srv.do(t, restReq{method: rt.method, path: cqFill(rt.path), body: b})
			what := rt.method + " " + rt.path + " (" + rt.opID + ") " + name
			n++
			if rep.status != http.StatusBadRequest || rep.err == nil || rep.err.Code != "E_INVALID_INPUT" || rep.err.Hint == "" {
				t.Errorf("%s: want 400 E_INVALID_INPUT with a hint, got %d %.200s", what, rep.status, rep.body)
				continue
			}
			cqCheckReply(t, doc, what, rt.method, rt.path, rep)
		}
	}
	if after := cqDataHash(t, rw.repo); after != before {
		t.Errorf("a malformed body wrote rows")
	}
	t.Logf("sent %d malformed bodies", n)
}

// TestCampaignQuery_RESTUnknownRoutes: every method on odd and unknown
// paths answers 404 E_NOT_FOUND as JSON naming the route; a known path
// with a method it does not serve is never a success.
func TestCampaignQuery_RESTUnknownRoutes(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	paths := []string{
		"/v1/no-such-noun", "/v2/memories", "/v1/memories/mem_01a1b49cf238767ca425d36a07b5cc1e/nope/deeper",
		"/v1/MEMORIES", "/v1/search/nope", "/openapi.yaml", "/v1/sql", "/v1/audits",
		"/v1/" + strings.Repeat("a", 4000), "/v1/%E2%80%AEevil", "/v1/rows",
	}
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, p := range paths {
		for _, m := range methods {
			req := restReq{method: m, path: p}
			if m != http.MethodGet && m != http.MethodDelete {
				req.body = `{}`
			}
			rep := srv.do(t, req)
			what := m + " " + p[:min(len(p), 60)]
			if rep.status != http.StatusNotFound || rep.err == nil || rep.err.Code != "E_NOT_FOUND" || !strings.Contains(rep.err.Message, "no route") {
				t.Errorf("%s: want 404 E_NOT_FOUND no route, got %d %.200s", what, rep.status, rep.body)
			}
			if ct := rep.header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("%s: Content-Type %q", what, ct)
			}
		}
	}
	// Known paths with a method they do not serve.
	for _, r := range []restReq{
		{method: http.MethodPut, path: "/v1/memories", body: `{}`},
		{method: http.MethodGet, path: "/v1/sql/run"},
		{method: http.MethodDelete, path: "/v1/search/run"},
		{method: http.MethodPatch, path: "/v1/audits/log", body: `{}`},
		{method: http.MethodPost, path: "/openapi.json", body: `{}`},
	} {
		rep := srv.do(t, r)
		if rep.status/100 == 2 || rep.status >= 500 || rep.err == nil || rep.err.Code == "" {
			t.Errorf("%s %s: %d %.200s", r.method, r.path, rep.status, rep.body)
		}
	}
}

// TestCampaignQuery_RESTReadOpsMatchSchemaAndCLI: the query area's read
// operations over REST answer their declared schema and the CLI's
// document; each per-kind search route agrees with `lore <kind> search`.
func TestCampaignQuery_RESTReadOpsMatchSchemaAndCLI(t *testing.T) {
	s := newCQSeed(t)
	c := s.rw.repo
	srv := restStart(t, c)
	doc, _, _ := restDocs(t, srv)
	cases := []struct {
		path, body string
		cli        []string
	}{
		{"/v1/search/run", `{"query":"deploy"}`, []string{"search", "run", "deploy"}},
		{"/v1/search/run", `{"query":"rollback","tables":["memory"],"limit":1}`, []string{"search", "run", "rollback", "--tables", "memory", "--limit", "1"}},
		{"/v1/search/run", `{"query":"قاعدة"}`, []string{"search", "run", "قاعدة"}},
		{"/v1/search/status", `{"tables":["rule"]}`, []string{"search", "status", "--tables", "rule"}},
		{"/v1/sql/run", `{"query":"SELECT id, body FROM memories ORDER BY id"}`, []string{"sql", "run", "SELECT id, body FROM memories ORDER BY id"}},
		{"/v1/audits/verify", `{}`, []string{"audit", "verify"}},
		{"/v1/memories/search", `{"query":"rollback"}`, []string{"memory", "search", "rollback"}},
		{"/v1/rules/search", `{"query":"deploy"}`, []string{"rule", "search", "deploy"}},
		{"/v1/decisions/search", `{"query":"stripe"}`, []string{"decision", "search", "stripe"}},
	}
	for _, cs := range cases {
		rep := srv.post(t, cs.path, cs.body)
		what := "POST " + cs.path + " " + cs.body
		if rep.status != http.StatusOK {
			t.Errorf("%s: %d %.300s", what, rep.status, rep.body)
			continue
		}
		cqCheckReply(t, doc, what, http.MethodPost, cs.path, rep)
		cli := c.MustLore(append(cs.cli, "--json")...).Stdout
		if !restJSONEqual([]byte(rep.body), []byte(cli)) {
			t.Errorf("%s differs from lore %s --json:\nREST %.400s\nCLI  %.400s", what, strings.Join(cs.cli, " "), rep.body, cli)
		}
	}
	// GET /v1/rows/{id} for a row of every seeded kind.
	for _, id := range []string{s.deploy, s.friday, s.decision} {
		rep := srv.get(t, "/v1/rows/"+id)
		cqCheckReply(t, doc, "GET /v1/rows/"+id, http.MethodGet, "/v1/rows/{id}", rep)
		if cli := c.MustLore("show", id, "--json").Stdout; !restJSONEqual([]byte(rep.body), []byte(cli)) {
			t.Errorf("GET /v1/rows/%s differs from lore show:\nREST %.300s\nCLI  %.300s", id, rep.body, cli)
		}
	}
	// Wrong member types are refused naming the member.
	for _, b := range []string{`{"query":42}`, `{"query":"x","limit":"ten"}`, `{"query":"x","tables":"memory"}`, `{"query":"x","colour":1}`} {
		rep := srv.post(t, "/v1/search/run", b)
		if rep.status != http.StatusBadRequest || rep.err == nil || rep.err.Code != "E_INVALID_INPUT" {
			t.Errorf("search %s: %d %.200s", b, rep.status, rep.body)
		}
	}
}

// TestCampaignQuery_RESTConcurrentCallers: 16 parallel clients read the
// query routes while strangers knock; every reader gets the single-caller
// answer, every stranger a 401, nobody a 5xx.
func TestCampaignQuery_RESTConcurrentCallers(t *testing.T) {
	s := newCQSeed(t)
	c := s.rw.repo
	srv := restStart(t, c)
	reads := []restReq{
		{method: http.MethodPost, path: "/v1/search/run", body: `{"query":"deploy"}`},
		{method: http.MethodPost, path: "/v1/sql/run", body: `{"query":"SELECT id FROM memories ORDER BY id"}`},
		{method: http.MethodGet, path: "/v1/rows/" + s.deploy},
		{method: http.MethodPost, path: "/v1/search/status", body: `{}`},
		{method: http.MethodGet, path: "/v1/memories"},
	}
	want := make([]string, len(reads))
	for i, r := range reads {
		rep := srv.do(t, r)
		if rep.status != http.StatusOK {
			t.Fatalf("%s %s: %d %.200s", r.method, r.path, rep.status, rep.body)
		}
		want[i] = rep.body
	}
	const clients, rounds = 16, 10
	var wg sync.WaitGroup
	errc := make(chan string, clients*rounds*2)
	for cl := 0; cl < clients; cl++ {
		wg.Add(1)
		go func(cl int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				k := (cl + i) % len(reads)
				rep, err := srv.try(reads[k])
				switch {
				case err != nil:
					errc <- err.Error()
				case rep.status != http.StatusOK:
					errc <- reads[k].path + ": " + rep.body
				case !restJSONEqual([]byte(rep.body), []byte(want[k])):
					errc <- reads[k].path + ": answer differs under load: " + rep.body[:min(len(rep.body), 200)]
				}
				st := reads[k]
				st.token = restNoToken
				if rep, err := srv.try(st); err != nil || rep.status != http.StatusUnauthorized {
					errc <- "stranger on " + st.path + " not refused"
				}
			}
		}(cl)
	}
	wg.Wait()
	close(errc)
	n := 0
	for e := range errc {
		if n < 10 {
			t.Error(e)
		}
		n++
	}
	if n > 0 {
		t.Errorf("%d failures among %d parallel requests", n, clients*rounds*2)
	}
	// The server still answers after the burst.
	if rep := srv.do(t, reads[0]); rep.status != http.StatusOK {
		t.Errorf("after the burst: %d", rep.status)
	}
}
