//go:build teame2e

package teame2e

// move_repo_e2e_test.go — BUG-D1 and BUG-D2 (coordinator ruling 18:40),
// real binary in the agency world:
//
//   - D1 (after BATCH 2): `lore move <internal mem> --to repo` lands the row
//     in the client's .lore/data under its id, the mover's actor file is the
//     projection, and the commit carrying it passes the leak hook.
//   - D2: an org person's actor copy in a client repo is only the F18
//     projection {id, display name, kind}; a copy holding stable_key (an
//     email) blocks the commit (fail closed).

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// movedBody is internal text the client is meant to receive by a move.
const movedBody = "checkout must support INR and USD"

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
	// created_at/updated_at: v2 envelope timestamps (the leak guard allows them too).
	allowed := map[string]bool{"_table": true, "_v": true, "id": true, "display_name": true, "kind": true, "created_at": true, "updated_at": true}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	for k, v := range doc {
		if allowed[k] || v == nil || v == "" || v == false || clientActorEnvelopeOK(k, raw[k]) {
			continue
		}
		t.Fatalf("%s holds %s=%v; a client copy is only {id, display name, kind}", rel, k, v)
	}
	if strings.Contains(body, "@northwind.example") {
		t.Fatalf("%s names an org email: %s", rel, body)
	}
}

// BATCH 2 (owner): `lore move` is the one way to give a row to another
// place, keeping its id (a row has one home, no copies): the row lands in
// the client repo under the same id with its text, and the deliberate
// transfer passes the leak hook.
func TestLeak_MoveToRepoKeepsTheIdAndPassesTheHook(t *testing.T) {
	a := newAgency(t, th.Cmd("move"))
	mem := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", movedBody), "mem")
	rel := filepath.Join(".lore", "data", tblMemories, mem+".json")
	if _, ok := a.aliceApp.ReadFile(rel); ok {
		t.Fatalf("precondition: the memory must go to the store, not the repo")
	}
	a.aliceApp.MustLore("move", mem, "--to", "repo", "--yes")
	body, ok := a.aliceApp.ReadFile(rel)
	if !ok || !strings.Contains(body, movedBody) {
		t.Fatalf("the moved row keeps its id and text in the client's .lore/data: %v %s", ok, body)
	}
	alice := actorIDFor(aliceKey)
	mustProjection(t, a.aliceApp, actorFile(alice))
	a.aliceApp.Git("add", "-A")
	if r := a.aliceApp.TryGit("commit", "-m", "move a note to the client"); r.Code != 0 {
		t.Fatalf("the moved row must pass the leak hook: %s", r.Out())
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

// FA-Q-2 (OQ25 agents override): moving a row still on the v0.1.17 path
// takes an agent's --yes exactly like the core move; before, that path
// refused it ("agents cannot confirm a move with --yes"). The row is a
// planted teams row (teams is not a core table), moved from the client
// repo to the agent's personal place (`--to me`: the v0.1.17 path cannot
// write a 0.1.18 org store); the original row file is gone afterwards.
func TestMove_V017RowTakesAnAgentsYes(t *testing.T) {
	a := newAgency(t, th.Cmd("move"), th.Cmd("agent", "start"))
	const id = "tem_0123456789abcdef0123456789abcdef"
	if _, ok := coreTableOf(id); ok {
		t.Fatalf("precondition: %s must not be a core row", id)
	}
	rel := filepath.Join(".lore", "data", "teams", id+".json")
	a.aliceApp.WriteFile(rel, `{"_table":"teams","_v":1,"id":"`+id+`","name":"checkout squad","body":"checkout squad owns payments"}`)
	agent := startAgent(t, a.alice, a.aliceApp, agentLabelBackend)
	a.aliceApp.As(agent).MustLore("move", id, "--to", "me", "--yes").Says(t, "moved from "+id).Never(t, "agents cannot confirm")
	if _, ok := a.aliceApp.ReadFile(rel); ok {
		t.Fatalf("the moved row must leave the repo: %s", rel)
	}
}
