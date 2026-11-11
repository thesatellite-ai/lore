package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// removeFolder runs store.folder.remove with input (yes = --yes).
func removeFolder(t *testing.T, a *App, input string, yes bool) (opsstores.FolderRemoveOut, error) {
	t.Helper()
	res, err := run(t, a, opsstores.OpFolderRemove, input, yes)
	if err != nil {
		return opsstores.FolderRemoveOut{}, err
	}
	var out opsstores.FolderRemoveOut
	if jerr := json.Unmarshal([]byte(res), &out); jerr != nil {
		t.Fatal(jerr)
	}
	return out, nil
}

// folderMetaPath is folder f's _meta.json in the folder store at url.
func folderMetaPath(url, f string) string {
	return filepath.Join(storeDir(url), f, rowfs.LoreDir, rowfs.DataDir, rowfs.MetaFileName)
}

// TestFolderRemoveEmpty: an empty folder is removed after --yes (asked
// first), folder list stops showing it, a second remove is E_NOT_FOUND,
// the name can be made again, and the org folder is never removed.
func TestFolderRemoveEmpty(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	a := openWith(t, t.TempDir(), Options{OrgStores: orgAt(url)})
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"team","folder":"client_old"}`, false); err != nil {
		t.Fatal(err)
	}
	_, err := removeFolder(t, a, `{"store":"team","folder":"client_old"}`, false)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.NeedsConfirm || !strings.Contains(e.Message, "empty folder client_old") {
		t.Fatalf("remove without --yes: %v, want E_NEEDS_CONFIRM naming the empty folder", err)
	}
	if _, serr := os.Stat(folderMetaPath(url, "client_old")); serr != nil {
		t.Fatalf("asking removed the folder: %v", serr)
	}
	out, err := removeFolder(t, a, `{"store":"team","folder":"client_old"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Purged || out.Rows != 0 || out.Links != 0 || !strings.HasPrefix(out.ProjectID, "prj_") || out.Store != "acme" {
		t.Fatalf("remove = %+v", out)
	}
	if _, serr := os.Stat(filepath.Join(storeDir(url), "client_old")); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("the folder is still on disk: %v", serr)
	}
	if got := folderList(t, a, `{"store":"team"}`); got.Count != 0 {
		t.Fatalf("folder list after remove: %+v", got)
	}
	if _, err = removeFolder(t, a, `{"store":"team","folder":"client_old"}`, true); !errs.Has(err, errs.NotFound) {
		t.Fatalf("second remove: %v, want E_NOT_FOUND", err)
	}
	if _, err = run(t, a, opsstores.OpFolderAdd, `{"store":"team","folder":"client_old"}`, false); err != nil {
		t.Fatalf("the name is free again: %v", err)
	}
	if _, err = removeFolder(t, a, `{"store":"team","folder":"org"}`, true); !errs.Has(err, errs.Refused) {
		t.Fatalf("remove the org folder: %v, want E_REFUSED", err)
	}
	if _, err = removeFolder(t, a, `{"store":"team","folder":"Bad Name"}`, true); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("remove a bad name: %v, want E_INVALID_INPUT", err)
	}
}

// TestFolderRemoveWithRows: a folder with a row and a repo link is refused
// without --purge, naming the counts and each next step; --purge with
// --yes alone asks for the literal --confirm (DQ2); with --confirm the
// live row goes to the trash under one bat_, the folder's _meta.json and
// links go, and the trashed row file stays.
func TestFolderRemoveWithRows(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	a := openWith(t, t.TempDir(), Options{OrgStores: orgAt(url)})
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"team","folder":"client_a"}`, false); err != nil {
		t.Fatal(err)
	}
	// one row (the folder's project row) and one repo link
	x, err := a.openFolderApp(background(), "", attach.Store{Name: "client_a", URL: url, Folder: "client_a"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := x.state(background())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := x.rosterProject(background(), st, "client_a", "client_a")
	if err != nil {
		t.Fatal(err)
	}
	if err = x.Close(); err != nil {
		t.Fatal(err)
	}
	meta := folderMetaPath(url, "client_a")
	raw, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["links"] = json.RawMessage(`[{"project_id":"prj_0190f1a2b3c47d5e8f90a1b2c3d4e5f6","remote":"github.com/client-co/hub"}]`)
	if raw, err = json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(meta, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = removeFolder(t, a, `{"store":"team","folder":"client_a"}`, true)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.Refused || !strings.Contains(e.Message, "1 row and 1 repo link") {
		t.Fatalf("remove a folder with rows: %v, want E_REFUSED naming 1 row and 1 repo link", err)
	}
	for _, want := range []string{"lore move", "lore org unlink", "--purge"} {
		if !strings.Contains(e.Hint, want) {
			t.Errorf("hint %q lacks %q", e.Hint, want)
		}
	}

	_, err = removeFolder(t, a, `{"store":"team","folder":"client_a","purge":true}`, true)
	if !errors.As(err, &e) || e.Code != errs.NeedsConfirm || e.Next == nil || !strings.Contains(strings.Join(e.Next.Args, " "), "--confirm") || strings.Contains(strings.Join(e.Next.Args, " "), "--yes") {
		t.Fatalf("--purge --yes: %v, want E_NEEDS_CONFIRM naming --confirm, never --yes", err)
	}
	if _, serr := os.Stat(meta); serr != nil {
		t.Fatalf("asking changed the folder: %v", serr)
	}

	out, err := removeFolder(t, a, `{"store":"team","folder":"client_a","purge":true,"confirm":true,"reason":"made in the wrong store"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Purged || out.Rows != 1 || out.Links != 1 || !strings.HasPrefix(out.Batch, "bat_") {
		t.Fatalf("remove --purge = %+v", out)
	}
	if _, serr := os.Stat(meta); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("_meta.json is still there: %v", serr)
	}
	rowFile := filepath.Join(storeDir(url), "client_a", rowfs.LoreDir, rowfs.DataDir, "projects", ref.String()+rowfs.RowFileExt)
	body, err := os.ReadFile(rowFile)
	if err != nil {
		t.Fatalf("the trashed row file is gone: %v", err)
	}
	if !bytes.Contains(body, []byte(trash.FieldDeletedAt)) || !bytes.Contains(body, []byte(out.Batch)) || !bytes.Contains(body, []byte("made in the wrong store")) {
		t.Fatalf("the row is not in the trash under %s: %s", out.Batch, body)
	}
	if got := folderList(t, a, `{"store":"team"}`); got.Count != 0 {
		t.Fatalf("folder list after the purge: %+v", got)
	}
}

// TestFolderRemoveOrgAdmin: an org store's folders are removed by the
// org's admin only; this machine without an admin setup of the org is
// refused naming the org, before anything is asked or removed.
func TestFolderRemoveOrgAdmin(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	// the store is acme's: its org folder holds acme's lore-org.yaml (an
	// org entry needs a hosted url, so the store is known by its file)
	orgFile := filepath.Join(storeDir(url), rowfs.FolderOrg, rowfs.LoreDir, "lore-org.yaml")
	if err := os.MkdirAll(filepath.Dir(orgFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orgFile, []byte("name: acme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := "setups:\n  - name: work\n    kind: member\n    org: acme\n"
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, t.TempDir(), Options{Home: home, OrgStores: orgAt(url)})
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"team","folder":"client_x"}`, false); err != nil {
		t.Fatal(err)
	}
	_, err := removeFolder(t, a, `{"store":"team","folder":"client_x"}`, true)
	if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "only an admin of acme") {
		t.Fatalf("a member removes an org folder: %v, want E_REFUSED naming the admin rule", err)
	}
	if _, serr := os.Stat(folderMetaPath(url, "client_x")); serr != nil {
		t.Fatalf("the refused remove changed the store: %v", serr)
	}
	admin := strings.Replace(cfg, "kind: member", "kind: admin", 1)
	if err = os.WriteFile(filepath.Join(home, "config.yaml"), []byte(admin), 0o600); err != nil {
		t.Fatal(err)
	}
	b := openWith(t, t.TempDir(), Options{Home: home, OrgStores: orgAt(url)})
	if _, err = removeFolder(t, b, `{"store":"team","folder":"client_x"}`, true); err != nil {
		t.Fatalf("the org's admin removes it: %v", err)
	}
}

// TestFolderRemovePersonOnly: agents.person_only listing the operation
// refuses a coding agent outside a repo; a person passes (the agent rule:
// allowed by default, strict by policy).
func TestFolderRemovePersonOnly(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("policies:\n  agents.person_only: [store.folder.remove]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, t.TempDir(), Options{Home: home, OrgStores: orgAt(url)})
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"team","folder":"x"}`, false); err != nil {
		t.Fatal(err)
	}
	e, ok := a.Registry().Lookup(opsstores.OpFolderRemove)
	if !ok {
		t.Fatal("no store.folder.remove")
	}
	_, err := a.Run(background(), e, op.JSONDecoder{Body: []byte(`{"store":"team","folder":"x"}`)}, op.Caller{Agent: "claude_code", AgentFixed: true, Yes: true})
	if err == nil || !strings.Contains(err.Error(), "agents.person_only") {
		t.Fatalf("agent: %v, want the agents.person_only refusal", err)
	}
	if _, serr := os.Stat(folderMetaPath(url, "x")); serr != nil {
		t.Fatalf("the refused agent changed the store: %v", serr)
	}
	if _, err = removeFolder(t, a, `{"store":"team","folder":"x"}`, true); err != nil {
		t.Fatalf("person: %v", err)
	}
}
