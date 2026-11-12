package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestWaitingNamedOncePerClone pins B2-5: a store and its folder share one
// clone, so one read names the clone's waiting commits once, after the
// store; the next request names them again.
func TestWaitingNamedOncePerClone(t *testing.T) {
	t.Parallel()
	var w waitedClones
	if !w.first("/clones/agency", 1) {
		t.Fatal("the first folder of the clone must name the commits")
	}
	if w.first("/clones/agency", 1) {
		t.Fatal("the second folder of the same clone named them again")
	}
	if !w.first("/clones/other", 1) {
		t.Fatal("another clone must name its own commits")
	}
	if !w.first("/clones/agency", 2) {
		t.Fatal("the next request must name them again")
	}
	if got := cloneStoreName(store.Source("agency/org")); got != "agency" {
		t.Fatalf("clone name of agency/org = %q", got)
	}
	if got := cloneStoreName(store.Source("me")); got != "me" {
		t.Fatalf("clone name of me = %q", got)
	}
}

// TestSyncStatusCountsAFolderCloneOnce pins B2-5 for sync status: the
// second store of one clone says where its commits are counted.
func TestSyncStatusCountsAFolderCloneOnce(t *testing.T) {
	t.Parallel()
	if got := sameCloneText("agency"); got != "same clone as agency (its commits are counted there)" {
		t.Fatalf("line = %q", got)
	}
}
