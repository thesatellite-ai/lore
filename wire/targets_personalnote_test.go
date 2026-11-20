package wire

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// TestUnattachedNotePersonalFolderNamesNoStoreAdd pins devco D-769: a
// `--to me/<folder>` write in a repo that does not attach the folder told
// the person to run `lore store add --name <name> --url <store-url>
// --folder <folder>`, which commits the personal store's machine-local url
// into the repo's shared .lore/stores.yaml. The note now names the read
// that works from any folder (`--store me/<folder>`) and no attach step;
// an org store's folder keeps its confirmed store add form.
func TestUnattachedNotePersonalFolderNamesNoStoreAdd(t *testing.T) {
	t.Parallel()
	var personal bytes.Buffer
	unattachedNote(&personal, attach.ReservedMe, "me/notes", "notes", "lore task list --store me/notes")
	got := personal.String()
	for _, want := range []string{"me/notes is a folder of your personal store", "--store me/notes", ": lore task list --store me/notes"} {
		if !strings.Contains(got, want) {
			t.Errorf("personal note misses %q: %s", want, got)
		}
	}
	for _, never := range []string{"store add", "--url", "<store-url>"} {
		if strings.Contains(got, never) {
			t.Errorf("personal note names %q (would commit the machine-local url into stores.yaml): %s", never, got)
		}
	}
	var org bytes.Buffer
	unattachedNote(&org, "acme", "acme/tips", "tips", "")
	if want := "lore store add --name <name> --url <store-url> --folder tips --yes"; !strings.Contains(org.String(), want) {
		t.Errorf("org note misses %q: %s", want, org.String())
	}
}

// TestPersonalReadCommandRunsForTheKind pins the read the D-769 note names:
// the list of the kind the op writes (`lore task list --store me/notes`
// after a task add), and a folder search for an op whose noun has no list
// (row move). Each is a real op, so the printed command runs.
func TestPersonalReadCommandRunsForTheKind(t *testing.T) {
	t.Parallel()
	a := newTestAppHome(t, t.TempDir(), t.TempDir())
	for _, c := range []struct{ write, read, want string }{
		{"task.add", "task.list", "lore task list --store me/notes"},
		{"memory.add", "memory.list", "lore memory list --store me/notes"},
		{"row.move", opssearch.OpRun, `lore search --store me/notes --query "<query>"`},
	} {
		e, ok := a.Registry().Lookup(c.write)
		if !ok {
			t.Fatalf("no op %s", c.write)
		}
		if got := a.personalReadCommand(e, "me/notes"); got != c.want {
			t.Errorf("%s: read command %q, want %q", c.write, got, c.want)
		}
		if _, ok := a.Registry().Lookup(c.read); !ok {
			t.Errorf("%s: the named read %q is no op %s", c.write, c.want, c.read)
		}
	}
}
