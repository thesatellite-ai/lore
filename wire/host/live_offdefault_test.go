package host

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
)

// TestLandLines_OffDefaultBranch (owner decision 2026-11-08): landed rows
// are read through only on the default branch, so a pass that lands or
// lets rows leave live while HEAD is elsewhere says when they show here;
// on the default branch, or when nothing landed or left, it says nothing.
func TestLandLines_OffDefaultBranch(t *testing.T) {
	t.Parallel()
	const note = "so those rows show here after you merge trunk"
	for _, tc := range []struct {
		name     string
		out      landOutcome
		want     string
		wantNone bool
	}{
		{"landed on a feature branch", landOutcome{Status: live.LandDone, OffDefault: true, Checkout: "feature"}, "landed on trunk; this checkout is on feature, " + note, false},
		{"left live on a feature branch", landOutcome{Left: 2, OffDefault: true, Checkout: "feature"}, "this checkout is on feature, " + note, false},
		{"landed on a detached HEAD", landOutcome{Status: live.LandDone, OffDefault: true}, "this checkout is on a detached HEAD, " + note, false},
		{"landed on the default branch", landOutcome{Status: live.LandDone, Checkout: "trunk"}, "", true},
		{"protected on a feature branch", landOutcome{Status: live.LandProtected, OffDefault: true, Checkout: "feature"}, "", true},
		{"offline on a feature branch", landOutcome{Status: live.LandOffline, OffDefault: true, Checkout: "feature"}, "", true},
		{"nothing on a feature branch", landOutcome{Status: live.LandNothing, OffDefault: true, Checkout: "feature"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.out.Branch, tc.out.Commit = "trunk", "0123456789abcdef"
			got := strings.Join(landLines(tc.out), "\n")
			if tc.wantNone {
				if strings.Contains(got, "this checkout is") {
					t.Fatalf("%q: want no branch note", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("%q: want %q", got, tc.want)
			}
		})
	}
}
