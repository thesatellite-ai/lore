package wire

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCleanFileURLLocalPaths: a store url naming a place on this machine.
// A bare git repository is a git store written as its plain path, whether
// typed as a path or file://; any other folder (a checkout too: git
// refuses a push to it) is a folder store (file://); a missing path names
// both spellings.
func TestCleanFileURLLocalPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	bare := filepath.Join(root, "notes.git")
	work := filepath.Join(root, "work")
	plain := filepath.Join(root, "plain")
	kept := filepath.Join(root, "kept.git") // a folder store made in a bare repo stays one
	for _, args := range [][]string{{"init", "-q", "--bare", bare}, {"init", "-q", work}, {"init", "-q", "--bare", kept}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	for _, d := range []string{plain, kept} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(kept, rowfs.MarkerFile), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	slash := filepath.ToSlash
	for in, want := range map[string]string{
		bare:                   slash(bare),
		bare + "/":             slash(bare),
		fileuri.URL(bare, ""):  slash(bare),
		work:                   fileuri.URL(work, ""),
		fileuri.URL(work, ""):  fileuri.URL(work, ""),
		plain:                  fileuri.URL(plain, ""),
		fileuri.URL(plain, ""): fileuri.URL(plain, ""),
		fileuri.URL(kept, ""):  fileuri.URL(kept, ""),
	} {
		got, err := cleanFileURL(in)
		if err != nil || got != want {
			t.Errorf("cleanFileURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	_, err := cleanFileURL(filepath.Join(root, "missing.git"))
	if !errs.Has(err, errs.BadPath) {
		t.Fatalf("a missing path = %v, want E_BAD_PATH", err)
	}
	var le *errs.Error
	if !errors.As(err, &le) || !strings.Contains(le.Hint, "/path/to/repo.git") || !strings.Contains(le.Hint, "file:///path/to/folder") {
		t.Fatalf("a missing path's hint does not name both forms: %v", err)
	}
}
