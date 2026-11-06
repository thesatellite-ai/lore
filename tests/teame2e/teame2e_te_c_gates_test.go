//go:build teame2e

package teame2e

// te-c gates: the format gate (FORMAT_EVOLUTION rule 2, OV48) seen by a
// team with a real remote. A project whose _meta.json declares a feature
// this lore does not know, or a min_lore_version above it, is read-only for
// every write path a person or an agent can take: a claim on the signals
// ref, an edit or add on the live ref, a landing on main, a finish, and a
// write into an org store project. Reads keep working and print the
// read-only line on stderr. Nothing reaches the remote: every ref keeps its
// commit.

import (
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teCFutureFeature is a feature name no lore build knows: declaring it in a
// _meta.json makes the folder read-only for this binary.
const teCFutureFeature = "te_c_feature_from_the_future"

// teCFutureFloor is a min_lore_version no build reaches.
const teCFutureFloor = "999.0.0"

// teCRepoMeta is the checkout's format declaration file.
const teCRepoMeta = dataDir + "/" + metaFile

// teCGate is one way a project becomes read-only for this binary.
type teCGate struct {
	name string
	// apply edits the decoded _meta.json object in place.
	apply func(t *testing.T, o map[string]json.RawMessage)
	// says is what the read-only line and the refusal name.
	says string
}

// teCGates are the two declarations that close a folder (unknown feature,
// floor above this binary).
var teCGates = []teCGate{
	{name: "unknown_feature", says: teCFutureFeature, apply: func(t *testing.T, o map[string]json.RawMessage) {
		var fs []string
		if raw, ok := o["features"]; ok {
			if err := json.Unmarshal(raw, &fs); err != nil {
				t.Fatalf("features: %v", err)
			}
		}
		o["features"] = teCJSON(t, append(fs, teCFutureFeature))
	}},
	{name: "floor_above_binary", says: teCFutureFloor, apply: func(t *testing.T, o map[string]json.RawMessage) {
		o["min_lore_version"] = teCJSON(t, teCFutureFloor)
	}},
}

// teCJSON encodes v or fails the test.
func teCJSON[T any](t *testing.T, v T) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// teCEditMeta rewrites the _meta.json at rel in c's worktree with g applied.
func teCEditMeta(t *testing.T, c *th.Clone, rel string, g teCGate) {
	t.Helper()
	body, ok := c.ReadFile(rel)
	if !ok {
		t.Fatalf("%s missing in %s", rel, c.Dir)
	}
	o := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	g.apply(t, o)
	out, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	c.WriteFile(rel, string(out)+"\n")
}

// teCRefs snapshots the remote refs a write could move.
func teCRefs(r *th.Remote, refs ...string) map[string]string {
	m := map[string]string{}
	for _, ref := range refs {
		m[ref] = r.Rev(ref)
	}
	return m
}

// teCSameRefs fails when any snapshotted ref moved.
func teCSameRefs(t *testing.T, r *th.Remote, before map[string]string) {
	t.Helper()
	for ref, rev := range before {
		if now := r.Rev(ref); now != rev {
			t.Errorf("%s moved on a read-only project: %s -> %s", ref, rev, now)
		}
	}
}

// TestTeC_GateReadOnlyEveryPath: dev2 pushes a _meta.json declaring a gate;
// dev1 pulls it. Every write path is refused with E_OLD_LORE (exit 19),
// nothing is written locally or on the remote, and reads still work with
// the read-only line on stderr (never on stdout).
func TestTeC_GateReadOnlyEveryPath(t *testing.T) {
	for _, g := range teCGates {
		t.Run(g.name, func(t *testing.T) {
			rt := newRepoTeam(t, capLand, th.Cmd("task", "claim"))
			id := rt.addTask(t, rt.c1, "gated task")
			toLand := rt.addTask(t, rt.c1, "task to land")

			rt.c2.Git("pull", "-q", "--ff-only", "origin", "main")
			teCEditMeta(t, rt.c2, teCRepoMeta, g)
			rt.c2.CommitAll("declare a future format")
			rt.c2.AdminPush("origin", "main")
			rt.c1.Git("pull", "-q", "--ff-only", "origin", "main")

			before := teCRefs(rt.app, refMain, storeLiveRef, refLive, storeSignals(), refSignals)
			status := rt.c1.Status()

			writes := [][]string{
				{"task", "claim", id},
				{"task", "edit", id, "--priority", "high"},
				{"task", "add", "new task", "--tasklist", rt.tasklist},
				{"land", toLand},
				{"task", "done", toLand},
				{"memory", "add", "a memory on a read-only project"},
			}
			for _, w := range writes {
				rt.c1.Lore(w...).Fails(t, th.ExitGuard, "E_OLD_LORE")
			}
			teCSameRefs(t, rt.app, before)
			if now := rt.c1.Status(); now != status {
				t.Errorf("a refused write changed the checkout:\nbefore %q\nafter  %q", status, now)
			}

			r := rt.c1.MustLore("task", "show", id).Says(t, "gated task")
			if !strings.Contains(r.Stderr, "read-only here") || !strings.Contains(r.Stderr, g.says) {
				t.Errorf("a read must print the read-only line naming %q on stderr: %s", g.says, r)
			}
			if strings.Contains(r.Stdout, "read-only here") {
				t.Errorf("the read-only line must never reach stdout: %s", r)
			}
			j := rt.c1.MustLore("task", "show", id, "--json")
			var v map[string]json.RawMessage
			if err := json.Unmarshal([]byte(j.Stdout), &v); err != nil {
				t.Errorf("--json stays one clean document on a read-only project: %v %s", err, j)
			}
		})
	}
}

// TestTeC_GateReadOnlyLiveMeta: the live ref's _live/meta.json gates the
// live paths (claim, edit, land) the same way, written by a stranger with
// push access straight onto the live ref.
func TestTeC_GateReadOnlyLiveMeta(t *testing.T) {
	rt := newRepoTeam(t, capLand, th.Cmd("task", "claim"))
	id := rt.addTask(t, rt.c1, "live gated")
	toLand := rt.addTask(t, rt.c1, "live to land")
	pushLiveFile(t, rt.c2, refLive, liveMetaPath, `{"min_lore_version":"`+teCFutureFloor+`","features":["live","signals"]}`)
	before := teCRefs(rt.app, refMain, refLive, refSignals)
	for _, w := range [][]string{
		{"task", "claim", id},
		{"task", "edit", id, "--priority", "high"},
		{"land", toLand},
		{"task", "done", toLand},
	} {
		rt.c1.Lore(w...).Fails(t, th.ExitGuard, "E_OLD_LORE")
	}
	teCSameRefs(t, rt.app, before)
	rt.c1.MustLore("task", "show", id).Says(t, "live gated")
}

// TestTeC_GateReadOnlyStoreProject: the org store's acme/ project declares
// a gate on the store branch; alice's task add, edit and claim from the
// client repo (tasks go to the org store, northwindOrg defaults) are
// refused and the store keeps every ref: the claim is refused before its
// signal is pushed (it used to reach refs/lore/signals/main and only the
// row half was refused). The store's signals are gated by the store
// folder's _meta.json on cmd/lore (TE-C-1: store tasks write no store
// live ref there, so OV48-SIG's live-ref meta has nothing to gate).
func TestTeC_GateReadOnlyStoreProject(t *testing.T) {
	for _, g := range teCGates {
		t.Run(g.name, func(t *testing.T) {
			a := newAgency(t, th.Cmd("task", "claim"))
			id := mustID(t, a.aliceApp.MustLore("task", "add", "store task", "--tasklist", a.tl), "tsk")
			sc := a.dev1.Clone(a.store, "store-checkout")
			rel := folderAcme + "/" + teCRepoMeta
			if _, ok := sc.ReadFile(rel); !ok {
				t.Fatalf("store project %s has no _meta.json on main: %v", folderAcme, a.store.Files(refMain))
			}
			teCEditMeta(t, sc, rel, g)
			sc.CommitAll("declare a future format")
			sc.AdminPush("origin", "main")

			before := teCRefs(a.store, refMain, storeLiveRef, storeSignals())
			for _, w := range [][]string{
				{"task", "add", "another", "--tasklist", a.tl},
				{"task", "edit", id, "--priority", "high"},
				{"task", "claim", id},
			} {
				a.aliceApp.Lore(w...).Fails(t, th.ExitGuard, "E_OLD_LORE")
			}
			teCSameRefs(t, a.store, before)
			a.aliceApp.MustLore("task", "show", id).Says(t, "store task")
		})
	}
}
