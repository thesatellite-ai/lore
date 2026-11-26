package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
)

// devco:D-1010: a floor commit left in the clone names the store push that
// sends it with the store as the org lists it; the normalised url without
// a scheme is "not a git store" to lore store push.
func TestStoreFloorPushNoteNamesARunnableStorePush(t *testing.T) {
	const url = "https://github.com/acme/team"
	got := storeFloorPushNote(orgcfg.StoreFloorResult{StoreURL: "github.com/acme/team", NoPush: true}, url)
	if !strings.Contains(got, "lore store push --store "+url+",") {
		t.Fatalf("the note must name lore store push --store %s, got %q", url, got)
	}
}
