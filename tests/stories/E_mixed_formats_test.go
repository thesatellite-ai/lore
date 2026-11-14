//go:build stories && !windows

package stories

// E_mixed_formats_test.go — story E (tests/stories/E-mixed-formats.md): a
// team where one person is still on lore 0.1.17 shares format-1 and format-2
// repos, then the CTO runs `lore migrate run` while a developer has an
// unpushed branch of edits. The journeys TestJourneyMigrateKeepsBranchEdits
// (rebase/merge/cherry-pick of an archived rule) and
// TestJourneyMigrateCountMatchesDryRun (tag folding counts) check single
// steps in depth; this replays the team's week with the standing rules.

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	eSvcSlug = "github.com/quill-test/ledger-svc"
	eKitSlug = "github.com/quill-test/kit"
)

func TestStoryEMixedFormats(t *testing.T) {
	s := New(t, "E")
	var prj V017Project
	svc := s.Seed(s.Remote(eSvcSlug), func(dir string) {
		writeFile(t, dir+"/main.go", "package main\n\nfunc main() {}\n")
		prj = V017Repo(t, dir, "ledger-svc", SmallShape)
		eOrphanTag(t, dir, prj.ID)
	})
	kit := s.Remote(eKitSlug)
	all := []*teamharness.Remote{svc, kit}

	cto := s.Person("Ines Cato", "ines.cato@example.test")
	dev1 := s.Person("Kofi Lind", "kofi.lind@example.test")
	dev2 := s.Person("Mara Voss", "mara.voss@example.test")
	old := journeys.UseOldLore(t, s.Persona("Olu Elder", "olu.elder@example.test").Laptop())
	for _, m := range []*journeys.Machine{cto, dev1, dev2} {
		m.Actor.SetEnv(teamharness.EnvPushClaims, "0")
	}

	var cs, d1, d2, os1, kc *teamharness.Clone
	var preRows map[string]bool
	var preMigration, migration string
	var osMain *teamharness.Clone // the 0.1.17 teammate's second checkout, kept on main

	s.Step("week1/old-and-new-lore-side-by-side", func() {
		cs = cto.Clone(svc, "ledger-svc")
		d1 = dev1.Clone(svc, "ledger-svc")
		os1 = old.Clone(svc, "ledger-svc")
		old.Old(nil, "identity", "set", "Olu Elder", "--key", "human:olu.elder@example.test").OK(t)
		old.Old(os1, "task", "list").OK(t)
		old.Old(os1, "memory", "add", "--body", "the ledger closes at midnight UTC").OK(t)
		old.Old(os1, "task", "add", "export the ledger as CSV").OK(t)
		// Rule 1 is the new lore's: 0.1.17 shares its live rows on its own,
		// so the snapshot starts after the old lore's writes.
		before := dSnap(s, all...)
		// the new lore reads a format-1 repo and refuses to write it until
		// someone migrates, naming the command (the format pin)
		r := cto.LoreIn(cs, "task", "add", "--title", "export the ledger as CSV", "--commitment", "accepted").
			Refused(t, "0.1.17 format", "lore migrate run")
		s.NamedFlags(t, r)
		cto.LoreIn(cs, "task", "list").OK(t)
		dNoPush(t, s, before, "the new lore's reads and a refused write")
		for _, c := range []*teamharness.Clone{cs, os1} {
			if bad := NewFormatFiles(t, c.Dir); len(bad) != 0 {
				t.Errorf("a write raised the format-1 repo's format before migrate run: %v", bad)
			}
		}
		os1.CommitAll("notes from old lore")
		os1.Git("push", "-q", "origin", "HEAD:main")
		cs.Git("pull", "-q", "--no-rebase", "origin", "main")
		cto.LoreIn(cs, "search", "--query", "CSV").OK(t).Says(t, "export the ledger as CSV")
		cto.LoreIn(cs, "memory", "list").OK(t).Says(t, "closes at midnight")
		d1.Git("pull", "-q", "--no-rebase", "origin", "main")
		dev1.LoreIn(d1, "search", "--query", "midnight").OK(t).Says(t, "closes at midnight")
		if st := d1.Status(); st != "" {
			t.Errorf("a read on a format-1 clone changed tracked files:\n%s", st)
		}
		if bad := NewFormatFiles(t, cs.Dir); len(bad) != 0 {
			t.Errorf("new lore's reads raised the pulled repo's format: %v", bad)
		}
	})

	s.Step("week1/old-lore-in-a-format-2-repo", func() {
		kc = cto.Clone(kit, "kit")
		kc.WriteFile("go.mod", "module example.test/kit\n\ngo 1.22\n")
		cto.LoreIn(kc, "init").OK(t)
		cto.LoreIn(kc, "memory", "add", "--body", "kit has no runtime dependencies").OK(t)
		kc.CommitAll("chore: adopt lore")
		kc.Git("push", "-q", "origin", "HEAD:main")
		ok := old.Clone(kit, "kit")
		r := old.Old(ok, "memory", "list")
		t.Logf("lore 0.1.17 in a format-2 repo:\n%s", r)
		if r.Code != 0 && !strings.Contains(r.Out(), "newer lore") {
			t.Errorf("lore 0.1.17 in a format-2 repo neither reads it nor says a newer lore is needed (D-53):\n%s", r)
		}
		if strings.Contains(r.Out(), "lore init") {
			t.Errorf("lore 0.1.17's hint in a format-2 repo sends the person to lore init (D-53 loop):\n%s", r)
		}
	})

	archived := prj.Rows["rules"][0]
	s.Step("week2/old-lore-edits-on-an-unpushed-branch", func() {
		s.AdvanceDays(7)
		os1.Git("pull", "-q", "--no-rebase", "origin", "main")
		preMigration = os1.Head()
		os1.Git("switch", "-q", "-c", "feat-refunds")
		old.Old(os1, "rule", "archive", archived).OK(t)
		old.Old(os1, "memory", "add", "--body", "refunds round half to even").OK(t)
		if bad := NewFormatFiles(t, os1.Dir); len(bad) != 0 {
			t.Errorf("branch edits raised the format before migrate run: %v", bad)
		}
		os1.CommitAll("feat: refunds notes")
		preRows = eRowIDs(t, os1.Dir)
	})

	s.Step("week2/cto-migrates-main", func() {
		cs.Git("pull", "-q", "--no-rebase", "origin", "main")
		before := dSnap(s, all...)
		dry := cto.LoreIn(cs, "migrate", "run", "--dry-run").OK(t)
		run := cto.LoreIn(cs, "migrate", "run").OK(t).Says(t, "tags: skipped entity_tags/")
		s.NamedFlags(t, dry, run)
		dc, rc := eCount.FindStringSubmatch(dry.Stdout), eCount.FindStringSubmatch(run.Stdout)
		if dc == nil || rc == nil || dc[1] != rc[1] {
			t.Errorf("migrate run's summary disagrees with its dry run (D-54): dry %q, run %q\n%s\n%s", dc, rc, dry.Stdout, run.Stdout)
		}
		dNoPush(t, s, before, "lore migrate run (D-8: nothing pushed until the person pushes)")
		// D-55: the migrator's actor row exists. (Doctor may still ask for
		// a render: the migration changed rows LORE.md was rendered from.)
		cto.LoreIn(cs, "doctor").Never(t, "actor_roster", "roster does not hold")
		r := cs.TryGit("add", "-A")
		if r.Code == 0 {
			r = cs.TryGit("commit", "-q", "-m", "lore: migrate rows to format 2")
		}
		if r.Code != 0 {
			if strings.Contains(r.Out(), "needs-lore") {
				t.Skip("waiting for D-154: pre-commit refuses the migration commit's signpost file")
			}
			t.Fatalf("committing the migration failed:\n%s", r)
		}
		migration = cs.Head()
		cs.Git("push", "-q", "origin", "HEAD:main")
	})

	s.Step("week2/old-lore-commits-in-the-migrated-repo", func() {
		osMain = old.Clone(svc, "ledger-svc-main")
		osMain.WriteFile("main.go", "package main\n\n// v2\nfunc main() {}\n")
		osMain.Git("add", "main.go")
		r := osMain.TryGit("commit", "-q", "-m", "fix: main v2")
		t.Logf("0.1.17 commit in a migrated repo (exit %d):\n%s", r.Code, r.Out())
		if n := strings.Count(r.Out(), "\n"); n > 15 {
			t.Errorf("a 0.1.17 commit in a migrated repo prints %d lines, docs promise one clear line (D-72)", n)
		}
		if strings.Contains(r.Out(), "credential") {
			t.Errorf("a 0.1.17 commit in a migrated repo is told to remove a credential (D-72):\n%s", r.Out())
		}
	})

	s.Step("week2/the-branch-crosses-the-migration-after-the-upgrade", func() {
		old.Upgrade()
		up := old.Machine
		os1.Git("fetch", "-q", "origin")
		r := os1.TryGit("rebase", "origin/main")
		if r.Code != 0 {
			t.Fatalf("rebasing the branch over the migration failed:\n%s\n%s", r, up.LoreIn(os1, "doctor"))
		}
		if strings.Contains(r.Stderr, "kept the newer edit") {
			t.Errorf("the merge driver dropped the branch's edit (D-50): %s", r.Stderr)
		}
		doc, _ := os1.ReadFile(".lore/data/rules/" + archived + ".json")
		if !strings.Contains(doc, `"archived_at": "`) {
			t.Errorf("the archive made on the branch before the migration was dropped by the rebase (D-50):\n%s", doc)
		}
		up.LoreIn(os1, "memory", "list").OK(t).Says(t, "round half to even", "closes at midnight")
		if miss := eMissing(preRows, eRowIDs(t, os1.Dir)); len(miss) != 0 {
			t.Errorf("rows lost across the rebase (D-50): %v", miss)
		}
		w := up.LoreIn(os1, "task", "add", "--title", "refund webhook", "--commitment", "accepted").OK(t)
		w.Never(t, "hand edit")
		if got := os1.Git("status", "--porcelain", "--", ".lore/data/actors"); strings.Contains(got, " M ") {
			t.Errorf("the first write after the migration re-stamped actor rows (D-70):\n%s", got)
		}
		os1.CommitAll("feat: refund webhook task")
		os1.Git("push", "-q", "origin", "HEAD:main")
	})

	s.Step("week2/a-teammate-syncs-after-the-migration", func() {
		d2 = dev2.Clone(svc, "ledger-svc")
		dev2.LoreIn(d2, "task", "list").OK(t).Says(t, "refund webhook")
		dev2.LoreIn(d2, "sync").OK(t)
		if st := d2.Status(); st != "" {
			t.Errorf("lore sync after the migration changed committed files (D-71):\n%s", st)
		}
	})

	s.Step("week3/a-revert-of-the-migration-is-named", func() {
		s.AdvanceDays(7)
		cs.Git("pull", "-q", "--no-rebase", "origin", "main")
		// revert right on top of the migration (later rows edited since
		// would make git stop on content conflicts first)
		cs.Git("switch", "-q", "-c", "try-revert", migration)
		r := cs.TryGit("revert", "--no-edit", migration)
		if r.Code != 0 {
			t.Fatalf("git revert of the migration:\n%s", r)
		}
		if v2 := eV2Files(t, cs.Dir); len(v2) != 0 {
			doc := cto.LoreIn(cs, "doctor")
			doc.Never(t, "Status: HEALTHY").Says(t, "git revert of the migration")
			s.NamedFlags(t, doc)
		}
		cs.Git("switch", "-q", "main")
		cs.Git("branch", "-q", "-D", "try-revert")
	})

	s.Step("week3/two-branches-migrate-and-merge", func() {
		d2.Git("switch", "-q", "-c", "mig-a", preMigration)
		d2.Git("switch", "-q", "-c", "mig-b", preMigration)
		count := len(eRowIDs(t, d2.Dir))
		// mig-a migrates with migrate run, mig-b with fix --migrate, whose
		// render line must say what it did (D-56)
		d2.Git("switch", "-q", "mig-a")
		dev2.LoreIn(d2, "migrate", "run").OK(t)
		d2.CommitAll("lore: migrate on mig-a")
		d2.Git("switch", "-q", "mig-b")
		fx := dev2.LoreIn(d2, "fix", "--migrate")
		s.NamedFlags(t, fx)
		if eRenderSkipped(fx.Out()) {
			for _, f := range []string{"AGENTS.md", ".lore/LORE.full.md", ".lore/LORE.md"} {
				if st := d2.Git("status", "--porcelain", "--", f); st != "" {
					t.Errorf("fix --migrate says render skipped but wrote %s (D-56):\n%s", f, fx)
				}
			}
		}
		dev2.LoreIn(d2, "memory", "add", "--body", "branch b note").OK(t)
		count++
		d2.CommitAll("lore: migrate on mig-b")
		d2.Git("switch", "-q", "mig-a")
		r := d2.TryGit("merge", "--no-edit", "mig-b")
		if r.Code != 0 {
			t.Fatalf("merging two migrations failed:\n%s\n%s", r, dev2.LoreIn(d2, "doctor"))
		}
		if got := len(eRowIDs(t, d2.Dir)); got < count {
			t.Errorf("two merged migrations hold %d row files, want at least %d", got, count)
		}
		dev2.LoreIn(d2, "memory", "list").OK(t).Says(t, "branch b note")

		// D-69: a branch from before the migration deleted a row the
		// migration rewrote; the merge stops on a modify/delete conflict and
		// doctor must see it and name the next step.
		d2.Git("switch", "-q", "-c", "drop-row", preMigration)
		d2.Git("rm", "-q", ".lore/data/decisions/"+prj.Rows["decisions"][0]+".json")
		d2.Git("commit", "-q", "-m", "drop an old decision")
		d2.Git("switch", "-q", "mig-a")
		r = d2.TryGit("merge", "--no-edit", "drop-row")
		if r.Code != 0 {
			doc := dev2.LoreIn(d2, "doctor")
			doc.Never(t, "open conflicts:   0")
			s.NamedFlags(t, doc)
			d2.Git("merge", "--abort")
		} else {
			t.Logf("the modify/delete merge across the migration resolved itself:\n%s", r)
		}
		d2.Git("switch", "-q", "main")
	})

	s.Step("week3/fix-converges-and-fix-all-skips-home", func() {
		cto.LoreIn(cs, "fix")
		second := cto.LoreIn(cs, "fix")
		second.Never(t, "fixed actor_roster")
		all := cto.Lore(cto.Home, "fix", "--all")
		all.Never(t, "skipped ~", "has a .lore folder but is not a git repo")
		s.NamedFlags(t, second, all)
		start := time.Now()
		cto.LoreIn(cs, "doctor")
		t.Logf("doctor on the migrated repo took %s", time.Since(start))
	})

	// Step 10 (stores made by old lore, D-84, D-89) is replayed by
	// TestJourneyUpgraderOldLayout and TestJourneyUpgraderPersonalNotes; it
	// needs an org and a team store, which those journeys already build.
}
