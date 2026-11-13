package host

import "testing"

// parseTreeListing reads `git ls-tree -r` lines (paths may hold spaces)
// and skips anything else.
func TestParseTreeListing(t *testing.T) {
	t.Parallel()
	raw := []byte("100644 blob 1111111111111111111111111111111111111111\t.lore/data/tasks/tsk_a.json\n" +
		"100644 blob 2222222222222222222222222222222222222222\t.lore/data/notes/a b.json\n" +
		"not a listing line\n\n")
	got := parseTreeListing(raw)
	want := map[string]string{
		".lore/data/tasks/tsk_a.json": "1111111111111111111111111111111111111111",
		".lore/data/notes/a b.json":   "2222222222222222222222222222222222222222",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for p, oid := range want {
		if got[p] != oid {
			t.Fatalf("%s: got %q, want %q", p, got[p], oid)
		}
	}
}
