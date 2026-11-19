package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestMetaFormatOneShape (devco N-15): an inited and a migrated project
// record format 2 the same way in .lore/data/_meta.json: `"format": 2` and
// `"_v": 2`. Before, lore init wrote only `_v: 2` and a migration wrote
// `_v: 1, format: 2`, so a reader of `_v` alone saw a migrated project as
// format 1. Both shapes are still read as format 2.
func TestMetaFormatOneShape(t *testing.T) {
	t.Parallel()
	ctx := background()
	inited := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(inited, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: inited, Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() }) // test teardown
	pid, err := ids.Parse[ids.Project](v17ProjA)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedRepoMeta(ctx, pid); err != nil {
		t.Fatal(err)
	}
	migrated := t.TempDir()
	if err = os.MkdirAll(filepath.Join(migrated, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(migrated, ".lore", "data", "_meta.json"), []byte(`{"_v": 1, "project_id": "`+v17ProjA+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = rowfs.WriteProjectFormat(migrated, rowfs.ProjectFormatCurrent, rowfs.ProjectMigration{By: "Ada Ray <ada@example.test>"}); err != nil {
		t.Fatal(err)
	}
	for name, top := range map[string]string{"inited": inited, "migrated": migrated} {
		b, rerr := os.ReadFile(filepath.Join(top, ".lore", "data", "_meta.json"))
		if rerr != nil {
			t.Fatal(rerr)
		}
		var m struct {
			V      int `json:"_v"`
			Format int `json:"format"`
		}
		if jerr := json.Unmarshal(b, &m); jerr != nil {
			t.Fatal(jerr)
		}
		if m.V != 2 || m.Format != rowfs.ProjectFormatCurrent {
			t.Errorf("%s _meta.json says _v %d, format %d; want 2 and 2:\n%s", name, m.V, m.Format, b)
		}
		pf, perr := ProjectFormatAt(top)
		if perr != nil || pf.Format != rowfs.ProjectFormatCurrent {
			t.Errorf("%s project reads as %+v (%v)", name, pf, perr)
		}
	}
	// a project written by the earlier shape still reads as format 2
	old := t.TempDir()
	if err = os.MkdirAll(filepath.Join(old, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(old, ".lore", "data", "_meta.json"), []byte(`{"_v": 1, "format": 2, "project_id": "`+v17ProjA+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pf, perr := ProjectFormatAt(old); perr != nil || pf.Format != rowfs.ProjectFormatCurrent {
		t.Errorf("the earlier migrated shape reads as %+v (%v)", pf, perr)
	}
}
