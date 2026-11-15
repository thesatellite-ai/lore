//go:build journeys && !windows

package journeys

// lorehome_storehook_test.go — devco D-423 (story K1): a person who runs a
// preview build as `lore-dev` (LORE_HOME=~/.lore-dev, set by the wrapper
// for lore only) edits a row of a store clone under that home by hand and
// commits with plain git. The clone's pre-commit hook ran lore without
// LORE_HOME, so lore looked in ~/.lore and refused: E_LEAK_BLOCKED "… is a
// clone of store … that lore does not use". The hook now carries the home
// it was installed under, and `lore doctor --fix` rewrites an older hook.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyLoreHomeStoreHandCommit(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	moved := filepath.Join(w.Root, "elsewhere", "lore-dev-home")
	lap.SetEnv(loreHomeEnv, moved)
	notes := w.Remote("github.com/sam-dev/notes")
	app := jPlainRepo(t, lap, w.Remote("github.com/sam-dev/app"), filepath.Join(lap.Home, "work", "app"))
	var cloneDir, rowFile string

	w.Step("day1/store-and-a-note", func() {
		lap.LoreIn(app, "init").OK(t)
		lap.LoreIn(app, "store", "init", "--name", "notes", "--url", notes.SSHURL()).OK(t)
		lap.LoreIn(app, "memory", "add", "--body", "timestamps are UTC", "--to", "notes").OK(t)
		cloneDir, rowFile = storeRowFile(t, filepath.Join(moved, "stores"), "timestamps are UTC")
	})

	// handCommit edits the note's text from old to body by hand and
	// commits with plain git from a shell without LORE_HOME.
	handCommit := func(old, body, msg string) {
		t.Helper()
		b, err := os.ReadFile(rowFile)
		if err != nil {
			t.Fatal(err)
		}
		jWrite(t, rowFile, strings.Replace(string(b), old, body, 1))
		clone := lap.OpenClone(notes, cloneDir)
		lap.SetEnv(loreHomeEnv, "") // the person's own shell: only the lore-dev wrapper sets LORE_HOME
		defer lap.SetEnv(loreHomeEnv, moved)
		if r := clone.TryGit("commit", "-q", "-am", msg); r.Code != 0 {
			t.Fatalf("a hand commit in the store clone under LORE_HOME was refused (exit %d):\n%s", r.Code, r.Out())
		}
	}

	w.Step("day1/hand-commit-in-the-store-clone", func() {
		handCommit("timestamps are UTC", "timestamps are UTC everywhere", "hand edit")
	})

	w.Step("day2/an-older-hook-is-rewritten-by-doctor-fix", func() {
		hook := filepath.Join(cloneDir, strings.TrimSpace(lap.OpenClone(notes, cloneDir).Git("rev-parse", "--git-path", "hooks/pre-commit")))
		b, err := os.ReadFile(hook)
		if err != nil {
			t.Fatal(err)
		}
		var kept []string
		for _, l := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(l, loreHomeEnv+"=") {
				kept = append(kept, l)
			}
		}
		if len(kept) == len(strings.Split(string(b), "\n")) {
			t.Fatalf("the store clone's hook does not set %s:\n%s", loreHomeEnv, b)
		}
		jWrite(t, hook, strings.Join(kept, "\n")) // what an older lore wrote
		lap.LoreIn(app, "doctor", "--fix", "--yes")
		if b, err = os.ReadFile(hook); err != nil || !strings.Contains(string(b), loreHomeEnv+"='"+moved+"'") {
			t.Fatalf("doctor --fix did not rewrite the older hook to carry %s=%s:\n%s", loreHomeEnv, moved, b)
		}
		handCommit("timestamps are UTC everywhere", "timestamps are UTC, always", "hand edit after doctor --fix")
	})
}

// storeRowFile finds, under stores, the store clone (a folder with .git)
// holding a row file that contains text, and that file.
func storeRowFile(t testing.TB, stores, text string) (clone, file string) {
	t.Helper()
	_ = filepath.WalkDir(stores, func(p string, d fs.DirEntry, err error) error {
		if err != nil || file != "" {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), text) {
			file = p
		}
		return nil
	})
	if file == "" {
		t.Fatalf("no row file under %s holds %q", stores, text)
	}
	for dir := filepath.Dir(file); dir != stores && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, file
		}
	}
	t.Fatalf("the row file %s is in no git clone under %s", file, stores)
	return "", ""
}
