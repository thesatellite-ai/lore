//go:build journeys && !windows

package journeys

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	jLinkOutSlug   = "github.com/acme-test/linkout-app"
	jLinkOutOther  = "github.com/acme-test/linkout-notes"
	jLinkOutOwnTxt = "# notes of the other repository\n"
)

// TestJourneyToolFileLinkedOutOfTheRepo (devco D-502): a developer keeps
// app's CLAUDE.md as a symbolic link to the CLAUDE.md of another
// repository checked out beside it. lore init and lore render --yes in app
// never write through it: the other repository's file stays byte-identical
// and clean in git, the link stays a link, and render says in one line
// which file it left, where it leads and where lore's context is instead.
func TestJourneyToolFileLinkedOutOfTheRepo(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Rowe", "ada.rowe@example.test").Laptop()
	other := ada.Clone(w.Remote(jLinkOutOther), "notes")
	other.WriteFile("CLAUDE.md", jLinkOutOwnTxt)
	other.CommitAll("notes: claude")
	a := ada.Clone(w.Remote(jLinkOutSlug), "app")
	a.WriteFile("README.md", "# app\n")
	otherFile := filepath.Join(other.Dir, "CLAUDE.md")
	link := filepath.Join(a.Dir, "CLAUDE.md")
	if err := os.Symlink(otherFile, link); err != nil {
		t.Fatal(err)
	}

	w.Step("day1/init-and-render-leave-the-other-repo-alone", func() {
		ada.LoreIn(a, "init").OK(t)
		ada.LoreIn(a, "render", "--yes").OK(t).
			Says(t, link+": pointer skipped", "outside the repository", "lore context")
		if b, err := os.ReadFile(otherFile); err != nil || string(b) != jLinkOutOwnTxt {
			t.Fatalf("the other repository's CLAUDE.md was written: %q (%v)", b, err)
		}
		if st := other.Status(); st != "" {
			t.Fatalf("the other repository is no longer clean:\n%s", st)
		}
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("app's CLAUDE.md is no longer a link (%v)", err)
		}
	})
}
