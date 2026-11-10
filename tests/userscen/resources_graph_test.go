//go:build userscen

// References between resources and what follows from them: a three-level
// ref chain read with graph --down / --up / --depth / --json, impact of a
// host going down, a dangling ref after a delete; field history (who,
// when, from → to, --field, --json) across edits; retyping a field that
// already holds values (refused with the rows named, --dry-run,
// --keep-as-text quarantines, lint lists it, an edit clears it); moving a
// resource row between levels (repo → a store) with row move: same id,
// lineage in row show --origin, the second run, and the way back refused.
package userscen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// graphJSON is `lore resource graph|impact --json`.
type graphJSON struct {
	Root  string `json:"root"`
	Depth int    `json:"depth"`
	Nodes []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Name string `json:"name"`
		Hops int    `json:"hops"`
		Via  []struct {
			Field string `json:"field"`
			To    string `json:"to"`
		} `json:"via"`
	} `json:"nodes"`
}

// resChain builds host g1 ← service sa (runs_on) ← lb lb1 (backend).
func resChain(t *testing.T, c *th.Clone) (host, svc, lb string) {
	t.Helper()
	resHostService(t, c)
	c.MustLore("resource", "kind", "add", "lb")
	c.MustLore("resource", "field", "add", "lb", "backend", "--type", "ref", "--ref-kind", "service")
	c.MustLore("resource", "add", "host", "g1", "--set", "provider=aws")
	c.MustLore("resource", "add", "service", "sa", "--set", "runs_on=g1")
	c.MustLore("resource", "add", "lb", "lb1", "--set", "backend=sa")
	return resShow(t, c, "g1").ID, resShow(t, c, "sa").ID, resShow(t, c, "lb1").ID
}

func TestResourcesGraphChain(t *testing.T) {
	c := resRepo(t)
	host, svc, lb := resChain(t, c)
	down := th.DecodeJSON[graphJSON](t, c.Lore("resource", "graph", "lb1", "--json"))
	if down.Root != lb || down.Depth != 3 || len(down.Nodes) != 2 {
		t.Fatalf("graph --down = %+v", down)
	}
	if n := down.Nodes[0]; n.ID != svc || n.Hops != 1 || n.Via[0].Field != "backend" {
		t.Fatalf("first hop = %+v", n)
	}
	if n := down.Nodes[1]; n.ID != host || n.Hops != 2 || n.Via[0].Field != "runs_on" {
		t.Fatalf("second hop = %+v", n)
	}
	up := th.DecodeJSON[graphJSON](t, c.Lore("resource", "graph", "g1", "--up", "--json"))
	if up.Root != host || len(up.Nodes) != 2 || up.Nodes[0].ID != svc || up.Nodes[1].ID != lb {
		t.Fatalf("graph --up = %+v", up)
	}
	one := th.DecodeJSON[graphJSON](t, c.Lore("resource", "graph", "g1", "--up", "--depth", "1", "--json"))
	if len(one.Nodes) != 1 || one.Nodes[0].ID != svc {
		t.Fatalf("--depth 1 stops after one hop: %+v", one)
	}
	c.MustLore("resource", "graph", "g1", "--up").Says(t, "host g1", "runs_on ← service sa", "backend ← lb lb1")
	// impact is the --up walk: what breaks if g1 goes down.
	imp := th.DecodeJSON[graphJSON](t, c.Lore("resource", "impact", "g1", "--json"))
	if len(imp.Nodes) != 2 {
		t.Fatalf("impact = %+v", imp)
	}
	c.MustLore("resource", "impact", "g1").Says(t, "what breaks if", "g1")
	// --fields narrows the walk.
	if f := th.DecodeJSON[graphJSON](t, c.Lore("resource", "graph", "lb1", "--fields", "runs_on", "--json")); len(f.Nodes) != 0 {
		t.Fatalf("--fields runs_on from lb1 follows no backend edge: %+v", f)
	}
	// A leaf has an empty graph, not an error.
	if leaf := th.DecodeJSON[graphJSON](t, c.Lore("resource", "graph", "g1", "--json")); len(leaf.Nodes) != 0 {
		t.Fatalf("g1 uses nothing: %+v", leaf)
	}
	// --depth 0 is the flag's zero value: the default 3 hops.
	if z := th.DecodeJSON[graphJSON](t, c.Lore("resource", "graph", "lb1", "--depth", "0", "--json")); z.Depth != 3 {
		t.Fatalf("--depth 0 = %+v", z)
	}
	for _, d := range []string{"11", "-1"} {
		c.Lore("resource", "graph", "g1", "--depth", d).Fails(t, resExitInvalid, "E_INVALID_INPUT", "give 1 to 10")
	}
	c.Lore("resource", "graph", "nope").Fails(t, resExitNotFound, "E_NOT_FOUND")
	// A ref to another kind than the field's ref kind is refused.
	c.Lore("resource", "add", "lb", "lb2", "--set", "backend=g1").Fails(t, resExitInvalid, "backend")
	// Deleting a used host warns who still points at it; the ref is kept.
	c.MustLore("resource", "delete", "g1", "--yes").Says(t, "still pointing at it: sa")
	c.MustLore("resource", "lint").Says(t, "sa")
}

// historyJSON is `lore resource history --json`.
type historyJSON struct {
	ID      string `json:"id"`
	Count   int    `json:"count"`
	Entries []struct {
		At    string `json:"at"`
		By    string `json:"by"`
		Field string `json:"field"`
		From  string `json:"from"`
		To    string `json:"to"`
	} `json:"entries"`
}

func TestResourcesHistory(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "h1", "--set", "provider=aws", "--set", "ip=10.0.0.1")
	c.MustLore("resource", "edit", "h1", "--set", "ip=10.0.0.2")
	c.MustLore("resource", "edit", "h1", "--set", "ip=10.0.0.3", "--set", "provider=hetzner")
	c.MustLore("resource", "edit", "h1", "--unset", "ip")
	h := th.DecodeJSON[historyJSON](t, c.Lore("resource", "history", "h1", "--json"))
	if h.ID != resShow(t, c, "h1").ID || h.Count != len(h.Entries) || h.Count != 6 {
		t.Fatalf("history --json = %+v", h)
	}
	if e := h.Entries[0]; e.Field != "ip" || e.From != "10.0.0.3" || e.To != "" || !strings.HasPrefix(e.By, "act_") || e.At == "" {
		t.Fatalf("newest entry first (the unset): %+v", e)
	}
	ip := th.DecodeJSON[historyJSON](t, c.Lore("resource", "history", "h1", "--field", "ip", "--json"))
	var chain []string
	for _, e := range ip.Entries {
		chain = append(chain, e.From+">"+e.To)
	}
	if got := strings.Join(chain, " "); got != "10.0.0.3> 10.0.0.2>10.0.0.3 10.0.0.1>10.0.0.2 >10.0.0.1" {
		t.Fatalf("--field ip history = %q", got)
	}
	c.MustLore("resource", "history", "h1").Says(t, "ip  10.0.0.1 → 10.0.0.2", "provider")
	c.MustLore("resource", "history", "h1", "--field", "cores").Says(t, "no field changes recorded")
	c.Lore("resource", "history", "nope").Fails(t, resExitNotFound, "E_NOT_FOUND")
}

// Retyping a field that already holds values: values that fit convert,
// values that do not are listed and nothing is written, unless
// --keep-as-text (quarantined until an edit fixes them).
func TestResourcesRetypeWithData(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "host")
	c.MustLore("resource", "field", "add", "host", "cores", "--type", "text")
	c.MustLore("resource", "add", "host", "h_ok", "--set", "cores=8")
	c.MustLore("resource", "add", "host", "h_bad", "--set", "cores=many")
	c.MustLore("resource", "add", "host", "h_none")
	for _, args := range [][]string{{"--dry-run"}, {"--yes"}} {
		c.Lore(append([]string{"resource", "field", "retype", "host", "cores", "number"}, args...)...).
			Fails(t, resExitInvalid, "E_RESOURCE_TYPE", "1 value does not fit number", "h_bad", "--keep-as-text")
	}
	k := th.DecodeJSON[kindShowJSON](t, c.Lore("resource", "kind", "show", "host", "--json")).Data
	if k.Fields[0].Type != "text" {
		t.Fatalf("a refused retype writes nothing: %+v", k.Fields)
	}
	c.MustLore("resource", "field", "retype", "host", "cores", "number", "--keep-as-text", "--yes").
		Says(t, "retyped host.cores text → number", "1 value converted", "1 kept as text and quarantined", "h_bad")
	c.MustLore("resource", "show", "h_bad").Says(t, "quarantined: cores", "lore resource edit h_bad")
	c.MustLore("resource", "lint").Says(t, "quarantined", "h_bad.cores")
	c.MustLore("resource", "edit", "h_bad", "--set", "cores=2")
	c.MustLore("resource", "show", "h_bad").Never(t, "quarantined")
	c.MustLore("resource", "add", "host", "h_new", "--set", "cores=3")
	c.Lore("resource", "add", "host", "h_text", "--set", "cores=lots").Fails(t, resExitInvalid, "cores")
	// The same retype again is a no-op or a clear refusal, never a rewrite.
	r := c.Lore("resource", "field", "retype", "host", "cores", "number", "--yes")
	if r.Code != 0 && r.Code != resExitInvalid {
		t.Fatalf("retype to the same type: %s", r)
	}
	c.Lore("resource", "field", "retype", "host", "cores", "nope", "--yes").Fails(t, resExitInvalid, "E_INVALID_INPUT")
}

// moveJSON is `lore row move --json`.
type moveJSON struct {
	ID    string `json:"id"`
	Table string `json:"table"`
	To    string `json:"to"`
}

// A resource row moves from the repo into a store at the org level with
// its id; lineage records the move; the same move again says the row is
// already there (bug found by this suite: it answered "is deleted" with a
// main-branch hint); moving back is refused (a row never returns to a
// place it left).
func TestResourcesRowMoveBetweenLevels(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	// a plain folder store (a file:// url is always one; a bare git repo
	// there is refused, TestCampaignSync_StoreInitBareRepoAsFolder)
	opsDir := filepath.Join(c.A.Home, "ops")
	if err := os.MkdirAll(opsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	c.MustLore("store", "init", "ops", "file://"+opsDir, "--yes")
	c.MustLore("resource", "add", "host", "mv1", "--set", "provider=aws")
	id := resShow(t, c, "mv1").ID
	c.Lore("row", "move", "mv1", "--to", "ops", "--yes").Fails(t, resExitInvalid, "E_INVALID_ID", "is not a row id")
	c.Lore("row", "move", id, "--yes").Fails(t, resExitInvalid)
	m := th.DecodeJSON[moveJSON](t, c.Lore("row", "move", id, "--to", "ops", "--yes", "--json"))
	if m.ID != id || m.Table != "resources" || m.To != "ops" {
		t.Fatalf("row move --json = %+v", m)
	}
	s := resShow(t, c, "mv1")
	if s.ID != id || s.Store != "ops" || s.Values["provider"] != "aws" {
		t.Fatalf("after the move: %+v", s)
	}
	c.MustLore("resource", "show", "mv1").Says(t, "ops:host/mv1")
	if got := resShow(t, c, "ops:host/mv1").ID; got != id {
		t.Fatalf("the store qualified name resolves: %s", got)
	}
	c.MustLore("row", "show", id, "--origin").Says(t, "lineage", "moved", "→ sto_")
	c.Lore("row", "move", id, "--to", "ops", "--yes").Fails(t, resExitInvalid, "E_INVALID_INPUT", "already in store ops", "nothing to move").Never(t, "is deleted", "landed on main")
	c.Lore("row", "move", id, "--to", "repo", "--yes").Fails(t, resExitRefused, "E_UNSUPPORTED", "never returns to a place it left")
	c.Lore("row", "move", id, "--to", "nostore", "--yes").Refused(t, "nostore")
	if l := resList(t, c, "host"); l.Count != 1 {
		t.Fatalf("one host after the moves (no copy left behind): %+v", l)
	}
}
