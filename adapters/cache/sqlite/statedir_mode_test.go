package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestStateFolderIsOwnerOnly: the machine store and the local state make
// their folder (and a missing ~/.lore above it) owner-only, so the
// machine.db that SQLite creates at the umask (0644) is not readable by
// the group (found by tests/machines on a fresh home, 2026-11-07).
func TestStateFolderIsOwnerOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	opens := map[string]func(string) (func() error, error){
		"machine store": func(p string) (func() error, error) {
			s, err := OpenMachineStore(ctx, p, false)
			if err != nil {
				return nil, err
			}
			return s.Close, nil
		},
		"local state": func(p string) (func() error, error) {
			s, err := OpenLocalState(ctx, p)
			if err != nil {
				return nil, err
			}
			return s.Close, nil
		},
	}
	for name, open := range opens {
		home := filepath.Join(t.TempDir(), ".lore")
		closeFn, err := open(filepath.Join(home, "state", "machine.db"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := closeFn(); err != nil {
			t.Fatalf("%s: close: %v", name, err)
		}
		for _, d := range []string{home, filepath.Join(home, "state")} {
			info, err := os.Stat(d)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm&0o077 != 0 {
				t.Errorf("%s: %s is %#o, want owner-only", name, d, perm)
			}
		}
	}
}
