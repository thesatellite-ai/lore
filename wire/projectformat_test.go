package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// TestProjectFormatAt (D12): a project's format is what its _meta.json
// records; without the key, a lore 0.1.17 row makes it format 1, a
// migrated _meta.json (min_lore_version) or current rows format 2; no
// .lore/data at all is no project. A recorded format beats the rows, and
// who migrated it and when is shown.
func TestProjectFormatAt(t *testing.T) {
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
	for _, c := range []struct {
		name, meta, row string
		want            int
		older           bool
	}{
		{"0.1.17 rows, no key", `{"_v": 1, "project_id": "p"}`, v1, rowfs.ProjectFormatV017, true},
		{"migrated by an earlier build", `{"_v": 1, "min_lore_version": "0.1.18", "project_id": "p"}`, v1, rowfs.ProjectFormatCurrent, false},
		{"current rows", `{"_v": 1, "project_id": "p"}`, v2, rowfs.ProjectFormatCurrent, false},
		{"recorded 1 beats current rows", `{"_v": 1, "format": 1, "project_id": "p"}`, v2, rowfs.ProjectFormatV017, true},
		{"recorded 2", `{"_v": 1, "format": 2, "project_id": "p"}`, v1, rowfs.ProjectFormatCurrent, false},
	} {
		pf, err := ProjectFormatAt(mk(t, c.meta, c.row))
		if err != nil || pf.Format != c.want || OlderFormat(pf) != c.older {
			t.Errorf("%s: %+v %v, want format %d older %v", c.name, pf, err, c.want, c.older)
		}
	}
	if pf, err := ProjectFormatAt(t.TempDir()); err != nil || pf.Format != rowfs.ProjectFormatUnknown {
		t.Errorf("no .lore/data: %+v %v", pf, err)
	}
	if _, err := ProjectFormatAt(mk(t, `{"format": "two"}`, v2)); err == nil {
		t.Error("a format key of the wrong shape must fail closed")
	}

	top := mk(t, `{"_v": 1, "keep": "me", "project_id": "p"}`, v2)
	rel, err := rowfs.WriteProjectFormat(top, rowfs.ProjectFormatCurrent, "Pat Doe <pat@example.com>", "2026-11-13T10:00:00Z")
	if err != nil || rel != rowfs.RepoMetaPath {
		t.Fatalf("write: %s %v", rel, err)
	}
	b, _ := os.ReadFile(filepath.Join(top, filepath.FromSlash(rel)))
	if !strings.Contains(string(b), `"keep": "me"`) {
		t.Errorf("other keys must be kept: %s", b)
	}
	pf, err := ProjectFormatAt(top)
	if err != nil || !pf.Recorded || pf.MigratedBy != "Pat Doe <pat@example.com>" {
		t.Fatalf("after write: %+v %v", pf, err)
	}
	if line := ProjectFormatLine(pf); !strings.Contains(line, "the lore 0.1.18 format (format 2), migrated by Pat Doe") {
		t.Errorf("line: %s", line)
	}
	if line := ProjectFormatLine(rowfs.ProjectFormat{Format: rowfs.ProjectFormatV017}); !strings.Contains(line, "lore migrate run") {
		t.Errorf("an older format names the command that raises it: %s", line)
	}
}
