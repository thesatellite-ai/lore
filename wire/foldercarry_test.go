package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// TestFolderCarryLinks (devco D-992): the repo links of the folder a move
// carries are read from its _meta.json and added to the new folder's,
// each once (a second run adds nothing), keeping the new folder's own
// fields; a folder with no links (or no _meta.json) carries none.
func TestFolderCarryLinks(t *testing.T) {
	t.Parallel()
	from, to := t.TempDir(), t.TempDir()
	write := func(dir, body string) string {
		p := filepath.Join(dir, filepath.FromSlash(rowfs.LayoutStore.StorePath("shop", rowfs.MetaFileName)))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write(from, `{"_v":2,"project_id":"prj_old","links":[{"project_id":"prj_web","remote":"example.com/acme/web","own":true}]}`)
	newMeta := write(to, `{"_v":2,"project_id":"prj_new","links":[{"project_id":"prj_api","remote":"example.com/acme/api"}]}`)

	links, err := folderMetaLinks(from, "shop")
	if err != nil || len(links) != 1 {
		t.Fatalf("links of the old folder: %v %v", links, err)
	}
	for i, want := range []bool{true, false} {
		added, aerr := addMetaLinks(newMeta, links)
		if aerr != nil || added != want {
			t.Fatalf("run %d: added %v (want %v): %v", i+1, added, want, aerr)
		}
	}
	raw, err := os.ReadFile(newMeta)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ProjectID string            `json:"project_id"`
		Links     []json.RawMessage `json:"links"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ProjectID != "prj_new" || len(doc.Links) != 2 || !strings.Contains(string(raw), "example.com/acme/web") {
		t.Fatalf("the new folder's _meta.json:\n%s", raw)
	}
	if none, nerr := folderMetaLinks(t.TempDir(), "shop"); nerr != nil || len(none) != 0 {
		t.Fatalf("a folder with no _meta.json: %v %v", none, nerr)
	}
}
