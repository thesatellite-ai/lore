package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
)

// TestLensOneSourcePerPlace (devco D-443): two names on one cache file
// (an org store and a link over the same folder, two stores.yaml entries)
// are one source, the first name, still filtered by its own name; the
// combined and deleted files are never dropped.
func TestLensOneSourcePerPlace(t *testing.T) {
	srcs := []sqlite.LensSource{
		{Name: "repo", Path: "/c/repo.db"},
		{Name: "acme", Path: "/c/sync.db"},
		{Name: "acme/org", Path: "/c/org.db"},
		{Name: "whole/sync", Path: "/c/sync.db"},
		{Name: "deleted", Path: "/c/sync.db", Deleted: true},
	}
	markShared(srcs)
	got := onePerPlace(srcs)
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if want := "repo,acme,acme/org,deleted"; joinNames(names) != want {
		t.Fatalf("sources %v, want %s", names, want)
	}
	if !got[1].Shared {
		t.Errorf("the kept name over a shared file reads every name's rows: %+v", got[1])
	}
}

func joinNames(n []string) string {
	out := ""
	for i, s := range n {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
