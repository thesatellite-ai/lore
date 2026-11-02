//go:build teame2e

package main

// share_repo_e2e_test.go — BUG-D1 and BUG-D2 (coordinator ruling 18:40),
// real binary in the agency world:
//
//   - D1: `lore share <internal mem> --to repo` writes a NEW row into the
//     client's .lore/data with no pointer back (§6.4), named by the sharer,
//     and the commit carrying it passes the leak hook.
//   - D2: an org person's actor copy in a client repo is only the F18
//     projection {id, display name, kind}; a copy holding stable_key (an
//     email) blocks the commit (fail closed).

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// sharedBody is internal text the client is meant to receive by a share.
const sharedBody = "checkout must support INR and USD"

// actorFile is the client repo path of an actor's row file.
func actorFile(id string) string {
	return filepath.Join(".lore", "data", tblActors, id+".json")
}

// mustProjection fails unless the actor file at rel holds only the F18
// projection keys (empty values count as absent: the engine writes every
// column).
func mustProjection(t *testing.T, c *th.Clone, rel string) {
	t.Helper()
	body, ok := c.ReadFile(rel)
	if !ok {
		t.Fatalf("%s is missing", rel)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	allowed := map[string]bool{"_table": true, "_v": true, "id": true, "display_name": true, "kind": true}
	for k, v := range doc {
		if allowed[k] || v == nil || v == "" || v == false {
			continue
		}
		t.Fatalf("%s holds %s=%v; a client copy is only {id, display name, kind}", rel, k, v)
	}
	if strings.Contains(body, "@northwind.example") {
		t.Fatalf("%s names an org email: %s", rel, body)
	}
}

func TestLeak_ShareToRepoWritesAnUnlinkedCopy(t *testing.T) {
	a := newAgency(t, th.Cmd("share"))
	mem := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", sharedBody), "mem")
	if _, ok := a.aliceApp.ReadFile(filepath.Join(".lore", "data", tblMemories, mem+".json")); ok {
		t.Fatalf("precondition: the memory must go to the store, not the repo")
	}
	out := a.aliceApp.MustLore("share", mem, "--to", "repo", "--yes").Out()
	cp := mustID(t, th.Result{Stdout: strings.SplitN(out, "✓", 2)[1]}, "mem")
	if cp == mem {
		t.Fatalf("the copy must get a new id: %s", out)
	}
	body, ok := a.aliceApp.ReadFile(filepath.Join(".lore", "data", tblMemories, cp+".json"))
	if !ok {
		t.Fatalf("no copy %s in the client's .lore/data: %s", cp, out)
	}
	if strings.Contains(body, mem) || !strings.Contains(body, sharedBody) {
		t.Fatalf("the copy must hold the text and no pointer back to %s: %s", mem, body)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	alice := actorIDFor(aliceKey)
	if doc["created_by_actor_id"] != alice {
		t.Fatalf("the copy is the sharer's (%s): %v", alice, doc["created_by_actor_id"])
	}
	mustProjection(t, a.aliceApp, actorFile(alice))
	a.aliceApp.Git("add", "-A")
	if r := a.aliceApp.TryGit("commit", "-m", "share a note"); r.Code != 0 {
		t.Fatalf("the shared copy must pass the leak hook: %s", r.Out())
	}
}

func TestLeak_ClientActorCopyIsTheProjection(t *testing.T) {
	a := newAgency(t)
	a.aliceApp.MustLore("rule", "add", "--body", "prices are shown with two decimals", "--to", "repo")
	alice := actorIDFor(aliceKey)
	mustProjection(t, a.aliceApp, actorFile(alice))
	a.aliceApp.Git("add", "-A")
	if r := a.aliceApp.TryGit("commit", "-m", "a rule"); r.Code != 0 {
		t.Fatalf("a projected actor copy must commit: %s", r.Out())
	}

	// Fail closed: a copy that holds an email blocks the commit.
	rel := actorFile(alice)
	body, _ := a.aliceApp.ReadFile(rel)
	leaky := strings.Replace(body, `"id":`, `"stable_key": "`+aliceKey+`", "id":`, 1)
	if leaky == body {
		t.Fatalf("could not plant stable_key in %s", body)
	}
	a.aliceApp.WriteFile(rel, leaky)
	a.aliceApp.Git("add", rel)
	if r := a.aliceApp.TryGit("commit", "-m", "leak"); r.Code == 0 || !strings.Contains(r.Out(), "E_LEAK_BLOCKED") {
		t.Fatalf("stable_key in a client actor copy must block: %s", r.Out())
	}
}

// The client's own developer: their actor row is projected too, and a
// fresh clone (which imports the projection before it knows the real key)
// still resolves them and writes.
func TestLeak_ClientOwnActorProjectedAndFreshCloneWorks(t *testing.T) {
	a := newAgency(t)
	for _, f := range mustGlob(t, filepath.Join(a.acmeApp.Dir, ".lore", "data", tblActors, "*.json")) {
		mustProjection(t, a.acmeApp, filepath.Join(".lore", "data", tblActors, filepath.Base(f)))
	}
	fresh := a.acmedev.Clone(a.app, "app-again")
	fresh.MustLore("memory", "add", "--body", "the build needs node 22")
	fresh.MustLore("whoami")
	alice := a.alice.Clone(a.app, "app-again")
	alice.MustLore("rule", "add", "--body", "amounts are integers", "--to", "repo")
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil || len(m) == 0 {
		t.Fatalf("nothing matches %s: %v", pattern, err)
	}
	return m
}
