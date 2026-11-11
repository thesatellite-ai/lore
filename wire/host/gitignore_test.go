package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opslive "github.com/thesatellite-ai/lore/ops/live"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// gitInit makes dir a git work tree, or skips when git is missing.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := gitOutput(context.Background(), "-C", dir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
}

// TestEnsureGitignoreAppendsOnlyMissingLines: lines already listed or
// already ignored by a broader rule are not repeated; a second run is a
// no-op; existing content without a final newline is kept intact.
func TestEnsureGitignoreAppendsOnlyMissingLines(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	gi := filepath.Join(root, gitignoreName)
	if err := os.WriteFile(gi, []byte("node_modules\n.lore/state/\n.lore/backups/*\nbuild"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := EnsureGitignore(ctx, root); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Clean(gi))
	got := string(b)
	if !strings.HasPrefix(got, "node_modules\n.lore/state/\n.lore/backups/*\nbuild\n\n# lore\n") {
		t.Fatalf("prefix: %q", got)
	}
	if strings.Count(got, ".lore/state/") != 1 || strings.Contains(got, ".lore/backups/\n") {
		t.Fatalf("listed or ignored line repeated: %q", got)
	}
	for _, l := range []string{".lore/lore-cache.db\n", ".lore/lock\n", ".lore/lore.db\n"} {
		if !strings.Contains(got, l) {
			t.Fatalf("missing %q in %q", l, got)
		}
	}
	if err := EnsureGitignore(ctx, root); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(filepath.Clean(gi)); string(b2) != got {
		t.Fatalf("second run changed the file: %q", b2)
	}
}

// TestGitignoreHook: read-only and folders outside a lore project write
// nothing; a project subfolder writes at the project root, once.
func TestGitignoreHook(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	gitInit(t, root)
	gi := filepath.Join(root, gitignoreName)
	// no .lore/: not a lore project
	if err := GitignoreHook(root).Before(ctx, &Run{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gi); err == nil {
		t.Fatal(".gitignore written outside a lore project")
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(filepath.Join(root, ".lore"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	h := GitignoreHook(sub)
	if err := h.Before(ctx, &Run{ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gi); err == nil {
		t.Fatal(".gitignore written in read-only mode")
	}
	if err := h.Before(ctx, &Run{}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Clean(gi)); err != nil || !strings.Contains(string(b), ".lore/lock") {
		t.Fatalf("not written at the project root: %q %v", b, err)
	}
	if err := os.Remove(gi); err != nil {
		t.Fatal(err)
	}
	if err := h.Before(ctx, &Run{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gi); err == nil {
		t.Fatal("hook ran twice in one process")
	}
}

// specEntry is an op.Entry whose only answered method is Spec.
type specEntry struct {
	op.Entry
	spec op.Spec
}

func (e specEntry) Spec() op.Spec { return e.spec }

// TestNeedsGitignore: a write adds the lines; --read-only and a live ref
// operation (it writes no row file) never do.
func TestNeedsGitignore(t *testing.T) {
	cases := []struct {
		name string
		run  Run
		want bool
	}{
		{"no entry", Run{}, true},
		{"write op", Run{Entry: specEntry{spec: op.Spec{Name: "memory.add"}}}, true},
		{"read-only", Run{ReadOnly: true, Entry: specEntry{spec: op.Spec{Name: "memory.add"}}}, false},
		{"live op", Run{Entry: specEntry{spec: op.Spec{Name: opslive.OpPurge}}}, false},
		// owner report 2026-11-11: a command that changes only a store
		// leaves the repo it runs in alone; the stores.yaml writers do not
		{"store folder add", Run{Entry: specEntry{spec: op.Spec{Name: opsstores.OpFolderAdd}}}, false},
		{"store folder list", Run{Entry: specEntry{spec: op.Spec{Name: opsstores.OpFolderList}}}, false},
		{"store set", Run{Entry: specEntry{spec: op.Spec{Name: opsstores.OpSet}}}, false},
		{"team invite", Run{Entry: specEntry{spec: op.Spec{Name: opssetup.OpTeamInvite}}}, false},
		{"store add", Run{Entry: specEntry{spec: op.Spec{Name: opsstores.OpAdd}}}, true},
		{"store init", Run{Entry: specEntry{spec: op.Spec{Name: opsstores.OpInit}}}, true},
	}
	for _, c := range cases {
		if got := NeedsGitignore(&c.run); got != c.want {
			t.Errorf("%s: NeedsGitignore = %v, want %v", c.name, got, c.want)
		}
	}
}
