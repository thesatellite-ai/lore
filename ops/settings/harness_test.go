package settings_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/wire"
)

// Store ids of the two folder stores of multiStore (arbitrary hex).
const (
	teamStoreID = "sto_0e45506e64357c94a87e70ef1c6bbb01"
	orgStoreID  = "sto_0e45506e64357c94a87e70ef1c6bbb02"
)

// newStore makes an initialised folder store and returns its file:// url.
func newStore(t *testing.T, id string) string {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.Parse[ids.Store](id)
	if err != nil {
		t.Fatal(err)
	}
	if err := folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	return "file://" + filepath.ToSlash(dir)
}

// multiStore is a harness whose repo attaches, by stores.yaml, the folder
// store "kb" at its root, its folder "kb/web" and its org folder
// "kb/org"; and, by the org link (Options.OrgStores, never written to a
// file), a second store's linked folder under the plain name "agency"
// (what `--to team` resolves to in a client repo) and its company folder
// "agency/org".
func multiStore(t *testing.T, mut ...func(*wire.Options)) *opstest.H {
	t.Helper()
	team := newStore(t, teamStoreID)
	org := newStore(t, orgStoreID)
	orgStores := func(o *wire.Options) {
		o.OrgStores = func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "agency", URL: org, Folder: "client1"}, {Name: "agency/org", URL: org, Folder: "org"}}, nil
		}
	}
	h := opstest.Bare(t, append([]func(*wire.Options){orgStores}, mut...)...)
	yaml := "stores:\n" +
		"  - name: kb\n    url: " + team + "\n" +
		"  - name: kb/web\n    url: " + team + "\n    folder: web\n" +
		"  - name: kb/org\n    url: " + team + "\n    folder: org\n"
	if err := os.MkdirAll(filepath.Join(h.Repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Repo, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	h.SeedProject()
	return h
}
