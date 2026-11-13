package repoguard

// The gate's first step, tests/lib/gocache-trim.sh, bounds the shared Go
// build cache. Lock-free check:fast runs may be building while it runs, so
// it must never empty the whole cache (go clean -cache failed those builds,
// and failed itself with "directory not empty" when a build wrote an entry
// mid-clean, 2026-11-13): over the limit it deletes only entries idle for
// LORE_GOCACHE_IDLE_MIN minutes and keeps the cache's own top files.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGocacheTrimDeletesOnlyIdleEntries(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	write := func(rel string, age time.Duration) string {
		p := filepath.Join(cache, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	idle := write("ab/idle-a", 3*time.Hour)
	fresh := write("ab/fresh-a", time.Minute)
	readme := write("README", 30*24*time.Hour)
	trim := write("trim.txt", 30*24*time.Hour)

	cmd := exec.Command("bash", filepath.Join(repoRoot, "tests", "lib", "gocache-trim.sh"))
	cmd.Env = append(os.Environ(), "GOCACHE="+cache, "LORE_GOCACHE_MAX_GB=0", "LORE_GOCACHE_IDLE_MIN=120", "LORE_GOCACHE_TRIM=on")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gocache-trim.sh: %v\n%s", err, out)
	}
	if _, err := os.Stat(idle); !os.IsNotExist(err) {
		t.Errorf("an entry idle for 3 h was kept (%v)\n%s", err, out)
	}
	for _, p := range []string{fresh, readme, trim} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was deleted: a fresh entry or the cache's own file must stay\n%s", filepath.Base(p), out)
		}
	}
	if !strings.Contains(string(out), "idle over 120 min") {
		t.Errorf("the summary does not say what it deleted:\n%s", out)
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, "tests", "lib", "gocache-trim.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if l := strings.TrimSpace(line); !strings.HasPrefix(l, "#") && strings.Contains(l, "go clean -cache") {
			t.Errorf("gocache-trim.sh empties the whole cache under running builds: %s", l)
		}
	}
}
