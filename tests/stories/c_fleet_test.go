//go:build stories && !windows

package stories

// c_fleet_test.go — story C (tests/stories/C-0117-ci-scale-fleet.md): a team
// whose repos lore 0.1.17 wrote, a CI runner, a 5.5k-row repo and the
// whole-fleet commands.
//
//	TestStoryCMixedFormat  format-1 repos stay format 1 under every new-lore
//	                       command until migrate run; migrate run keeps
//	                       every row and pushes nothing
//	TestStoryCCI           a CI job's depth-1 clone in a fresh HOME: the
//	                       checks change nothing and push nothing
//	TestStoryCScaleFleet   the 5.5k-row repo answers in time; doctor --all
//	                       and fix --all leave the brick and the format-1
//	                       repos alone and name each repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/untouchedwalk"
	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// cOldLine is the one line every refused write in a format-1 project says.
const cOldLine = "is on the lore 0.1.17 format; run lore migrate run to upgrade it"

// cCTO is the team's admin (placeholders).
var cCTO = journeys.Person{Name: "Cleo Tobin", Email: "cleo@acme.example.test", Login: "cleo-gh"}

// cListCount is how many rows a `<noun> list --all --json` answer holds.
func cListCount(t testing.TB, r teamharness.Result) int {
	t.Helper()
	var out struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("not a JSON list (%v): %s", err, r)
	}
	return len(out.Data)
}

// cRowFiles counts the row files of one table in repo's .lore/data.
func cRowFiles(t testing.TB, repo, table string) int {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(repo, ".lore", "data", table))
	if err != nil {
		t.Fatalf("read %s rows: %v", table, err)
	}
	n := 0
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

// cTracked is the clone's tracked changes (`git status --porcelain
// --untracked-files=no`): what a teammate's git pull would trip on.
func cTracked(c *teamharness.Clone) string {
	return c.Git("status", "--porcelain", "--untracked-files=no")
}

// cTree is a clone's files for a CI comparison (internal/untouchedwalk),
// leaving out lore's runtime folder .git/lore and git's FETCH_HEAD, which a
// read may refresh.
func cTree(t testing.TB, dir string) map[string]string {
	t.Helper()
	tree, err := untouchedwalk.Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	for p := range tree {
		if r := filepath.ToSlash(p); r == ".git/lore" || strings.HasPrefix(r, ".git/lore/") || r == ".git/FETCH_HEAD" {
			delete(tree, p)
		}
	}
	return tree
}

// cTimed runs lore in c as m, logs its wall time and fails a run slower
// than limit (generous: the gate shares the machine).
func cTimed(t testing.TB, m *journeys.Machine, c *teamharness.Clone, limit time.Duration, args ...string) teamharness.Result {
	t.Helper()
	start := time.Now()
	r := m.LoreIn(c, args...)
	took := time.Since(start)
	t.Logf("timing: lore %s took %v", strings.Join(args, " "), took.Round(time.Millisecond))
	if took > limit {
		t.Errorf("[C] lore %s took %v on %s, over %v", strings.Join(args, " "), took.Round(time.Millisecond), filepath.Base(c.Dir), limit)
	}
	return r
}

// TestStoryCMixedFormat: week 1, two repos lore 0.1.17 wrote. The new lore
// reads them, refuses every write with one line, and nothing it runs
// (render, doctor --fix, fix, sync) raises the format, dirties a tracked
// file or moves a remote ref. Then the admin runs lore migrate run once:
// every row is kept, in one commit, and nothing is pushed.
func TestStoryCMixedFormat(t *testing.T) {
	s := New(t, "C")
	syncGo := s.Seed(s.Remote("github.com/acme/sync_go"), func(dir string) { V017Repo(t, dir, "sync_go", SmallShape) })
	auth := s.Seed(s.Remote("github.com/acme/auth"), func(dir string) { V017Repo(t, dir, "auth", SmallShape) })
	cto := s.Person(cCTO.Name, cCTO.Email)
	var cs, ca *teamharness.Clone
	var refsSync, refsAuth string

	s.Step("week1/clone-and-read", func() {
		cs, ca = cto.Clone(syncGo, "sync_go"), cto.Clone(auth, "auth")
		refsSync, refsAuth = s.RefsOf(syncGo), s.RefsOf(auth)
		for _, c := range []*teamharness.Clone{cs, ca} {
			if n := cListCount(t, cto.LoreIn(c, "task", "list", "--all", "--json").OK(t)); n != SmallShape["tasks"] {
				t.Errorf("task list in %s shows %d tasks, want the %d lore 0.1.17 wrote", filepath.Base(c.Dir), n, SmallShape["tasks"])
			}
			cto.LoreIn(c, "migrate", "status").OK(t).Says(t, "the lore 0.1.17 format (format 1)", "lore migrate run")
		}
	})

	s.Step("week1/writes-refused-with-one-line", func() {
		for _, args := range [][]string{
			{"memory", "add", "--body", "the retry queue drains on deploy"},
			{"task", "add", "--title", "rotate the signing key"},
			{"rule", "add", "--body", "every handler logs its request id"},
		} {
			r := cto.LoreIn(cs, args...)
			if r.Code == 0 {
				t.Errorf("lore %s in a format-1 repo succeeded; it must refuse naming lore migrate run: %s", strings.Join(args, " "), r)
			}
			r.Says(t, cOldLine)
			s.NamedFlags(t, r)
		}
	})

	s.Step("week1/nothing-raises-the-format", func() {
		for _, c := range []*teamharness.Clone{cs, ca} {
			cto.LoreIn(c, "render")
			cto.LoreIn(c, "doctor", "--fix", "--yes")
			cto.LoreIn(c, "fix", "--yes")
			cto.LoreIn(c, "sync")
			if bad := NewFormatFiles(t, c.Dir); len(bad) != 0 {
				t.Errorf("new-lore commands raised %s's format: %v", filepath.Base(c.Dir), bad[:min(3, len(bad))])
			}
			if st := cTracked(c); st != "" {
				t.Errorf("new-lore commands changed tracked files in format-1 %s (a 0.1.17 teammate's pull would trip):\n%s", filepath.Base(c.Dir), st)
			}
		}
		s.NoPushSince(t, syncGo, refsSync, "render, doctor --fix, fix and sync in a format-1 repo")
		s.NoPushSince(t, auth, refsAuth, "render, doctor --fix, fix and sync in a format-1 repo")
	})

	s.Step("week2/migrate-run-once", func() {
		s.AdvanceDays(7)
		cMigrate(t, s, cto, cs)
		s.NoPushSince(t, syncGo, refsSync, "lore migrate run and the person's commit (D-8: no refs/lore/signals or branch pushed)")
		if bad := NewFormatFiles(t, ca.Dir); len(bad) != 0 {
			t.Errorf("migrating sync_go raised auth's format too: %v", bad[:min(3, len(bad))])
		}
	})
}

// cMigrate is the admin's one migration of a SmallShape repo: lore migrate
// run rewrites every row, keeps them all and commits nothing (lore never
// commits a code repo), naming the git line; the person runs that line and
// gets ONE commit.
func cMigrate(t *testing.T, s *Story, m *journeys.Machine, c *teamharness.Clone) {
	t.Helper()
	head := c.Head()
	r := m.LoreIn(c, "migrate", "run").OK(t).Says(t, "Upgraded "+fmt.Sprint(SmallShape.Rows())+" rows", "git add .lore/data && git commit")
	s.NamedFlags(t, r)
	for tb, n := range SmallShape {
		if got := cRowFiles(t, c.Dir, tb); got < n {
			t.Errorf("after migrate run %s has %d row files, want all %d kept", tb, got, n)
		}
	}
	if n := cListCount(t, m.LoreIn(c, "task", "list", "--all", "--json").OK(t)); n != SmallShape["tasks"] {
		t.Errorf("task list after migrate run shows %d tasks, want %d", n, SmallShape["tasks"])
	}
	if bad := NewFormatFiles(t, c.Dir); len(bad) < SmallShape.Rows() {
		t.Errorf("migrate run rewrote %d files, want every one of the %d rows", len(bad), SmallShape.Rows())
	}
	m.LoreIn(c, "migrate", "status").OK(t).Says(t, "format 2")
	func(tb testing.TB) {
		c.Git("add", ".lore/data")
		if res := c.TryGit("commit", "-q", "-m", "lore: migrate rows to format 2"); res.Code != 0 {
			tb.Fatalf("the migration commit lore's own line names was refused: %s", res)
		}
		if n := c.Git("rev-list", "--count", head+"..HEAD"); n != "1" {
			tb.Errorf("the migration is %s commits, want ONE change set", n)
		}
		if st := cTracked(c); st != "" {
			tb.Errorf("tracked changes left after the migration commit:\n%s", st)
		}
	}(t)
}

// TestStoryCCI: a CI runner clones auth at depth 1 into a fresh HOME that
// was never set up for lore. On the 0.1.17 format the leak check refuses the
// 0.1.17 actor copies and names lore migrate run; after the admin migrates
// and pushes, it passes. The read-only commands answer, and none of them
// writes a file in the clone or moves a ref.
func TestStoryCCI(t *testing.T) {
	s := New(t, "C")
	auth := s.Seed(s.Remote("github.com/acme/auth"), func(dir string) { V017Repo(t, dir, "auth", SmallShape) })
	cto := s.Person(cCTO.Name, cCTO.Email)
	ci := s.Person("CI Runner", "ci@acme.example.test")
	ci.SetEnv("CI", "true")
	job := func(name string) (*teamharness.Clone, string, map[string]string) {
		c := s.ShallowClone(t, ci, auth, name, 1)
		return c, s.RefsOf(auth), cTree(t, c.Dir)
	}
	unchanged := func(c *teamharness.Clone, refs string, before map[string]string) {
		if diff := untouchedwalk.Diff(before, cTree(t, c.Dir)); diff != "" {
			t.Errorf("[C] the CI job's lore commands wrote into its clone:\n%s", diff)
		}
		if st := c.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Errorf("[C] the CI clone is not clean after the checks:\n%s", st)
		}
		s.NoPushSince(t, auth, refs, "the CI job")
	}
	s.Step("day1/ci-on-the-0.1.17-format", func() {
		c, refs, before := job("auth-ci-1")
		r := ci.LoreIn(c, "check", "leaks", "--ci").Fails(t, teamharness.ExitGuard, "stable_key", "lore migrate run")
		s.NamedFlags(t, r)
		cTimed(t, ci, c, 30*time.Second, "task", "list").OK(t)
		ci.LoreIn(c, "render", "--check")
		ci.LoreIn(c, "migrate", "status").OK(t).Says(t, "format 1")
		ci.LoreIn(c, "doctor")
		unchanged(c, refs, before)
	})
	s.Step("day2/admin-migrates-and-pushes", func() {
		s.AdvanceDays(1)
		c := cto.Clone(auth, "auth")
		cMigrate(t, s, cto, c)
		c.Git("push", "-q", "origin", "HEAD:main")
	})
	s.Step("day2/ci-after-the-migration", func() {
		c, refs, before := job("auth-ci-2")
		s.NamedFlags(t, ci.LoreIn(c, "check", "leaks", "--ci").OK(t))
		cTimed(t, ci, c, 30*time.Second, "task", "list").OK(t)
		cTimed(t, ci, c, 30*time.Second, "search", "--query", "cache").OK(t)
		ci.LoreIn(c, "render", "--check")
		ci.LoreIn(c, "migrate", "status").OK(t).Says(t, "format 2")
		ci.LoreIn(c, "doctor")
		unchanged(c, refs, before)
	})
}

// TestStoryCScaleFleet: the hub repo holds 5.5k rows lore 0.1.17 wrote.
// Common reads answer in time; then doctor --all and fix --all over every
// repo of the admin (two format-1 repos, the hub and the brick, a repo with
// no lore data) name each repo, change no format and leave the brick
// byte-identical.
func TestStoryCScaleFleet(t *testing.T) {
	s := New(t, "C")
	hub := s.Seed(s.Remote("github.com/acme/hub"), func(dir string) { V017Repo(t, dir, "hub", HubShape) })
	syncGo := s.Seed(s.Remote("github.com/acme/sync_go"), func(dir string) { V017Repo(t, dir, "sync_go", SmallShape) })
	brick := s.Seed(s.Remote("github.com/acme/brick"), func(dir string) { PlainRepo(t, dir) })
	cto := s.Person(cCTO.Name, cCTO.Email)
	var ch, cs, cb *teamharness.Clone

	s.Step("day1/hub-reads-in-time", func() {
		ch, cs, cb = cto.Clone(hub, "hub"), cto.Clone(syncGo, "sync_go"), cto.Clone(brick, "brick")
		limit := 30 * time.Second
		if n := cListCount(t, cTimed(t, cto, ch, limit, "task", "list", "--all", "--json").OK(t)); n != HubShape["tasks"] {
			t.Errorf("hub task list shows %d tasks, want %d", n, HubShape["tasks"])
		}
		cTimed(t, cto, ch, limit, "task", "list").OK(t)
		cTimed(t, cto, ch, limit, "memory", "list").OK(t)
		cTimed(t, cto, ch, limit, "search", "--query", "webhook").OK(t)
		cTimed(t, cto, ch, limit, "whoami").OK(t)
		cTimed(t, cto, ch, limit, "migrate", "status").OK(t).Says(t, "format 1")
		cTimed(t, cto, ch, limit, "doctor")
		cTimed(t, cto, ch, limit, "task", "add", "--title", "a write in a big format-1 repo").Says(t, cOldLine)
		if bad := NewFormatFiles(t, ch.Dir); len(bad) != 0 {
			t.Errorf("reads raised the hub's format: %v", bad[:min(3, len(bad))])
		}
	})

	s.Step("day1/fleet", func() {
		dirs := filepath.Dir(cb.Dir)
		brickTree := TreeOf(t, cb.Dir)
		refs := map[*teamharness.Remote]string{hub: s.RefsOf(hub), syncGo: s.RefsOf(syncGo), brick: s.RefsOf(brick)}
		d := cTimed(t, cto, cs, 120*time.Second, "doctor", "--all")
		f := cTimed(t, cto, cs, 300*time.Second, "fix", "--all", "--dirs", dirs, "--yes")
		f.Says(t, "hub", "sync_go", "lore migrate run")
		s.NamedFlags(t, d, f)
		d.Says(t, "no setup on this machine", "lore fix --all --dirs")
		f.Says(t, "2 repo(s)", "2 on the lore 0.1.17 format", "lore fix --migrate")
		for _, c := range []*teamharness.Clone{ch, cs} {
			if bad := NewFormatFiles(t, c.Dir); len(bad) != 0 {
				t.Errorf("fix --all without --migrate raised %s's format: %v", filepath.Base(c.Dir), bad[:min(3, len(bad))])
			}
		}
		s.Untouched(t, cb.Dir, brickTree, "doctor --all and fix --all")
		if _, err := os.Stat(filepath.Join(cb.Dir, ".lore")); err == nil {
			t.Error("fix --all created .lore in the brick, a repo with no lore data (FP-7)")
		}
		for r, before := range refs {
			s.NoPushSince(t, r, before, "doctor --all and fix --all")
		}
	})
}
