package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// wiredNotice is the one-time notice of the git wiring (v0.1.17
// sync_wire.go reportGitWiring), prefixed with the command that wired
// (owner bug 6): here the first write in the clone.
const wiredNotice = "lore task add: set up git for lore's row files in this clone: "

// unwiredNote is a read's one note in a clone git is not set up in.
const unwiredNote = "lore task list: git is not set up for lore's row files in this clone: `lore sync`"

// TestFreshCloneWiringNotice (E-GAPS-1 G3, owner bug 6): a read in a
// teammate's fresh clone wires nothing and says so once, naming `lore
// sync`; the first write wires git and says so once on stderr, as itself,
// naming the hooks folder; the next command says nothing (the clone is
// wired already). With LORE_SYNC_QUIET=1 the clone is wired just the same but
// the notice is not printed (README, docs/reference/environment.md,
// docs/sync/how-notes-travel-with-git.md).
func TestFreshCloneWiringNotice(t *testing.T) {
	t.Parallel()
	w, a, b := twoLiveClones(t)
	driver := func(dir string) string {
		out, _ := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: dir, Args: []string{"config", "--local", "--get", "merge.lore.driver"}}) // unset = exit 1: an empty driver
		return strings.TrimSpace(string(out))
	}
	if driver(b) != "" {
		t.Fatal("setup: the fresh clone already has merge.lore.driver")
	}

	code, out := w.loreOut(t, b, "task", "list")
	if code != 0 || !strings.Contains(out, unwiredNote) || strings.Contains(out, "set up git") || driver(b) != "" {
		t.Fatalf("a read in the fresh clone: exit %d, want the one note and no wiring:\n%s", code, out)
	}
	if code, out = w.loreOut(t, b, "task", "list"); code != 0 || strings.Contains(out, unwiredNote) {
		t.Fatalf("a second read repeated the note: exit %d\n%s", code, out)
	}
	code, out = w.loreOut(t, b, "task", "add", "warm the cache")
	if code != 0 {
		t.Fatalf("first write in the fresh clone: exit %d %s", code, out)
	}
	hooks := filepath.Join(b, ".git", "hooks")
	// no hidden magic: the notice names each thing it changed and where
	for _, want := range []string{wiredNotice, "merge drivers lore and lore-keep-ours in " + filepath.Join(b, ".git", "config"), "hook blocks in " + hooks + " (", "pre-commit", "`lore where`"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the first write in a fresh clone does not print %q:\n%s", want, out)
		}
	}
	if strings.Count(out, wiredNotice) != 1 {
		t.Fatalf("the wiring notice is printed more than once:\n%s", out)
	}
	if !strings.Contains(driver(b), "merge-driver") {
		t.Fatal("the first write did not wire merge.lore.driver")
	}
	if code, out = w.loreOut(t, b, "task", "list"); code != 0 || strings.Contains(out, wiredNotice) {
		t.Fatalf("second command in the wired clone: exit %d, must not repeat the notice:\n%s", code, out)
	}

	c := filepath.Join(filepath.Dir(a), "c")
	w.git(t, filepath.Dir(a), "clone", "-q", filepath.Join(filepath.Dir(a), "remote.git"), c)
	w.set(hosthooks.EnvSyncQuiet, "1")
	if code, out = w.loreOut(t, c, "task", "add", "drain the queue"); code != 0 || strings.Contains(out, "set up git") {
		t.Fatalf("LORE_SYNC_QUIET=1 first write in a fresh clone: exit %d, must print no set-up line:\n%s", code, out)
	}
	if !strings.Contains(driver(c), "merge-driver") {
		t.Fatal("LORE_SYNC_QUIET=1 must still wire the clone (merge.lore.driver unset)")
	}
}
