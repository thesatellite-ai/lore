package tagref_test

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/internal/tagref"
)

// TestFindDroppableIndex: a tag name in two stores is two rows (natural
// ids are per store) and Find lists both, nearest store first; the repo
// store's row keeps v0.1.17's id (Legacy); Droppable adds the legacy id
// even once the tag row is gone; Index.Has matches any live row of the
// name or the legacy id, per project, and an unknown name or project
// never matches.
func TestFindDroppableIndex(t *testing.T) {
	ctx := context.Background()
	h := opstest.WithStores(t, []string{"agency"})
	repoTag := h.ID(h.Must("tag.add", `{"name":"cert_rotation"}`))
	agencyTag := h.ID(h.Must("tag.add", `{"name":"cert_rotation","to":"agency"}`))
	if repoTag == agencyTag {
		t.Fatal("the same name in two stores got one id")
	}
	legacy, err := tagref.Legacy(h.Project, "cert_rotation")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.String() != repoTag {
		t.Fatalf("repo store tag %s, v0.1.17 id %s", repoTag, legacy)
	}
	found, err := tagref.Find(ctx, h.App, h.Project, "cert_rotation")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].String() != repoTag || found[1].String() != agencyTag {
		t.Fatalf("Find = %v, want [repo %s, agency %s]", found, repoTag, agencyTag)
	}
	if none, _ := tagref.Find(ctx, h.App, h.Project, "nope"); len(none) != 0 {
		t.Fatalf("unknown name found %v", none)
	}
	drop, err := tagref.Droppable(ctx, h.App, h.Project, "gone_tag")
	if err != nil {
		t.Fatal(err)
	}
	gone, _ := tagref.Legacy(h.Project, "gone_tag")
	if len(drop) != 1 || drop[0] != gone {
		t.Fatalf("Droppable of a deleted tag = %v, want its legacy id", drop)
	}
	if both, _ := tagref.Droppable(ctx, h.App, h.Project, "cert_rotation"); len(both) != 2 || !slices.Contains(both, legacy) {
		t.Fatalf("Droppable = %v: legacy id not repeated, both rows kept", both)
	}

	idx, err := tagref.Load(ctx, h.App)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		have          []string
		project, name string
		want          bool
	}{
		{[]string{agencyTag}, h.Project, "cert_rotation", true},
		{[]string{repoTag}, h.Project, "cert_rotation", true},
		{[]string{gone.String()}, h.Project, "gone_tag", true}, // legacy id of a deleted tag
		{[]string{agencyTag}, h.Project, "other", false},
		{[]string{agencyTag}, "prj_01a1a5aca7f57659be7320c5f4b70000", "cert_rotation", false},
		{nil, h.Project, "cert_rotation", false},
	} {
		if got := idx.Has(c.have, c.project, c.name); got != c.want {
			t.Errorf("Has(%v, %s, %s) = %v, want %v", c.have, c.project, c.name, got, c.want)
		}
	}
}
