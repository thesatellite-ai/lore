package wire

import "testing"

// An untracked .DS_Store (or ._name, Thumbs.db) Finder or Explorer dropped
// into a store clone is not work `lore store forget` would lose: the
// warning counts only real changes.
func TestRealChangesSkipsOSJunk(t *testing.T) {
	cases := []struct {
		name, porcelain string
		want            int
	}{
		{"clean", "", 0},
		{"only junk", "?? .DS_Store\n?? memories/._mem_1.json\n?? \"Thumbs.db\"\n", 0},
		{"a real row and junk", "?? .DS_Store\n M memories/mem_1.json\n", 1},
		{"a rename onto a row", "R  a.json -> memories/mem_2.json\n", 1},
		{"a rename onto junk", "R  a -> ._a\n", 0},
		{"two real changes", " M a.json\n?? b.json", 2},
	}
	for _, c := range cases {
		if got := realChanges(c.porcelain); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
