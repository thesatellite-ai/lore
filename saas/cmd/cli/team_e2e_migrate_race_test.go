//go:build teame2e

package main

// FORMAT_EVOLUTION 7.8 race tests for `lore migrate run`: real lore
// processes, real git, started at one barrier (th.World.Simultaneously).
//
//	TestMigrateRaceOneRepo      N processes migrate the same clone at once: one
//	                            holds the machine lock, the rest stop E_MIGRATE_LOCKED
//	                            or find nothing left; one migration's result, no lock left
//	TestMigrateRaceNClones      N clones of one remote migrate at once with push: each
//	                            run succeeds or stops E_MIGRATE_LOCKED; after reruns every
//	                            clone's .lore/data is byte-identical
//	TestMigrateRaceOfflineThenMerge
//	                            two clones migrate --offline under different clocks and
//	                            actors, commit, push one, pull into the other: no conflict,
//	                            the same tree
//
// Not built here (FORMAT_EVOLUTION 7.8 rows that need hooks the binary does
// not have yet): RaceDuringSync, RaceLiveWritesAndClaims, LockHolderCrash,
// RollbackAfterTeammatesPushed, CrashMidway, TestLockOrderRanks.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	th "saas/cmd/cli/internal/teamharness"
)

// rowRef matches a "memories/<id>" row a migrate run lists.
var rowRef = regexp.MustCompile(`memories/mem_[0-9a-f]{32}`)

// migrateLockedCode is the error code a run that lost a migration lock prints.
const migrateLockedCode = "E_MIGRATE_LOCKED"

// v1Memories is how many v0.1.17 rows each race fixture plants.
const v1Memories = 6

// v1MemoryDoc is a v0.1.17 memories row file (`_v: 1`, no `_hlc`), the
// layout v0.1.17 wrote; i makes the id and body unique.
func v1MemoryDoc(i int) (string, string) {
	id := fmt.Sprintf("mem_01a1a59b891c70b78914%012x", i+1)
	doc := `{"_table":"memories","_v":1,"body":"race fixture ` + strconv.Itoa(i) + ` café","created_at":"2026-11-04T08:09:56.252002659Z","id":"` + id +
		`","kind":"fact","source_kind":"manual","updated_at":"2026-11-04T08:09:56.252002894Z","updated_by":"act_0e45506e64357c94a87e70ef1c6bbba9"}`
	return id, doc
}

// migrateFixture bootstraps acme/app with v1 memory rows committed on main:
// alice runs lore init, plants the v1 files, commits and pushes.
func migrateFixture(t *testing.T) (*th.World, *th.Remote, *th.Clone) {
	t.Helper()
	w := newTeamWorld(t, th.Cmd("migrate", "run"))
	app := w.Remote(slugApp)
	alice := w.Human("alice", "alice@acme.example")
	c := alice.Clone(app, "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	for i := range v1Memories {
		id, doc := v1MemoryDoc(i)
		c.WriteFile(filepath.Join(dataDir, tblMemories, id+".json"), doc)
	}
	c.CommitAll("lore: v0.1.17 rows")
	c.AdminPush("origin", "main")
	return w, app, c
}

// memoryFiles reads the clone's memories folder, keyed by file name.
func memoryFiles(t *testing.T, c *th.Clone) map[string]string {
	t.Helper()
	root := filepath.Join(c.Dir, dataDir, tblMemories)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(filepath.Join(root, e.Name())))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

// v1Files returns the planted fixture files, keyed by file name.
func v1Files() map[string]string {
	out := map[string]string{}
	for i := range v1Memories {
		id, doc := v1MemoryDoc(i)
		out[id+".json"] = doc
	}
	return out
}

// assertMigrated checks every planted row is `_v: 2` and nothing else of the
// file changed beyond the format (the body survives).
func assertMigrated(t *testing.T, who string, files map[string]string) {
	t.Helper()
	for name := range v1Files() {
		doc, ok := files[name]
		if !ok {
			t.Fatalf("%s: %s is gone", who, name)
		}
		if !strings.Contains(doc, `"_v": 2`) || !strings.Contains(doc, `"_hlc"`) || !strings.Contains(doc, "race fixture") {
			t.Fatalf("%s: %s not migrated:\n%s", who, name, doc)
		}
	}
}

// raceOutcome classifies a run: "ok" or "locked"; anything else fails.
func raceOutcome(t *testing.T, r th.Result) string {
	t.Helper()
	switch {
	case r.Code == 0:
		return "ok"
	case strings.Contains(r.Out(), migrateLockedCode):
		return "locked"
	}
	t.Fatalf("a racing migrate run failed with neither success nor %s:\n%s", migrateLockedCode, r)
	return ""
}

// TestMigrateRaceOneRepo: four `lore migrate run --no-push` processes on one
// clone at the same instant. The machine lock (.lore/state/migrate.lock) lets
// one through; the others stop with E_MIGRATE_LOCKED naming the holder, or,
// when they start after it finished, find nothing to do: across all runs
// each row is rewritten exactly once. The rows end at
// `_v: 2` with the bytes one offline migration of a fresh clone gives, the
// lock file is gone and a rerun changes nothing.
func TestMigrateRaceOneRepo(t *testing.T) {
	w, app, c := migrateFixture(t)
	// A held lock (a live process named in it) refuses the run and leaves
	// the rows alone: the path a racer that loses takes.
	lock := filepath.Join(c.Dir, ".lore", "state", "migrate.lock")
	c.WriteFile(filepath.Join(".lore", "state", "migrate.lock"), strconv.Itoa(os.Getpid()))
	if r := c.Lore("migrate", "run", "--no-push"); raceOutcome(t, r) != "locked" {
		t.Fatalf("a run under a held lock went ahead:\n%s", r)
	} else {
		r.Says(t, strconv.Itoa(os.Getpid()))
	}
	for name, doc := range memoryFiles(t, c) {
		if doc != v1Files()[name] {
			t.Fatalf("a locked-out run changed %s", name)
		}
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	const racers = 4
	ps := make([]*th.Pending, racers)
	for i := range ps {
		ps[i] = c.Prepare("migrate", "run", "--no-push")
	}
	ok, locked := 0, 0
	rewrote := map[string]int{} // row → how many racing runs rewrote it
	for _, r := range w.Simultaneously(ps...) {
		switch raceOutcome(t, r) {
		case "ok":
			ok++
			for _, m := range rowRef.FindAllString(r.Stdout, -1) {
				rewrote[m]++
			}
		case "locked":
			locked++
			r.Says(t, "pid ")
		}
	}
	if ok == 0 {
		t.Fatal("no racing run succeeded")
	}
	// No row is rewritten by two runs, and every row by one (the lock or
	// the _v check in between keeps the runs from interleaving).
	if len(rewrote) != v1Memories {
		t.Fatalf("the racing runs rewrote %d distinct rows, want %d: %v", len(rewrote), v1Memories, rewrote)
	}
	for row, n := range rewrote {
		if n != 1 {
			t.Fatalf("%s rewritten by %d racing runs", row, n)
		}
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("the migration lock outlived every run: %v", err)
	}
	got := memoryFiles(t, c)
	assertMigrated(t, "alice", got)
	c.MustLore("migrate", "run", "--no-push").Never(t, "memories/mem_")
	if again := memoryFiles(t, c); len(again) != len(got) {
		t.Fatal("a rerun changed the memories folder")
	} else {
		for k, v := range got {
			if again[k] != v {
				t.Fatalf("a rerun rewrote %s", k)
			}
		}
	}
	// Reference: one uncontended offline migration of the same commit.
	bob := w.Human("bob", "bob@acme.example")
	ref := bob.Clone(app, "app")
	ref.MustLore("migrate", "run", "--offline")
	want := memoryFiles(t, ref)
	for _, k := range sortedKeys(v1Files()) {
		if got[k] != want[k] {
			t.Errorf("%s differs from an uncontended migration:\n%s\n%s", k, got[k], want[k])
		}
	}
	t.Logf("%d runs succeeded, %d were locked out", ok, locked)
}

// TestMigrateRaceNClones (7.8, the barrier variant without a fake GitHub
// pause): five clones of one remote start `lore migrate run` at once, each
// pushing (so each asks for the team lock on the signals ref). Every run
// succeeds or stops E_MIGRATE_LOCKED; then each clone that was locked out
// reruns until it succeeds, and every clone's memories folder is
// byte-identical to every other's.
func TestMigrateRaceNClones(t *testing.T) {
	w, app, first := migrateFixture(t)
	clones := []*th.Clone{first}
	for _, name := range []string{"bob", "carol", "dave", "erin"} {
		clones = append(clones, w.Human(name, name+"@acme.example").Clone(app, "app"))
	}
	ps := make([]*th.Pending, len(clones))
	for i, c := range clones {
		ps[i] = c.Prepare("migrate", "run")
	}
	results := w.Simultaneously(ps...)
	ok := 0
	for i, r := range results {
		if raceOutcome(t, r) == "ok" {
			ok++
			continue
		}
		// Locked out: once the winner released, a rerun completes (7.8).
		deadline := time.Now().Add(30 * time.Second)
		for {
			rr := clones[i].Lore("migrate", "run")
			if raceOutcome(t, rr) == "ok" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("clone %d stayed locked out:\n%s", i, rr)
			}
			time.Sleep(200 * time.Millisecond) // a polling retry, as a person would rerun
		}
	}
	if ok == 0 {
		t.Fatal("no racing run succeeded")
	}
	want := memoryFiles(t, clones[0])
	assertMigrated(t, "clone 0", want)
	for i, c := range clones[1:] {
		got := memoryFiles(t, c)
		for _, k := range sortedKeys(want) {
			if got[k] != want[k] {
				t.Errorf("clone %d: %s differs:\n%s\n%s", i+1, k, got[k], want[k])
			}
		}
	}
	if app.Has(refSignals) {
		for _, f := range app.Files(refSignals) {
			if strings.Contains(f, "lore-migrate") {
				t.Errorf("the team migration hold %s outlived every run", f)
			}
		}
	}
}

// TestMigrateRaceOfflineThenMerge (7.8): two clones migrate --offline with
// clocks three days apart and different actors, commit and push in turn;
// the second pulls the first's commit with no conflict and the same tree,
// and a further run reports nothing to do.
func TestMigrateRaceOfflineThenMerge(t *testing.T) {
	w, app, c1 := migrateFixture(t)
	bob := w.Human("bob", "bob@acme.example")
	c2 := bob.Clone(app, "app")
	c1.A.SetNow(time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC))
	bob.SetNow(time.Date(2026, 11, 8, 23, 0, 0, 0, time.UTC))
	bob.SetEnv("TZ", "Asia/Kolkata")
	res := w.Simultaneously(c1.Prepare("migrate", "run", "--offline"), c2.Prepare("migrate", "run", "--offline"))
	for _, r := range res {
		r.OK(t)
	}
	f1, f2 := memoryFiles(t, c1), memoryFiles(t, c2)
	assertMigrated(t, "alice", f1)
	for _, k := range sortedKeys(f1) {
		if f1[k] != f2[k] {
			t.Errorf("%s differs between the offline runs:\n%s\n%s", k, f1[k], f2[k])
		}
	}
	c1.Git("add", "-A", dataDir)
	c1.Git("commit", "-q", "-m", "lore: migrate")
	c1.AdminPush("origin", "main")
	c2.Git("add", "-A", dataDir)
	c2.Git("commit", "-q", "-m", "lore: migrate")
	if r := c2.TryGit("pull", "--no-rebase", "--no-edit", "origin", "main"); r.Code != 0 {
		t.Fatalf("pulling the other offline migration conflicted:\n%s", r)
	}
	if r := c2.TryGit("diff", "--quiet", "origin/main", "--", dataDir); r.Code != 0 {
		t.Fatalf("the merged tree differs from the pushed migration:\n%s", c2.Git("diff", "--stat", "origin/main", "--", dataDir))
	}
	c2.MustLore("migrate", "run", "--offline").Never(t, "memories/mem_")
}
