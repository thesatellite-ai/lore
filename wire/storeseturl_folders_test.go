package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestSetStoreURLFolderEntries (B2-sync): a folder attachment of a store
// (stores.yaml entry "kb/acme", written by the first --to kb/acme) carries
// the store's url, so store set-url re-points it with the store entry;
// before the fix it kept the old address and every later command failed
// E_NOT_A_STORE once the old folder was gone. Another store whose name
// only shares the first letters (kb_other/acme) is left alone. A second
// set-url to the same url repairs a folder entry an older lore left stale.
func TestSetStoreURLFolderEntries(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	otherURL := folderStoreAt(t, otherStoreID, rowfs.VisibilityInternal)
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+url+"\n  - name: kb_other\n    url: "+otherURL+"\n")
	home := t.TempDir()
	a := openWith(t, repo, Options{Home: home})
	for _, in := range []string{`{"store":"kb","folder":"acme"}`, `{"store":"kb_other","folder":"acme"}`} {
		if _, err := run(t, a, opsstores.OpFolderAdd, in, true); err != nil {
			t.Fatal(err)
		}
	}
	entries := func(kbURL string) string {
		return "stores:\n  - name: kb\n    url: " + kbURL + "\n  - name: kb/acme\n    url: " + url + "\n    folder: acme\n" +
			"  - name: kb_other\n    url: " + otherURL + "\n  - name: kb_other/acme\n    url: " + otherURL + "\n    folder: acme\n"
	}
	writeStoresYAML(t, repo, entries(url))
	_ = a.Close()

	moved := copyStore(t, url)
	if err := os.RemoveAll(storeDir(url)); err != nil {
		t.Fatal(err)
	}
	a = openWith(t, repo, Options{Home: home})
	res, err := run(t, a, opsstores.OpSetURL, `{"name":"kb","url":"`+moved+`"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var out opsstores.SetURLOut
	if err = json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Changed || !slices.Equal(out.Folders, []string{"kb/acme"}) {
		t.Fatalf("set-url: %+v, want changed with folders [kb/acme]", out)
	}
	cfg := readStores(t, repo)
	want := map[string]string{"kb": moved, "kb/acme": moved, "kb_other": otherURL, "kb_other/acme": otherURL}
	for _, s := range cfg.Stores {
		if want[string(s.Name)] != s.URL {
			t.Fatalf("entry %s url %s, want %s", s.Name, s.URL, want[string(s.Name)])
		}
	}
	_ = a.Close()

	// the repo is usable again: a read opens every attached store
	a = openWith(t, repo, Options{Home: home})
	if _, err = run(t, a, opsmemory.OpList, `{}`, true); err != nil {
		t.Fatalf("memory list after set-url: %v", err)
	}
	_ = a.Close()

	// an entry an older lore left stale: set-url to the current url repairs it
	writeStoresYAML(t, repo, entries(moved))
	a = openWith(t, repo, Options{Home: home})
	res, err = run(t, a, opsstores.OpSetURL, `{"name":"kb","url":"`+moved+`"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Changed || !slices.Equal(out.Folders, []string{"kb/acme"}) {
		t.Fatalf("repair: %+v, want changed with folders [kb/acme]", out)
	}
	for _, s := range readStores(t, repo).Stores {
		if want[string(s.Name)] != s.URL {
			t.Fatalf("after repair: entry %s url %s, want %s", s.Name, s.URL, want[string(s.Name)])
		}
	}
}

// readStores parses repo's .lore/stores.yaml.
func readStores(t *testing.T, repo string) attach.Config {
	t.Helper()
	cfg, err := attach.Parse(mustRead(t, filepath.Join(repo, filepath.FromSlash(attach.FileName))))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
