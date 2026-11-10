package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestEnsureGitignoreRefusesLinkOutOfTheRepo: a .gitignore that is a
// symlink leading out of the repository (a cloned repository can commit
// one) is refused and the file it names keeps its content; a .gitignore
// linked to a file inside the repository still gets the lines.
func TestEnsureGitignoreRefusesLinkOutOfTheRepo(t *testing.T) {
	const keep = "precious content\n"
	ctx := context.Background()
	t.Run("outside", func(t *testing.T) {
		root := t.TempDir()
		gitInit(t, root)
		victim := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(victim, []byte(keep), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(root, gitignoreName)); err != nil {
			t.Fatal(err)
		}
		if err := EnsureGitignore(ctx, root); errs.CodeOf(err) != errs.Refused {
			t.Fatalf("a .gitignore linked out of the repository: got %v, want E_REFUSED", err)
		}
		if got, err := os.ReadFile(filepath.Clean(victim)); err != nil || string(got) != keep {
			t.Fatalf("the linked file was written: %q (%v)", got, err)
		}
	})
	t.Run("inside", func(t *testing.T) {
		root := t.TempDir()
		gitInit(t, root)
		shared := filepath.Join(root, "shared", "ignore")
		if err := os.MkdirAll(filepath.Dir(shared), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(shared, []byte("node_modules\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("shared", "ignore"), filepath.Join(root, gitignoreName)); err != nil {
			t.Fatal(err)
		}
		if err := EnsureGitignore(ctx, root); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Clean(shared)); err != nil || !strings.Contains(string(got), ".lore/lock\n") {
			t.Fatalf("the in-repo link target did not get the lines: %q (%v)", got, err)
		}
	})
}
