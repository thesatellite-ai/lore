package sqlite

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// conflictText is a field value holding a merge conflict block.
const conflictText = "<<<<<<< ours\nip\n=======\naddr\n>>>>>>> theirs"

// goodFieldNames reads LastGoodKind's field names (ok false: none).
func goodFieldNames(t *testing.T, f *fixture, c *Cache, src store.Source, kind ids.ID[ids.Kind]) (string, []string, bool) {
	t.Helper()
	k, fs, ok, err := c.LastGoodKind(f.ctx, src, kind.String())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return "", nil, false
	}
	var names []string
	for _, fr := range fs {
		names = append(names, fr.Fields["name"].String())
	}
	return k.Fields["name"].String(), names, true
}

// TestKindsGood pins OV35-LASTGOOD (RES 3.13): _kinds_good records a kind
// and its field rows; a kind or field row holding conflict markers keeps
// the old snapshot; the snapshot survives Rebuild; an unseen kind (or one
// seen only in another source) is ok=false.
func TestKindsGood(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	kind, other := must(ids.New[ids.Kind]()), must(ids.New[ids.Kind]())
	ipF, regionF := must(ids.New[ids.KindField]()), must(ids.New[ids.KindField]())

	if _, _, ok := goodFieldNames(t, f, c, store.SourceRepo, kind); ok {
		t.Fatal("unseen kind ok")
	}
	f.apply(c, store.SourceRepo, "1", f.kindRow(kind, "server"),
		f.fieldRow(ipF, kind, "ip", "ip", ids.ID[ids.Kind]{}),
		f.fieldRow(regionF, kind, "region", "text", ids.ID[ids.Kind]{}))
	name, fields, ok := goodFieldNames(t, f, c, store.SourceRepo, kind)
	if !ok || name != "server" || len(fields) != 2 {
		t.Fatalf("snapshot %v %q %v", ok, name, fields)
	}
	if _, _, ok := goodFieldNames(t, f, c, "agency", kind); ok {
		t.Error("kind seen in repo is ok in another source")
	}
	if _, _, ok := goodFieldNames(t, f, c, store.SourceRepo, other); ok {
		t.Error("other kind ok")
	}

	// a field row with conflict markers: the old snapshot stays
	f.apply(c, store.SourceRepo, "2", f.fieldRow(ipF, kind, conflictText, "ip", ids.ID[ids.Kind]{}))
	if _, kept, _ := goodFieldNames(t, f, c, store.SourceRepo, kind); !containsAll(kept, "ip", "region") {
		t.Errorf("field conflict replaced the snapshot: %v", kept)
	}
	// the kind row itself in conflict: still the old snapshot
	f.apply(c, store.SourceRepo, "3", f.kindRow(kind, conflictText))
	if kept, _, _ := goodFieldNames(t, f, c, store.SourceRepo, kind); kept != "server" {
		t.Errorf("kind conflict replaced the snapshot: %q", kept)
	}
	// settled: the new definition becomes the snapshot
	f.apply(c, store.SourceRepo, "4", f.kindRow(kind, "host"), f.fieldRow(ipF, kind, "addr", "ip", ids.ID[ids.Kind]{}))
	name, fields, _ = goodFieldNames(t, f, c, store.SourceRepo, kind)
	if name != "host" || !containsAll(fields, "addr", "region") {
		t.Errorf("settled snapshot %q %v", name, fields)
	}
	// a deleted field row drops out of the next snapshot
	f.apply(c, store.SourceRepo, "5", f.tomb(schema.TableKindFields, regionF.Any()), f.kindRow(kind, "host2"))
	if n2, f2, _ := goodFieldNames(t, f, c, store.SourceRepo, kind); n2 != "host2" || len(f2) != 1 || f2[0] != "addr" {
		t.Errorf("after field delete %q %v", n2, f2)
	}
	// Rebuild keeps the snapshot (3.13: a rebuild must not forget it)
	if err := c.Rebuild(f.ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	if n3, _, ok3 := goodFieldNames(t, f, c, store.SourceRepo, kind); !ok3 || n3 != "host2" {
		t.Errorf("after rebuild %v %q", ok3, n3)
	}
	// a kind tombstone leaves the snapshot alone
	f.apply(c, store.SourceRepo, "6", f.tomb(schema.TableKinds, kind.Any()))
	if _, _, ok := goodFieldNames(t, f, c, store.SourceRepo, kind); !ok {
		t.Error("kind tombstone dropped the snapshot")
	}
}

func containsAll(xs []string, want ...string) bool {
	set := map[string]bool{}
	for _, x := range xs {
		set[x] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return len(xs) == len(want)
}
