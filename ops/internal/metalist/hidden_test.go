package metalist

import "testing"

// TestHiddenNamesFolders: a row out of scope because of its dir: scope is
// counted as scoped to a folder and named (bug hunt B3-10), never as
// "scoped to a repo".
func TestHiddenNamesFolders(t *testing.T) {
	cases := []struct {
		dirs []string
		want string
	}{
		{nil, ""},
		{[]string{""}, "1 hidden (1 scoped to a repo); --all shows them"},
		{[]string{"src/booking"}, "1 hidden (1 scoped to a folder (dir:src/booking)); --all shows them"},
		{[]string{"a", "", "a", "b"}, "4 hidden (1 scoped to a repo, 3 scoped to folders (dir:a, dir:b)); --all shows them"},
		{[]string{"a", "b", "c", "d"}, "4 hidden (4 scoped to folders (dir:a, dir:b, dir:c, …)); --all shows them"},
	}
	for _, c := range cases {
		var h Hidden
		for _, d := range c.dirs {
			h.OutOfScope(d)
		}
		if got := h.Note(); got != c.want {
			t.Errorf("dirs %q: got %q, want %q", c.dirs, got, c.want)
		}
	}
}
