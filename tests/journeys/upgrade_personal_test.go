//go:build journeys && !windows

package journeys

// upgrade_personal_test.go — the upgrader, part four: personal notes and
// store clones lore 0.1.17 left behind. Pia used 0.1.17 with a git
// personal store: `--to me` filed each note under the org folder of the
// repo she was in (acme/) or under everywhere/, and one note was saved
// while her wifi was off, so it sits only in 0.1.17's bare clone under
// ~/.lore/clones/github.com/pia/lore-me.git. Lou used 0.1.17 with
// `personal.store: local`. Both install this lore.
//
// What they check: every personal note is still listed (`--store me`), none
// twice, and keeps its scope: a note filed for acme loads in acme's repos
// only, an everywhere/ note in every repo; the note that never
// left the laptop reaches the remote; the old bare clones are named by
// doctor as lore 0.1.17's with a command naming real paths; the local
// store's first write lands where lore reads it, with no copy left behind.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pia's notes (placeholders).
const (
	upAcmeNote    = "the acme staging vpn profile is acme-stg"
	upAnyNote     = "prefer short commit subjects"
	upOfflineNote = "written on the train with no network"
	upLocalNote   = "a local personal note from 0.1.17"
	upLocalAfter  = "a local personal note after the upgrade"
)

// upWriteConfig writes a 0.1.17 ~/.lore/config.yaml on m.
func upWriteConfig(t testing.TB, m *Machine, body string) {
	t.Helper()
	p := m.LorePath("config.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// upCount fails unless body appears exactly once in out.
func upCount(t testing.TB, who, out, body string) {
	t.Helper()
	if n := strings.Count(out, body); n != 1 {
		t.Errorf("%s: %q listed %d times, want once:\n%s", who, body, n, out)
	}
}

func TestJourneyUpgraderPersonalNotes(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	pia := w.Persona("Pia Personal", "pia@example.test")
	laptop := UseOldLore(t, pia.Laptop())
	notes := w.Remote("github.com/acme/lore-notes")
	me := w.Remote("github.com/pia/lore-me")
	api := laptop.Clone(w.Remote("github.com/acme/api"), "api")
	dots := laptop.Clone(w.Remote("github.com/pia/dots"), "dots")

	w.Step("day1/0117-personal-notes", func() {
		upWriteConfig(t, laptop.Machine, "identity:\n  key: human:pia@example.test\n  name: Pia Personal\npersonal:\n  store: "+me.SSHURL()+"\norgs:\n  - name: acme\n    identity: {key: \"human:pia@example.test\", name: \"Pia Personal\", handle: pia}\n    stores:\n      - url: "+notes.SSHURL()+"\n        org_folder: org\n    own: [github.com/acme/*]\n")
		laptop.Old(nil, "store", "init", notes.SSHURL()).OK(t)
		laptop.Old(nil, "store", "init", me.SSHURL()).OK(t)
		laptop.Old(api, "init", "--non-interactive", "--name=api").OK(t)
		laptop.Old(dots, "init", "--non-interactive", "--name=dots").OK(t)
		laptop.Old(api, "memory", "add", "--body", upAcmeNote, "--to", "me").OK(t).Says(t, "acme/")
		laptop.Old(dots, "memory", "add", "--body", upAnyNote, "--to", "me").OK(t).Says(t, "everywhere/")
		laptop.Offline()
		laptop.Old(api, "memory", "add", "--body", upOfflineNote, "--to", "me").OK(t)
		// listed while still offline: 0.1.17 pushes waiting commits on its
		// next online command, and this one must stay in the bare clone
		laptop.Old(api, "memory", "list").OK(t).Says(t, upAcmeNote, upAnyNote, upOfflineNote)
		laptop.Online()
		if got := me.FilesMatching("main", "/memories/mem_"); len(got) != 2 {
			t.Fatalf("the personal store's remote holds %d notes, want 2 (the offline one only in the bare clone): %v", len(got), got)
		}
	})

	w.AdvanceDays(1)
	m := laptop.Upgrade()

	w.Step("day2/upgrade-keeps-every-personal-note", func() {
		// D16: the first command moves the clones on this machine only; the
		// store stays on 0.1.17's layout (read, never written) and says so
		r := m.LoreIn(api, "memory", "list", "--store", "me").OK(t)
		r.Says(t, "(0.1.17) -> ", "local commit", "store lore-me is on the lore 0.1.17 layout; run lore migrate run").Never(t, "moved to _store/")
		for _, b := range []string{upAcmeNote, upAnyNote, upOfflineNote} {
			upCount(t, "api --store me (0.1.17 layout)", r.Stdout, b)
		}
		m.LoreIn(api, "memory", "add", "--body", "refused for now", "--to", "me").Fails(t, 24, "store me is on the lore 0.1.17 layout")
		// lore migrate run upgrades the store: its layout commit and the
		// everywhere/ move
		m.LoreIn(api, "migrate", "run").OK(t).Says(t, "personal store lore-me:", "moved to _store/")
		r = m.LoreIn(api, "memory", "list", "--store", "me").OK(t)
		for _, b := range []string{upAcmeNote, upAnyNote, upOfflineNote} {
			upCount(t, "api --store me", r.Stdout, b)
		}
		all := m.LoreIn(api, "memory", "list").OK(t)
		for _, b := range []string{upAcmeNote, upAnyNote, upOfflineNote} {
			upCount(t, "api", all.Stdout, b)
		}
		// D5: a note 0.1.17 filed for acme loads in acme's repos only
		d := m.LoreIn(dots, "memory", "list").OK(t)
		upCount(t, "dots", d.Stdout, upAnyNote)
		d.Never(t, upAcmeNote, upOfflineNote)
		files := strings.Join(me.Files("main"), "\n")
		if n := strings.Count(files, "/memories/mem_"); n != 3 {
			t.Errorf("the personal store's remote holds %d notes, want 3 (the offline one pushed):\n%s", n, files)
		}
		if n := strings.Count(files, "_store/memories/"); n != 1 {
			t.Errorf("the personal store's remote holds %d notes in _store/, want 1 (everywhere/'s):\n%s", n, files)
		}
		if strings.Contains(files, "everywhere/") {
			t.Errorf("the personal store's remote still has everywhere/ after the move:\n%s", files)
		}
	})

	w.Step("day2/doctor-names-the-0117-clones", func() {
		r := m.LoreIn(api, "doctor")
		bare := m.LorePath("clones", "github.com", "pia", "lore-me.git")
		r.Says(t, "left from lore 0.1.17", "rm -rf "+bare).Never(t, "half of an old store clone", "lore-me "+bare)
		m.LoreIn(api, "fix").OK(t)
		if _, err := os.Stat(bare); err == nil {
			t.Errorf("lore fix left %s, which doctor proved safe to remove", bare)
		}
		m.LoreIn(api, "memory", "list", "--store", "me").OK(t).Never(t, "moved store clone", "personal store lore-me:")
	})

	lou := w.Persona("Lou Local", "lou@example.test")
	old := UseOldLore(t, lou.Laptop())
	notesRepo := old.Clone(w.Remote("github.com/lou/notes"), "notes")

	w.Step("day3/local-personal-store", func() {
		upWriteConfig(t, old.Machine, "identity:\n  key: human:lou@example.test\n  name: Lou Local\npersonal:\n  store: local\n")
		old.Old(notesRepo, "init", "--non-interactive", "--name=notes").OK(t)
		old.Old(notesRepo, "memory", "add", "--body", upLocalNote, "--to", "me").OK(t)
		lm := old.Upgrade()
		lm.LoreIn(notesRepo, "memory", "add", "--body", upLocalAfter, "--to", "me").OK(t)
		// lore fix --migrate moves the local store's folders to its root: notes
		// is a 0.1.17-format project (its project and actor rows are _v: 1),
		// so plain lore fix refuses it (D15) and the move waits for --migrate
		lm.LoreIn(notesRepo, "fix").Fails(t, 24, "E_FORMAT_OLD")
		lm.LoreIn(notesRepo, "fix", "--migrate").OK(t)
		r := lm.LoreIn(notesRepo, "memory", "list", "--store", "me").OK(t)
		upCount(t, "local --store me", r.Stdout, upLocalNote)
		upCount(t, "local --store me", r.Stdout, upLocalAfter)
		var left []string
		_ = filepath.WalkDir(lm.LorePath("personal", "everywhere"), func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") && filepath.Base(p) != "_meta.json" {
				left = append(left, p)
			}
			return nil
		})
		if len(left) > 0 {
			t.Errorf("row files left in the old everywhere/ folder after the move: %v", left)
		}
		lm.LoreIn(notesRepo, "doctor").Never(t, "lore 0.1.17 folder this lore does not read")
	})
}
