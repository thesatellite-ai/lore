package rowfs

import (
	"strings"
	"testing"
)

// TestLayoutChangeSummary: every combination of moved rows, README and
// marker reads as what changed, and no summary holds a zero count.
func TestLayoutChangeSummary(t *testing.T) {
	cases := []struct {
		c    LayoutChange
		want string
	}{
		{LayoutChange{Readme: ReadmeRefreshed, Marker: 4}, "README refreshed, marker lore_store: 4"},
		{LayoutChange{Readme: ReadmeAdded, Marker: 4}, "README added, marker lore_store: 4"},
		{LayoutChange{Marker: 4}, "marker lore_store: 4"},
		{LayoutChange{Readme: ReadmeRefreshed}, "README refreshed"},
		{LayoutChange{Moved: 12, Readme: ReadmeRefreshed, Marker: 4}, "12 rows moved to _store/, README refreshed, marker lore_store: 4"},
		{LayoutChange{Moved: 12, Marker: 4}, "12 rows moved to _store/, marker lore_store: 4"},
		{LayoutChange{Moved: 1, Readme: ReadmeRefreshed}, "1 row moved to _store/, README refreshed"},
		{LayoutChange{Moved: 12}, "12 rows moved to _store/"},
		{LayoutChange{Moved: 3, FromOld: true, Folders: []string{".", "team"}, Readme: ReadmeAdded, Marker: 4}, "3 rows moved from <folder>/.lore/data/ to <folder>/, the store's own rows to _store/ (folders: ., team), README added, marker lore_store: 4"},
		{LayoutChange{Untracked: 1, Marker: 4}, "1 lore state file no longer tracked, marker lore_store: 4"},
		{LayoutChange{}, "nothing changed"},
	}
	for _, c := range cases {
		got := c.c.Summary()
		if got != c.want {
			t.Errorf("%+v: %q, want %q", c.c, got, c.want)
		}
		if strings.HasPrefix(got, "0 ") || strings.Contains(got, " 0 ") {
			t.Errorf("%+v: zero count in %q", c.c, got)
		}
	}
	if !(LayoutChange{}).Empty() || (LayoutChange{Marker: 4}).Empty() {
		t.Error("Empty")
	}
}
