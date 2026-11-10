package wire

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// another store id for the "different store" set-url case.
const otherStoreID = "sto_01a1a5aca7f57659be7320c5f4b70099"

// writeV1Marker writes a v0.1.17 lore-store.yaml (version 1, no id) in
// dir: rowfs.WriteMarker refuses a marker without an id, so the bytes are
// written as v0.1.17 wrote them.
func writeV1Marker(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rowfs.MarkerFile), []byte("lore_store: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// storeDir is the folder behind a file:// store url.
func storeDir(url string) string { return strings.TrimPrefix(url, "file://") }

// TestSetVisibility (OV23, CFG4): store visibility rewrites the store's
// lore-store.yaml; the same value changes nothing; a v0.1.17 marker (no
// sto_ id) is E_OLD_LORE naming migrate run and is left as it was; an
// unknown store is E_NOT_FOUND; a value outside internal|shareable is
// refused before anything is read.
func TestSetVisibility(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+url+"\n")
	a := openWith(t, repo, Options{})

	var out opsstores.VisibilityOut
	res, err := run(t, a, opsstores.OpVisibility, `{"name":"kb","visibility":"shareable"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Changed || out.Previous != "internal" || out.Visibility != "shareable" {
		t.Fatalf("visibility: %+v", out)
	}
	m, err := rowfs.ReadMarker(storeDir(url))
	if err != nil || m.Visibility != rowfs.VisibilityShareable || m.ID.String() != folderStore {
		t.Fatalf("marker after: %+v %v", m, err)
	}
	res, err = run(t, a, opsstores.OpVisibility, `{"name":"kb","visibility":"shareable"}`, true)
	if err != nil || strings.Contains(res, `"changed":true`) {
		t.Fatalf("same value again: %s %v", res, err)
	}
	if _, err = run(t, a, opsstores.OpVisibility, `{"name":"nope","visibility":"internal"}`, true); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown store: %v", err)
	}
	for _, bad := range []string{"public", "", "Internal"} {
		if _, err = run(t, a, opsstores.OpVisibility, `{"name":"kb","visibility":"`+bad+`"}`, true); err == nil || errs.Has(err, errs.Internal) {
			t.Fatalf("visibility %q: %v, want an input refusal", bad, err)
		}
	}

	// a v0.1.17 store: marker without an id
	oldDir := t.TempDir()
	writeV1Marker(t, oldDir)
	before := mustRead(t, filepath.Join(oldDir, rowfs.MarkerFile))
	repo2 := gitRepo(t)
	writeStoresYAML(t, repo2, "stores:\n  - name: old\n    url: file://"+filepath.ToSlash(oldDir)+"\n")
	b := openWith(t, repo2, Options{})
	_, err = run(t, b, opsstores.OpVisibility, `{"name":"old","visibility":"shareable"}`, true)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.OldLore || !strings.Contains(e.Hint, "migrate run") {
		t.Fatalf("v0.1.17 marker: %v, want E_OLD_LORE naming migrate run", err)
	}
	if after := mustRead(t, filepath.Join(oldDir, rowfs.MarkerFile)); string(after) != string(before) {
		t.Fatalf("a refused visibility change rewrote the v0.1.17 marker:\n%s", after)
	}
}

// TestAddFolder (OV23, STORE TARGETS): store folder add writes the
// folder's _meta.json with the natural prj_ id a first row write would
// mint (so two clones agree and the place id is the same); a second add
// says already and keeps the id; a bad folder name is refused and
// creates nothing.
func TestAddFolder(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+url+"\n")
	a := openWith(t, repo, Options{})

	minted := placeAt(t, storeDir(url), "acme", "kb/acme") // before the folder exists
	var out opsstores.FolderAddOut
	res, err := run(t, a, opsstores.OpFolderAdd, `{"store":"kb","folder":"acme"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatal(err)
	}
	if out.Already || out.Folder != "acme" || folderStore+"/"+out.ProjectID != minted {
		t.Fatalf("folder add: %+v, minted place %s", out, minted)
	}
	meta := rowFile(t, filepath.Join(storeDir(url), "acme", rowfs.LoreDir, rowfs.DataDir, rowfs.MetaFileName))
	if string(meta["project_id"]) != `"`+out.ProjectID+`"` {
		t.Fatalf("_meta.json = %v, want project_id %s", meta, out.ProjectID)
	}
	res, err = run(t, a, opsstores.OpFolderAdd, `{"store":"kb","folder":"acme"}`, true)
	if err != nil || !strings.Contains(res, `"already":true`) || !strings.Contains(res, out.ProjectID) {
		t.Fatalf("second add: %s %v", res, err)
	}
	for _, bad := range []string{"../escape", "Bad Name", ".hidden", "a/../../b"} {
		if _, err = run(t, a, opsstores.OpFolderAdd, `{"store":"kb","folder":"`+bad+`"}`, true); err == nil || errs.Has(err, errs.Internal) {
			t.Fatalf("folder %q: %v, want an input refusal", bad, err)
		}
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(storeDir(url)), "escape")); !os.IsNotExist(err) {
		t.Fatal("a refused folder name created a folder outside the store")
	}
	if _, err = run(t, a, opsstores.OpFolderAdd, `{"store":"nope","folder":"acme"}`, true); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown store: %v", err)
	}
}

// copyStore copies a folder store to a new dir (the same store at a new
// address, as after moving the folder) and returns its url.
func copyStore(t *testing.T, url string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "moved")
	if err := os.CopyFS(dst, os.DirFS(storeDir(url))); err != nil {
		t.Fatalf("copy store: %v", err)
	}
	return "file://" + filepath.ToSlash(dst)
}

// TestSetStoreURL (OV61, PROV 11.9): store set-url re-points a stores.yaml
// entry at the same store's new address (same sto_ id) and keeps the
// entry's other keys and comments; an address holding another store is
// E_INVALID_INPUT and changes nothing; an old address that no longer
// opens is re-pointed with a note; a folder that is no store is
// E_NOT_A_STORE; the same url is a no-op; an unknown name E_NOT_FOUND.
func TestSetStoreURL(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	const comment = "# our knowledge base\n"
	writeStoresYAML(t, repo, comment+"stores:\n  - name: kb\n    url: "+url+"\n    read_only: true\n")
	yamlPath := filepath.Join(repo, filepath.FromSlash(attach.FileName))
	a := openWith(t, repo, Options{})

	setURL := func(name, to string) (opsstores.SetURLOut, error) {
		var out opsstores.SetURLOut
		res, err := run(t, a, opsstores.OpSetURL, `{"name":"`+name+`","url":"`+to+`"}`, true)
		if err != nil {
			return out, err
		}
		if jerr := json.Unmarshal([]byte(res), &out); jerr != nil {
			t.Fatal(jerr)
		}
		return out, nil
	}

	if out, err := setURL("kb", url); err != nil || out.Changed || out.ID != folderStore {
		t.Fatalf("same url: %+v %v", out, err)
	}

	other := folderStoreAt(t, otherStoreID, rowfs.VisibilityInternal)
	before := string(mustRead(t, yamlPath))
	if _, err := setURL("kb", other); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "another store") {
		t.Fatalf("another store's address: %v", err)
	}
	if _, err := setURL("kb", "file://"+filepath.ToSlash(t.TempDir())); !errs.Has(err, errs.NotAStore) {
		t.Fatalf("not a store: %v", err)
	}
	if _, err := setURL("nope", url); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown name: %v", err)
	}
	if after := string(mustRead(t, yamlPath)); after != before {
		t.Fatalf("a refused set-url changed stores.yaml:\n%s", after)
	}

	moved := copyStore(t, url)
	out, err := setURL("kb", moved)
	if err != nil || !out.Changed || out.ID != folderStore || out.Previous != url || out.Note != "" {
		t.Fatalf("same store, new address: %+v %v", out, err)
	}
	body := string(mustRead(t, yamlPath))
	if !strings.HasPrefix(body, comment) || !strings.Contains(body, "url: "+moved) || strings.Contains(body, "url: "+url+"\n") || !strings.Contains(body, "read_only: true") {
		t.Fatalf("stores.yaml after set-url:\n%s", body)
	}

	// the old address is gone: re-pointing still works, with a note
	again := copyStore(t, moved)
	if err = os.RemoveAll(storeDir(moved)); err != nil {
		t.Fatal(err)
	}
	if out, err = setURL("kb", again); err != nil || !out.Changed || out.Note == "" {
		t.Fatalf("old address gone: %+v %v", out, err)
	}
}
