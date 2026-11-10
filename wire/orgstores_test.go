package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// A second store id for the org-store fixtures.
const orgStoreID = "sto_01a1a5aca7f57659be7320c5f4b70003"

// TestRenameOrgClashes: the org's linked-folder entry (a bare name at a
// folder) is renamed "<org>/<folder>" when stores.yaml uses the bare name
// for another place (r4: the store root made by `lore store init agency`),
// kept when stores.yaml names the same url and folder, and never touched
// when it is a company entry, a root entry, has no clash, or stores.yaml
// cannot be read.
func TestRenameOrgClashes(t *testing.T) {
	t.Parallel()
	const urlA, urlB = "file:///srv/agency", "file:///srv/other"
	cases := []struct {
		name   string
		yaml   string // "" = no stores.yaml; "!" = unreadable
		in     attach.Store
		wantAs store.Source
	}{
		{"clash at root", "stores:\n  - name: agency\n    url: " + urlA + "\n", attach.Store{Name: "agency", URL: urlA, Folder: "acme"}, "agency/acme"},
		{"clash other url", "stores:\n  - name: agency\n    url: " + urlB + "\n    folder: acme\n", attach.Store{Name: "agency", URL: urlA, Folder: "acme"}, "agency/acme"},
		{"clash other folder", "stores:\n  - name: agency\n    url: " + urlA + "\n    folder: beta\n", attach.Store{Name: "agency", URL: urlA, Folder: "acme"}, "agency/acme"},
		{"same url and folder", "stores:\n  - name: agency\n    url: " + urlA + "\n    folder: acme\n", attach.Store{Name: "agency", URL: urlA, Folder: "acme"}, "agency"},
		{"same url and folder, slashes", "stores:\n  - name: agency\n    url: " + urlA + "\n    folder: /acme/\n", attach.Store{Name: "agency", URL: urlA, Folder: "acme/"}, "agency"},
		{"trimmed folder in new name", "stores:\n  - name: agency\n    url: " + urlA + "\n", attach.Store{Name: "agency", URL: urlA, Folder: "/acme/"}, "agency/acme"},
		{"no clash", "stores:\n  - name: shared\n    url: " + urlA + "\n", attach.Store{Name: "agency", URL: urlA, Folder: "acme"}, "agency"},
		{"company entry", "stores:\n  - name: agency\n    url: " + urlA + "\n", attach.Store{Name: "agency/org", URL: urlA, Folder: "org"}, "agency/org"},
		{"root entry", "stores:\n  - name: agency\n    url: " + urlB + "\n", attach.Store{Name: "agency", URL: urlA, Folder: rowfs.FolderRoot}, "agency"},
		{"empty folder", "stores:\n  - name: agency\n    url: " + urlB + "\n", attach.Store{Name: "agency", URL: urlA}, "agency"},
		{"no stores.yaml", "", attach.Store{Name: "agency", URL: urlA, Folder: "acme"}, "agency"},
		{"unreadable stores.yaml", "!", attach.Store{Name: "agency", URL: urlA, Folder: "acme"}, "agency"},
		{"unicode folder", "stores:\n  - name: agency\n    url: " + urlA + "\n", attach.Store{Name: "agency", URL: urlA, Folder: "çlient"}, "agency/çlient"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			top := t.TempDir()
			switch c.yaml {
			case "":
			case "!":
				writeStoresYAML(t, top, "stores: [unclosed\n")
			default:
				writeStoresYAML(t, top, c.yaml)
			}
			in := []attach.Store{c.in}
			got := renameOrgClashes(in, top)
			if len(got) != 1 || got[0].Name != c.wantAs {
				t.Fatalf("got %+v, want name %s", got, c.wantAs)
			}
			if in[0].Name != c.in.Name {
				t.Fatal("the caller's slice was changed")
			}
			if got[0].URL != c.in.URL || got[0].Folder != c.in.Folder {
				t.Fatalf("rename changed more than the name: %+v", got[0])
			}
		})
	}
	if got := renameOrgClashes(nil, t.TempDir()); got != nil {
		t.Fatalf("nil in: %+v", got)
	}
	one := []attach.Store{{Name: "agency", URL: urlA, Folder: "acme"}}
	if got := renameOrgClashes(one, ""); got[0].Name != "agency" {
		t.Fatalf("no top: %+v", got)
	}
}

// TestOrgAliasesByOriginalName: team and company are classified by the
// name Options.OrgStores gave, then point at the name the entry is
// attached under (after a rename).
func TestOrgAliasesByOriginalName(t *testing.T) {
	t.Parallel()
	const url = "file:///srv/agency"
	top := t.TempDir()
	writeStoresYAML(t, top, "stores:\n  - name: agency\n    url: "+url+"\n")
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "agency", URL: url, Folder: "acme"}, {Name: "agency/org", URL: url, Folder: "org"}}, nil
	}
	got := orgAliases(background(), Options{OrgStores: hook}, top)
	if got[attach.ReservedTeam] != "agency/acme" || got[attach.ReservedCompany] != "agency/org" {
		t.Fatalf("aliases = %v", got)
	}
	if none := orgAliases(background(), Options{}, top); len(none) != 0 {
		t.Fatalf("no hook: %v", none)
	}
	failing := func(context.Context) ([]attach.Store, error) { return nil, errors.New("offline") }
	if none := orgAliases(background(), Options{OrgStores: failing}, top); len(none) != 0 {
		t.Fatalf("hook error: %v", none)
	}
}

// TestOrgStoresAttach: Options.OrgStores entries attach beside stores.yaml
// for this process (never written to any repo file); stores.yaml wins a
// same-name clash at the same place; a hook error attaches nothing; and
// --to team / --to company reach the org folders while a repo without an
// org refuses them.
func TestOrgStoresAttach(t *testing.T) {
	// Heavy on SQLite: its own process keeps it off this process's
	// allocator lock (see package isolate).
	if isolate.Run(t) {
		return
	}
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "agency", URL: orgURL, Folder: "acme"}, {Name: "agency/org", URL: orgURL, Folder: "org"}}, nil
	}

	repo := gitRepo(t)
	a := openWith(t, repo, Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	names := a.Stores()
	for _, want := range []store.Source{store.SourceRepo, "agency", "agency/org"} {
		if !slices.Contains(names, want) {
			t.Fatalf("stores = %v, missing %s", names, want)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(attach.FileName))); !os.IsNotExist(err) {
		t.Fatalf("an org store was recorded in stores.yaml: %v", err)
	}
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"for the team","to":"team"}`, true); err != nil {
		t.Fatalf("--to team: %v", err)
	}
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"for the company","to":"company"}`, true); err != nil {
		t.Fatalf("--to company: %v", err)
	}
	if n := countMemories(t, a, "agency"); n != 1 {
		t.Fatalf("team folder holds %d memories, want 1", n)
	}
	if n := countMemories(t, a, "agency/org"); n != 1 {
		t.Fatalf("company folder holds %d memories, want 1", n)
	}
	assertNoURLInRepo(t, repo, strings.TrimPrefix(orgURL, "file://"))

	// stores.yaml names the org store root as agency: the linked folder is
	// attached as agency/acme instead (r4), and team points there.
	repo2 := gitRepo(t)
	writeStoresYAML(t, repo2, "stores:\n  - name: agency\n    url: "+orgURL+"\n")
	b := openWith(t, repo2, Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, b)
	if got := b.Stores(); !slices.Contains(got, "agency/acme") || !slices.Contains(got, "agency") {
		t.Fatalf("stores after a clash = %v", got)
	}
	// the same org store dir: the first repo's team note is already there
	before, rootBefore := countMemories(t, b, "agency/acme"), countMemories(t, b, "agency")
	if _, err := run(t, b, opsmemory.OpAdd, `{"body":"team note","to":"team"}`, true); err != nil {
		t.Fatal(err)
	}
	if n := countMemories(t, b, "agency/acme"); n != before+1 {
		t.Fatalf("--to team after a clash landed elsewhere: agency/acme holds %d, had %d", n, before)
	}
	if n := countMemories(t, b, "agency"); n != rootBefore {
		t.Fatalf("--to team wrote into the store root: %d, had %d", n, rootBefore)
	}

	// a failing hook attaches nothing; team is refused.
	repo3 := gitRepo(t)
	c := openWith(t, repo3, Options{OrgStores: func(context.Context) ([]attach.Store, error) { return nil, errors.New("offline") }})
	withProject(t, c)
	if got := c.Stores(); slices.Contains(got, "agency") {
		t.Fatalf("a failing hook attached %v", got)
	}
	_, err := run(t, c, opsmemory.OpAdd, `{"body":"x","to":"team"}`, true)
	if !errs.Has(err, errs.NotFound) && !errs.Has(err, errs.Unsupported) {
		t.Fatalf("--to team with no org: %v", err)
	}
}

// countMemories counts the live memories of store n.
func countMemories(t *testing.T, a *App, n store.Source) int {
	t.Helper()
	rows, err := a.List(background(), n, schema.TableMemories)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// assertNoURLInRepo fails when any file under repo/.lore names path.
func assertNoURLInRepo(t *testing.T, repo, path string) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(repo, ".lore"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".db") || strings.Contains(p, ".db-") {
			return err
		}
		if strings.Contains(string(mustRead(t, p)), path) {
			t.Errorf("%s names the org store path", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
