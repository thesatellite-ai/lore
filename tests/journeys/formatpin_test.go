//go:build journeys && !windows

package journeys

// formatpin_test.go — a project's format is the rule, never the binary's
// (decisions D12–D15). A team works for a week on a repo lore 0.1.17 wrote
// (format 1): two people stay on lore 0.1.17, one has lore 0.1.18. The
// 0.1.18 person reads everything, but every write to the project is refused
// with one line naming `lore migrate run`, and nothing they run (writes,
// live rows, render, lore fix, lore fix --all, a code commit with the hooks,
// pulls and syncs) puts a format-2 row anywhere: every row file in every
// clone, every local branch and every ref of the remote stays `"_v": 1`.
// The 0.1.17 people are never broken. Then the owner runs `lore migrate
// run` once: the format is raised in one change set with who and when, a
// signpost is left, and the 0.1.17 teammates' next command tells them to
// upgrade.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Placeholders of this journey.
const (
	fpRepo       = "github.com/acme-fp/app"
	fpOwnerName  = "Olga Owner"
	fpOwnerEmail = "olga@example.test"
	// fpOldLine is the one line of a refused write in a format-1 project (D13).
	fpOldLine = "is on the lore 0.1.17 format; run lore migrate run to upgrade it (teammates need lore 0.1.18)"
	// fpFormatOld is the exit code of E_FORMAT_OLD.
	fpFormatOld = 24
	// fpSignpost is the file migrate run leaves for 0.1.17 teammates (D14).
	fpSignpost = ".lore/data/.0-needs-lore-0.1.18-or-newer"
)

// fpNewRow matches a row (or metadata) file of a format newer than 0.1.17's.
var fpNewRow = regexp.MustCompile(`"_v":\s*([2-9]|[1-9][0-9])\b`)

// fpGit runs git in dir and returns its stdout (the test fails on an error).
func fpGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git -C %s %s: %v", dir, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// fpNewFormatInRefs lists "<ref>:<path>" for every file under .lore/ in
// any ref of the git dir dir whose content carries a format newer than 1.
func fpNewFormatInRefs(t testing.TB, dir string) []string {
	t.Helper()
	var hits []string
	for _, ref := range strings.Split(fpGit(t, dir, "for-each-ref", "--format=%(refname)"), "\n") {
		if ref == "" || strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		// git grep exits 1 when nothing matches: that is the good case
		out, _ := exec.Command("git", "-C", dir, "grep", "-l", "-E", `"_v":[[:space:]]*([2-9]|[1-9][0-9])([^0-9]|$)`, ref, "--", ".lore").Output()
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l != "" {
				hits = append(hits, l)
			}
		}
	}
	return hits
}

// fpNewFormatInTree lists every file under dir/.lore whose content carries
// a format newer than 1 (the work tree, committed or not).
func fpNewFormatInTree(t testing.TB, dir string) []string {
	t.Helper()
	var hits []string
	_ = filepath.WalkDir(filepath.Join(dir, ".lore"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && fpNewRow.Match(b) {
			hits = append(hits, p)
		}
		return nil
	})
	return hits
}

// fpStillFormat1 fails the step when any clone (work tree and every ref)
// or the remote (every ref) holds a row of a newer format.
func fpStillFormat1(t testing.TB, when string, remote *teamharness.Remote, clones ...*teamharness.Clone) {
	t.Helper()
	if hits := fpNewFormatInRefs(t, remote.Dir); len(hits) > 0 {
		t.Errorf("%s: the remote holds format-2 files: %v", when, hits)
	}
	for _, c := range clones {
		if hits := fpNewFormatInRefs(t, c.Dir); len(hits) > 0 {
			t.Errorf("%s: a ref of %s holds format-2 files: %v", when, c.Dir, hits)
		}
		if hits := fpNewFormatInTree(t, c.Dir); len(hits) > 0 {
			t.Errorf("%s: the work tree of %s holds format-2 files: %v", when, c.Dir, hits)
		}
	}
}

// fpSync pulls main into c and pushes c's commits (the person's git).
func fpSync(t testing.TB, c *teamharness.Clone) {
	t.Helper()
	if r := c.TryGit("pull", "-q", "--no-rebase", "origin", "main"); r.Code != 0 {
		t.Fatalf("git pull in %s: %s", c.Dir, r)
	}
	if r := c.TryGit("push", "-q", "origin", "HEAD:main"); r.Code != 0 {
		t.Fatalf("git push in %s: %s", c.Dir, r)
	}
}

// fpCommitPush commits everything with the clone's own hooks, then syncs.
func fpCommitPush(t testing.TB, c *teamharness.Clone, msg string) {
	t.Helper()
	if strings.TrimSpace(c.Git("status", "--porcelain")) != "" {
		c.CommitAll(msg)
	}
	fpSync(t, c)
}

// TestJourneyUpgraderFormatPin: a week on a format-1 repo with one person
// on lore 0.1.18, then the owner's one `lore migrate run`.
func TestJourneyUpgraderFormatPin(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	bea := UseOldLore(t, w.Persona("Bea Builder", "bea@example.test").Laptop())
	cal := UseOldLore(t, w.Persona("Cal Coder", "cal@example.test").Laptop())
	olga := w.Persona(fpOwnerName, fpOwnerEmail).Laptop()
	repo := w.Remote(fpRepo)
	bc := bea.Clone(repo, "app")
	var cc, oc *teamharness.Clone
	notes := []string{}

	w.Step("day1/team-on-0.1.17", func() {
		bea.Old(bc, "init", "--name", "app").OK(t)
		bea.Old(bc, "memory", "add", "--body", "deploys run from the release branch").OK(t)
		bea.Old(bc, "task", "add", "Write the export job").OK(t)
		// a live row: 0.1.17 shares it at once on refs/lore/live
		bea.Old(bc, "memory", "add", "--live", "--body", "staging is reset every monday").OK(t)
		notes = append(notes, "deploys run from the release branch", "staging is reset every monday")
		fpCommitPush(t, bc, "lore data")
		cc = cal.Clone(repo, "app")
		cal.Old(cc, "sync").OK(t)
		cal.Old(cc, "task", "add", "Review the export job").OK(t)
		fpCommitPush(t, cc, "cal's task")
		cal.Old(cc, "memory", "list").OK(t).Says(t, notes...)
		oc = olga.Clone(repo, "app")
		fpStillFormat1(t, "day1", repo, bc, cc, oc)
	})

	w.Step("day1/0.1.18-reads-but-never-writes", func() {
		olga.LoreIn(oc, "memory", "list").OK(t).Says(t, notes...)
		olga.LoreIn(oc, "task", "list").OK(t).Says(t, "Write the export job", "Review the export job")
		// every write to the project's own rows: the one line, nothing written
		before := oc.Head()
		olga.LoreIn(oc, "memory", "add", "--body", "the export runs nightly").Fails(t, fpFormatOld, fpOldLine)
		olga.LoreIn(oc, "memory", "add", "--live", "--body", "the export runs nightly").Fails(t, fpFormatOld, fpOldLine)
		olga.LoreIn(oc, "task", "add", "Schedule the export").Fails(t, fpFormatOld, fpOldLine)
		if oc.Head() != before {
			t.Errorf("a refused write committed in the 0.1.18 clone")
		}
		// what a person runs around the writes: none raises the format
		olga.LoreIn(oc, "render")
		olga.LoreIn(oc, "fix")
		olga.Lore(olga.Home, "fix", "--all")
		olga.LoreIn(oc, "sync")
		olga.LoreIn(oc, "doctor")
		// a code commit and push with whatever hooks the clone has
		oc.WriteFile("main.go", "package main\n")
		fpCommitPush(t, oc, "code from the 0.1.18 laptop")
		fpStillFormat1(t, "day1 after the 0.1.18 commands", repo, bc, cc, oc)
	})

	for day := 2; day <= 7; day++ {
		w.AdvanceDays(1)
		d := "day" + string(rune('0'+day))
		w.Step(d+"/a-working-day", func() {
			beaNote := "bea's note of " + d
			calTask := "Cal's task of " + d
			// the 0.1.17 people work as always and are never broken
			fpSync(t, bc)
			bea.Old(bc, "memory", "add", "--body", beaNote).OK(t)
			fpCommitPush(t, bc, "bea "+d)
			fpSync(t, cc)
			cal.Old(cc, "task", "add", calTask).OK(t)
			cal.Old(cc, "memory", "list").OK(t).Says(t, beaNote)
			fpCommitPush(t, cc, "cal "+d)
			notes = append(notes, beaNote)
			// the 0.1.18 person pulls, reads every note, is refused, commits code
			fpSync(t, oc)
			olga.LoreIn(oc, "memory", "list").OK(t).Says(t, notes...)
			olga.LoreIn(oc, "task", "list").OK(t).Says(t, calTask)
			olga.LoreIn(oc, "memory", "add", "--body", "olga's note of "+d).Fails(t, fpFormatOld, fpOldLine)
			olga.LoreIn(oc, "sync")
			oc.WriteFile("main.go", "package main\n\n// "+d+"\n")
			fpCommitPush(t, oc, "code "+d)
			// and the teammates pull the 0.1.18 person's commits cleanly
			fpSync(t, bc)
			bea.Old(bc, "memory", "list").OK(t).Says(t, notes...)
			fpStillFormat1(t, d, repo, bc, cc, oc)
		})
	}

	w.AdvanceDays(1)
	w.Step("day8/owner-migrates-once", func() {
		fpSync(t, oc)
		pre := repo.Rev("main")
		olga.LoreIn(oc, "migrate", "run").OK(t) // FP-3: run by the person, no --yes flag
		// one change set: commit it if migrate run left it staged
		fpCommitPush(t, oc, "lore migrate run")
		if hits := fpNewFormatInRefs(t, repo.Dir); len(hits) == 0 {
			t.Errorf("after lore migrate run no row on the remote is on the new format")
		}
		meta, ok := repo.Read("main", ".lore/data/_meta.json")
		if !ok {
			t.Fatalf("no .lore/data/_meta.json on main after the migration: %v", repo.Files("main"))
		}
		for _, want := range []string{"migrated_by", "migrated_at", fpOwnerEmail} {
			if !strings.Contains(meta, want) {
				t.Errorf("_meta.json after the migration does not record %q:\n%s", want, meta)
			}
		}
		if !regexp.MustCompile(`"format":\s*2`).MatchString(meta) {
			t.Errorf("_meta.json after the migration does not say format 2:\n%s", meta)
		}
		// the signpost for 0.1.17 teammates, in the same change as the rows
		changed := fpGit(t, repo.Dir, "diff", "--name-only", pre, "main")
		if !strings.Contains("\n"+changed+"\n", "\n"+fpSignpost+"\n") {
			t.Errorf("the migration change leaves no signpost %s; it changed:\n%s", fpSignpost, changed)
		}
		if !strings.Contains("\n"+changed+"\n", "\n.lore/data/_meta.json\n") {
			t.Errorf("the migration change does not record the format in _meta.json; it changed:\n%s", changed)
		}
		olga.LoreIn(oc, "memory", "add", "--body", "the project is on the new format").OK(t)
		olga.LoreIn(oc, "memory", "list").OK(t).Says(t, notes...)
	})

	w.Step("day8/0.1.17-teammates-are-told-to-upgrade", func() {
		for _, p := range []struct {
			m *OldMachine
			c *teamharness.Clone
		}{{bea, bc}, {cal, cc}} {
			if r := p.c.TryGit("pull", "-q", "--no-rebase", "origin", "main"); r.Code != 0 {
				t.Fatalf("git pull of the migration in %s: %s", p.c.Dir, r)
			}
			r := p.m.Old(p.c, "memory", "list")
			if out := strings.ToLower(r.Out()); !strings.Contains(out, "upgrade") || !strings.Contains(out, "0.1.18") {
				t.Errorf("lore 0.1.17 after the migration does not tell %s to upgrade to 0.1.18: %s", p.m.Name, r)
			}
		}
	})
}
