package rowfs

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestMetaWithConflictMarkersNamesTheMerge (E-8): a _meta.json a merge
// left with conflict markers stays fail closed (E_OLD_LORE, read-only) but
// says it is a conflict and names the way out, never "restore from git
// history" (which throws one side away); other unreadable files keep the
// old message.
func TestMetaWithConflictMarkersNamesTheMerge(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	meta := filepath.Join(top, LoreDir, DataDir, MetaFileName)
	if err := os.MkdirAll(filepath.Dir(meta), 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(meta, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("{\n<<<<<<< HEAD\n  \"format\": 2,\n=======\n  \"format\": 2,\n  \"migrated_by\": \"x\",\n>>>>>>> origin/side\n}\n")
	_, err := ReadProjectFormat(top)
	if !errs.Has(err, errs.OldLore) || !strings.Contains(err.Error(), "conflict markers") {
		t.Fatalf("conflicted _meta.json: %v", err)
	}
	var e *errs.Error
	if !stderrors.As(err, &e) || !strings.Contains(e.Hint, "--abort") || strings.Contains(e.Hint, "git history") {
		t.Fatalf("hint of %v, want the abort and lore sync, not a restore", err)
	}
	write("not json")
	if _, err = ReadProjectFormat(top); err == nil || strings.Contains(err.Error(), "conflict markers") {
		t.Fatalf("a broken _meta.json without markers: %v", err)
	}
}
