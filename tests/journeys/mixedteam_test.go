//go:build journeys && !windows

package journeys

// mixedteam_test.go — one store, two machines on lore 0.1.17, and only one
// of them upgrades. The upgrading machine's `lore fix` moves the store to
// the 0.1.18 layout in that same command (never on a later read) and says,
// before the layout commit, that whoever uses the store with 0.1.17 must
// upgrade. 0.1.17 cannot be told the store is newer (it refuses any file
// outside a .lore folder as "not a lore store"), so the other machine is
// refused until it upgrades; nothing is lost, and after its own upgrade and
// `lore fix` it reads and writes the store again.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Placeholders of this journey.
const (
	mxName  = "Mia Mixed"
	mxEmail = "mia@example.test"
	mxRepo  = "github.com/mia-dev/app"
	mxStore = "github.com/mia-dev/lore-me"
	// mxOrgStore is the org's store; the repo's internal notes live there.
	mxOrgStore = "github.com/mia-dev/lore-notes"
	mxOrgNote  = "release notes are drafted on fridays"
	mxNote     = "the nightly export runs at 03:00 UTC"
	mxLater    = "export files are kept for 30 days"
	// mxRepoNote is a repo row, so the repo has v0.1.17 rows to migrate.
	mxRepoNote = "the export job lives in cmd/export"
)

// mxConfig writes the 0.1.17 config naming the personal store and, when
// org is set, the org with its store (the repos under mia-dev are the
// org's own).
func mxConfig(t testing.TB, m *Machine, store, org *teamharness.Remote) {
	t.Helper()
	dir := filepath.Join(m.Home, ".lore")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "identity:\n  key: human:" + mxEmail + "\n  name: " + mxName + "\npersonal:\n  store: " + store.URL() + "\n"
	if org != nil {
		body += "orgs:\n  - name: acme\n    identity: {key: \"human:" + mxEmail + "\", name: \"" + mxName + "\", handle: mia}\n    stores:\n      - url: " + org.URL() + "\n        org_folder: org\n    own: [github.com/mia-dev/*]\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestJourneyMixedVersionsOneStore: the laptop upgrades, the desk stays on
// 0.1.17 for a day.
func TestJourneyMixedVersionsOneStore(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mia := w.Persona(mxName, mxEmail)
	repo := w.Remote(mxRepo)
	st := w.Remote(mxStore)
	org := w.Remote(mxOrgStore)
	laptop := UseOldLore(t, mia.Laptop())
	desk := UseOldLore(t, mia.NewLaptop("desk"))
	lap := laptop.Clone(repo, "app")
	var dsk *teamharness.Clone

	w.Step("day1/both-on-0.1.17", func() {
		mxConfig(t, laptop.Machine, st, org)
		laptop.Old(nil, "store", "init", st.URL()).OK(t)
		laptop.Old(nil, "store", "init", org.URL()).OK(t)
		laptop.Old(lap, "init", "--name", "app").OK(t)
		laptop.Old(lap, "org", "link", "acme", "--project", "app-internal", "--own", "--yes").OK(t)
		laptop.Old(lap, "memory", "add", "--body", mxOrgNote, "--to", "acme").OK(t)
		laptop.Old(lap, "memory", "add", "--body", mxNote, "--to", "me").OK(t)
		laptop.Old(lap, "memory", "add", "--body", mxRepoNote).OK(t) // v0.1.17 rows in the repo: lore fix migrates
		upCommitPush(t, lap, "lore data")
		mxConfig(t, desk.Machine, st, org)
		dsk = desk.Clone(repo, "app")
		desk.Old(dsk, "sync").OK(t)
		desk.Old(dsk, "memory", "list").OK(t).Says(t, mxNote, mxOrgNote)
	})

	w.AdvanceDays(1)
	w.Step("day2/laptop-upgrades", func() {
		up := laptop.Upgrade()
		// lore fix migrates and makes the layout commit in the same run,
		// after the line that warns the 0.1.17 users of the store
		// (the personal note was filed for acme: it keeps its folder, D5, so
		// no personal move commit is made)
		up.LoreIn(lap, "fix").Says(t, "must upgrade lore", "0.1.17 refuses the new layout", "layout commit", "pushed store lore-notes").
			Never(t, "run `lore sync` to push the org store changes")
		up.LoreIn(lap, "memory", "list").OK(t).Says(t, mxOrgNote, mxNote)
		if len(org.FilesMatching("main", "/.lore/data/")) != 0 {
			t.Errorf("the org store still holds rows in the 0.1.17 place after lore fix: %v", org.FilesMatching("main", "/.lore/data/"))
		}
		if len(st.FilesMatching("main", "/.lore/data/")) != 0 {
			t.Errorf("the store still holds rows in the 0.1.17 place after lore fix: %v", st.FilesMatching("main", "/.lore/data/"))
		}
		// a read afterwards has nothing left to move or push
		up.LoreIn(lap, "memory", "list").OK(t).Says(t, mxRepoNote).Never(t, "layout commit", "pushed store")
		if len(st.FilesMatching("main", "memories/mem_")) == 0 {
			t.Errorf("the personal note is not in the store after the move: %v", st.Files("main"))
		}
		// a help call never writes
		up.LoreIn(lap, "migrate", "run", "--help").OK(t).Never(t, "pushed store", "layout commit")
	})

	w.Step("day2/desk-still-on-0.1.17", func() {
		// 0.1.17 cannot tell a newer store from a foreign repo: it refuses
		// the write and the commit hook. (It may still push commits it had
		// waiting into the personal store, in the old place; the new lore
		// moves those after the upgrade, checked below.)
		before := org.CommitCount("main")
		desk.Old(dsk, "memory", "add", "--body", mxLater, "--to", "me").Refused(t, "E_NOT_A_STORE")
		desk.Old(dsk, "memory", "add", "--body", mxLater, "--to", "acme").Refused(t, "E_NOT_A_STORE")
		// in a repo linked to the org store, 0.1.17's pre-commit hook runs
		// the same check: code commits are refused until the upgrade (the
		// upgrade docs say so, and --no-verify gets past it)
		dsk.WriteFile("main.go", "package main\n")
		dsk.Git("add", "main.go")
		if r := dsk.TryGit("commit", "-q", "-m", "code"); r.Code == 0 || !strings.Contains(r.Out(), "E_NOT_A_STORE") {
			t.Errorf("0.1.17's pre-commit hook in an org-linked repo: want a refusal naming E_NOT_A_STORE: %s", r)
		}
		if r := dsk.TryGit("commit", "-q", "--no-verify", "-m", "code"); r.Code != 0 {
			t.Errorf("git commit --no-verify past the 0.1.17 hook: %s", r)
		}
		if got := org.CommitCount("main"); got != before {
			t.Errorf("the refused 0.1.17 write changed the org store: %d commits, was %d: %v", got, before, org.Log("main"))
		}
	})

	w.Step("day2/desk-upgrades", func() {
		up := desk.Upgrade()
		up.LoreIn(dsk, "fix")
		before := len(st.FilesMatching("main", "memories/mem_"))
		up.LoreIn(dsk, "memory", "add", "--body", mxLater, "--to", "me").OK(t)
		up.LoreIn(dsk, "sync").OK(t)
		if got := len(st.FilesMatching("main", "memories/mem_")); got != before+1 {
			t.Errorf("after the upgrade the desk's personal note did not reach the store: %d notes, was %d", got, before)
		}
		up.LoreIn(dsk, "memory", "list").OK(t).Says(t, mxRepoNote, mxOrgNote)
		if old := st.FilesMatching("main", "/.lore/data/"); len(old) != 0 {
			t.Errorf("rows 0.1.17 pushed into the old place are still there after the desk's upgrade: %v", old)
		}
		dsk.WriteFile("main.go", "package main\n\nfunc main() {}\n")
		dsk.CommitAll("code after the upgrade")
	})
}

// TestJourneyStoreOnlyUpgrade (M-1): under 0.1.17 Mia's repo holds no row
// of its own; her note is in her personal store. After the upgrade, `lore
// fix` in the repo still converts the store and moves it to the new layout (it used to say "current" and leave them at
// lore_store: 1 forever).
func TestJourneyStoreOnlyUpgrade(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mia := w.Persona(mxName, mxEmail)
	repo := w.Remote(mxRepo)
	st := w.Remote(mxStore)
	laptop := UseOldLore(t, mia.Laptop())
	lap := laptop.Clone(repo, "app")

	w.Step("day1/notes-only-in-stores", func() {
		mxConfig(t, laptop.Machine, st, nil)
		laptop.Old(nil, "store", "init", st.URL()).OK(t)
		laptop.Old(lap, "init", "--name", "app").OK(t)
		laptop.Old(lap, "memory", "add", "--body", mxNote, "--to", "me").OK(t)
		upCommitPush(t, lap, "lore data")
	})

	w.AdvanceDays(1)
	w.Step("day2/upgrade-and-fix", func() {
		up := laptop.Upgrade()
		// the first command, a read, moves 0.1.17's per-org personal notes
		// to _store/ and pushes: the lockout block comes first, on its own,
		// then one line for the move and its push (B4-18)
		r := up.LoreIn(lap, "memory", "list").OK(t).Says(t, "store lore-me moves to the 0.1.18 layout now", "E_NOT_A_STORE", "moved to _store/: commit", ", pushed;")
		if strings.Index(r.Stderr, "moves to the 0.1.18 layout now") > strings.Index(r.Stderr, "moved to _store/: commit") {
			t.Errorf("the lockout block comes after the move:\n%s", r.Stderr)
		}
		// the read moved every row already; fix converts the marker
		// (lore_store: 4, checked below) and pushes it
		up.LoreIn(lap, "fix").Says(t, "lore-store.yaml v1 → v2", "pushed").
			Never(t, "store lore-me moves to the 0.1.18 layout now") // said once per store and machine (B4-21)
		for _, r := range []*teamharness.Remote{st} {
			if m, _ := r.Read("main", "lore-store.yaml"); !strings.Contains(m, "lore_store: 4") {
				t.Errorf("%s: marker after lore fix:\n%s", r.Slug, m)
			}
			if old := r.FilesMatching("main", "/.lore/data/"); len(old) != 0 {
				t.Errorf("%s: rows still in the 0.1.17 place after lore fix: %v", r.Slug, old)
			}
		}
		// a second fix has nothing left to do in the stores
		up.LoreIn(lap, "fix").Never(t, "layout commit", "v1 → v2")
	})
}

// TestJourneyFirstWriteMovesOrgStore (B4-18, B4-10): after the upgrade the
// first command in the repo is a write. Its safe steps migrate the project
// and make the org store's layout commit: the lockout block is printed
// before it, the layout commit and its push are named, and no line names
// a row file at its old place.
func TestJourneyFirstWriteMovesOrgStore(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mia := w.Persona(mxName, mxEmail)
	repo := w.Remote(mxRepo)
	st := w.Remote(mxStore)
	org := w.Remote(mxOrgStore)
	laptop := UseOldLore(t, mia.Laptop())
	lap := laptop.Clone(repo, "app")

	w.Step("day1/old-lore", func() {
		mxConfig(t, laptop.Machine, st, org)
		laptop.Old(nil, "store", "init", st.URL()).OK(t)
		laptop.Old(nil, "store", "init", org.URL()).OK(t)
		laptop.Old(lap, "init", "--name", "app").OK(t)
		laptop.Old(lap, "org", "link", "acme", "--project", "app-internal", "--own", "--yes").OK(t)
		laptop.Old(lap, "memory", "add", "--body", mxOrgNote, "--to", "acme").OK(t)
		laptop.Old(lap, "memory", "add", "--body", mxRepoNote).OK(t)
		upCommitPush(t, lap, "lore data")
	})

	w.AdvanceDays(1)
	w.Step("day2/first-write", func() {
		up := laptop.Upgrade()
		r := up.LoreIn(lap, "memory", "add", "--body", mxLater).OK(t).
			Says(t, "store lore-notes moves to the 0.1.18 layout now", "store lore-notes: layout commit", "pushed store lore-notes")
		for _, l := range strings.Split(r.Out(), "\n") {
			if strings.Contains(l, "stores/mia-dev/") && strings.Contains(l, "/.lore/data") {
				t.Errorf("a line names a store row at its old place: %s", l)
			}
		}
		if strings.Index(r.Stderr, "store lore-notes moves to the 0.1.18 layout now") > strings.Index(r.Stderr, "store lore-notes: layout commit") {
			t.Errorf("the lockout block comes after the layout commit:\n%s", r.Stderr)
		}
		if old := org.FilesMatching("main", "/.lore/data/"); len(old) != 0 {
			t.Errorf("the org store keeps rows in the 0.1.17 place after the first write: %v", old)
		}
		up.LoreIn(lap, "memory", "list").OK(t).Says(t, mxOrgNote, mxLater)
	})
}
