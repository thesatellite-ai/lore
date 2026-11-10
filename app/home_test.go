package app

// Not parallel: sets HOME and the working directory.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRelativeHomeRefused (E-GAPS-1 S2): a relative HOME answers
// E_INVALID_INPUT (exit 20) naming HOME and "absolute" before anything
// opens, for doctor and any other command; an absolute, a missing and an
// unset HOME are not refused. Break-verified: without the run() call
// `lore doctor` under HOME=../x exits 0 (the cache check's "invalid uri
// authority" is only a warning line).
func TestRelativeHomeRefused(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		return // os.UserHomeDir reads USERPROFILE / home there, not HOME
	}
	wd := filepath.Join(t.TempDir(), "wd")
	if err := os.Mkdir(wd, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wd) // a broken guard writes ../x beside a temp folder, never into the repo
	t.Setenv("LORE_SYNC_GIT", "0")
	for _, rel := range []string{"../x", "x", "./home/me"} {
		t.Setenv("HOME", rel)
		err := refuseRelativeHome()
		if errs.CodeOf(err) != errs.InvalidInput || !strings.Contains(err.Error(), "HOME") || !strings.Contains(err.Error(), "absolute") {
			t.Errorf("HOME=%s: %v", rel, err)
		}
		for _, args := range [][]string{{"doctor"}, {"memory", "list", "--json"}} {
			if code := run(context.Background(), args); code != errs.DefOf(errs.InvalidInput).Exit {
				t.Errorf("HOME=%s lore %v: exit %d, want %d", rel, args, code, errs.DefOf(errs.InvalidInput).Exit)
			}
		}
	}
	for _, ok := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing"), ""} {
		t.Setenv("HOME", ok)
		if err := refuseRelativeHome(); err != nil {
			t.Errorf("HOME=%q refused: %v", ok, err)
		}
	}
}
