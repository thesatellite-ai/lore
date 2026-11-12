package app

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// TestReadOnlyCommandWiresGit: owner bug 6 (2026-11-12) replaced
// OWNERQ-9 / G-DECIDE-9 (a): a read never wires git, read-only mode or
// not; an explicit `lore sync` or `lore doctor --fix`, or a write, does.
func TestReadOnlyCommandWiresGit(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	root := w.initRepo(t) // runs `lore init`
	w.git(t, root, "config", "--local", "--remove-section", "merge.lore")
	driver := func() string {
		out, _ := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: root, Args: []string{"config", "--local", "--get", "merge.lore.driver"}}) // unset = exit 1: an empty driver
		return strings.TrimSpace(string(out))
	}
	if driver() != "" {
		t.Fatal("setup: merge.lore.driver still set")
	}
	for _, args := range [][]string{{"task", "list"}, {"memory", "list"}, {"doctor"}} {
		if code, out, errOut := w.lore(t, root, args...); code != 0 && args[0] != "doctor" {
			t.Fatalf("lore %v: exit %d\n%s\n%s", args, code, out, errOut)
		}
		if driver() != "" {
			t.Fatalf("the read lore %v set merge.lore.driver", args)
		}
	}
	for _, args := range [][]string{{"sync"}, {"doctor", "--fix"}, {"memory", "add", "--body", "the widget cache warms at boot"}} {
		if code, out, errOut := w.lore(t, root, args...); code != 0 && args[0] != "doctor" {
			t.Fatalf("lore %v: exit %d\n%s\n%s", args, code, out, errOut)
		}
		if !strings.Contains(driver(), "merge-driver") {
			t.Fatalf("lore %v left merge.lore.driver unset", args)
		}
		w.git(t, root, "config", "--local", "--remove-section", "merge.lore")
	}
}
