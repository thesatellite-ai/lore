package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// wiredNotice is v0.1.17's one-time notice of the git wiring
// (sync_wire.go reportGitWiring), byte for byte up to the hooks folder.
const wiredNotice = "lore sync: configured git for lore data (merge driver, .gitattributes, hooks in "

// TestFreshCloneWiringNotice (E-GAPS-1 G3): the first lore command in a
// teammate's fresh clone wires git and says so once on stderr, naming the
// hooks folder; the second command says nothing (the clone is wired
// already). With LORE_SYNC_QUIET=1 the clone is wired just the same but
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
	if code != 0 {
		t.Fatalf("first command in the fresh clone: exit %d %s", code, out)
	}
	hooks := filepath.Join(b, ".git", "hooks")
	if !strings.Contains(out, wiredNotice+hooks+")\n") {
		t.Fatalf("first command in a fresh clone does not print %q naming %s:\n%s", wiredNotice, hooks, out)
	}
	if strings.Count(out, wiredNotice) != 1 {
		t.Fatalf("the wiring notice is printed more than once:\n%s", out)
	}
	if !strings.Contains(driver(b), "merge-driver") {
		t.Fatal("the first command did not wire merge.lore.driver")
	}
	if code, out = w.loreOut(t, b, "task", "list"); code != 0 || strings.Contains(out, wiredNotice) {
		t.Fatalf("second command in the wired clone: exit %d, must not repeat the notice:\n%s", code, out)
	}

	c := filepath.Join(filepath.Dir(a), "c")
	w.git(t, filepath.Dir(a), "clone", "-q", filepath.Join(filepath.Dir(a), "remote.git"), c)
	w.set(hosthooks.EnvSyncQuiet, "1")
	if code, out = w.loreOut(t, c, "task", "list"); code != 0 || strings.Contains(out, "lore sync:") {
		t.Fatalf("LORE_SYNC_QUIET=1 first command in a fresh clone: exit %d, must print no lore sync: line:\n%s", code, out)
	}
	if !strings.Contains(driver(c), "merge-driver") {
		t.Fatal("LORE_SYNC_QUIET=1 must still wire the clone (merge.lore.driver unset)")
	}
}
