package main

// `lore api` through the CLI's own host runner (apirunner.go): a REST
// request is never tied to the server process's session (OV3), and the
// agent rules the CLI applies apply to X-Lore-Agent the same way.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/surface/rest"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// apiTestToken is the bearer token of the in-process API (a fixed test
// value, not a credential).
const apiTestToken = "test-token-of-the-in-process-api"

// apiServer is the REST handler `lore api` serves in the current repo:
// the CLI's host runner with the default hooks behind apiRunner.
func apiServer(t *testing.T) http.Handler {
	t.Helper()
	wd, home := dirs()
	base, _ := wire.StandaloneOptionsOrg(wd, home, wd, version)
	hooks := hosthooks.Default(wd)
	app, err := wire.New(hosthooks.Options(base, hooks))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	actor, err := app.Actor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := rest.New(app.Registry(), &apiRunner{inner: hosthooks.NewRunner(app, hooks)}, rest.Options{Token: apiTestToken, Actor: actor, LoreVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	return srv.Handler()
}

// apiDo sends one request and returns the status and body.
func apiDo(t *testing.T, h http.Handler, method, path, body string, header map[string]string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+apiTestToken)
	r.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// auditSessions maps each audited row id to the session id its entry
// carries (lore audit log --json).
func auditSessions(t *testing.T) map[string]string {
	t.Helper()
	code, out, errOut := runCaptured(t, "audit", "log", "--json")
	if code != 0 {
		t.Fatalf("audit log: exit %d %s", code, errOut)
	}
	var env struct {
		Data []struct {
			TargetID  string `json:"target_id"`
			SessionID string `json:"session_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("audit log --json: %v\n%s", err, out)
	}
	m := map[string]string{}
	for _, e := range env.Data {
		m[e.TargetID] = e.SessionID
	}
	return m
}

// sessionWrites is the writes count of every session row (lore session
// list --json).
func sessionWrites(t *testing.T) map[string]int {
	t.Helper()
	code, out, errOut := runCaptured(t, "session", "list", "--json")
	if code != 0 {
		t.Fatalf("session list: exit %d %s", code, errOut)
	}
	var env struct {
		Sessions []struct {
			ID     string `json:"id"`
			Writes int    `json:"writes"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("session list --json: %v\n%s", err, out)
	}
	m := map[string]int{}
	for _, s := range env.Sessions {
		m[s.ID] = s.Writes
	}
	return m
}

// TestAPIRequestNotTiedToServerSession (OV3, 29750e2e + g-host's audit
// hunk): the process runs inside a detected agent session (Claude Code
// env). A CLI write is linked to that session; REST writes, with no agent
// header and with X-Lore-Agent, carry no session and leave the session
// row's write count alone, and the audit chain stays intact.
func TestAPIRequestNotTiedToServerSession(t *testing.T) {
	envScopeRepo(t)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")

	code, out, errOut := runCaptured(t, "memory", "add", "--body", "cli write", "--json")
	if code != 0 {
		t.Fatalf("memory add: exit %d %s", code, errOut)
	}
	var cli struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &cli); err != nil || cli.ID == "" {
		t.Fatalf("memory add --json %q: %v", out, err)
	}
	ses := auditSessions(t)[cli.ID]
	if ses == "" {
		t.Fatalf("the CLI write carries no session: the test's agent env was not detected")
	}
	before := sessionWrites(t)

	h := apiServer(t)
	for _, header := range []map[string]string{nil, {rest.HeaderAgent: "codex"}} {
		status, body := apiDo(t, h, http.MethodPost, "/v1/memories", `{"body":"rest write"}`, header)
		if status != http.StatusCreated {
			t.Fatalf("POST /v1/memories %v: %d %s", header, status, body)
		}
		var res struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(body), &res); err != nil || res.ID == "" {
			t.Fatalf("POST body %q: %v", body, err)
		}
		if got, ok := auditSessions(t)[res.ID]; !ok || got != "" {
			t.Errorf("REST write %s (header %v): audited=%v session %q, want audited with no session", res.ID, header, ok, got)
		}
	}
	if after := sessionWrites(t); after[ses] != before[ses] {
		t.Errorf("session %s writes %d -> %d after REST writes, want unchanged", ses, before[ses], after[ses])
	}
	if code, out, errOut := runCaptured(t, "audit", "verify"); code != 0 {
		t.Errorf("audit verify: exit %d\n%s%s", code, out, errOut)
	}
}

// TestAPIAgentPersonOnly: with agents.person_only listing memory.delete,
// a REST delete carrying X-Lore-Agent is refused 409 E_POLICY_DENIED
// naming the agent and the row stays; the same delete without the header
// (the person) goes through. A bad header value is refused before any
// operation runs.
func TestAPIAgentPersonOnly(t *testing.T) {
	envScopeRepo(t)
	if code, _, errOut := runCaptured(t, "policy", "set", "agents.person_only", "memory.delete", "--yes"); code != 0 {
		t.Fatalf("policy set: exit %d %s", code, errOut)
	}
	h := apiServer(t)
	status, body := apiDo(t, h, http.MethodPost, "/v1/memories", `{"body":"keep me"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("POST: %d %s", status, body)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	path := "/v1/memories/" + res.ID
	status, body = apiDo(t, h, http.MethodDelete, path, `{"yes":true}`, map[string]string{rest.HeaderAgent: "codex"})
	if status != http.StatusConflict || !strings.Contains(body, "E_POLICY_DENIED") || !strings.Contains(body, "codex") {
		t.Fatalf("agent delete: %d %s, want 409 E_POLICY_DENIED naming codex", status, body)
	}
	if status, body = apiDo(t, h, http.MethodGet, path, "", nil); status != http.StatusOK {
		t.Fatalf("the refused delete removed the row: %d %s", status, body)
	}
	if status, body = apiDo(t, h, http.MethodDelete, path, `{"yes":true}`, map[string]string{rest.HeaderAgent: strings.Repeat("a", rest.MaxAgentLen+1)}); status != http.StatusBadRequest || !strings.Contains(body, "E_INVALID_INPUT") {
		t.Fatalf("over-long agent header: %d %s, want 400 E_INVALID_INPUT", status, body)
	}
	if status, body = apiDo(t, h, http.MethodDelete, path, `{"yes":true}`, nil); status != http.StatusOK {
		t.Fatalf("person delete: %d %s, want 200", status, body)
	}
}
