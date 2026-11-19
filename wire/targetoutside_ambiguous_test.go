package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRowLookupSeesAnIDInEveryFolder (devco D-689): an id an older lore
// derived without the folder can be live in two folders of one store. The
// outside-a-repo lookup (home with --setup) answered the first folder
// holding it, so `show --id` printed one of two rows silently. It now
// scans every folder: with no --store the id is refused E_AMBIGUOUS_ID
// naming both places with a read for each, and a --store naming one place
// reads that place's row.
func TestRowLookupSeesAnIDInEveryFolder(t *testing.T) {
	const id = "pol_0123456789abcdef0123456789abcdef"
	clone := t.TempDir()
	for _, folder := range []string{"web", "api"} {
		dir := filepath.Join(clone, folder, "policies")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"_id":"`+id+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scan := orgFolderScan{Folders: []orgFolder{
		{Label: "acme-co", Org: "acme-co", Folder: "api", Dir: clone, URL: "https://git.example.test/acme-co/store.git"},
		{Label: "acme-co", Org: "acme-co", Folder: "web", Dir: clone, URL: "https://git.example.test/acme-co/store.git"},
		// the same folder reached twice counts once
		{Label: "acme-co", Org: "acme-co", Folder: "web", Dir: clone, URL: "https://git.example.test/acme-co/store.git"},
	}}
	a := &App{opts: Options{Home: t.TempDir()}}
	look := &rowLookup{id: id}
	if _, ok := a.rowFolderIn(context.Background(), scan, id, look); !ok {
		t.Fatal("no folder found holding the id")
	}
	if len(look.live) != 2 {
		t.Fatalf("live holders %+v, want acme-co/api and acme-co/web", look.live)
	}
	err := look.answer("")
	if errs.CodeOf(err) != errs.AmbiguousID {
		t.Fatalf("answer with no --store: %v, want E_AMBIGUOUS_ID", err)
	}
	all := err.Error() + " " + hintOf(err)
	for _, want := range []string{"acme-co/api", "acme-co/web", "`lore show --id " + id + " --store acme-co/web`", "`lore show --id " + id + " --store acme-co/api`"} {
		if !strings.Contains(all, want) {
			t.Fatalf("refusal %q lacks %q", all, want)
		}
	}
	if err := look.answer("acme-co/web"); err != nil {
		t.Fatalf("answer naming a place: %v", err)
	}
	if h, ok := look.holderNamed("acme-co/web"); !ok || h.Folder != "web" {
		t.Fatalf("holderNamed(acme-co/web) = %+v, %v; want the web folder", h, ok)
	}
}
