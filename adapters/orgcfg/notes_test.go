package orgcfg

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHoldsNotes: devco:D-723. Only a live row of a note kind counts; the
// rows `lore init` writes, actor rows, _meta.json and tombstones do not.
func TestHoldsNotes(t *testing.T) {
	note := func(table string) bool { return table == "memories" || table == "rules" }
	write := func(t *testing.T, dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, ".lore", "data", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	moved := func(t *testing.T) string {
		dir := t.TempDir()
		write(t, dir, "_meta.json", `{"project_id":"prj_1"}`)
		write(t, dir, "projects/prj_1.json", `{"_id":"prj_1"}`)
		write(t, dir, "repos/rep_1.json", `{"_id":"rep_1"}`)
		write(t, dir, "actors/act_1.json", `{"_id":"act_1"}`)
		write(t, dir, "memories/mem_1.json", `{"_id":"mem_1","_deleted":true}`)
		return dir
	}
	t.Run("no lore data", func(t *testing.T) {
		if HoldsNotes(t.TempDir(), note) {
			t.Fatal("a repo with no .lore/data holds no notes")
		}
	})
	t.Run("notes moved out", func(t *testing.T) {
		if HoldsNotes(moved(t), note) {
			t.Fatal("init rows, actors, _meta.json and a tombstone are no notes")
		}
	})
	t.Run("a live note", func(t *testing.T) {
		dir := moved(t)
		write(t, dir, "rules/rul_1.json", `{"_id":"rul_1","fields":{"body":"x"}}`)
		if !HoldsNotes(dir, note) {
			t.Fatal("a live rule row is a note")
		}
	})
	t.Run("an unreadable row counts as live", func(t *testing.T) {
		dir := moved(t)
		write(t, dir, "memories/mem_2.json", `not json`)
		if !HoldsNotes(dir, note) {
			t.Fatal("a row file that does not parse must keep the old default")
		}
	})
}
