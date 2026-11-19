package renderhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// privateRepo is a git repo holding tracked AGENTS.md and CLAUDE.md and a
// .lore folder, which .gitignore lists when private.
func privateRepo(t *testing.T, private bool) string {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...) // #nosec G204 -- fixed test arguments
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	ignore := "bin/\n"
	if private {
		ignore += ".lore/\n"
	}
	for name, body := range map[string]string{".gitignore": ignore, "AGENTS.md": "# Agents\n", "CLAUDE.md": "# Claude\n", ".lore/LORE.md": "# lore\n"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".gitignore", "AGENTS.md", "CLAUDE.md")
	git("commit", "-q", "-m", "start")
	return root
}

// TestPrivateProjectPointerGoesToLocalFile (devco D-500): render and init
// stitched lore's pointer block into the tracked CLAUDE.md and AGENTS.md
// of a repo whose .gitignore lists .lore/. Now both stay byte-identical;
// claude_code's pointer goes into its git-excluded CLAUDE.local.md, codex's
// file is skipped with a note saying why and how it gets the notes (the
// project-wide line is the git wiring's, devco D-513). A repo that
// commits .lore/ still gets the pointer in its tools' files.
func TestPrivateProjectPointerGoesToLocalFile(t *testing.T) {
	targets := []contextTarget{
		{profile: builtin(t, toolprofile.NameClaudeCode), file: "CLAUDE.md", known: true},
		{profile: builtin(t, toolprofile.NameCodex), file: "AGENTS.md", known: true},
	}
	ctx := context.Background()

	root := privateRepo(t, true)
	var w strings.Builder
	if err := New(Options{}).stitchRepoPointers(ctx, &w, root, targets, filepath.Join(root, ".lore", "LORE.md")); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"AGENTS.md": "# Agents\n", "CLAUDE.md": "# Claude\n"} {
		if b, _ := os.ReadFile(filepath.Join(root, name)); string(b) != want {
			t.Fatalf("the tracked %s changed in a private project:\n%s\noutput:\n%s", name, b, w.String())
		}
	}
	local, err := os.ReadFile(filepath.Join(root, "CLAUDE.local.md"))
	if err != nil || !strings.Contains(string(local), toolprofile.PointerStartMarker) || !strings.Contains(string(local), "@.lore/LORE.md") {
		t.Fatalf("CLAUDE.local.md holds no pointer (%v):\n%s", err, local)
	}
	if out, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=all").Output(); err != nil || strings.TrimSpace(string(out)) != "" { // #nosec G204 -- fixed test arguments
		t.Fatalf("git status not clean after a private render (%v):\n%s", err, out)
	}
	if out := w.String(); !strings.Contains(out, "private to this machine") || !strings.Contains(out, "AGENTS.md") {
		t.Fatalf("the render does not say why nothing went into the repo:\n%s", out)
	}

	shared := privateRepo(t, false)
	w.Reset()
	if err := New(Options{}).stitchRepoPointers(ctx, &w, shared, targets, filepath.Join(shared, ".lore", "LORE.md")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(shared, "CLAUDE.md")); !strings.Contains(string(b), toolprofile.PointerStartMarker) {
		t.Fatalf("a repo that commits .lore/ lost its CLAUDE.md pointer:\n%s", b)
	}
}
