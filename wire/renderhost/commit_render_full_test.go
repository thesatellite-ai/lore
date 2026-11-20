package renderhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// TestCommitRenderStagesNewFullFile is devco:D-759: a project made by an
// older lore commits .lore/LORE.md but never had LORE.full.md. The commit
// hook's refresh writes the full file, and it must go into the same commit
// as LORE.md; leaving it out left `?? .lore/LORE.full.md` in git status
// right after the commit `lore migrate run` prints. A full file git
// ignores, or one lore did not write, stays out of the commit.
func TestCommitRenderStagesNewFullFile(t *testing.T) {
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
	git("add", LoreMDRel)
	git("commit", "-q", "-m", "render from an older lore")

	write(LoreFullRel, buildFullFile(nil, nil)) // what the refresh just wrote
	if got := commitRenderStage(ctx, root); !slices.Equal(got, []string{LoreMDRel, LoreFullRel}) {
		t.Fatalf("new lore-written full file beside a tracked LORE.md: stage %v, want both render files", got)
	}

	write(LoreFullRel, "my own notes\n")
	if got := commitRenderStage(ctx, root); !slices.Equal(got, []string{LoreMDRel}) {
		t.Fatalf("a LORE.full.md lore did not write: stage %v, want only LORE.md", got)
	}

	write(LoreFullRel, buildFullFile(nil, nil))
	write(".git/info/exclude", LoreFullRel+"\n") // a teammate's clone keeps it local
	if got := commitRenderStage(ctx, root); !slices.Equal(got, []string{LoreMDRel}) {
		t.Fatalf("an ignored full file: stage %v, want only LORE.md", got)
	}
}
