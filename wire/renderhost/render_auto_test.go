package renderhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRenderHeld (A-12): an automatic render leaves a tracked render that
// matches the index alone; an untracked one or one already changed is
// free to write; outside git nothing is held.
func TestRenderHeld(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	h := &Host{}
	if h.RenderHeld(ctx, root) {
		t.Fatal("outside git: held, want free")
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=T", "-c", "user.email=t@example.test"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	md := filepath.Join(root, filepath.FromSlash(LoreMDRel))
	if err := os.MkdirAll(filepath.Dir(md), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(md, []byte("rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h.RenderHeld(ctx, root) {
		t.Fatal("untracked LORE.md: held, want free")
	}
	git("add", LoreMDRel)
	git("commit", "-q", "-m", "render")
	if !h.RenderHeld(ctx, root) {
		t.Fatal("tracked LORE.md matching the index: free, want held")
	}
	if err := os.WriteFile(md, []byte("rules and more\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h.RenderHeld(ctx, root) {
		t.Fatal("LORE.md already changed in the work tree: held, want free")
	}
}
