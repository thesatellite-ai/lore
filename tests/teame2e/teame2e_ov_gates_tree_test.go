//go:build teame2e

package teame2e

// The three team tests the format-gate design (FORMAT_EVOLUTION rule 2,
// OV48) and the task-tree design (TASK_SYSTEM_DESIGN 6.13 and 6.15, OV41)
// name, with the dimensions the te-c and te-b tests leave out:
//
//   - TestGateReadOnlyEveryPath: one write verb per path (task edit on a
//     branch row, task edit on a live row, claim, store write, land) on a
//     read-only project, for each way a project closes: an unknown feature,
//     a floor above this binary, and an unparseable floor plus an unknown
//     feature (a mangled floor must never unlock the folder).
//   - TestGateSignalsSmokeUnparseableFloor: the signals smoke of
//     TestTeC_SignalsGateSmokeWithARemote for the third gate, against the
//     harness's local bare remote.
//   - TestTreeCheckAfterGitMergeTwoClones: the 6.13 merge matrix on the
//     BRANCH path (rows in .lore/data, live rows off): two developers change
//     the tree on main, the second one runs a real `git pull` merge (through
//     lore's merge driver where both touched the same row file), and both
//     clones show the same tree and the same `task tree check` findings.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// ovUnparseableFloor is a min_lore_version no version parser reads.
const ovUnparseableFloor = "not-a-version"

// ovUnparseableGate closes a folder with an unparseable floor plus an
// unknown feature: the unknown feature alone must still block (gate.go
// Check: "a hand-mangled floor must not unlock a format").
var ovUnparseableGate = teCGate{name: "unparseable_floor_plus_unknown_feature", says: teCFutureFeature, apply: func(t *testing.T, o map[string]json.RawMessage) {
	var fs []string
	if raw, ok := o["features"]; ok {
		if err := json.Unmarshal(raw, &fs); err != nil {
			t.Fatalf("features: %v", err)
		}
	}
	o["features"] = teCJSON(t, append(fs, teCFutureFeature))
	o["min_lore_version"] = teCJSON(t, ovUnparseableFloor)
}}

// ovGates is every way a project becomes read-only for this binary.
func ovGates() []teCGate { return append(slices.Clone(teCGates), ovUnparseableGate) }

// ovLiveOff is the environment that turns live rows off, so writes go to
// the checkout's .lore/data (the branch path).
const ovLiveOff = "LORE_LIVE=0"

// TestGateReadOnlyEveryPath: dev1 has a live row, a branch row (committed
// in .lore/data) and a task to land; dev2 pushes a gate in _meta.json and
// dev1 pulls it. Each write path is refused with E_OLD_LORE (exit 19)
// before anything is written: the checkout (.lore/data included), main,
// the live ref and the signals ref keep their state. The store path runs
// in an agency world whose org store project declares the same gate.
func TestGateReadOnlyEveryPath(t *testing.T) {
	for _, g := range ovGates() {
		t.Run(g.name, func(t *testing.T) {
			rt := newRepoTeam(t, capLand, th.Cmd("task", "claim"), th.Flag("no-live", "task", "add"), th.Flag("no-live", "task", "edit"))
			live := rt.addTask(t, rt.c1, "live row")
			toLand := rt.addTask(t, rt.c1, "task to land")
			branch := mustID(t, rt.c1.MustLore("task", "add", "branch row", "--tasklist", rt.tasklist, "--no-live"), "tsk")
			rt.c1.CommitAll("a branch row")
			rt.c1.AdminPush("origin", "main")
			if _, ok := rt.mainTask(t, branch); !ok {
				t.Fatalf("premise: the --no-live row %s is a file on main", branch)
			}
			branchFile := mainRowPath("", tblTasks, branch)
			branchBody, _ := rt.c1.ReadFile(branchFile)

			rt.c2.Git("pull", "-q", "--ff-only", "origin", "main")
			teCEditMeta(t, rt.c2, teCRepoMeta, g)
			rt.c2.CommitAll("declare a future format")
			rt.c2.AdminPush("origin", "main")
			rt.c1.Git("pull", "-q", "--ff-only", "origin", "main")

			before := teCRefs(rt.app, refMain, refLive, refSignals)
			status := rt.c1.Status()
			for _, p := range []struct {
				name string
				c    *th.Clone
				args []string
			}{
				{"task edit on a branch row", rt.c1, []string{"task", "edit", branch, "--priority", "high", "--no-live"}},
				{"task edit on a branch row, live off", rt.c1.With(ovLiveOff), []string{"task", "edit", branch, "--priority", "high"}},
				{"task add on the branch, live off", rt.c1.With(ovLiveOff), []string{"task", "add", "new branch row", "--tasklist", rt.tasklist}},
				{"task edit on a live row", rt.c1, []string{"task", "edit", live, "--priority", "high"}},
				{"claim", rt.c1, []string{"task", "claim", live}},
				{"land", rt.c1, []string{"land", toLand}},
			} {
				t.Run(p.name, func(t *testing.T) {
					p.c.Lore(p.args...).Fails(t, th.ExitGuard, "E_OLD_LORE")
				})
			}
			teCSameRefs(t, rt.app, before)
			if now := rt.c1.Status(); now != status {
				t.Errorf("a refused write changed the checkout:\nbefore %q\nafter  %q", status, now)
			}
			if now, _ := rt.c1.ReadFile(branchFile); now != branchBody {
				t.Errorf("the branch row file %s changed on a read-only project:\nbefore %s\nafter  %s", branchFile, branchBody, now)
			}
			r := rt.c1.MustLore("task", "show", branch).Says(t, "branch row")
			if !strings.Contains(r.Stderr, "read-only here") || !strings.Contains(r.Stderr, g.says) {
				t.Errorf("a read must print the read-only line naming %q on stderr: %s", g.says, r)
			}

			t.Run("store write", func(t *testing.T) {
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
				sbefore := teCRefs(a.store, refMain, storeLiveRef, storeSignals())
				for _, w := range [][]string{
					{"task", "add", "another", "--tasklist", a.tl},
					{"task", "edit", id, "--priority", "high"},
					{"task", "claim", id},
				} {
					a.aliceApp.Lore(w...).Fails(t, th.ExitGuard, "E_OLD_LORE")
				}
				teCSameRefs(t, a.store, sbefore)
				a.aliceApp.MustLore("task", "show", id).Says(t, "store task")
			})
		})
	}
}

// TestGateSignalsSmokeUnparseableFloor: a claim taken while the project was
// writable, then dev2 pushes an unparseable floor plus an unknown feature.
// Every signals action of the holder (heartbeat, release, claim next) and
// of a teammate (claim, revoke) is refused before anything is pushed to the
// bare remote, whose signals ref keeps its commit.
func TestGateSignalsSmokeUnparseableFloor(t *testing.T) {
	g := ovUnparseableGate
	rt := newRepoTeam(t, th.Cmd("task", "claim"), th.Cmd("task", "heartbeat"), th.Cmd("task", "release"), th.Cmd("task", "revoke"))
	id := rt.addTask(t, rt.c1, "claimed before the gate")
	other := rt.addTask(t, rt.c1, "free task")
	rt.c1.MustLore("task", "claim", id)
	sig := rt.app.Rev(refSignals)
	if sig == "" {
		t.Fatalf("the claim must reach the remote signals ref %s", refSignals)
	}

	rt.c2.Git("pull", "-q", "--ff-only", "origin", "main")
	teCEditMeta(t, rt.c2, teCRepoMeta, g)
	rt.c2.CommitAll("declare a future format")
	rt.c2.AdminPush("origin", "main")
	rt.c1.Git("pull", "-q", "--ff-only", "origin", "main")

	for _, w := range []struct {
		name string
		c    *th.Clone
		args []string
	}{
		{"holder heartbeat", rt.c1, []string{"task", "heartbeat", id}},
		{"holder release", rt.c1, []string{"task", "release", id}},
		{"holder claims next", rt.c1, []string{"task", "claim", other}},
		{"teammate claim", rt.c2, []string{"task", "claim", other}},
		{"teammate revoke", rt.c2, []string{"task", "revoke", id}},
	} {
		t.Run(w.name, func(t *testing.T) {
			w.c.Lore(w.args...).Fails(t, th.ExitGuard, "E_OLD_LORE")
		})
	}
	if now := rt.app.Rev(refSignals); now != sig {
		t.Errorf("the signals ref moved on a read-only project: %s -> %s", sig, now)
	}
	r := rt.c1.MustLore("task", "show", id)
	if !strings.Contains(r.Stderr, "read-only here") || !strings.Contains(r.Stderr, g.says) {
		t.Errorf("a read must print the read-only line naming %q on stderr: %s", g.says, r)
	}
}

// TestTreeCheckAfterGitMergeTwoClones: the 6.13 merge matrix with every row
// on the branch (live rows off). dev1 and dev2 start from the same main;
// dev1 pushes first, dev2 commits and merges with a real `git pull
// --no-rebase` (a merge commit: two parents). The rows that both sides
// changed (the re-parented X) merge through lore's merge driver, HLC-newer
// side wins. Then both clones report the same findings (a parent cycle
// whose top is the smallest id, an orphan of a trashed parent, an open
// child of a closed parent, a subtree over max_depth, two siblings sharing
// a rank); the check writes nothing; --fix on dev2 writes only the cycle's
// shown result, which dev1 sees after its pull.
func TestTreeCheckAfterGitMergeTwoClones(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("task", "tree", "check"), th.Flag("parent", "task", "add"), th.Flag("top", "task", "list"))
	rt.dev1.SetNow(t0)
	rt.dev2.SetNow(t0)
	c1, c2 := rt.c1.With(ovLiveOff), rt.c2.With(ovLiveOff)
	add := func(c *th.Clone, title string, extra ...string) string {
		t.Helper()
		return mustID(t, c.MustLore(append([]string{"task", "add", title, "--tasklist", rt.tasklist}, extra...)...), "tsk")
	}
	x := add(c1, "cycle x")
	p := add(c1, "cycle p")
	m := add(c1, "re-parented on both sides")
	m1 := add(c1, "dev1's parent for m")
	m2 := add(c1, "dev2's parent for m")
	q := add(c1, "rank parent")
	add(c1, "rank first", "--parent", q)
	r := add(c1, "parent trashed on one side")
	s := add(c1, "parent closed on one side")
	a := add(c1, "depth a")
	b := add(c1, "depth b", "--parent", a)
	cc := add(c1, "depth c", "--parent", b)
	e := add(c1, "depth e")
	c1.CommitAll("the base tree")
	c1.AdminPush("origin", "main")
	if _, ok := rt.mainTask(t, x); !ok {
		t.Fatalf("premise: with live rows off the tasks are files on main")
	}
	c2.Git("pull", "-q", "--ff-only", "origin", "main")
	if before := teBTreeCheckOn(t, c2); len(before.Findings) != 0 {
		t.Fatalf("premise: a clean tree before the round: %+v", before.Findings)
	}

	rt.dev1.SetNow(t0.Add(time.Hour))
	c1.MustLore("task", "edit", x, "--parent", p)
	c1.MustLore("task", "edit", m, "--parent", m1)
	k1 := add(c1, "rank dev1", "--parent", q)
	c1.MustLore("task", "delete", r, "--reason", "not needed", "--yes")
	c1.MustLore("task", "done", s, "--no-lesson")
	d := add(c1, "depth d", "--parent", cc) // depth 4: allowed
	c1.CommitAll("dev1's tree changes")
	c1.AdminPush("origin", "main")

	rt.dev2.SetNow(t0.Add(2 * time.Hour)) // dev2's m edit is the HLC-newer one
	c2.MustLore("task", "edit", p, "--parent", x)
	c2.MustLore("task", "edit", m, "--parent", m2)
	k2 := add(c2, "rank dev2", "--parent", q)
	rChild := add(c2, "child of the trashed", "--parent", r)
	sChild := add(c2, "child of the closed", "--parent", s)
	c2.MustLore("task", "edit", a, "--parent", e) // c at depth 4: allowed
	c2.CommitAll("dev2's tree changes")

	if res := c2.TryGit("-c", "pull.rebase=false", "pull", "--no-edit", "origin", "main"); res.Code != 0 {
		t.Fatalf("git pull merge of the two tree rounds: %s\nstatus: %s", res, c2.Status())
	}
	if n := len(strings.Fields(c2.Git("rev-list", "--parents", "-n", "1", "HEAD"))); n != 3 {
		t.Fatalf("premise: dev2's pull is a real merge commit (two parents), got %d fields", n)
	}
	if st := c2.Status(); st != "" {
		t.Fatalf("the merge leaves a clean checkout (no conflict markers): %q", st)
	}
	c2.AdminPush("origin", "main")
	c1.Git("pull", "-q", "--ff-only", "origin", "main")

	if got := teBParentOf(t, c2, m); got != m2 {
		t.Fatalf("the row both sides re-parented keeps the HLC-newer side's parent %s: %q", m2, got)
	}
	mainBefore := rt.app.Rev(refMain)
	v1, v2 := teBTreeCheckOn(t, c1), teBTreeCheckOn(t, c2)
	if v1.canon() != v2.canon() {
		t.Fatalf("both clones report the same findings after the merge:\ndev1 %s\ndev2 %s", v1.canon(), v2.canon())
	}
	top := min(x, p)
	if cyc := v1.of("parent_cycle"); len(cyc) != 1 || cyc[0].ID != top {
		t.Fatalf("one parent_cycle finding whose top is the smallest id %s: %+v\n%s", top, cyc, v1.canon())
	}
	if o := v1.of("orphan"); len(o) != 1 || o[0].ID != rChild || o[0].Parent != r || !strings.Contains(o[0].Message, "trash") {
		t.Fatalf("the child of the trashed parent is an orphan whose parent is in the trash: %+v", o)
	}
	if pc := v1.of("parent_closed"); len(pc) != 1 || pc[0].ID != sChild || pc[0].Parent != s {
		t.Fatalf("the open child of the closed parent is parent_closed: %+v", pc)
	}
	if od := v1.of("over_depth"); len(od) != 1 || od[0].ID != d {
		t.Fatalf("the merge pushes %s to depth 5 (over the default 4): %+v", d, od)
	}
	if sr := v1.sharedUnder(q); len(sr) != 1 || !strings.Contains(sr[0].Message, k1) || !strings.Contains(sr[0].Message, k2) {
		t.Fatalf("the two siblings added on both sides share a rank: %+v", v1.of("shared_rank"))
	}
	for _, c := range []*th.Clone{c1, c2} {
		if st := c.Status(); st != "" {
			t.Fatalf("a tree check without --fix writes nothing (no repair on read): %q", st)
		}
		tops := teBTopIDs(t, c)
		if !slices.Contains(tops, top) || slices.Contains(tops, max(x, p)) || !slices.Contains(tops, rChild) {
			t.Fatalf("every clone shows the cycle's top %s and the orphan %s at top level, not %s: %v", top, rChild, max(x, p), tops)
		}
	}
	if rt.app.Rev(refMain) != mainBefore {
		t.Fatal("a tree check without --fix pushes nothing")
	}

	fixed := teBTreeCheckOn(t, c2, "--fix")
	if fixed.Fixed != 1 {
		t.Fatalf("--fix repairs exactly the one parent cycle: %+v", fixed)
	}
	changed := strings.Fields(c2.Git("status", "--porcelain", "--", ".lore/data"))
	var files []string
	for _, f := range changed {
		if strings.HasSuffix(f, ".json") {
			files = append(files, f)
		}
	}
	if len(files) != 1 || !strings.Contains(files[0], top) {
		t.Fatalf("--fix writes only the cycle top's row file (%s): %v", top, changed)
	}
	c2.CommitAll("tree check --fix")
	c2.AdminPush("origin", "main")
	c1.Git("pull", "-q", "--ff-only", "origin", "main")
	after := teBTreeCheckOn(t, c1)
	if len(after.of("parent_cycle")) != 0 {
		t.Fatalf("dev1 sees the repaired cycle after its pull: %s", after.canon())
	}
	for _, kind := range []string{"orphan", "parent_closed", "over_depth"} {
		if len(after.of(kind)) != 1 {
			t.Fatalf("--fix leaves the %s finding alone (it names its own fix): %s", kind, after.canon())
		}
	}
}
