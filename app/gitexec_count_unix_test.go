//go:build !windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitExecsStayFew: every command once ran 100+ git subprocesses, and
// `resource import` about 150 per row (minutes for 200 rows). Reads now go
// through one per-session git read cache, so a list or an add runs a few
// dozen git commands and an import's count does not grow with its rows.
// The bounds leave room for new reads, not for a per-row or per-read storm.
func TestGitExecsStayFew(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t)
	w.person().author()
	repo := w.initRepo(t)
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "git.log")
	wrapper := "#!/bin/sh\necho x >> '" + log + "'\nexec '" + w.gitBin + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	w.set("PATH", bin+string(os.PathListSeparator)+w.env["PATH"])
	count := func(args ...string) int {
		t.Helper()
		if err := os.WriteFile(log, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		w.mustLore(t, repo, args...)
		b, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "x\n")
	}
	const perCommand = 100
	for _, args := range [][]string{{"memory", "add", "--body", "a note"}, {"memory", "list"}, {"task", "list"}} {
		if n := count(args...); n == 0 || n >= perCommand {
			t.Errorf("lore %s ran %d git commands, want 1..%d (is the wrapper on PATH?)", strings.Join(args, " "), n, perCommand-1)
		}
	}
	w.mustLore(t, repo, "resource", "kind", "add", "--kind", "host", "--description", "a server")
	csv := func(n int) string {
		var b strings.Builder
		b.WriteString("name\n")
		for i := range n {
			fmt.Fprintf(&b, "web%d%d\n", n, i)
		}
		p := filepath.Join(t.TempDir(), "hosts.csv")
		if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	few := count("resource", "import", "--file", csv(3), "--kind", "host", "--yes")
	many := count("resource", "import", "--file", csv(40), "--kind", "host", "--yes")
	if many-few > 20 || many >= perCommand {
		t.Errorf("resource import: 3 rows ran %d git commands, 40 rows %d; the count must not grow with the rows", few, many)
	}
}
