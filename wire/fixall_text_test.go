package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestHasLoreProject pins B1-7: only a repo with .lore/data or
// .lore/stores.yaml is a lore project; a .lore holding only LORE.md, or no
// .lore at all, is not, so lore fix leaves its tool settings alone.
func TestHasLoreProject(t *testing.T) {
	dir := t.TempDir()
	if hasLoreProject(dir) {
		t.Fatal("no .lore is a project")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".lore"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".lore", "LORE.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasLoreProject(dir) {
		t.Fatal("a .lore with only LORE.md is a project")
	}
	if err := os.WriteFile(filepath.Join(dir, ".lore", "stores.yaml"), []byte("stores: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasLoreProject(dir) {
		t.Fatal("stores.yaml is not a project")
	}
}

// TestCrashDumpCut pins B4-4: a stopped child's Go runtime dump never
// reaches the repo's block; lore's own lines before it stay.
func TestCrashDumpCut(t *testing.T) {
	got := stderrTail("lore: working\nSIGQUIT: quit\nPC=0x1 m=0\n\ngoroutine 1 [running]:\nmain.main()\nrax    0x0")
	if strings.Contains(got, "rax") || strings.Contains(got, "goroutine") || !strings.Contains(got, "lore: working") {
		t.Fatalf("tail %q", got)
	}
	if plain := stderrTail("one\ntwo"); plain != ": one\ntwo" {
		t.Fatalf("plain %q", plain)
	}
}

// TestDataIgnoredLineNamesRule pins B1-8: the warning names each ignored
// lore file and the rule, so it can be checked with git check-ignore -v.
func TestDataIgnoredLineNamesRule(t *testing.T) {
	got := dataIgnoredLine(map[string]string{".lore/data/_meta.json": ".gitignore:6:_*"})
	if !strings.Contains(got, ".lore/data/_meta.json (by .gitignore:6:_*)") {
		t.Fatalf("line %q", got)
	}
	if dataIgnoredLine(nil) != doctorDataIgnoredWarning {
		t.Fatal("no hits: the generic line")
	}
}

// TestHasMoveRecord pins B1-5's owner rule: a copy that arrived by a move
// carries a moves list in _o.
func TestHasMoveRecord(t *testing.T) {
	moved, err := value.JSONOf([]byte(`{"moves":[{"at":"2026-01-01T00:00:00Z","by":"x","moved_from":"repo","moved_to":"org"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !hasMoveRecord(row.Row{Origin: moved}) {
		t.Fatal("a moved copy has no move record")
	}
	other, err := value.JSONOf([]byte(`{"old_folder":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if hasMoveRecord(row.Row{Origin: other}) || hasMoveRecord(row.Row{}) {
		t.Fatal("an unmoved copy has a move record")
	}
}
