package wire

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// folderStoreRepo is a repo with the folder store "northwind" (folder acme
// already made) attached at its root in stores.yaml.
func folderStoreRepo(t *testing.T) (repo, storeDir, yamlPath, yaml string) {
	t.Helper()
	storeDir = t.TempDir()
	sid, err := ids.Parse[ids.Store](folderStore)
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(storeDir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(storeDir, "acme", ".lore", "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	repo = gitRepo(t)
	yaml = "stores:\n  - name: northwind\n    url: file://" + filepath.ToSlash(storeDir) + "\n"
	if err = os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	yamlPath = filepath.Join(repo, filepath.FromSlash(attach.FileName))
	if err = os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, storeDir, yamlPath, yaml
}

// TestFolderTargetConfirmThenCreate (STORE TARGETS, PL-CREATE-1): `--to
// <store>/<folder>` for a folder the store lacks is E_NEEDS_CONFIRM naming
// the existing folders, writing nothing (no row, no folder, stores.yaml
// untouched); --create or a yes creates it, lands the row there and
// records "<store>/<folder>" in stores.yaml; the same App, already open
// (a long-lived `lore api`), reopens and reads the new row; a fresh App
// attaches the recorded target. team/ and company/ with no org are refused
// before anything is written.
func TestFolderTargetConfirmThenCreate(t *testing.T) {
	repo, storeDir, yamlPath, yaml := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"warm the engine"}`, false); err != nil {
		t.Fatal(err) // the engine is open from here on: the next targets reopen it
	}
	_, err := run(t, a, opsmemory.OpAdd, `{"body":"typo","to":"northwind/acmee"}`, false)
	if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "has no folder acmee yet; nothing written (existing: acme)") {
		t.Fatalf("missing folder: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(storeDir, "acmee")); !os.IsNotExist(serr) {
		t.Fatalf("a refused target made the folder: %v", serr)
	}
	if b := mustRead(t, yamlPath); string(b) != yaml {
		t.Fatalf("a refused target changed stores.yaml:\n%s", b)
	}

	for _, c := range []struct {
		folder, body string
		yes          bool
	}{
		{"beta", `{"body":"made with --create","to":"northwind/beta","create":true}`, false},
		{"gamma", `{"body":"made with yes","to":"northwind/gamma"}`, true},
	} {
		out, rerr := run(t, a, opsmemory.OpAdd, c.body, c.yes)
		if rerr != nil {
			t.Fatalf("%s: %v", c.folder, rerr)
		}
		id := idOf(t, out)
		if _, serr := os.Stat(filepath.Join(storeDir, c.folder, ".lore", "data", schema.TableMemories, id+".json")); serr != nil {
			t.Fatalf("%s: the row is not in the new folder: %v", c.folder, serr)
		}
		if b := string(mustRead(t, yamlPath)); !strings.Contains(b, "name: northwind/"+c.folder) || !strings.Contains(b, "folder: "+c.folder) {
			t.Fatalf("%s: stores.yaml does not record the target:\n%s", c.folder, b)
		}
		if _, r, gerr := a.Get(background(), engineRefIn(store.Source("northwind/"+c.folder), id)); gerr != nil || r.Deleted {
			t.Fatalf("%s: the open App does not read the new row: %v", c.folder, gerr)
		}
		b := newTestApp(t, repo)
		if _, eerr := b.Engine(background()); eerr != nil {
			t.Fatal(eerr)
		}
		if !slices.Contains(b.Stores(), store.Source("northwind/"+c.folder)) {
			t.Fatalf("%s: a fresh App does not attach the recorded target: %v", c.folder, b.Stores())
		}
	}

	for _, to := range []string{"team/x", "company/x"} {
		before := string(mustRead(t, yamlPath))
		if _, err = run(t, a, opsmemory.OpAdd, `{"body":"no org","to":"`+to+`","create":true}`, true); err == nil {
			t.Fatalf("--to %s with no org was accepted", to)
		}
		if after := string(mustRead(t, yamlPath)); after != before {
			t.Fatalf("--to %s changed stores.yaml", to)
		}
	}
}

// engineRefIn names a memory row in src.
func engineRefIn(src store.Source, id string) engine.Ref {
	r := engineRef(schema.TableMemories, id)
	r.Store = src
	return r
}
