package wire

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// orgAt is an OrgStores hook naming one org, acme, whose store is url:
// its root as the team store and its org/ folder as the company folder,
// as the org resolution gives them for a folder of the setup that no
// repo link narrows.
func orgAt(url string) func(context.Context) ([]attach.Store, error) {
	return func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "acme", URL: url}, {Name: "acme/org", URL: url, Folder: rowfs.FolderOrg}}, nil
	}
}

// folderList runs store.folder.list and decodes it.
func folderList(t *testing.T, a *App, input string) opsstores.FolderListOut {
	t.Helper()
	res, err := run(t, a, opsstores.OpFolderList, input, false)
	if err != nil {
		t.Fatal(err)
	}
	var out opsstores.FolderListOut
	if err = json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestStoreFoldersWithoutRepo (owner report 2026-11-11): from a plain
// folder of the setup, not a git repo, `store folder add` resolves team,
// the org's name and the org store's url from the setup alone, `store
// list` and `store folder list` read the setup's stores, a stores.yaml
// name is refused naming the cause and the names that work, the
// stores.yaml writers say they need a repo, and nothing is created in the
// folder.
func TestStoreFoldersWithoutRepo(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	plain := t.TempDir()
	a := openWith(t, plain, Options{OrgStores: orgAt(url)})

	for _, c := range []struct{ store, folder string }{{"team", "client_north"}, {"acme", "client_south"}, {url, "client_east"}} {
		res, err := run(t, a, opsstores.OpFolderAdd, `{"store":"`+c.store+`","folder":"`+c.folder+`"}`, false)
		if err != nil {
			t.Fatalf("folder add %s %s outside a repo: %v", c.store, c.folder, err)
		}
		want := `"store":"acme"`
		if c.store == url {
			want = `"store":"` + url + `"` // an org store named by its url is shown by it (an org may have several)
		}
		if !strings.Contains(res, `"project_id":"prj_`) || !strings.Contains(res, want) {
			t.Fatalf("folder add %s %s: %s", c.store, c.folder, res)
		}
		if _, err = os.Stat(filepath.Join(storeDir(url), c.folder, rowfs.MetaFileName)); err != nil {
			t.Fatalf("folder %s not in the store: %v", c.folder, err)
		}
	}

	// a .lore/stores.yaml name needs a repo: the error says so and lists
	// what works here (exact errors)
	_, err := run(t, a, opsstores.OpFolderAdd, `{"store":"golang","folder":"x"}`, false)
	if !errs.Has(err, errs.NotFound) {
		t.Fatalf("stores.yaml name outside a repo: %v, want E_NOT_FOUND", err)
	}
	var e *errs.Error
	if !errors.As(err, &e) || !strings.Contains(e.Message, ".lore/stores.yaml") || !strings.Contains(e.Message, "not inside a git repo") {
		t.Fatalf("message %q: want the stores.yaml cause", err)
	}
	for _, want := range []string{"team", "acme", url} {
		if !strings.Contains(e.Hint, want) {
			t.Errorf("hint %q lacks %q", e.Hint, want)
		}
	}
	if strings.Contains(e.Hint, "acme/org") {
		t.Errorf("hint %q names the company folder entry, which is no store name", e.Hint)
	}

	res, err := run(t, a, opsstores.OpList, `{}`, false)
	if err != nil || !strings.Contains(res, `"name":"acme"`) || !strings.Contains(res, `"name":"acme/org"`) {
		t.Fatalf("store list outside a repo: %s %v", res, err)
	}

	got := folderList(t, a, `{}`)
	var names []string
	for _, f := range got.Folders {
		names = append(names, f.Folder)
		if f.Store != "acme" || !strings.HasPrefix(f.ID, "prj_") || f.Attached || f.CreatedAt != "" || f.CreatedBy != "" {
			t.Errorf("row %+v: want store acme, a prj_ id, no creator (a folder store keeps no history), not attached (no repo)", f)
		}
	}
	if !slices.Equal(names, []string{"client_east", "client_north", "client_south"}) || !slices.Equal(got.Stores, []string{"acme"}) {
		t.Fatalf("folder list = %v of %v", names, got.Stores)
	}
	if one := folderList(t, a, `{"store":"team"}`); len(one.Folders) != 3 || !slices.Equal(one.Stores, []string{"acme"}) {
		t.Fatalf("folder list team = %+v", one)
	}

	for _, c := range []struct{ name, input string }{
		{opsstores.OpInit, `{"name":"foo","url":"` + url + `"}`},
		{opsstores.OpAdd, `{"name":"foo","url":"` + url + `"}`},
		{opsstores.OpRemove, `{"name":"foo"}`},
		{opsstores.OpSetURL, `{"name":"foo","url":"` + url + `"}`},
	} {
		_, err = run(t, a, c.name, c.input, true)
		if !errs.Has(err, errs.NotAStore) || !strings.Contains(err.Error(), "edits a repo's .lore/stores.yaml") {
			t.Errorf("%s outside a repo: %v, want E_NOT_A_STORE naming stores.yaml", c.name, err)
		}
	}
	if entries, _ := os.ReadDir(plain); len(entries) != 0 {
		t.Fatalf("the plain folder got %v", entries)
	}
}

// TestStoreFoldersNoSetup: outside a repo and outside every setup with an
// org, team names the missing setup, an unknown url is not written to.
func TestStoreFoldersNoSetup(t *testing.T) {
	t.Parallel()
	a := openWith(t, t.TempDir(), Options{})
	_, err := run(t, a, opsstores.OpFolderAdd, `{"store":"team","folder":"x"}`, false)
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "in no setup with a team store") {
		t.Fatalf("team with no setup: %v", err)
	}
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	_, err = run(t, a, opsstores.OpFolderAdd, `{"store":"`+url+`","folder":"x"}`, false)
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "not the store of an org in your setups") {
		t.Fatalf("a url of no org: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(storeDir(url), "x")); !os.IsNotExist(serr) {
		t.Fatal("a url of no org was written to")
	}
}

// TestStoreFolderAddLeavesRepo: `store folder add team x` run inside a git
// repo that is not a lore project changes nothing there: no .lore/, no
// cache, no hook, a clean status (the host's git wiring is covered by
// tests/journeys TestJourneyFoldersNoRepo and wire/host).
func TestStoreFolderAddLeavesRepo(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	repo := gitRepo(t)
	before := untouchedState(t, repo)
	a := openWith(t, repo, Options{OrgStores: orgAt(url)})
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"team","folder":"x"}`, false); err != nil {
		t.Fatal(err)
	}
	folderList(t, a, `{}`)
	if after := untouchedState(t, repo); after != before {
		t.Fatalf("store folder add changed the repo:\nbefore %s\nafter  %s", before, after)
	}
}

// TestFolderListAttached: inside a repo, a folder the repo's
// .lore/stores.yaml attaches is marked attached, the store's other
// folders are not.
func TestFolderListAttached(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+url+"\n  - name: kb/acme\n    url: "+url+"\n    folder: acme\n")
	a := openWith(t, repo, Options{})
	for _, f := range []string{"acme", "globex"} {
		if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"kb","folder":"`+f+`"}`, true); err != nil {
			t.Fatal(err)
		}
	}
	got := folderList(t, a, `{"store":"kb"}`)
	if len(got.Folders) != 2 {
		t.Fatalf("folder list kb = %+v", got)
	}
	for _, f := range got.Folders {
		if f.Attached != (f.Folder == "acme") {
			t.Errorf("%s attached = %v", f.Folder, f.Attached)
		}
	}
	// every store this repo sees, the store listed once although two
	// entries name it
	if all := folderList(t, a, `{}`); !slices.Equal(all.Stores, []string{"kb"}) {
		t.Fatalf("folder list = %v", all.Stores)
	}
}

// untouchedState is a repo's status (ignored files too), its top-level entries
// and its hooks: what a store-only command must leave as it was.
func untouchedState(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain", "--ignored", "--untracked-files=all")
	cmd.Dir, cmd.Env = repo, gitenv.Clean(os.Environ())
	st, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(string(st))
	for _, d := range []string{repo, filepath.Join(repo, ".git", "hooks")} {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			b.WriteString(" " + e.Name())
		}
		b.WriteString(";")
	}
	return b.String()
}

// TestStoreFolderAddOutsideRepoPolicy: outside a repo the machine-only
// engine still applies this machine's agents.person_only to an agent
// caller (the repo path's agent detection would have opened a repo that
// is not there); a person passes.
func TestStoreFolderAddOutsideRepoPolicy(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("policies:\n  agents.person_only: [store.folder.add]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, t.TempDir(), Options{Home: home, OrgStores: orgAt(url)})
	e, ok := a.Registry().Lookup(opsstores.OpFolderAdd)
	if !ok {
		t.Fatal("no store.folder.add")
	}
	in := op.JSONDecoder{Body: []byte(`{"store":"team","folder":"x"}`)}
	_, err := a.Run(background(), e, in, op.Caller{Agent: "claude_code", AgentFixed: true})
	if err == nil || !strings.Contains(err.Error(), "agents.person_only") {
		t.Fatalf("agent outside a repo: %v, want the agents.person_only refusal", err)
	}
	if _, err = run(t, a, opsstores.OpFolderAdd, `{"store":"team","folder":"x"}`, false); err != nil {
		t.Fatalf("person outside a repo: %v", err)
	}
}
