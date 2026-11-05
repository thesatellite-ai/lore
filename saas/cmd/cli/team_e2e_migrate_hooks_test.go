//go:build teame2e

package main

// FORMAT_EVOLUTION 7.8 race tests that stop a migration at a named point
// (the test build's hooks, migrate_testhooks_loretest.go):
//
//	TestMigrateRaceCrashMidway   kill -9 at every point of a run: status names the
//	                             interrupted run, unlock + run resumes to the bytes of
//	                             an uninterrupted run, rollback restores the originals
//	TestMigrateRaceLockHolderCrash
//	                             the holder pauses right after the team lock, then is
//	                             killed: a teammate is locked out until the hold
//	                             expires, then takes over; unlock rules on both machines
//	TestMigrateRaceRollbackAfterTeammatesPushed
//	                             a teammate's edit blocks the rollback (exactly that
//	                             row), reverting it lets it through (new row kept); the
//	                             variant lands the edit while rollback is paused between
//	                             its check and its restore: still refused
//	TestMigrateRaceDuringSync    sync passes while the run is paused mid branch phase:
//	                             no error, every row ends at v2 with its content, the
//	                             next pass has nothing to do
//
// The binary must be the loretest build (pl/build.sh); a release build has
// no hooks and these tests fail at the first "did not stop" check.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	th "saas/cmd/cli/internal/teamharness"
)

// The hook variables (migrate_testhooks_loretest.go).
const (
	envCrashAt  = "LORE_TEST_MIGRATE_CRASH_AT"
	envPauseAt  = "LORE_TEST_MIGRATE_PAUSE_AT"
	envPauseDir = "LORE_TEST_MIGRATE_PAUSE_DIR"
)

// tblPatterns is the second migrated table of the hooks fixture (CoreTables
// order: memories, then patterns), so a crash between tables leaves the
// branch files half done.
const tblPatterns = "patterns"

// v1Patterns is how many v0.1.17 patterns rows the hooks fixture plants.
const v1Patterns = 3

// pauseWait bounds how long a test waits for a paused process to reach its point.
const pauseWait = 60 * time.Second

// v1PatternDoc is a v0.1.17 patterns row file; i makes it unique.
func v1PatternDoc(i int) (string, string) {
	id := fmt.Sprintf("pat_01a1a59b891c70b78914%012x", i+1)
	doc := `{"_table":"patterns","_v":1,"body":"hooks fixture pattern ` + strconv.Itoa(i) + `","created_at":"2026-11-04T08:09:56.252002659Z","id":"` + id +
		`","title":"pattern ` + strconv.Itoa(i) + `","updated_at":"2026-11-04T08:09:56.252002894Z","updated_by":"act_0e45506e64357c94a87e70ef1c6bbba9"}`
	return id, doc
}

// hooksFixture is migrateFixture plus v1 patterns rows, committed and pushed.
func hooksFixture(t *testing.T) (*th.World, *th.Remote, *th.Clone) {
	t.Helper()
	w, app, c := migrateFixture(t)
	for i := range v1Patterns {
		id, doc := v1PatternDoc(i)
		c.WriteFile(filepath.Join(dataDir, tblPatterns, id+".json"), doc)
	}
	c.CommitAll("lore: v0.1.17 patterns")
	c.AdminPush("origin", "main")
	return w, app, c
}

// fixtureOriginals is every planted file, keyed "table/file".
func fixtureOriginals() map[string]string {
	out := map[string]string{}
	for k, v := range v1Files() {
		out[tblMemories+"/"+k] = v
	}
	for i := range v1Patterns {
		id, doc := v1PatternDoc(i)
		out[tblPatterns+"/"+id+".json"] = doc
	}
	return out
}

// tableFiles reads the clone's memories and patterns folders, keyed "table/file".
func tableFiles(t *testing.T, c *th.Clone) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, tbl := range []string{tblMemories, tblPatterns} {
		entries, err := os.ReadDir(filepath.Join(c.Dir, dataDir, tbl))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Clean(filepath.Join(c.Dir, dataDir, tbl, e.Name())))
			if err != nil {
				t.Fatal(err)
			}
			out[tbl+"/"+e.Name()] = string(b)
		}
	}
	return out
}

// sameFiles fails when got and want differ in any key or byte.
func sameFiles(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for _, k := range sortedKeys(want) {
		if got[k] != want[k] {
			t.Errorf("%s: %s differs:\n got %s\nwant %s", what, k, got[k], want[k])
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: unexpected file %s", what, k)
		}
	}
}

// crashed fails unless r is a run killed by the hook (no exit 0, no report).
func crashed(t *testing.T, point string, r th.Result) {
	t.Helper()
	if r.Code == 0 {
		t.Fatalf("the run did not stop at %s (is LORE_TEAM_BIN a loretest build?):\n%s", point, r)
	}
}

// paused starts c's command with a pause at point and returns the running
// command, once the process has reached the point, plus the pause folder.
type pausedRun struct {
	t    *testing.T
	dir  string
	base string
	pid  int
	done chan th.Result
}

// pauseAt starts args in c paused at point and waits until it gets there.
func pauseAt(t *testing.T, c *th.Clone, point string, args ...string) *pausedRun {
	t.Helper()
	dir := t.TempDir()
	p := &pausedRun{t: t, dir: dir, base: filepath.Join(dir, point), done: make(chan th.Result, 1)}
	run := c.With(envPauseAt+"="+point, envPauseDir+"="+dir)
	go func() { p.done <- run.Lore(args...) }()
	deadline := time.Now().Add(pauseWait)
	for {
		if b, err := os.ReadFile(p.base + ".paused"); err == nil && len(b) > 0 {
			pid, perr := strconv.Atoi(string(b))
			if perr != nil {
				t.Fatalf("pause file: %v", perr)
			}
			p.pid = pid
			return p
		}
		select {
		case r := <-p.done:
			t.Fatalf("%v ended before reaching %s:\n%s", args, point, r)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%v never reached %s", args, point)
		}
		time.Sleep(10 * time.Millisecond) // polling the hook's pause file
	}
}

// resume lets the paused process go on and returns its result.
func (p *pausedRun) resume() th.Result {
	p.t.Helper()
	if err := os.WriteFile(p.base+".resume", nil, 0o600); err != nil {
		p.t.Fatal(err)
	}
	return <-p.done
}

// kill ends the paused process as kill -9 does and returns its result.
func (p *pausedRun) kill() th.Result {
	p.t.Helper()
	proc, err := os.FindProcess(p.pid)
	if err != nil {
		p.t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		p.t.Fatal(err)
	}
	return <-p.done
}

// crashPoints are the run's points in order; "table:1" is between the
// memories and the patterns rewrite (half the branch files done).
var crashPoints = []string{"team-locked", "begun", "settings", "live", "table:1", "table:2", "finish"}

// TestMigrateRaceCrashMidway (7.7, 7.8): for every point, alice's run is
// killed there. `migrate status` names the interrupted run from "begun" on
// and the machine lock as stale; `unlock` then `run` ends with the bytes an
// uninterrupted run gives; bob, killed at the same point, rolls back to the
// planted bytes.
func TestMigrateRaceCrashMidway(t *testing.T) {
	w, app, _ := hooksFixture(t)
	ref := w.Human("ref", "ref@acme.example").Clone(app, "app")
	ref.MustLore("migrate", "run", "--no-push")
	want := tableFiles(t, ref)
	for i, point := range crashPoints {
		t.Run(strings.ReplaceAll(point, ":", "_"), func(t *testing.T) {
			// Each point gets fresh clones of the unmigrated remote.
			name := "c" + strconv.Itoa(i)
			a := w.Human(name+"a", name+"a@acme.example").Clone(app, "app")
			crashed(t, point, a.With(envCrashAt+"="+point).Lore("migrate", "run", "--no-push"))
			st := a.MustLore("migrate", "status")
			st.Says(t, "stale")
			if point != "team-locked" {
				st.Says(t, "interrupted migration")
			}
			a.MustLore("migrate", "unlock")
			a.MustLore("migrate", "run", "--no-push")
			sameFiles(t, "resumed run", tableFiles(t, a), want)

			b := w.Human(name+"b", name+"b@acme.example").Clone(app, "app")
			crashed(t, point, b.With(envCrashAt+"="+point).Lore("migrate", "run", "--no-push"))
			b.MustLore("migrate", "unlock")
			if r := b.Lore("migrate", "rollback"); r.Code != 0 && point != "team-locked" {
				t.Fatalf("rollback after a crash at %s failed:\n%s", point, r)
			}
			sameFiles(t, "rolled back", tableFiles(t, b), fixtureOriginals())
		})
	}
}

// TestMigrateRaceLockHolderCrash (7.2, 7.8): alice's pushing run pauses
// right after it took the team lock. While it lives, her own `unlock`
// refuses; bob's run is E_MIGRATE_LOCKED naming her and his `unlock --team`
// refuses. Then her process is killed (kill -9): her `unlock` clears the
// machine lock; bob stays locked out until his clock passes the hold's
// expiry plus the grace AND he has watched the same renewed_seq for
// renew_every (carol, looking for the first time then, is still locked
// out); then his run takes over and completes.
//
// Not asserted: "refused from another host" (every clone runs on this
// machine; the refusal tested is "another actor").
func TestMigrateRaceLockHolderCrash(t *testing.T) {
	w, app, alice := hooksFixture(t)
	bob := w.Human("bob", "bob@acme.example")
	bc := bob.Clone(app, "app")
	start := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	alice.A.SetNow(start)
	bob.SetNow(start)
	p := pauseAt(t, alice, "team-locked", "migrate", "run")
	alice.Lore("migrate", "unlock").Refused(t, migrateLockedCode)
	bc.Lore("migrate", "run").Refused(t, migrateLockedCode, "alice")
	bc.Lore("migrate", "unlock", "--team", "--yes").Refused(t, migrateLockedCode)
	crashed(t, "team-locked", p.kill())
	alice.MustLore("migrate", "unlock").Says(t, "released")
	bob.Advance(5 * time.Minute) // inside the 10-minute lease
	bc.Lore("migrate", "run").Refused(t, migrateLockedCode)
	bob.Advance(6 * time.Minute) // past Expires, inside the 2-minute grace
	bc.Lore("migrate", "run").Refused(t, migrateLockedCode)
	bob.Advance(2 * time.Minute) // past Expires + grace, the same renewed_seq watched since t0
	// carol looks for the first time now: past the time check, but she has
	// not watched the hold for renew_every yet, so it is still held for her.
	carol := w.Human("carol", "carol@acme.example")
	carol.SetNow(bob.Now())
	carol.Clone(app, "app").Lore("migrate", "run").Refused(t, migrateLockedCode)
	bc.MustLore("migrate", "run").Says(t, "team lock")
	assertMigrated(t, "bob", memoryFiles(t, bc))
}

// TestMigrateRaceRollbackAfterTeammatesPushed (7.6, 7.8). alice migrates,
// commits and pushes; tomas pulls, edits one migrated row, adds one new
// row, pushes; alice pulls. Her rollback refuses naming exactly the edited
// row and changes nothing; once tomas restores the migrated bytes (pushed,
// pulled), the rollback restores every migrated row byte for byte, keeps
// his new row (listed) and leaves _meta.json raised. The variant lands
// tomas's edit while the rollback is paused between its check and its
// restore: the re-check refuses.
func TestMigrateRaceRollbackAfterTeammatesPushed(t *testing.T) {
	for _, variant := range []string{"pulled-first", "between-check-and-restore"} {
		t.Run(variant, func(t *testing.T) {
			w, app, alice := hooksFixture(t)
			alice.MustLore("migrate", "run", "--no-push")
			alice.CommitAll("lore: migrate format")
			alice.AdminPush("origin", "main")
			migrated := tableFiles(t, alice)

			tomas := w.Human("tomas", "tomas@acme.example").Clone(app, "app")
			id, _ := v1MemoryDoc(0)
			edited := filepath.Join(dataDir, tblMemories, id+".json")
			tomas.WriteFile(edited, strings.Replace(migrated[tblMemories+"/"+id+".json"], "race fixture 0", "race fixture 0 (edited by tomas)", 1))
			tomas.MustLore("memory", "add", "tomas adds a row after the migration")
			tomas.CommitAll("tomas: edit and add")
			tomas.AdminPush("origin", "main")

			if variant == "pulled-first" {
				alice.Git("pull", "-q", "--no-rebase", "origin", "main")
				before := tableFiles(t, alice)
				alice.Lore("migrate", "rollback").Refused(t, "E_MIGRATE_ROLLBACK_LOSES", id).Never(t, "mem_01a1a59b891c70b78914000000000002")
				sameFiles(t, "refused rollback", tableFiles(t, alice), before)
			} else {
				p := pauseAt(t, alice, "rollback-checked", "migrate", "rollback")
				alice.Git("pull", "-q", "--no-rebase", "origin", "main")
				before := tableFiles(t, alice)
				p.resume().Refused(t, "E_MIGRATE_ROLLBACK_LOSES", id)
				sameFiles(t, "refused rollback", tableFiles(t, alice), before)
			}

			// tomas puts the migrated bytes back; now nothing would be lost.
			tomas.WriteFile(edited, migrated[tblMemories+"/"+id+".json"])
			tomas.CommitAll("tomas: revert the edit")
			tomas.AdminPush("origin", "main")
			alice.Git("pull", "-q", "--no-rebase", "origin", "main")
			alice.MustLore("migrate", "rollback").Says(t, tblMemories+"/mem_")
			got := tableFiles(t, alice)
			for k, v := range fixtureOriginals() {
				if got[k] != v {
					t.Errorf("rolled back %s differs from the original:\n got %s\nwant %s", k, got[k], v)
				}
			}
			newRows := 0
			for k, v := range got {
				if _, planted := fixtureOriginals()[k]; !planted {
					newRows++
					if !strings.Contains(v, `"_v": 2`) {
						t.Errorf("tomas's new row %s left v2:\n%s", k, v)
					}
				}
			}
			if newRows != 1 {
				t.Errorf("want tomas's one new row kept, found %d", newRows)
			}
			if meta, ok := alice.ReadFile(filepath.Join(dataDir, "_meta.json")); !ok || !strings.Contains(meta, "min_lore_version") {
				t.Errorf("_meta.json was lowered: %q", meta)
			}
		})
	}
}

// TestMigrateRaceDuringSync (7.5, 7.8): alice's run pauses after the
// memories rewrite (half the branch files at v2); `lore sync` passes on
// her clone meanwhile succeed. After the run, every row is v2 with its
// content and a further sync pass reports nothing changed.
func TestMigrateRaceDuringSync(t *testing.T) {
	_, _, alice := hooksFixture(t)
	p := pauseAt(t, alice, "table", "migrate", "run", "--no-push")
	for range 3 {
		alice.MustLore("sync")
	}
	p.resume().OK(t)
	got := tableFiles(t, alice)
	for k, orig := range fixtureOriginals() {
		doc := got[k]
		if !strings.Contains(doc, `"_v": 2`) {
			t.Errorf("%s not at v2 after the run:\n%s", k, doc)
		}
		var was, now rowDoc
		if err := json.Unmarshal([]byte(orig), &was); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(doc), &now); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if now.Body != was.Body || now.ID != was.ID {
			t.Errorf("%s changed content: body %q, want %q", k, now.Body, was.Body)
		}
	}
	alice.MustLore("sync")
	sameFiles(t, "after a further sync", tableFiles(t, alice), got)
}
