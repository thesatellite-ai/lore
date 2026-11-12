package wire

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	opscheck "github.com/thesatellite-ai/lore/ops/check"
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

// TestCommitHookSaysRunLoreFix (B4-11): `lore check leaks`, which the
// commit-msg hook runs, is a Local operation the registry's v0.1.17 gate
// never sees; in a repo with v0.1.17 rows it still says "run lore fix"
// once, so someone who only commits learns the upgrade is pending.
func TestCommitHookSaysRunLoreFix(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	data := filepath.Join(dir, ".lore", "data", "memories")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".lore", "data", "_meta.json"), []byte(`{"_v": 1, "project_id": "p"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "mem_1.json"), []byte(`{"_table": "memories", "_v": 1, "body": "x", "id": "mem_1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	l := &standaloneLeaks{opts: Options{RepoDir: dir, Home: t.TempDir(), Actor: actor, Notice: func(m string) { notes = append(notes, m) }}}
	if _, err := l.CheckLeaks(t.Context(), opscheck.LeaksIn{Staged: true}); err != nil {
		t.Fatalf("check leaks: %v", err)
	}
	if l.app != nil {
		t.Cleanup(func() { _ = l.app.Close() })
	}
	if len(notes) != 1 || notes[0] != OldRowsReadLine {
		t.Fatalf("notes = %q, want exactly %q", notes, OldRowsReadLine)
	}
}
