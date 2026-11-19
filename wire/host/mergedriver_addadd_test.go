package host

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/merge"
)

// TestMergeDriverAddAddNewerWins (devco O-10): a force-pushed branch
// brings back a row both sides hold with no common copy (git's add/add).
// The driver wrote conflict markers into the task's title and every read
// showed them. Now the newer edit of each text field wins by HLC, the
// replaced value is named on stderr, and the merge is clean.
func TestMergeDriverAddAddNewerWins(t *testing.T) {
	const rowPath = ".lore/data/memories/mem_01936f3300007d4e8f5a6b7c8d9e0f1a.json"
	for _, swap := range []bool{false, true} {
		older, newer := memRow(t, 5, mdActorA, "core", "first body"), memRow(t, 6, mdActorA, "core", "first body (rewritten)")
		ours, theirs := older, newer
		if swap {
			ours, theirs = newer, older
		}
		out, stderr, err := runDriver(t, rowPath, nil, ours, theirs)
		if err != nil {
			t.Fatalf("swap=%v: add/add must merge cleanly, got %v (%s)", swap, err, stderr)
		}
		_, b := decodeMem(t, out)
		if merge.HasMarkers(b) || b != "first body (rewritten)" {
			t.Fatalf("swap=%v: body = %q, want the newer edit and no markers", swap, b)
		}
		if !strings.Contains(stderr, "kept the newer edit (replaced body \"first body\")") {
			t.Fatalf("swap=%v: stderr must name the replaced value: %q", swap, stderr)
		}
	}
}
