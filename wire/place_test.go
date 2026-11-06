package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/store"
)

// placeAt opens folder name of the store at dir as attached store n and
// returns its place id (what Open records as engine.Attached.Place).
func placeAt(t *testing.T, dir, name string, n store.Source) string {
	t.Helper()
	b, err := folder.Open(dir, name, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return placeOf(background(), engine.Attached{Name: n, Backend: b})
}

// TestPlaceOf pins FQ4's place id for `_o.moves`: a store folder is
// "<sto_>/<folder prj_>" read from (or minted into) the folder's
// _meta.json (the same id before its first write: rowfs mints it from
// the store id and folder name), stable across reopens and, once
// written, across a folder rename; the store root is the bare sto_ id; an unreadable
// _meta.json falls back to "<sto_>/<folder name>" instead of failing.
func TestPlaceOf(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	dir := strings.TrimPrefix(url, "file://")

	if got := placeAt(t, dir, rowfs.FolderRoot, "agency"); got != folderStore {
		t.Fatalf("store root place = %q, want %q", got, folderStore)
	}

	first := placeAt(t, dir, "acme", "agency/acme")
	sto, prj, ok := strings.Cut(first, "/")
	if !ok || sto != folderStore || !strings.HasPrefix(prj, "prj_") || len(prj) != len("prj_")+32 {
		t.Fatalf("folder place = %q, want %s/prj_<32 hex>", first, folderStore)
	}
	if again := placeAt(t, dir, "acme", "agency/acme"); again != first {
		t.Fatalf("reopen changed the place: %q → %q", first, again)
	}
	if other := placeAt(t, dir, "globex", "agency/globex"); other == first || !strings.HasPrefix(other, folderStore+"/prj_") {
		t.Fatalf("another folder's place = %q (acme %q)", other, first)
	}

	// after the folder's first write its _meta.json holds that id (what
	// rowfs writes then); renamed on disk, the folder keeps its place
	// because the id lives in the file, not in the name
	metaDir := filepath.Join(dir, "acme", rowfs.LoreDir, rowfs.DataDir)
	if err := os.MkdirAll(metaDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, rowfs.MetaFileName), []byte(`{"_v":2,"project_id":"`+prj+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "acme"), filepath.Join(dir, "acme-renamed")); err != nil {
		t.Fatal(err)
	}
	if moved := placeAt(t, dir, "acme-renamed", "agency/acme-renamed"); moved != first {
		t.Fatalf("renamed folder place = %q, want %q", moved, first)
	}

	// unreadable _meta.json: the name-based namespace, not an error
	meta := filepath.Join(dir, "acme-renamed", rowfs.LoreDir, rowfs.DataDir, rowfs.MetaFileName)
	if err := os.WriteFile(meta, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := placeAt(t, dir, "acme-renamed", "agency/acme-renamed"); got != folderStore+"/acme-renamed" {
		t.Fatalf("unreadable meta place = %q, want %s/acme-renamed", got, folderStore)
	}
}

// TestPlaceOfNoMarker: a backend without a store marker (the repo store)
// has no place id; Open keeps the attached name.
func TestPlaceOfNoMarker(t *testing.T) {
	t.Parallel()
	if got := placeOf(background(), engine.Attached{Name: store.SourceRepo, Backend: nil}); got != "" {
		t.Fatalf("no-marker place = %q", got)
	}
}
