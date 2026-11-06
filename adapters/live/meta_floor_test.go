package live

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
	"github.com/thesatellite-ai/lore/logic/canonjson"
)

// F-COMPAT-LIVE-1 (FORMAT_EVOLUTION rule 2): a live write of a 0.1.18 row
// raises _live/meta.json in the same commit, so a lore below the floor
// (v0.1.17) is refused instead of rewriting the row without its new fields.

const (
	floorTaskID = "tsk_01936f3300007d4e8f5a6b7c8d9e0f1a"
	floorParent = "tsk_01936f3300007d4e8f5a6b7c8d9e0f1b"
)

// floorTask is a task body in row format ver ("v2" or "v1"), with a
// parent_id when withParent is set.
func floorTask(t *testing.T, ver string, withParent bool) File {
	t.Helper()
	ts := "2026-11-05T10:00:00.000Z"
	head := `"_v":1,`
	if ver == "v2" {
		head = `"_v":2,"_hlc":"` + ts + `/0000/` + unkActor + `",`
	}
	doc := `{` + head + `"_table":"tasks","id":"` + floorTaskID + `","title":"t","updated_at":"` + ts + `","updated_by":"` + unkActor + `"`
	if withParent {
		doc += `,"parent_id":"` + floorParent + `"`
	}
	return fileWith(t, doc+`}`)
}

func decodeMeta(t *testing.T, b []byte) Meta {
	t.Helper()
	m, ok, err := (Tree{MetaPath: b}).Meta()
	if err != nil || !ok {
		t.Fatalf("meta: ok=%v err=%v", ok, err)
	}
	return m
}

func TestRowFeatures(t *testing.T) {
	cases := []struct {
		name string
		f    File
		want []string
	}{
		{"v2 plain task", floorTask(t, "v2", false), []string{"hlc"}},
		{"v2 task with parent", floorTask(t, "v2", true), []string{"hlc", "task_tree"}},
		{"v1 task carried as it is", floorTask(t, "v1", true), nil},
		{"not a row document", fileWith(t, `{"id":"tsk_1","title":"x"}`), nil},
	}
	for _, c := range cases {
		if got := rowFeatures([]File{c.f}); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRaiseMetaFile(t *testing.T) {
	// No meta yet: DefaultMeta plus the needed features.
	b, changed, err := raiseMetaFile(nil, []string{"hlc"})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if m := decodeMeta(t, b); m.MinLoreVersion != "0.1.18" || !slices.Equal(m.Features, []string{"hlc", "live"}) {
		t.Fatalf("new meta: %+v", m)
	}
	// Already declared: unchanged bytes.
	again, changed, err := raiseMetaFile(b, []string{"hlc"})
	if err != nil || changed || string(again) != string(b) {
		t.Fatalf("a second raise changes nothing: changed=%v err=%v", changed, err)
	}
	// Never lowered, unknown keys kept.
	high := []byte(`{"features":["live"],"min_lore_version":"0.2.0","zz_team":"x"}`)
	out, changed, err := raiseMetaFile(high, []string{"task_tree"})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	m := decodeMeta(t, out)
	if m.MinLoreVersion != "0.2.0" || !slices.Equal(m.Features, []string{"live", "task_tree"}) {
		t.Fatalf("raise keeps the higher floor: %+v", m)
	}
	obj, _ := canonjson.Decode(out, canonjson.DefaultMaxDepth)
	if obj.Str("zz_team") != "x" {
		t.Fatalf("unknown key dropped: %s", out)
	}
}

func TestMergeMetaTakesHigherFloorAndUnion(t *testing.T) {
	raised := []byte(`{"features":["hlc","live","task_tree"],"min_lore_version":"0.1.18"}`)
	plain := []byte(`{"features":["live","signals"],"min_lore_version":"0.1.0","zz":"k"}`)
	base := []byte(`{"features":["live"],"min_lore_version":"0.1.0"}`)
	for _, sides := range [][2][]byte{{raised, plain}, {plain, raised}} {
		out, _, err := MergeTrees(Tree{MetaPath: base}, Tree{MetaPath: sides[0]}, Tree{MetaPath: sides[1]}, nil)
		if err != nil {
			t.Fatal(err)
		}
		m := decodeMeta(t, out[MetaPath])
		if m.MinLoreVersion != "0.1.18" || !slices.Equal(m.Features, []string{"hlc", "live", "signals", "task_tree"}) {
			t.Fatalf("merged meta: %+v", m)
		}
		obj, _ := canonjson.Decode(out[MetaPath], canonjson.DefaultMaxDepth)
		if obj.Str("zz") != "k" {
			t.Fatalf("unknown key dropped in merge: %s", out[MetaPath])
		}
	}
	// Version-aware: 0.1.19 beats 0.1.9.
	a := []byte(`{"features":["live"],"min_lore_version":"0.1.9"}`)
	c := []byte(`{"features":["live"],"min_lore_version":"0.1.19"}`)
	out, _, err := MergeTrees(Tree{}, Tree{MetaPath: c}, Tree{MetaPath: a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeMeta(t, out[MetaPath]); m.MinLoreVersion != "0.1.19" {
		t.Fatalf("string max instead of version max: %+v", m)
	}
}

// The write itself: the commit that adds a v2 row with a parent also
// raises the meta, a v0.1.17 binary is then refused, and this binary (and
// a dev build) still writes.
func TestLiveWriteRaisesFloor(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	ctx := context.Background()
	if _, err := r.Write(ctx, WriteRequest{Folder: RootFolder, BinaryVersion: "0.1.17",
		Rows: put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "a"))}); err != nil {
		t.Fatalf("a v0.1.17-shaped row (no _v) raises nothing: %v", err)
	}
	f := floorTask(t, "v2", true)
	res, err := r.Write(ctx, WriteRequest{Folder: RootFolder, BinaryVersion: "0.1.18",
		Rows: map[RowKey]File{{Table: "tasks", ID: floorTaskID}: f}})
	if err != nil || !res.Changed {
		t.Fatalf("write: %v", err)
	}
	_, tree, err := r.LocalTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMeta(t, tree[MetaPath])
	if m.MinLoreVersion != "0.1.18" || !slices.Contains(m.Features, "task_tree") {
		t.Fatalf("the row's commit raises the meta: %+v", m)
	}
	// v0.1.17's own check is a plain VersionBelow on min_lore_version (its
	// live/write.go checkFloor); this binary's check lifts any version it
	// is given to the features it knows (FMT-EFF), so it is asked here.
	if !VersionBelow("0.1.17", m.MinLoreVersion) {
		t.Fatalf("v0.1.17 is not below the raised floor %q", m.MinLoreVersion)
	}
	for _, v := range []string{"0.1.18", "0.1.0-dev", "dev", "0.1.17-34-gabc"} {
		if err := r.CheckFloor(ctx, v); err != nil {
			t.Fatalf("%s must still write: %v", v, err)
		}
	}
}
