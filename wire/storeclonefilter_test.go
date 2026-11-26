package wire

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestReadCloneFilter: store.clone_filter defaults to none (full clones
// until every machine reads partial clones), takes none and blob:none,
// and fails closed on anything else, such as a shallow depth
// (devco:D-1001).
func TestReadCloneFilter(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		yaml, want string
		bad        bool
	}{
		{"", git.CloneFilterNone, false},
		{"store:\n  lock_timeout: 30s\n", git.CloneFilterNone, false},
		{"store:\n  clone_filter: none\n", git.CloneFilterNone, false},
		{"store:\n  clone_filter: blob:none\n", git.CloneFilterBlobless, false},
		{"store:\n  clone_filter: depth=1\n", git.CloneFilterNone, true},
		{"store:\n  clone_filter: blob:limit=1m\n", git.CloneFilterNone, true},
	} {
		home := t.TempDir()
		if c.yaml != "" {
			if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(c.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := readCloneFilter(home)
		if c.bad != errs.Has(err, errs.ConfigInvalid) || got != c.want {
			t.Errorf("readCloneFilter(%q) = %q, %v; want %q (bad %v)", c.yaml, got, err, c.want, c.bad)
		}
	}
}

// TestSweepOldClones: a clone `lore store reclone` replaced is kept a week
// under the cache folder, then the maintenance pass removes it.
func TestSweepOldClones(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	now := time.Date(2030, 1, 10, 12, 0, 0, 0, time.UTC)
	for name, age := range map[string]time.Duration{"notes-old": 8 * 24 * time.Hour, "notes-new": 24 * time.Hour} {
		p := filepath.Join(git.ReplacedClonesDir(home), name)
		if err := os.MkdirAll(filepath.Join(p, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	gone, err := git.SweepOldClones(home, now)
	if err != nil || len(gone) != 1 || filepath.Base(gone[0]) != "notes-old" {
		t.Fatalf("SweepOldClones = %v, %v; want only the week-old clone removed", gone, err)
	}
	if _, err := os.Stat(filepath.Join(git.ReplacedClonesDir(home), "notes-new")); err != nil {
		t.Fatalf("the day-old replaced clone is gone: %v", err)
	}
}
