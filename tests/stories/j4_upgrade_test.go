//go:build stories && !windows

package stories

// j4_upgrade_test.go — story J4, a lore 0.1.17 user installs this lore
// (tests/stories/J4-upgrade-0117.md). Their repos hold what 0.1.17 wrote
// (generated: V017Repo), one of them the 5.5k-row big repo; a repo with no
// lore data sits beside them. Mixed teams on two binaries, personal notes
// and the store layout move are the journeys' (linked from the page).

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// j4OldLine is the one line every write in a 0.1.17-format project answers
// with (FP-2, D13).
const j4OldLine = "is on the lore 0.1.17 format; run lore migrate run to upgrade it"

// j4Count is how many rows `lore <noun> list --all --json` shows.
func j4Count(t testing.TB, r teamharness.Result) int {
	t.Helper()
	var out struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("not a --json list (%v): %s", err, r)
	}
	return len(out.Data)
}

// TestStoryJ4Upgrade walks the upgrade of a 0.1.17 user.
func TestStoryJ4Upgrade(t *testing.T) {
	s := New(t, "J4")
	app := s.Seed(s.Remote("github.com/ola-dev/app"), func(dir string) { V017Repo(t, dir, "app", SmallShape) })
	big := s.Seed(s.Remote("github.com/ola-dev/big"), func(dir string) { V017Repo(t, dir, "big", HubShape) })
	brick := s.Seed(s.Remote("github.com/ola-dev/brick"), func(dir string) { PlainRepo(t, dir) })
	ola := s.Person("Ola Old", "ola@example.test")
	var ca, cb, cbrick *teamharness.Clone
	var refs map[string]string
	var brickTree string
	clean := func(what string, cs ...*teamharness.Clone) {
		t.Helper()
		for _, c := range cs {
			if st := c.Status(); st != "" {
				t.Errorf("%s left changes in %s:\n%s", what, filepath.Base(c.Dir), st)
			}
		}
	}
	noPush := func(what string) {
		t.Helper()
		for _, r := range []*teamharness.Remote{app, big, brick} {
			s.NoPushSince(t, r, refs[r.Slug], what)
		}
	}

	s.Step("day1/clone", func() {
		code := filepath.Join(ola.Home, "code")
		ca, cb, cbrick = ola.Clone(app, "app"), ola.Clone(big, "big"), ola.Clone(brick, "brick")
		ola.MoveClone(t, ca, filepath.Join(code, "app"))
		ola.MoveClone(t, cb, filepath.Join(code, "big"))
		ola.MoveClone(t, cbrick, filepath.Join(code, "brick"))
		refs = map[string]string{app.Slug: s.RefsOf(app), big.Slug: s.RefsOf(big), brick.Slug: s.RefsOf(brick)}
		brickTree = TreeOf(t, cbrick.Dir)
	})

	s.Step("day1/reads-work-and-change-nothing", func() {
		r := ola.LoreIn(ca, "task", "list", "--all", "--json").OK(t)
		if n := j4Count(t, r); n != SmallShape["tasks"] {
			t.Errorf("the upgraded lore reads %d of the 0.1.17 project's %d tasks", n, SmallShape["tasks"])
		}
		r.Says(t, j4OldLine)
		ola.LoreIn(ca, "memory", "list").OK(t)
		ola.LoreIn(ca, "migrate", "status").OK(t).Says(t, "0.1.17 format", "lore migrate run")
		start := time.Now()
		r = ola.LoreIn(cb, "task", "list", "--all", "--json").OK(t)
		took := time.Since(start)
		t.Logf("task list --all on the 5.5k-row 0.1.17 repo: %v", took.Round(time.Millisecond))
		if n := j4Count(t, r); n != HubShape["tasks"] {
			t.Errorf("the big repo lists %d of its %d tasks", n, HubShape["tasks"])
		}
		if took > 60*time.Second {
			t.Errorf("a read of the 5.5k-row repo took %v (T-3: a few seconds)", took)
		}
		clean("reads after the upgrade", ca, cb)
		for _, c := range []*teamharness.Clone{ca, cb} {
			if bad := NewFormatFiles(t, c.Dir); len(bad) != 0 {
				t.Errorf("a read raised %s's format: %v", filepath.Base(c.Dir), bad[:min(3, len(bad))])
			}
		}
		noPush("reads")
	})

	s.Step("day1/writes-refuse-with-one-line", func() {
		r := ola.LoreIn(ca, "memory", "add", "--body", "written right after the upgrade")
		if r.Code == 0 {
			t.Fatalf("a write in a 0.1.17-format project went through: %s", r)
		}
		r.Says(t, j4OldLine)
		s.NamedFlags(t, r)
		clean("a refused write", ca)
		if bad := NewFormatFiles(t, ca.Dir); len(bad) != 0 {
			t.Errorf("a refused write left newer-format files: %v", bad)
		}
	})

	s.Step("day1/code-commit-passes", func() {
		ca.WriteFile("main.go", "package main\n\nfunc main() {}\n")
		ca.CommitAll("add main")
		clean("a code commit", ca)
		if bad := NewFormatFiles(t, ca.Dir); len(bad) != 0 {
			t.Errorf("the commit hooks raised the format: %v", bad)
		}
	})

	s.Step("day2/fix-dry-run", func() {
		// B4-14: a dry run changes nothing and reports no FAILED step.
		r := ola.LoreIn(ca, "fix", "--dry-run")
		r.Never(t, "FAILED", "E_LINK_UNKNOWN")
		clean("lore fix --dry-run", ca)
	})

	s.Step("day2/fix-all", func() {
		// B4-8, FP-4, FP-7: fix --all finds the 0.1.17 repos, lists each one
		// still on the old format with its own command and migrates nothing
		// without --migrate; the repo with no lore data stays byte-identical.
		r := ola.Lore("code", "fix", "--all", "--yes", "--dirs", filepath.Join(ola.Home, "code"))
		r.Says(t, "app", "big", "lore migrate run")
		s.NamedFlags(t, r)
		for _, c := range []*teamharness.Clone{ca, cb} {
			if bad := NewFormatFiles(t, c.Dir); len(bad) != 0 {
				t.Errorf("fix --all migrated %s without --migrate: %v", filepath.Base(c.Dir), bad[:min(3, len(bad))])
			}
		}
		s.Untouched(t, cbrick.Dir, brickTree, "lore fix --all")
		noPush("lore fix --all")
	})

	var migrated string
	s.Step("day3/migrate-run", func() {
		before := ca.Head()
		// FP-3: migrate run raises the format, records who and when, leaves
		// the signpost for 0.1.17 teammates, and commits nothing itself
		// (lore never commits a code repo): it prints the one commit to make.
		mr := ola.LoreIn(ca, "migrate", "run").OK(t)
		mr.Says(t, "by Ola Old <ola@example.test>", ".0-needs-lore-0.1.18-or-newer", "git add .lore/data && git commit")
		s.NamedFlags(t, mr)
		if ca.Head() != before {
			t.Error("lore migrate run committed in a code repo by itself")
		}
		ca.Git("add", ".lore/data")
		ca.Git("commit", "-q", "-m", "lore: migrate rows to format 2")
		if n := strings.TrimSpace(ca.Git("rev-list", "--count", before+"..HEAD")); n != "1" {
			t.Errorf("the migration took %s commits, want 1", n)
		}
		migrated = ca.Head()
		r := ola.LoreIn(ca, "task", "list", "--all", "--json").OK(t)
		if n := j4Count(t, r); n != SmallShape["tasks"] {
			t.Errorf("after migrate run the project lists %d of its %d tasks", n, SmallShape["tasks"])
		}
		r.Never(t, j4OldLine)
		if len(NewFormatFiles(t, ca.Dir)) == 0 {
			t.Error("after migrate run no row file is in the new format")
		}
		clean("lore migrate run", ca)
		noPush("lore migrate run (the person pushes it)")
		// The other old-format repo stays old until its own migrate run.
		if bad := NewFormatFiles(t, cb.Dir); len(bad) != 0 {
			t.Errorf("migrating app raised big's format: %v", bad[:min(3, len(bad))])
		}
	})

	s.Step("day3/writes-work-and-push-is-theirs", func() {
		ola.LoreIn(ca, "memory", "add", "--body", "first note after the migration").OK(t)
		noPush("a write after the migration")
		ca.Git("push", "-q", "origin", "main")
		if app.Rev("refs/heads/main") == refs[app.Slug] || !strings.Contains(s.Git(app.Dir, "branch", "--contains", migrated), "main") {
			t.Error("the person's git push did not carry the migration commit to main")
		}
	})
}
