package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
)

// TestStoreRootTarget: `--to <store>/.` is the store root. Store attached
// at its root: the write lands there with no folder question and
// stores.yaml is not touched (same place, same name). Store attached at a
// folder: `<store>/.` still never asks (the root holds the marker) and the
// row lands in the root's data folder. A missing real folder still asks.
func TestStoreRootTarget(t *testing.T) {
	for _, c := range []struct {
		name, folderLine string
	}{{"attached at root", ""}, {"attached at a folder", "    folder: acme\n"}} {
		t.Run(c.name, func(t *testing.T) {
			storeDir := t.TempDir()
			sid, err := ids.Parse[ids.Store](folderStore)
			if err != nil {
				t.Fatal(err)
			}
			if err = folder.Init(storeDir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
				t.Fatal(err)
			}
			if c.folderLine != "" {
				if err = os.MkdirAll(filepath.Join(storeDir, "acme", ".lore", "data"), 0o750); err != nil {
					t.Fatal(err)
				}
			}
			repo := gitRepo(t)
			yaml := "stores:\n  - name: northwind\n    url: file://" + filepath.ToSlash(storeDir) + "\n" + c.folderLine
			if err = os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
				t.Fatal(err)
			}
			yamlPath := filepath.Join(repo, filepath.FromSlash(attach.FileName))
			if err = os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			a := newTestApp(t, repo)
			withProject(t, a)
			out, err := run(t, a, opsmemory.OpAdd, `{"body":"at the store root","to":"northwind/."}`, false)
			if err != nil {
				t.Fatalf("--to northwind/.: %v", err)
			}
			id := idOf(t, out)
			if _, err = os.Stat(filepath.Join(storeDir, ".lore", "data", schema.TableMemories, id+".json")); err != nil {
				t.Fatalf("the row is not at the store root: %v", err)
			}
			if c.folderLine == "" {
				if b := mustRead(t, yamlPath); string(b) != yaml {
					t.Fatalf("stores.yaml changed for the root of a root-attached store:\n%s", b)
				}
			}
			if _, err = run(t, a, opsmemory.OpAdd, `{"body":"x","to":"northwind/newfolder"}`, false); !errs.Has(err, errs.NeedsConfirm) {
				t.Fatalf("a missing folder must still ask: %v", err)
			}
		})
	}
}
