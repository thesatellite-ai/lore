package wire

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOldRowsLinesUnreadableMeta: in a project whose _meta.json cannot be
// read, the read notice and lore sync's skip line say so in the words a
// write there refuses with ("_meta.json cannot be read (…)"), never "on the
// lore 0.1.17 format"; a project really on that format keeps those lines.
func TestOldRowsLinesUnreadableMeta(t *testing.T) {
	mk := func(t *testing.T, meta string) string {
		t.Helper()
		top := t.TempDir()
		if out, err := exec.Command("git", "init", "-q", top).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
		data := filepath.Join(top, ".lore", "data", "memories")
		if err := os.MkdirAll(data, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(top, ".lore", "data", "_meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "mem_1.json"), []byte(`{"_table": "memories", "_v": 1, "body": "x", "id": "mem_1"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return top
	}
	for _, c := range []struct{ name, meta, reason string }{
		{"not JSON", "{broken", "(not a JSON object)"},
		{"floor not a string", `{"_v": 1, "min_lore_version": 18, "project_id": "p"}`, "(min_lore_version is not a string)"},
	} {
		top := mk(t, c.meta)
		if !ProjectFormatOld(top) {
			t.Fatalf("%s: an unreadable _meta.json must still fail closed (count as old)", c.name)
		}
		want := "this project's .lore/data/_meta.json cannot be read " + c.reason + "; lore is read-only here until it is fixed; restore .lore/data/_meta.json from git history"
		for label, line := range map[string]string{"read": OldRowsReadLineAt(top), "sync": OldRowsSyncLineAt(t.Context(), filepath.Join(top, ".lore"))} {
			if !strings.Contains(line, want) || strings.Contains(line, "0.1.17") || strings.Contains(line, "migrate run") {
				t.Errorf("%s %s line: %q, want it to contain %q and no format claim", c.name, label, line, want)
			}
		}
	}
	top := mk(t, `{"_v": 1, "project_id": "p"}`)
	if got := OldRowsReadLineAt(top); got != OldRowsReadLine {
		t.Errorf("0.1.17 project read line: %q", got)
	}
	if got := OldRowsSyncLineAt(t.Context(), top); got != OldRowsSyncLine {
		t.Errorf("0.1.17 project sync line: %q", got)
	}
}
