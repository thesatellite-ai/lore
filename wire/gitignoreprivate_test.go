package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// privateRepo makes a git repo with .lore/data whose .lore/ is excluded
// only in the clone's own .git/info/exclude (the dev's shape, devco:D-856)
// and a tracked .gitignore holding gitignore.
func privateRepo(t *testing.T, gitignore string) string {
	t.Helper()
	root := t.TempDir()
	gitInitIn(t, root)
	if err := os.MkdirAll(filepath.Join(root, ".lore", "data", "memories"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte(".lore/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignore), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// devco:D-856: a project whose .lore/ git ignores through info/exclude
// misses no lore line, so no command appends the block to the tracked
// .gitignore; the same repo without the exclude still misses them all.
func TestMissingGitignoreLinesNoneInPrivateProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := privateRepo(t, "node_modules/\n")
	if got := MissingGitignoreLines(ctx, root); len(got) != 0 {
		t.Fatalf("a private project (.lore/ in info/exclude) is asked for %v", got)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := MissingGitignoreLines(ctx, root); !slices.Contains(got, ".lore/lock") {
		t.Fatalf("a shared project misses %v, want .lore/lock among them", got)
	}
}

// devco:D-856: the block an older lore appended to a private project's
// .gitignore is named and taken out; the person's own lines (outside the
// block and inside it) stay, and a shared project's block is left alone.
func TestDropPrivateGitignoreBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const before = "node_modules/\n\n# lore\n.lore/lore.db\n.lore/lock\n.lore/data/**/.tmp-*\n\n# lore\n.lore/state/\ndist/\n"
	const want = "node_modules/\n\n# lore\ndist/\n"
	root := privateRepo(t, before)
	if got := PrivateGitignoreBlock(ctx, root); len(got) != 4 {
		t.Fatalf("PrivateGitignoreBlock = %v, want the 4 lore lines", got)
	}
	removed, err := DropPrivateGitignoreBlock(ctx, root)
	if err != nil || len(removed) != 4 {
		t.Fatalf("DropPrivateGitignoreBlock = %v, %v", removed, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if string(b) != want {
		t.Fatalf(".gitignore = %q, want %q", b, want)
	}
	if again, _ := DropPrivateGitignoreBlock(ctx, root); len(again) != 0 {
		t.Fatalf("a second run removed %v", again)
	}
	shared := privateRepo(t, before)
	if err := os.WriteFile(filepath.Join(shared, ".git", "info", "exclude"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := DropPrivateGitignoreBlock(ctx, shared); len(got) != 0 {
		t.Fatalf("a shared project's block was taken out: %v", got)
	}
}

// The lore-written block (blank line, header, lore lines) leaves no trace;
// a "# lore" heading over the person's own lines is not lore's block.
func TestWithoutLoreBlock(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"a\n\n# lore\n.lore/lock\n.lore/state/\n", "a\n"},
		{"# lore\n.lore/lock\n", ""},
		{"a\n# lore\nnotes/\n", "a\n# lore\nnotes/\n"},
		{"a\n.lore/lock\n", "a\n.lore/lock\n"},
	} {
		if got, _ := withoutLoreBlock(c.in); got != c.want {
			t.Errorf("withoutLoreBlock(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
