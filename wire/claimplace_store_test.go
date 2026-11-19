package wire

import (
	"strings"
	"testing"
)

// TestClaimPlaceNamesTheStore is devco:D-560: a linked repo's claim line
// named only "refs/lore/signals/main", a ref that exists neither in the code
// repo nor as a branch of the store clone. It now names the store folder as
// <org>/<folder> and whether the claim reached the store's remote.
func TestClaimPlaceNamesTheStore(t *testing.T) {
	const ref = "refs/lore/signals/main"
	pushed := (&coordWorld{spot: sigSpot{store: true, label: "acme/web"}, autopush: true}).claimPlace(ref)
	if !strings.Contains(pushed, "the signals ref "+ref+" of store acme/web") || !strings.Contains(pushed, "pushed to the store's remote now") {
		t.Fatalf("pushed claim place = %q", pushed)
	}
	// devco:D-580: a store signals write is pushed or refused, never kept
	// locally, so no wording may promise a local-only claim.
	off := (&coordWorld{spot: sigSpot{store: true, label: "acme/web"}}).claimPlace(ref)
	if strings.Contains(off, "local store clone") || strings.Contains(off, "until `lore sync`") {
		t.Fatalf("claim place promises a local-only store claim: %q", off)
	}
}

// TestStorePushLabelIsOrgFolder is devco:D-566: a row written in one folder
// of an org store is "committed locally to store <org>/<folder>", the label
// search and sync print, never the bare attached name.
func TestStorePushLabelIsOrgFolder(t *testing.T) {
	var notes strings.Builder
	b := &storePush{name: "acme", own: "acme/web", notes: &notes}
	b.sayLocal()
	if got := notes.String(); got != "lore: committed locally to store acme/web; lore sync sends it\n" {
		t.Fatalf("note = %q", got)
	}
	if got := (&storePush{name: "kb"}).label(); got != "kb" {
		t.Fatalf("a store with no own name keeps its attached name, got %q", got)
	}
}
