package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTrashPurgeReachesTeammatesAsPurged (BUG-LIVE-PURGE-LIST, DQ4): on a
// repo with a live ref, `lore trash purge` lists the id on the live ref's
// own purge list, so after `lore sync` the purging clone and a teammate
// both answer E_PURGED (never E_NOT_FOUND "was deleted (live, …)") to a
// restore and a show of the purged task.
func TestTrashPurgeReachesTeammatesAsPurged(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author()
	a, b := w.livePair(t)

	code, out := w.loreOut(t, a, "task", "add", "doomed")
	id := fenceTaskID.FindString(out)
	if code != 0 || id == "" {
		t.Fatalf("task add: %d %s", code, out)
	}
	if code, out = w.loreOut(t, a, "task", "delete", id, "--yes"); code != 0 {
		t.Fatalf("task delete: %d %s", code, out)
	}
	if code, out = w.loreOut(t, a, "trash", "purge", id, "--confirm"); code != 0 {
		t.Fatalf("trash purge: %d %s", code, out)
	}
	for _, dir := range []string{a, b} {
		if code, out = w.loreOut(t, dir, "sync"); code != 0 {
			t.Fatalf("sync in %s: %d %s", filepath.Base(dir), code, out)
		}
		for _, args := range [][]string{{"trash", "restore", id}, {"task", "show", id}} {
			code, out = w.loreOut(t, dir, args...)
			if code == 0 || !strings.Contains(out, "E_PURGED") || !strings.Contains(out, id) {
				t.Errorf("%s in %s after the purge and a sync: exit %d %s, want E_PURGED", strings.Join(args, " "), filepath.Base(dir), code, out)
			}
		}
	}
}
