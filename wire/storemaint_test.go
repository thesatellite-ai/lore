package wire

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestReadMaintSettings: store.maintenance and store.maintenance_every
// default to auto and 24h, take off and a duration in [1h, 720h], and fail
// closed on anything else (devco:D-996).
func TestReadMaintSettings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		yaml  string
		on    bool
		every time.Duration
		bad   bool
	}{
		{"", true, git.DefaultMaintEvery, false},
		{"store:\n  lock_timeout: 30s\n", true, git.DefaultMaintEvery, false},
		{"store:\n  maintenance: off\n", false, git.DefaultMaintEvery, false},
		{"store:\n  maintenance: auto\n  maintenance_every: 6h\n", true, 6 * time.Hour, false},
		{"store:\n  maintenance: sometimes\n", true, git.DefaultMaintEvery, true},
		{"store:\n  maintenance_every: 10m\n", true, git.DefaultMaintEvery, true},
		{"store:\n  maintenance_every: 2000h\n", true, git.DefaultMaintEvery, true},
	}
	for _, c := range cases {
		home := t.TempDir()
		if c.yaml != "" {
			if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(c.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		s, err := readMaintSettings(home)
		if c.bad {
			if errs.CodeOf(err) != errs.ConfigInvalid {
				t.Errorf("%q: err = %v, want E_CONFIG_INVALID", c.yaml, err)
			}
			continue
		}
		if err != nil || s.on != c.on || s.every != c.every {
			t.Errorf("%q: %+v, %v; want on=%v every=%v", c.yaml, s, err, c.on, c.every)
		}
	}
}

// TestStoreAutoMaintainNeverRunsATestBinary: the background pass re-runs
// lore's own binary; a go test binary (pkg.test) would run the tests again.
func TestStoreAutoMaintainNeverRunsATestBinary(t *testing.T) {
	t.Parallel()
	for exe, want := range map[string]bool{
		"/tmp/go-build1/b001/wire.test": true, `C:\b\wire.test.exe`: true,
		"/usr/local/bin/lore": false, "/opt/lore/lore.exe": false, "/home/a/.local/lib/lore-dev/lore": false,
	} {
		if got := isTestBinary(exe); got != want {
			t.Errorf("isTestBinary(%q) = %v, want %v", exe, got, want)
		}
	}
}

// TestCloneMatchesNames: --store names a clone by its folder under the
// stores folder, its path or its url in any spelling.
func TestCloneMatchesNames(t *testing.T) {
	t.Parallel()
	f := git.CloneFound{Rel: "acme-co/notes", Paths: git.PathsOf("/home/a/.lore/stores/acme-co/notes"), Origin: "git@github.com:acme-co/notes.git"}
	for name, want := range map[string]bool{
		"acme-co/notes": true, "/home/a/.lore/stores/acme-co/notes": true, "/home/a/.lore/stores/acme-co/notes/": true,
		"https://github.com/acme-co/notes": true, "git@github.com:acme-co/notes": true,
		"acme-co/other": false, "https://github.com/acme-co/other": false,
	} {
		if got := cloneMatches(f, name); got != want {
			t.Errorf("cloneMatches(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestDueClonesStampsSmallOnes: a due clone with nothing to pack and a
// small .git is stamped in-process (no background process starts for a
// throwaway store); a due clone with loose objects to pack is returned.
func TestDueClonesStampsSmallOnes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mk := func(rel string, loose int) string {
		dir := filepath.Join(home, git.StoresDir, rel)
		objs := filepath.Join(dir, ".git", "objects")
		if err := os.MkdirAll(filepath.Join(objs, "ab"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for i := range loose {
			if err := os.WriteFile(filepath.Join(objs, "ab", fmt.Sprintf("%038x", i)), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	small := mk("acme-co/small", 3)
	busy := mk("acme-co/busy", 600)
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	due := dueClones(home, git.DefaultMaintEvery, now)
	if len(due) != 1 || due[0].Paths.Dir != busy {
		t.Fatalf("due = %v, want only %s", due, busy)
	}
	if last, ok := git.LastMaintenance(small); !ok || !last.Equal(now) {
		t.Fatalf("the small clone's stamp = %v (%v), want %v", last, ok, now)
	}
	if _, ok := git.LastMaintenance(busy); ok {
		t.Fatal("the clone with loose objects was stamped without a pass")
	}
}

// TestMaintCheckNowOncePerMinute: a long-lived process (the read server)
// looks for a due clone at most once a minute, not on every served read.
func TestMaintCheckNowOncePerMinute(t *testing.T) {
	// not parallel: it moves the package's process-wide stamp
	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	lastMaintCheck.Store(0)
	t.Cleanup(func() { lastMaintCheck.Store(0) })
	if !maintCheckNow(t0) {
		t.Fatal("the first look of a process is refused")
	}
	if maintCheckNow(t0.Add(30 * time.Second)) {
		t.Fatal("a second look 30 s later is allowed")
	}
	if !maintCheckNow(t0.Add(61 * time.Second)) {
		t.Fatal("a look a minute later is refused")
	}
}
