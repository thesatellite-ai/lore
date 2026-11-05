//go:build teame2e

package main

// team_e2e_repowrite_paths_test.go — F16 on every path that writes a row
// into a client repo's own .lore/data (the branch, which the client reads
// once committed), with live rows off so the live-ref check is not what
// refuses. The engine's repo-write check (org.RepoWriteCheck, run by
// GuardRepoWrite) is asked only on a create or restore of a routed kind
// (memory, rule, decision, comment, task, mission); an edit, or a create
// of any other table, reaches the branch only through cmd/lore's live
// backend check (wire/host leakCheckBranch). This test names each path, so
// dropping either check shows which paths lose F16.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

func TestF16_EveryRepoWritePathIsChecked(t *testing.T) {
	a := newAgency(t, th.Flag("no-live", "task", "edit"))
	a.aliceApp.MustLore("memory", "add", "--body", internalNote) // the internal note (personal store)
	task := mustID(t, a.aliceApp.MustLore("task", "add", "plain follow up", "--to", "repo", "--no-live", "--tasklist", a.tl), "tsk")
	mem := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", "plain repo memory", "--to", "repo", "--no-live"), "mem")
	quote := "follow up: " + internalNote
	paths := []struct {
		name string
		args []string
	}{
		{"create of a routed kind (task add)", []string{"task", "add", quote, "--to", "repo", "--no-live", "--tasklist", a.tl}},
		{"create of a routed kind (rule add)", []string{"rule", "add", "--body", quote, "--to", "repo", "--no-live"}},
		{"create of another table (hotfix add)", []string{"hotfix", "add", "--title", "pay", "--body", quote, "--to", "repo"}},
		{"edit (task edit --body)", []string{"task", "edit", task, "--body", quote, "--no-live"}},
		{"edit (memory edit --body)", []string{"memory", "edit", mem, "--body", quote, "--no-live"}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			a.aliceApp.Lore(p.args...).Fails(t, th.ExitGuard, "E_LEAK_BLOCKED")
		})
	}
	// The same paths over `lore api` (REST runs the same engine; the CLI
	// host's checks must not be the only ones that refuse).
	srv := startAPI(t, a.aliceApp)
	rest := []struct {
		name, method, path string
		body               map[string]any
	}{
		{"REST create of a routed kind", http.MethodPost, "/v1/tasks", map[string]any{"title": quote, "to": "repo", "tasklist": a.tl, "no_live": true}},
		{"REST create of another table", http.MethodPost, "/v1/hotfixes", map[string]any{"title": "pay", "body": quote, "to": "repo"}},
		{"REST edit (memory)", http.MethodPatch, "/v1/memories/" + mem, map[string]any{"body": quote, "no_live": true}},
		{"REST edit (task)", http.MethodPatch, "/v1/tasks/" + task, map[string]any{"body": quote, "no_live": true}},
	}
	for _, r := range rest {
		t.Run(r.name, func(t *testing.T) {
			if got := srv.send(t, r.method, r.path, r.body); got.code != "E_LEAK_BLOCKED" {
				t.Fatalf("%s %s: want E_LEAK_BLOCKED, got %d %s: %s", r.method, r.path, got.status, got.code, got.body)
			}
		})
	}
	if f := a.clientSees(t, "send the estimate before the call"); len(f) != 0 {
		t.Fatalf("a blocked repo write reached the client: %v", f)
	}
}

// send is apiServer.post for any method.
func (s *apiServer) send(t *testing.T, method, path string, body map[string]any) apiReply {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, s.base+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body) // a short read shows in the assertion
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env) // a success body has no error member
	return apiReply{status: resp.StatusCode, code: env.Error.Code, body: string(raw)}
}
