package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestOrgTargetMatrix: where each --to lands in a repo linked to org store
// northwind at folder acme (CONFIG_GUIDE: a store's writes go to its
// folder when one is attached). With and without a stores.yaml entry that
// attaches the bare name at the store root: `--to northwind` and `--to
// team` land in acme, `--to northwind/.` in the root, `--to company` in
// org/.
func TestOrgTargetMatrix(t *testing.T) {
	t.Parallel()
	for _, clash := range []bool{false, true} {
		name := "linked only"
		if clash {
			name = "stores.yaml attaches the root too"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
			dir := fileuri.LocalPath(orgURL)
			hook := func(context.Context) ([]attach.Store, error) {
				return []attach.Store{{Name: "northwind", URL: orgURL, Folder: "acme"}, {Name: "northwind/org", URL: orgURL, Folder: "org"}}, nil
			}
			repo := gitRepo(t)
			if clash {
				writeStoresYAML(t, repo, "stores:\n  - name: northwind\n    url: "+orgURL+"\n")
			}
			a := openWith(t, repo, Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
			withProject(t, a)
			for _, c := range []struct{ to, folder string }{
				{"northwind", "acme"}, {"team", "acme"}, {"northwind/.", ""}, {"company", "org"},
			} {
				out, err := run(t, a, opsmemory.OpAdd, `{"body":"to `+c.to+`","to":"`+c.to+`"}`, true)
				if err != nil {
					t.Fatalf("--to %s: %v", c.to, err)
				}
				id := idOf(t, out)
				folder := c.folder
				if folder == "" {
					folder = rowfs.StoreDataDir // the store root folder's rows (D-ad)
				}
				p := filepath.Join(dir, filepath.FromSlash(folder), schema.TableMemories, id+".json")
				if _, serr := os.Stat(p); serr != nil {
					t.Fatalf("--to %s: the row is not in folder %q (%v)", c.to, c.folder, serr)
				}
			}
		})
	}
}

// TestStoreLabel: a store attached at a non-root folder under a bare name
// is labelled "<name>/<folder>"; the repo, a root store, a
// "<store>/<folder>" name, an unknown name and an unopened App keep the
// name as it is.
func TestStoreLabel(t *testing.T) {
	t.Parallel()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "northwind", URL: orgURL, Folder: "acme"}, {Name: "northwind/org", URL: orgURL, Folder: "org"}}, nil
	}
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n  - name: shared\n    url: "+folderStoreAt(t, folderStore, rowfs.VisibilityShareable)+"\n")
	a := openWith(t, repo, Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if got := a.StoreLabel("northwind"); got != "northwind" {
		t.Fatalf("unopened App: %q", got)
	}
	withProject(t, a)
	for in, want := range map[string]string{
		"northwind": "northwind/acme", "northwind/org": "northwind/org", "shared": "shared", "repo": "repo", "nope": "nope",
	} {
		if got := a.StoreLabel(store.Source(in)); got != want {
			t.Errorf("StoreLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
