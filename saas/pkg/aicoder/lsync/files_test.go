package lsync

import (
	"strings"
	"testing"
)

func TestSamplePaths(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry()
	got := SamplePaths(reg)
	if len(got) != len(reg.Names())+2 || got[0] != MetaFileName || got[1] != PurgedFileName {
		t.Fatalf("want both control files then one per table: %v", got)
	}
	seen := map[string]bool{}
	for _, p := range got[2:] {
		table := strings.SplitN(p, "/", 2)[0]
		if _, ok := reg.Table(table); !ok || seen[table] {
			t.Fatalf("unexpected or repeated table folder %q", p)
		}
		seen[table] = true
		// The sample name must never be mistaken for a real row.
		if _, _, ok := parseRelPath(p); ok {
			t.Fatalf("%s parses as a row file", p)
		}
	}
}
