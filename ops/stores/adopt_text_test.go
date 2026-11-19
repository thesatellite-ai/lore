package stores

import (
	"strings"
	"testing"
)

// TestAdoptTextPrintsTheCommit (devco:D-710): --remove-source's next step
// is a commit that runs as printed (a bare `git commit` opened an editor,
// and lore's own hook then refused it): add and commit with a message
// naming the store folder the rows went to; a dry run prints no command.
func TestAdoptTextPrintsTheCommit(t *testing.T) {
	o := AdoptOut{Store: "acme", Folder: "client_a", From: "/work/notes-app", Written: 0, Removed: 3}
	want := `git -C /work/notes-app add -A .lore/data && git -C /work/notes-app commit -m "lore: move the notes to acme/client_a"`
	if got := o.Text(); !strings.Contains(got, "removed 3 row files from /work/notes-app/.lore/data: review and commit it ("+want+")") {
		t.Fatalf("adopt --remove-source text:\n%s\nwant the command %s", got, want)
	}
	o.DryRun = true
	if got := o.Text(); strings.Contains(got, "git -C") || !strings.Contains(got, "would remove 3 row files") {
		t.Fatalf("dry run text: %s", got)
	}
}
