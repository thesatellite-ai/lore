package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
)

// TestOrgTargetMatrix: where each --to lands in a repo linked to org store
// northwind at folder acme (CONFIG_GUIDE: a store's writes go to its
// folder when one is attached). With and without a stores.yaml entry that
// attaches the bare name at the store root: `--to northwind` and `--to
// team` land in acme, `--to northwind/.` in the root, `--to company` in
// org/.
func TestOrgTargetMatrix(t *testing.T) {
	for _, clash := range []bool{false, true} {
		name := "linked only"
		if clash {
			name = "stores.yaml attaches the root too"
		}
		t.Run(name, func(t *testing.T) {
			orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
			dir := strings.TrimPrefix(orgURL, "file://")
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
				p := filepath.Join(dir, filepath.FromSlash(c.folder), ".lore", "data", schema.TableMemories, id+".json")
				if _, serr := os.Stat(p); serr != nil {
					t.Fatalf("--to %s: the row is not in folder %q (%v)", c.to, c.folder, serr)
				}
			}
		})
	}
}
