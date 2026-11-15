package app

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// TestRefusedWriteSaysOnlyTheError (devco:S-11): a refused write prints
// its error as the first line. In a fresh clone the first write sets git
// up before it runs; refused, it used to print the set-up line and "the
// command was refused, so what it changed first is undone (…)" before the
// error, and in a clone already wired the undo line alone (its wiring
// stamp). Now the set-up line is said only when the write goes through,
// the undone set-up says nothing, and the clone is left as it was.
func TestRefusedWriteSaysOnlyTheError(t *testing.T) {
	t.Parallel()
	w, _, b := twoLiveClones(t)
	driver := func(dir string) string {
		out, _ := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: dir, Args: []string{"config", "--local", "--get", "merge.lore.driver"}})
		return strings.TrimSpace(string(out))
	}
	key := "AKIA" + strings.Repeat("Q", 16) // made-up, built at run time
	firstLine := func(out string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.TrimSpace(l) != "" {
				return l
			}
		}
		return ""
	}
	for i := 0; i < 2; i++ { // every refused write, not only the first
		code, out := w.loreOut(t, b, "memory", "add", "--body", "deploy key "+key)
		if code == 0 {
			t.Fatalf("a secret in memory add was not refused:\n%s", out)
		}
		if l := firstLine(out); !strings.HasPrefix(l, "ERROR: ") || strings.Contains(out, "set up git") || strings.Contains(out, "is undone") {
			t.Fatalf("refused write %d in a fresh clone: the error is not the first and only news:\n%s", i+1, out)
		}
		if driver(b) != "" {
			t.Fatal("a refused write left merge.lore.driver set up")
		}
	}
	code, out := w.loreOut(t, b, "memory", "add", "--body", "retry the flaky job once", "--yes")
	if code != 0 || strings.Count(out, "set up git for lore's row files") != 1 || driver(b) == "" {
		t.Fatalf("the first write that goes through must set git up and say so once: exit %d\n%s", code, out)
	}
	code, out = w.loreOut(t, b, "memory", "add", "--body", "deploy key "+key)
	if code == 0 || !strings.HasPrefix(firstLine(out), "ERROR: ") || strings.Contains(out, "is undone") {
		t.Fatalf("refused write in a wired clone: exit %d, want only the error:\n%s", code, out)
	}
}
