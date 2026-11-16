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

// TestLensStoreSourceReadsOnlyItsOwnName pins devco D-473: a store's cache
// file is one per place on the machine, filled by every open under the
// name it gives the place, so a lens with one source over it (`sql --store
// acme` after reads from a repo and a store clone) must keep only the rows
// applied under its own name; the repo's own cache needs no filter.
func TestLensStoreSourceReadsOnlyItsOwnName(t *testing.T) {
	srcs := []sqlite.LensSource{
		{Name: "repo", Path: "/c/repo.db"},
		{Name: "acme/sync", Path: "/c/sync.db"},
	}
	markShared(srcs)
	if srcs[0].Shared {
		t.Errorf("the repo's own cache is filtered by name: %+v", srcs[0])
	}
	if !srcs[1].Shared {
		t.Errorf("a store source alone on its cache file reads every open's rows (each row once per name): %+v", srcs[1])
	}
}
