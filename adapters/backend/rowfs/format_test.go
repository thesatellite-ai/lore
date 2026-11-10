package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestFirstWriteDeclaresFeatures: a write raises features and the floor in
// the same batch (path in a.Paths, undone with it), keeps unknown keys, and
// a second write leaves the file alone.
func TestFirstWriteDeclaresFeatures(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	f := folder(t, storeRoot(t, "sto_01936f0000007a1b8c2d3e4f5a6b7c8d"), "org")
	if err := os.MkdirAll(f.dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	orig := `{"_v":1,"features":["live"],"project_id":"` + prjBase + `","zz_future":{"a":[1,2]}}` + "\n"
	if err := os.WriteFile(filepath.Join(f.dataDir, MetaFileName), []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	a := apply(t, f, store.Change{Row: g.row(newID(t), "x", hlc.HLC{})})
	if !slices.Contains(a.Paths, metaPath) {
		t.Fatal("_meta.json not in the batch")
	}
	d, err := f.Format(context.Background())
	if err != nil || !slices.Equal(d.Features, []string{"hlc", "live"}) || d.MinLoreVersion != "0.1.18" {
		t.Fatalf("declared %+v %v", d, err)
	}
	doc, _ := readMeta(t, f)
	if !strings.Contains(doc, `"zz_future"`) {
		t.Fatalf("unknown key dropped: %s", doc)
	}
	if b := apply(t, f, store.Change{Row: g.row(newID(t), "y", hlc.HLC{})}); slices.Contains(b.Paths, metaPath) {
		t.Fatal("a second write rewrote _meta.json")
	}
	if err := a.Undo(); err != nil {
		t.Fatal(err)
	}
}

// TestUnreadableMetaFailsClosed: a _meta.json that does not parse refuses
// the write with E_OLD_LORE and is never replaced.
func TestUnreadableMetaFailsClosed(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	for name, bad := range map[string]string{"markers": "<<<<<<< ours\n{}\n", "features not a list": `{"features":"hlc"}`} {
		f := folder(t, storeRoot(t, "sto_01936f0000007a1b8c2d3e4f5a6b7c8d"), "org")
		if err := os.MkdirAll(f.dataDir, 0o750); err != nil {
			t.Fatal(err)
		}
		full := filepath.Join(f.dataDir, MetaFileName)
		if err := os.WriteFile(full, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := f.Apply(context.Background(), []store.Change{{Row: g.row(newID(t), "x", hlc.HLC{})}})
		if !errs.Has(err, errs.OldLore) {
			t.Fatalf("%s: err %v", name, err)
		}
		if got, _ := os.ReadFile(filepath.Clean(full)); string(got) != bad {
			t.Fatalf("%s: file replaced: %s", name, got)
		}
		if _, ferr := f.Format(context.Background()); !errs.Has(ferr, errs.OldLore) {
			t.Fatalf("%s: Format err %v", name, ferr)
		}
	}
}

// TestMergeFeaturesAndFloor: both sides changed the declaration — the
// features are a set and the floor the higher version (0.1.19 > 0.1.9).
func TestMergeFeaturesAndFloor(t *testing.T) {
	t.Parallel()
	base := `{"_v":1,"project_id":"` + prjBase + `"}`
	ours := `{"_v":1,"features":["hlc","meta"],"min_lore_version":"0.1.9","project_id":"` + prjBase + `"}`
	theirs := `{"_v":1,"features":["hlc","live"],"min_lore_version":"0.1.19","project_id":"` + prjBase + `"}`
	out, err := MergeFolderFile(metaPath, []byte(base), []byte(ours), []byte(theirs))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"0.1.19"`) || !strings.Contains(s, "\"hlc\",\n    \"live\",\n    \"meta\"") {
		t.Fatalf("merged %s", s)
	}
}
