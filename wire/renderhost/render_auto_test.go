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

// TestRerenderStaleQuietKeepsHeldRender (devco:K-8): a sync on a clean
// tree never rewrites a committed render that matches the index, whatever
// the sync brought in; only row files this clone already changed free it
// (the commit that carries them carries the render too). The probe is the
// project id lookup, the first step of an actual render.
func TestRerenderStaleQuietKeepsHeldRender(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=T", "-c", "user.email=t@example.test"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write(LoreMDRel, "rules\n")
	write(rowsDirRel+"/rules/r1.json", "{}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "render")
	t.Chdir(root)
	asked := 0
	h := New(Options{
		ProjectID:    func(context.Context) (string, error) { asked++; return "", os.ErrNotExist },
		RepoIsClient: func(context.Context) (bool, error) { return false, nil },
	})
	h.rerenderStaleQuiet(ctx, root)
	if asked != 0 {
		t.Fatal("clean tree, committed render: the sync went on to render it, want it left alone")
	}
	write(rowsDirRel+"/rules/r2.json", "{}\n")
	h.rerenderStaleQuiet(ctx, root)
	if asked != 1 {
		t.Fatalf("uncommitted row files: render checks = %d, want 1", asked)
	}
}
