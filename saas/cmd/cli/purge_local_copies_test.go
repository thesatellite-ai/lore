package main

import (
	"slices"
	"testing"

	"saas/pkg/aicoder/lsync"

	"github.com/thesatellite-ai/lore/model/ids"
)

// TestPurgedThisRun pins PASS3-A-4's per-command record: ids a purge
// noted are taken once per project root (a second take is empty, so
// doctor's own finishSyncSessions call drops nothing twice), and the
// end-of-command report no longer says a purged row is "kept in lore
// trash --local" while other trashed rows still show.
func TestPurgedThisRun(t *testing.T) {
	const purgedID = "mem_01a1abd86b777ae5abae62fa353b06a1"
	const otherID = "mem_01a1abd86b777ae5abae62fa353b06a2"
	id, err := ids.ParseAny(purgedID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { takePurgedThisRun() })
	takePurgedThisRun() // start empty

	rep := lsync.Report{Deleted: []string{"memories/" + purgedID + ".json", "memories/" + otherID + ".json"}}
	if got := withoutPurgedThisRun(rep).Deleted; len(got) != 2 {
		t.Fatalf("nothing purged yet: Deleted = %v, want both", got)
	}

	notePurgedThisRun("/repo", id)
	notePurgedThisRun("/repo", id) // run twice = once
	if got := withoutPurgedThisRun(rep).Deleted; !slices.Equal(got, []string{"memories/" + otherID + ".json"}) {
		t.Fatalf("Deleted = %v, want only the row not purged", got)
	}
	if len(rep.Deleted) != 2 {
		t.Fatalf("the input report was changed: %v", rep.Deleted)
	}

	got := takePurgedThisRun()
	if len(got) != 1 || len(got["/repo"]) != 1 || !got["/repo"][purgedID] {
		t.Fatalf("take = %v, want /repo: {%s}", got, purgedID)
	}
	if again := takePurgedThisRun(); len(again) != 0 {
		t.Fatalf("second take = %v, want empty", again)
	}
}
