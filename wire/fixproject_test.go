package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// TestHasV017Rows pins the v0.1.17 detection the gate of every command
// uses: an unmigrated repo with a _v: 1 row file is old; a migrated one
// (min_lore_version declared) and one with only current rows are not.
func TestHasV017Rows(t *testing.T) {
	mk := func(t *testing.T, meta, row string) string {
		t.Helper()
		top := t.TempDir()
		data := filepath.Join(top, ".lore", "data", "memories")
		if err := os.MkdirAll(data, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(top, ".lore", "data", "_meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "mem_1.json"), []byte(row), 0o600); err != nil {
			t.Fatal(err)
		}
		return top
	}
	v1 := `{"_table": "memories", "_v": 1, "body": "x", "id": "mem_1"}` + "\n"
	v2 := `{"_hlc": "1", "_table": "memories", "_v": 2, "body": "x", "id": "mem_1"}` + "\n"
	if !hasV017Rows(mk(t, `{"_v": 1, "project_id": "p"}`, v1)) {
		t.Fatal("an unmigrated repo with a v1 row is not old")
	}
	if hasV017Rows(mk(t, `{"_v": 1, "min_lore_version": "0.1.18", "project_id": "p"}`, v1)) {
		t.Fatal("a migrated repo counts as old")
	}
	if hasV017Rows(mk(t, `{"_v": 1, "project_id": "p"}`, v2)) {
		t.Fatal("a repo with only current rows counts as old")
	}
	if hasV017Rows("") {
		t.Fatal("no repo counts as old")
	}
}

// newTestAppWith is newTestApp with its Options changed by edit first.
func newTestAppWith(t *testing.T, dir string, edit func(*Options)) *App {
	t.Helper()
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	o := Options{RepoDir: dir, Home: t.TempDir(), Actor: actor}
	edit(&o)
	a, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}
