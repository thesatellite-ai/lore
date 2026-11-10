package main

// The v0.1.17 privilege tests for an unusable HOME (saas/pkg/aicoder/
// identity privilege_test.go TestCheckPrivilege_HomeUnset,
// TestCheckPrivilege_HomeNonExistent), ported before phase E onto dirs():
// an unset or missing HOME never refuses a command; an unset one falls
// back to a temp lore home, a missing one is used as given (the folder is
// made on first write).
//
// Not parallel: sets HOME.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

func TestDirsWithUnusableHome(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		// os.UserHomeDir reads USERPROFILE / home there, not HOME.
		return
	}
	t.Setenv("HOME", "")
	_, home := dirs()
	if want := filepath.Join(os.TempDir(), unresolvedHome); home != want {
		t.Fatalf("HOME unset: lore home %q, want the temp fallback %q", home, want)
	}
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	t.Setenv("HOME", missing)
	wd, home := dirs()
	if want := filepath.Join(missing, rowfs.LoreDir); home != want {
		t.Fatalf("HOME missing: lore home %q, want %q", home, want)
	}
	if wd == "" {
		t.Fatal("working directory is empty")
	}
}
