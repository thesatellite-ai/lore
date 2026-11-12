package app

// storeusedefault_test.go — docs bug B4 (plain files round, 2026-11-12):
// `lore store use --default <store>` goes back to lore's own clone and
// says the person's clone is "left as it is", but it wrote lore's git
// identity, merge driver and pre-commit block into that clone. Neither
// `store use <path>` nor `store use --default` may touch the person's
// clone: its .git/config, hooks and info (attributes, exclude) stay byte
// for byte as they were.

import (
	"strings"
	"testing"
)

func TestStoreUseDefaultLeavesOwnCloneAlone(t *testing.T) {
	t.Parallel()
	w, repo, _, bare, _ := pfStoreWorld(t)
	mine := w.path("code-notes")
	_ = bare
	w.git(t, w.root, "clone", "-q", "https://github.com/acme/notes.git", mine) // origin: the store url (url.insteadOf reaches the bare repo)
	snap := func() string { return pfSnapshot(t, mine) }                       // .git too: config, hooks, info
	fresh := snap()
	for _, args := range [][]string{
		{"store", "use", mine, "--yes"},
		{"memory", "list", "--store", "notes"}, // a read through the person's clone
		{"store", "use", "--default", "notes", "--yes"},
	} {
		code, out, errOut := w.lore(t, repo, args...)
		if code != 0 {
			t.Fatalf("lore %v: exit %d\n%s\n%s", args, code, out, errOut)
		}
		if now := snap(); now != fresh {
			t.Fatalf("lore %v wrote into the person's own clone:\n%s\nstderr:\n%s", args, pfDiff(fresh, now), errOut)
		}
		if strings.Contains(errOut, mine) && (strings.Contains(errOut, "set up the store clone") || strings.Contains(errOut, "set user.name")) {
			t.Fatalf("lore %v says it set up the person's own clone:\n%s", args, errOut)
		}
	}
}
