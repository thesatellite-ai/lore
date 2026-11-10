package procwalk

import "testing"

// TestParentOlderBy is the 10.7 pid-reuse decision over fake creation
// times: an older or same-time parent keeps the link, a newer parent (the
// pid was reused) cuts it, and an unreadable time on either side keeps it.
func TestParentOlderBy(t *testing.T) {
	const child, parent = 100, 50
	type at struct {
		t  int64
		ok bool
	}
	cases := []struct {
		name         string
		childAt      at
		parentAt     at
		wantKeepLink bool
	}{
		{"parent older", at{200, true}, at{100, true}, true},
		{"parent created the same tick", at{200, true}, at{200, true}, true},
		{"parent newer: pid reused", at{200, true}, at{201, true}, false},
		{"parent much newer", at{1, true}, at{1 << 60, true}, false},
		{"child time unreadable", at{0, false}, at{999, true}, true},
		{"parent time unreadable", at{200, true}, at{0, false}, true},
		{"both unreadable", at{0, false}, at{0, false}, true},
		{"zero times are real times", at{0, true}, at{0, true}, true},
		{"negative parent time is older", at{0, true}, at{-5, true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lookup := func(pid int) (int64, bool) {
				switch pid {
				case child:
					return c.childAt.t, c.childAt.ok
				case parent:
					return c.parentAt.t, c.parentAt.ok
				default:
					t.Fatalf("lookup of unexpected pid %d", pid)
					return 0, false
				}
			}
			if got := parentOlderBy(lookup, child, parent); got != c.wantKeepLink {
				t.Fatalf("parentOlderBy = %v, want %v", got, c.wantKeepLink)
			}
		})
	}
}
