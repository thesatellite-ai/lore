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

// TestRerenderStaleQuietHoldsDuringGitOp is devco:Q-15: with uncommitted
// row files a sync may refresh a committed render, except while a rebase
// (or merge, cherry-pick, revert, bisect) is stopped in the work tree: a
// changed tracked LORE.md then makes `git rebase --continue` refuse.
func TestRerenderStaleQuietHoldsDuringGitOp(t *testing.T) {
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
	write(rowsDirRel+"/rules/r2.json", "{}\n") // an uncommitted write
	for _, marker := range []string{"rebase-merge", "MERGE_HEAD", "CHERRY_PICK_HEAD"} {
		write(".git/"+marker, "")
		if marker == "rebase-merge" {
			_ = os.Remove(filepath.Join(root, ".git", marker))
			if err := os.Mkdir(filepath.Join(root, ".git", marker), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		t.Chdir(root)
		asked := 0
		h := New(Options{
			ProjectID:    func(context.Context) (string, error) { asked++; return "", os.ErrNotExist },
			RepoIsClient: func(context.Context) (bool, error) { return false, nil },
		})
		h.rerenderStaleQuiet(ctx, root)
		if asked != 0 {
			t.Errorf("%s in progress: the sync went on to render the tracked LORE.md, want it left alone", marker)
		}
		if err := os.RemoveAll(filepath.Join(root, ".git", marker)); err != nil {
			t.Fatal(err)
		}
	}
}
