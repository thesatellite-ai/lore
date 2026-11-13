package wire

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
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
	if err = os.MkdirAll(filepath.Join(storeDir, "acme"), 0o750); err != nil {
		t.Fatal(err)
	}
	// a folder is one with its _meta.json (rowfs.HasStoreFolder)
	if err = os.WriteFile(filepath.Join(storeDir, "acme", rowfs.MetaFileName), []byte(`{"_v":1}`+"\n"), 0o600); err != nil {
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
	t.Parallel()
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
		if _, serr := os.Stat(filepath.Join(storeDir, c.folder, schema.TableMemories, id+".json")); serr != nil {
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

	// team and company with no org: E_NOT_FOUND (STORE_SERVER_SPEC STORE
	// TARGETS: "A repo with no org link (or no such folder) gets
	// E_NOT_FOUND", hint naming `lore org link` or --to <store>/<folder>);
	// the bare shortcut says which org folder is missing, a folder under a
	// reserved word is no attached store. Nothing is written either way.
	for _, c := range []struct{ to, msg, hint string }{
		{"team", "--to team: this repository has no org store folder linked to it", "lore org link --org <org> --project <folder>"},
		{"company", "--to company: this repository has no org store org folder", "--to <store>/<folder>"},
		{"team/x", "no store named team/x is attached", ""},
		{"company/x", "no store named company/x is attached", ""},
	} {
		before := string(mustRead(t, yamlPath))
		_, err = run(t, a, opsmemory.OpAdd, `{"body":"no org","to":"`+c.to+`","create":true}`, true)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.NotFound || !strings.Contains(e.Message, c.msg) || !strings.Contains(e.Hint, c.hint) {
			t.Fatalf("--to %s with no org: %#v", c.to, err)
		}
		if after := string(mustRead(t, yamlPath)); after != before {
			t.Fatalf("--to %s changed stores.yaml", c.to)
		}
		if _, serr := os.Stat(filepath.Join(storeDir, c.to)); !os.IsNotExist(serr) {
			t.Fatalf("--to %s made a folder in the store: %v", c.to, serr)
		}
	}
}

// TestPersonalStoreLoadedOnce (STORE TARGETS, BATCH 15): the personal
// store `me` is attached last, and skipped when stores.yaml already
// attaches the same url at its root (the store loads once, under the
// stores.yaml name); the same url at a folder, or another url, keeps `me`.
func TestPersonalStoreLoadedOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		yaml    func(url string) string
		wantMe  bool
		wantErr bool
	}{
		{"same url at the root: me skipped", func(u string) string { return "stores:\n  - name: northwind\n    url: " + u + "\n" }, false, false},
		{"same url with a trailing slash: me skipped", func(u string) string { return "stores:\n  - name: northwind\n    url: " + u + "/\n" }, false, false},
		{"same url at a folder: me kept", func(u string) string {
			return "stores:\n  - name: northwind\n    url: " + u + "\n    folder: acme\n"
		}, true, false},
		{"no stores.yaml: me attached", nil, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			repo, storeDir, yamlPath, _ := folderStoreRepo(t)
			url := fileuri.URL(storeDir, "")
			if c.yaml == nil {
				if err := os.Remove(yamlPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(yamlPath, []byte(c.yaml(url)), 0o600); err != nil {
				t.Fatal(err)
			}
			a := newTestApp(t, repo)
			if err := os.MkdirAll(a.opts.Home, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath(a.opts.Home), []byte("personal:\n  store: "+url+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Engine(background()); err != nil {
				t.Fatal(err)
			}
			got := a.Stores()
			if hasMe := slices.Contains(got, store.Source(attach.ReservedMe)); hasMe != c.wantMe {
				t.Fatalf("me attached = %v, want %v: %v", hasMe, c.wantMe, got)
			}
			n := 0
			for _, s := range got {
				if s == "northwind" || s == store.Source(attach.ReservedMe) {
					n++
				}
			}
			want := 1
			if c.wantMe && c.yaml != nil {
				want = 2 // a folder entry and the root are two places
			}
			if n != want {
				t.Fatalf("stores = %v, want %d of northwind/me", got, want)
			}
		})
	}
}

// engineRefIn names a memory row in src.
func engineRefIn(src store.Source, id string) engine.Ref {
	r := engineRef(schema.TableMemories, id)
	r.Store = src
	return r
}
