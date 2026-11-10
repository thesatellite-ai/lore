package meta

import (
	"slices"
	"testing"
)

// TestNearest pins P2-META-7: same stem after dropping _ and a common
// suffix, or edit distance ≤2 (≤1 when either name has ≤4 chars), at most
// n names, closest first, ties by name, silent when nothing is close.
func TestNearest(t *testing.T) {
	cases := []struct {
		name     string
		existing []string
		n        int
		want     []string
	}{
		{"clients", []string{"client"}, 3, []string{"client"}},                                                                 // plural suffix
		{"client_id", []string{"client"}, 3, []string{"client"}},                                                               // _id suffix
		{"clientid", []string{"client_ids"}, 3, []string{"client_ids"}},                                                        // _ids + underscores dropped
		{"cost_hours", []string{"costhours"}, 3, []string{"costhours"}},                                                        // only '_' differs
		{"env", []string{"dns"}, 3, nil},                                                                                       // short names: distance 2 is not close
		{"env", []string{"envs"}, 3, []string{"envs"}},                                                                         // short but same stem
		{"env", []string{"enx"}, 3, []string{"enx"}},                                                                           // short, distance 1
		{"region", []string{"regoin"}, 3, []string{"regoin"}},                                                                  // transposition = distance 2
		{"region", []string{"rgn"}, 3, nil},                                                                                    // other is short: limit 1
		{"deploy", []string{"billing", "owner"}, 3, nil},                                                                       // nothing close: silent
		{"client", []string{"client"}, 3, nil},                                                                                 // the name itself is excluded
		{"client", []string{"CLIENT_ID"}, 3, []string{"CLIENT_ID"}},                                                            // case-insensitive
		{"client", []string{"clients", "client_no", "client_id", "clientx"}, 3, []string{"client_id", "client_no", "clients"}}, // ties by name, max 3, distance-1 last
		{"client", []string{"clientx", "clients"}, 1, []string{"clients"}},                                                     // stem match beats distance 1
		{"client", []string{"client_id", "client_id"}, 3, []string{"client_id"}},                                               // duplicates once
		{"", []string{"a"}, 3, []string{"a"}},                                                                                  // empty name: short, distance 1
		{"kunde", []string{"kündee"}, 3, nil},                                                                                  // non-ascii counts bytes; not close
		{"client", nil, 3, nil},
		{"client", []string{"clients"}, 0, nil},
	}
	for _, c := range cases {
		got := Nearest(c.name, c.existing, c.n)
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("Nearest(%q, %v, %d) = %v, want %v", c.name, c.existing, c.n, got, c.want)
		}
	}
}
