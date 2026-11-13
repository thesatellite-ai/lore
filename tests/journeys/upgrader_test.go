//go:build journeys && !windows

package journeys

// upgrader_test.go — persona 6, the upgrader from lore 0.1.17. Una has used
// 0.1.17 for months: lore.db in the checkout, 0.1.17's git hooks, row files
// at _v: 1. She installs the new lore on her desk machine, runs `lore
// migrate run` there, and for a week keeps using the old lore on her laptop,
// both writing to the same repo. Then she upgrades the laptop too. Part two
// is every way back: rollback after a second run, rollback refused after an
// edit and edited back, delete then restore, and the old lore committing
// again after a full rollback.
//
// What she checks after every step: each lore reads every note it can, a
// note one lore cannot read is named (never silently missing), nothing she
// wrote is lost, the commit hook stops what would break the other machine
// and says why, and the new lore's doctor names what is pending.
//
// The old lore is a real 0.1.17 built from its tag (harness_oldlore.go).

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// upMemID finds a memory id in what lore printed.
var upMemID = regexp.MustCompile(`mem_[0-9a-f]{32}`)

// upFormat is the _v of a row file ("" when the file is missing).
var upFormat = regexp.MustCompile(`"_v": *(\d+)`)

// Notes Una writes, each a placeholder.
const (
	upNoteBranch = "deploys run from the release branch"
	upNoteLive   = "staging database is reset every monday"
	upNoteEdited = "deploys run from the release branch after the freeze"
	upNoteOldNew = "cache keys carry the schema version"
	upNoteNewNew = "feature flags live in flags.yaml"
	// upWorkEmail is the identity 0.1.17 kept in ~/.lore/identity.yaml.
	upWorkEmail = "una.work@example.test"
)

// upAddMemory adds a memory with run and returns its id.
func upAddMemory(t testing.TB, r teamharness.Result) string {
	t.Helper()
	r.OK(t)
	id := upMemID.FindString(r.Out())
	if id == "" {
		t.Fatalf("memory add printed no id: %s", r)
	}
	return id
}

// upRowFile is a memory's row file in a clone.
func upRowFile(c *teamharness.Clone, id string) string {
	return filepath.Join(".lore", "data", "memories", id+".json")
}

// upFileFormat is the _v of a memory's row file in c ("" when absent).
func upFileFormat(c *teamharness.Clone, id string) string {
	body, ok := c.ReadFile(upRowFile(c, id))
	if !ok {
		return ""
	}
	if m := upFormat.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return "?"
}

// upWantFormat fails unless every id's row file in c is at _v want.
func upWantFormat(t testing.TB, c *teamharness.Clone, want string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if got := upFileFormat(c, id); got != want {
			t.Errorf("%s: row file %s is _v %q, want %q", c.Dir, id, got, want)
		}
	}
}

// upLists fails unless a memory list shows every body and none of hidden.
func upLists(t testing.TB, who string, r teamharness.Result, bodies []string, hidden ...string) {
	t.Helper()
	r.OK(t)
	for _, b := range bodies {
		if !strings.Contains(r.Stdout, b) {
			t.Errorf("%s: memory list misses %q:\n%s", who, b, r.Stdout)
		}
	}
	for _, h := range hidden {
		if strings.Contains(r.Stdout, h) {
			t.Errorf("%s: memory list shows %q:\n%s", who, h, r.Stdout)
		}
	}
}

// upPull brings a clone up to date with main (rebasing what it has).
func upPull(t testing.TB, c *teamharness.Clone) {
	t.Helper()
	if r := c.TryGit("pull", "-q", "--rebase", "origin", "main"); r.Code != 0 {
		t.Fatalf("git pull in %s: %s", c.Dir, r)
	}
}

// upCommitPush commits everything with the machine's own hooks and pushes.
func upCommitPush(t testing.TB, c *teamharness.Clone, msg string) {
	t.Helper()
	c.CommitAll(msg)
	if r := c.TryGit("push", "-q", "origin", "HEAD:main"); r.Code != 0 {
		t.Fatalf("git push in %s: %s", c.Dir, r)
	}
}

// upDataDirty lists the changed files under .lore/data in c.
func upDataDirty(c *teamharness.Clone) string {
	return strings.TrimSpace(c.Git("status", "--porcelain", "--", ".lore/data"))
}

// TestJourneyUpgraderTwoMachines: migrate on the desk, keep the laptop on
// 0.1.17 for a week, then upgrade the laptop.
func TestJourneyUpgraderTwoMachines(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	una := w.Persona("Una Upgrader", "una@example.test")
	repo := w.Remote("github.com/acme/app")
	laptop := UseOldLore(t, una.Laptop())
	lap := laptop.Clone(repo, "app")
	var idBranch, idLive string

	w.Step("day1/old-lore-notes", func() {
		// 0.1.17's identity: ~/.lore/identity.yaml, a work email git does not have.
		laptop.Old(nil, "identity", "set", "Una Upgrader", "--key", "human:"+upWorkEmail).OK(t)
		laptop.Old(lap, "init", "--name", "app").OK(t)
		idBranch = upAddMemory(t, laptop.Old(lap, "memory", "add", "--body", upNoteBranch))
		idLive = upAddMemory(t, laptop.Old(lap, "memory", "add", "--live", "--body", upNoteLive))
		upPull(t, lap) // the live note landed on main on its own
		upCommitPush(t, lap, "lore data")
		upLists(t, "laptop 0.1.17", laptop.Old(lap, "memory", "list"), []string{upNoteBranch, upNoteLive})
		upWantFormat(t, lap, "1", idBranch, idLive)
		if _, ok := lap.ReadFile(".lore/lore.db"); !ok {
			t.Errorf("0.1.17 made no .lore/lore.db")
		}
		hook, _ := lap.ReadFile(".git/hooks/commit-msg")
		if !strings.Contains(hook, "lore-sync") {
			t.Errorf("0.1.17 installed no commit-msg hook block:\n%s", hook)
		}
	})

	w.AdvanceDays(30)
	desk := una.NewLaptop("desk")
	dsk := desk.Clone(repo, "app")

	w.Step("month1/desk-new-lore-reads-old-notes", func() {
		upLists(t, "desk", desk.LoreIn(dsk, "memory", "list"), []string{upNoteBranch, upNoteLive})
		desk.LoreIn(dsk, "migrate", "status").OK(t).Says(t, "pending: actors: 1 row at v1, memories: 2 rows at v1, projects: 1 row at v1")
		// Reading never rewrites the old files: the laptop must still read them.
		upWantFormat(t, dsk, "1", idBranch, idLive)
		if d := upDataDirty(dsk); d != "" {
			t.Errorf("reading with the new lore changed row files before any migration:\n%s", d)
		}
	})

	w.Step("month1/migrate-dry-run", func() {
		r := desk.LoreIn(dsk, "migrate", "run", "--dry-run").OK(t)
		r.Says(t, "Would upgrade 4 rows", "Nothing written (dry run).")
		for _, id := range []string{idBranch, idLive} {
			if n := strings.Count(r.Stdout, id); n != 1 {
				t.Errorf("dry run names %s %d times, want once:\n%s", id, n, r.Stdout)
			}
		}
		upWantFormat(t, dsk, "1", idBranch, idLive)
	})

	w.Step("month1/migrate", func() {
		r := desk.LoreIn(dsk, "migrate", "run").OK(t)
		r.Says(t, "Upgraded 4 rows", "backup: refs/lore-backup/migrate/", "undo: lore migrate rollback", "git add .lore/data")
		upWantFormat(t, dsk, "2", idBranch, idLive)
		desk.LoreIn(dsk, "migrate", "status").OK(t).Says(t, "min_lore_version: 0.1.18").Never(t, "pending:")
		upCommitPush(t, dsk, "lore: migrate format")
		upLists(t, "desk", desk.LoreIn(dsk, "memory", "list"), []string{upNoteBranch, upNoteLive})
	})

	w.AdvanceDays(1)
	w.Step("month1/laptop-0117-pulls-migrated-files", func() {
		upPull(t, lap)
		r := laptop.Old(lap, "memory", "list")
		// 0.1.17 cannot read _v: 2 files: it keeps its lore.db copy and names
		// every such file with "upgrade lore", so nothing is silently gone.
		upLists(t, "laptop 0.1.17", r, []string{upNoteBranch, upNoteLive})
		r.Says(t, "newer lore format", "upgrade lore")
		laptop.Old(lap, "doctor").Says(t, "newer-format")
	})

	w.Step("month1/laptop-0117-edit-is-refused-at-commit", func() {
		laptop.Old(lap, "memory", "edit", idBranch, "--body", upNoteEdited).OK(t)
		// 0.1.17 cannot write the _v: 2 file back: the edit stays in its
		// lore.db, and the commit hook refuses the commit (whole commit).
		upWantFormat(t, lap, "2", idBranch)
		before := repo.Rev("refs/heads/main")
		lap.Git("add", "-A")
		r := lap.TryGit("commit", "-q", "--allow-empty", "-m", "laptop work")
		if r.Code == 0 {
			t.Fatalf("0.1.17's hook let a commit through beside _v: 2 files: %s", r)
		}
		if !strings.Contains(r.Out(), "newer-format") {
			t.Errorf("the refused commit does not say why (newer-format): %s", r)
		}
		if repo.Rev("refs/heads/main") != before {
			t.Errorf("main moved although the commit was refused")
		}
	})

	var idOldNew string
	w.AdvanceDays(1)
	w.Step("month1/laptop-0117-forces-a-new-note-through", func() {
		// What a blocked person does: skips the hook.
		idOldNew = upAddMemory(t, laptop.Old(lap, "memory", "add", "--body", upNoteOldNew))
		lap.Git("add", "-A")
		lap.Git("commit", "-q", "--no-verify", "-m", "laptop note")
		if r := lap.TryGit("push", "-q", "origin", "HEAD:main"); r.Code != 0 {
			t.Fatalf("push: %s", r)
		}
		upWantFormat(t, lap, "1", idOldNew)
	})

	w.Step("month1/desk-sees-the-0117-note-and-says-so", func() {
		upPull(t, dsk)
		upLists(t, "desk", desk.LoreIn(dsk, "memory", "list"), []string{upNoteBranch, upNoteLive, upNoteOldNew})
		desk.LoreIn(dsk, "migrate", "status").OK(t).Says(t, "pending: memories: 1 row at v1")
		// After the migration a _v: 1 file means a lore older than 0.1.18
		// still writes here: doctor names it and the way out.
		desk.LoreIn(dsk, "doctor").Says(t, "row_format", "_v: 1", "lore fix")
	})

	var idNewNew string
	w.AdvanceDays(1)
	w.Step("month1/desk-writes-laptop-cannot-read-it", func() {
		idNewNew = upAddMemory(t, desk.LoreIn(dsk, "memory", "add", "--body", upNoteNewNew))
		upCommitPush(t, dsk, "desk note")
		upPull(t, lap)
		r := laptop.Old(lap, "memory", "list")
		// The desk's note is _v: 2: 0.1.17 cannot show it, and names its file.
		upLists(t, "laptop 0.1.17", r, []string{upNoteLive, upNoteOldNew}, upNoteNewNew)
		r.Says(t, idNewNew, "upgrade lore")
	})

	w.AdvanceDays(4)
	w.Step("week2/laptop-upgrades", func() {
		up := laptop.Upgrade()
		r := up.LoreIn(lap, "memory", "list")
		upLists(t, "laptop upgraded", r, []string{upNoteLive, upNoteOldNew, upNoteNewNew})
		// The edit 0.1.17 kept in lore.db only (it could not write the
		// _v: 2 file) is not in any file: the new lore must not drop it
		// without a word.
		// The new lore keeps 0.1.17's identity on this machine.
		up.LoreIn(lap, "whoami").OK(t).Says(t, "human:"+upWorkEmail)
		up.LoreIn(lap, "doctor").Says(t, "old_db_edits", "memories/"+idBranch, "lore fix")
		// Nothing was written for her yet: the edit still waits in lore.db.
		up.LoreIn(lap, "memory", "show", idBranch).OK(t).Never(t, upNoteEdited)
	})

	w.Step("week2/laptop-migrates-the-stragglers", func() {
		up := laptop.Machine
		up.LoreIn(lap, "migrate", "run").OK(t).Says(t, idOldNew, "old_db_edits: brought in 1 edit", "memories/"+idBranch)
		upWantFormat(t, lap, "2", idOldNew, idBranch)
		up.LoreIn(lap, "memory", "show", idBranch).OK(t).Says(t, upNoteEdited)
		up.LoreIn(lap, "doctor").Never(t, "old_db_edits", "row_format")
		up.LoreIn(lap, "migrate", "status").OK(t).Never(t, "pending:")
		up.LoreIn(lap, "whoami").OK(t).Says(t, "human:"+upWorkEmail)
		upCommitPush(t, lap, "lore: migrate format")
		upPull(t, dsk)
		desk.LoreIn(dsk, "doctor").Never(t, "row_format")
		upLists(t, "desk", desk.LoreIn(dsk, "memory", "list"), []string{upNoteLive, upNoteOldNew, upNoteNewNew, upNoteEdited})
	})
}

// TestJourneyUpgraderUndo: every way back. The desk migrates a stale
// checkout, pulls, migrates again, then rolls both runs back so the laptop's
// 0.1.17 can commit again; later a rollback is refused after an edit and
// works once the edit is undone; a migrated note is deleted and restored.
func TestJourneyUpgraderUndo(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	una := w.Persona("Una Upgrader", "una@example.test")
	repo := w.Remote("github.com/acme/tools")
	laptop := UseOldLore(t, una.Laptop())
	lap := laptop.Clone(repo, "tools")
	var idFirst, idSecond string

	w.Step("day1/old-lore-first-note", func() {
		laptop.Old(lap, "init", "--name", "tools").OK(t)
		idFirst = upAddMemory(t, laptop.Old(lap, "memory", "add", "--body", upNoteBranch))
		upCommitPush(t, lap, "lore data")
	})

	desk := una.NewLaptop("desk")
	dsk := desk.Clone(repo, "tools") // cloned now, pulled only later

	w.AdvanceDays(1)
	w.Step("day2/old-lore-second-note", func() {
		idSecond = upAddMemory(t, laptop.Old(lap, "memory", "add", "--body", upNoteOldNew))
		upCommitPush(t, lap, "laptop note")
	})

	w.Step("day2/desk-migrates-a-stale-checkout-then-again", func() {
		// 0.1.17 had no identity here: its actor copy says "una", the new
		// lore's git name is "Una Upgrader"; the run refreshes it too.
		desk.LoreIn(dsk, "migrate", "run").OK(t).Says(t, "Upgraded 3 rows", idFirst, "actor_copies: refreshed 1 actor copy")
		dsk.CommitAll("lore: migrate format")
		upPull(t, dsk)
		desk.LoreIn(dsk, "migrate", "status").OK(t).Says(t, "pending: memories: 1 row at v1")
		desk.LoreIn(dsk, "migrate", "run").OK(t).Says(t, "Upgraded 1 row", idSecond)
		dsk.CommitAll("lore: migrate format")
		upWantFormat(t, dsk, "2", idFirst, idSecond)
	})

	w.Step("day2/undo-second-run", func() {
		r := desk.LoreIn(dsk, "migrate", "rollback").OK(t)
		// The first run's row is still _v: 2: the rollback says so and how.
		r.Says(t, "earlier migration", "still stands", "lore migrate rollback")
		r.Says(t, "Rolled back the migration (backup refs/lore-backup/migrate/", "1 file restored").Never(t, "lines:", "1 files")
		upWantFormat(t, dsk, "1", idSecond)
		upWantFormat(t, dsk, "2", idFirst)
		desk.LoreIn(dsk, "migrate", "status").OK(t).Says(t, "undo: lore migrate rollback").Never(t, "was rolled back")
	})

	w.Step("day2/undo-first-run", func() {
		desk.LoreIn(dsk, "migrate", "rollback").OK(t).Never(t, "earlier migration")
		upWantFormat(t, dsk, "1", idFirst, idSecond)
		desk.LoreIn(dsk, "migrate", "status").OK(t).Says(t, "pending: actors: 1 row at v1, memories: 2 rows at v1, projects: 1 row at v1", "was rolled back")
		desk.LoreIn(dsk, "migrate", "rollback").Refused(t, "no migration to roll back")
		upLists(t, "desk", desk.LoreIn(dsk, "memory", "list"), []string{upNoteBranch, upNoteOldNew})
		upCommitPush(t, dsk, "lore: roll back format migration")
	})

	w.AdvanceDays(1)
	w.Step("day3/laptop-0117-commits-again", func() {
		upPull(t, lap)
		r := laptop.Old(lap, "memory", "list")
		upLists(t, "laptop 0.1.17", r, []string{upNoteBranch, upNoteOldNew})
		r.Never(t, "newer lore format")
		laptop.Old(lap, "memory", "add", "--body", upNoteLive).OK(t)
		upCommitPush(t, lap, "laptop note after the rollback") // the 0.1.17 hook lets it through
	})

	w.AdvanceDays(7)
	w.Step("week2/migrate-again-for-good", func() {
		upPull(t, dsk)
		desk.LoreIn(dsk, "migrate", "run").OK(t).Says(t, "Upgraded 5 rows", "actor_copies: refreshed 1 actor copy")
		upWantFormat(t, dsk, "2", idFirst, idSecond)
	})

	w.Step("week2/rollback-refused-after-an-edit", func() {
		desk.LoreIn(dsk, "memory", "edit", idFirst, "--body", upNoteEdited).OK(t)
		desk.LoreIn(dsk, "migrate", "rollback").Refused(t, "E_MIGRATE_ROLLBACK_LOSES", idFirst, "nothing changed")
		upWantFormat(t, dsk, "2", idFirst, idSecond)
		desk.LoreIn(dsk, "memory", "show", idFirst).OK(t).Says(t, upNoteEdited)
	})

	w.Step("week2/edit-back-then-rollback", func() {
		desk.LoreIn(dsk, "memory", "edit", idFirst, "--body", upNoteBranch).OK(t)
		desk.LoreIn(dsk, "memory", "show", idFirst).OK(t).Says(t, upNoteBranch)
		// The edit is undone: the rollback loses nothing and goes ahead.
		desk.LoreIn(dsk, "migrate", "rollback").OK(t)
		upWantFormat(t, dsk, "1", idFirst, idSecond)
		desk.LoreIn(dsk, "memory", "show", idFirst).OK(t).Says(t, upNoteBranch)
	})

	w.Step("week2/migrate-and-keep-it", func() {
		desk.LoreIn(dsk, "migrate", "run").OK(t).Says(t, "Upgraded 5 rows", "actor_copies: refreshed 1 actor copy")
		upCommitPush(t, dsk, "lore: migrate format")
	})

	w.AdvanceDays(1)
	w.Step("week2/delete-then-restore-a-migrated-note", func() {
		desk.LoreIn(dsk, "memory", "delete", idSecond, "--yes").OK(t)
		upLists(t, "desk", desk.LoreIn(dsk, "memory", "list"), []string{upNoteBranch}, upNoteOldNew)
		desk.LoreIn(dsk, "trash", "restore", idSecond).OK(t)
		upLists(t, "desk", desk.LoreIn(dsk, "memory", "list"), []string{upNoteBranch, upNoteOldNew})
		upWantFormat(t, dsk, "2", idSecond)
		upCommitPush(t, dsk, "restored")
	})

	w.Step("week2/laptop-upgrades-and-sees-everything", func() {
		upPull(t, lap)
		up := laptop.Upgrade()
		upLists(t, "laptop upgraded", up.LoreIn(lap, "memory", "list"), []string{upNoteBranch, upNoteOldNew, upNoteLive})
		up.LoreIn(lap, "doctor").Never(t, "old_db_edits", "row_format")
	})
}
