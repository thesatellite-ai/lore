//go:build journeys && !windows

package journeys

// personal_folder_note_test.go — a note written to a folder of the
// personal store from a repo that does not attach that folder (devco
// D-769). The write's note told the person to run `lore store add --name
// <name> --url <store-url> --folder <folder>`; following it commits the
// personal store's machine-local url into the repo's shared
// .lore/stores.yaml. The note now says the folder is the personal store's
// and names the read that works from any folder, and that read, run
// exactly as printed, finds the note.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyPersonalFolderWriteNote(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Quill", "ada@acme.example")
	lap := ada.Laptop()
	personal := w.Remote("github.com/ada-quill/notes")
	app := lap.Clone(w.Remote("github.com/acme-co/app"), "app")

	w.Step("write-to-me-folder-names-the-personal-read", func() {
		lap.LoreIn(app, "init").OK(t)
		lap.LoreIn(app, "store", "init", "--name", "me", "--url", personal.SSHURL(), "--yes").OK(t)
		storesYAML := filepath.Join(app.Dir, ".lore", "stores.yaml")
		before, _ := os.ReadFile(storesYAML)
		lap.LoreIn(app, "memory", "add", "--body", "vpn needs the second factor", "--to", "me/notes", "--create").OK(t).
			Says(t, "me/notes is a folder of your personal store", ": lore memory list --store me/notes").
			Never(t, "store add", "<store-url>")
		// the printed read, run exactly where it was printed, finds the note
		lap.LoreIn(app, "memory", "list", "--store", "me/notes").OK(t).Says(t, "second factor")
		after, _ := os.ReadFile(storesYAML)
		if string(before) != string(after) || strings.Contains(string(after), "ada-quill/notes") {
			t.Fatalf("the write changed the repo's stores.yaml:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
}
