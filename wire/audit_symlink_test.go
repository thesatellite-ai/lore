package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestAuditAppendRefusesSymlink: an audit file (.lore/state/audit-v2.jsonl,
// the per-store and batch files) that is a symbolic link is refused with
// E_SYMLINK_DB and the file it points at keeps its content; a regular file,
// and a missing one, get the line.
func TestAuditAppendRefusesSymlink(t *testing.T) {
	const keep = "precious content\n"
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte(keep), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "state", "audit-v2.jsonl")
	if err := os.MkdirAll(filepath.Dir(linked), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, linked); err != nil {
		t.Fatal(err)
	}
	if err := appendAuditLine(linked, 0o700, []byte(`{"op":"x"}`)); errs.CodeOf(err) != errs.SymlinkDB {
		t.Fatalf("append through a symlinked audit file: got %v, want E_SYMLINK_DB", err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != keep {
		t.Fatalf("the linked file was written: %q (%v)", got, err)
	}
	plain := filepath.Join(dir, "state", "plain.jsonl")
	for range 2 { // missing, then existing
		if err := appendAuditLine(plain, 0o700, []byte(`{"op":"x"}`)); err != nil {
			t.Fatalf("append to a regular file: %v", err)
		}
	}
	if got, err := os.ReadFile(plain); err != nil || string(got) != "{\"op\":\"x\"}\n{\"op\":\"x\"}\n" {
		t.Fatalf("regular file content %q (%v)", got, err)
	}
}
