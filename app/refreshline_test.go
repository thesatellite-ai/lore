package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/thesatellite-ai/lore/wire/renderhost"
)

// TestChangedFiles (B3-32): the refresh hook names only the render files
// whose bytes changed; a render that wrote the same bytes is silent.
func TestChangedFiles(t *testing.T) {
	wd := t.TempDir()
	md := filepath.Join(wd, ".lore", "LORE.md")
	full := filepath.Join(wd, ".lore", "LORE.full.md")
	if err := os.MkdirAll(filepath.Dir(md), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{md, full} {
		if err := os.WriteFile(f, []byte("same\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{md, full}
	before := readAll(files)
	if got := changedFiles(wd, files, before); len(got) != 0 {
		t.Fatalf("nothing changed, got %v", got)
	}
	if err := os.WriteFile(md, []byte("new rows\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := changedFiles(wd, files, before); !reflect.DeepEqual(got, []string{".lore/LORE.md"}) {
		t.Fatalf("changed = %v, want [.lore/LORE.md]", got)
	}
}

// TestRefreshHookHoldsTrackedRender (G-2): after any hook (the post-rewrite
// that ends a `git pull --rebase` included) a tracked .lore/LORE.md that
// matches the index is left as committed, so the work tree stays clean and
// the next pull is not refused over it. The zero Host would fail its render
// if the hook tried one.
func TestRefreshHookHoldsTrackedRender(t *testing.T) {
	ctx := context.Background()
	wd := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wd, "-c", "user.name=T", "-c", "user.email=t@example.test"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	md := filepath.Join(wd, ".lore", "LORE.md")
	if err := os.MkdirAll(filepath.Dir(md), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(md, []byte("rules from one side\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".lore/LORE.md")
	git("commit", "-q", "-m", "render")
	refreshHook(&renderhost.Host{}, wd)(ctx)
	if b, err := os.ReadFile(md); err != nil || string(b) != "rules from one side\n" {
		t.Fatalf("the hook rewrote the committed render: %q, %v", b, err)
	}
	if st := git("status", "--porcelain"); st != "" {
		t.Fatalf("the hook left the work tree dirty:\n%s", st)
	}
}
