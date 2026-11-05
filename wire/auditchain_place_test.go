package wire

// auditchain_place_test.go — OWNERQ-1 (g-monorepo PATCH): cmd/lore's chain
// sink writes the fallback AuditFile into the project's own .lore/state,
// never into the git top of a monorepo whose top holds no project (that
// file was left untracked and un-ignored at <top>/.lore/state).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestChainAuditFileInTheProjectFolder: where the AuditFile and the
// legacy lore.db resolve for each place a command can run from, in a
// monorepo with a top project, without one, and in a single-project repo.
func TestChainAuditFileInTheProjectFolder(t *testing.T) {
	cases := []struct {
		name     string
		projects []string // folders (relative to the git top) holding .lore
		runIn    string   // the command's folder
		want     string   // the project folder the files belong to
	}{
		{"sub-project, no top project", []string{"sub"}, "sub", "sub"},
		{"deeper folder of a sub-project, no top project", []string{"sub"}, "sub/deeper", "sub"},
		{"sub-project beside a top project", []string{".", "sub"}, "sub", "sub"},
		{"top project run from the top", []string{".", "sub"}, ".", "."},
		{"plain folder under a top project", []string{"."}, "docs", "."},
		{"single-project repo", []string{"."}, ".", "."},
		{"no project anywhere: the git top", nil, "x", "."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			top := t.TempDir()
			if real, err := filepath.EvalSymlinks(top); err == nil {
				top = real
			}
			cmd := exec.Command("git", "init", "-q", top)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			for _, p := range c.projects {
				if err := os.MkdirAll(filepath.Join(top, p, repoMarkerDir), 0o750); err != nil {
					t.Fatal(err)
				}
			}
			run := filepath.Join(top, c.runIn)
			if err := os.MkdirAll(run, 0o750); err != nil {
				t.Fatal(err)
			}
			ca, ok := NewChainAudit(t.TempDir(), run, time.Now).(*chainAudit)
			if !ok {
				t.Fatal("NewChainAudit is not the chain sink")
			}
			ca.resolve(context.Background())
			want := filepath.Join(top, c.want)
			if got, w := ca.file.path, filepath.Join(want, filepath.FromSlash(AuditFile)); got != w {
				t.Fatalf("AuditFile = %s, want %s", got, w)
			}
			if got, w := ca.dbPath, filepath.Join(want, filepath.FromSlash(legacyLoreDB)); got != w {
				t.Fatalf("lore.db = %s, want %s", got, w)
			}
		})
	}
}
