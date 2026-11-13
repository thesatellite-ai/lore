package stores

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestIsStoreURL: a store argument with a scheme or in git's scp form is
// a url; a name (snake_case, team, me, <store>/<folder>) never is.
func TestIsStoreURL(t *testing.T) {
	for arg, want := range map[string]bool{
		"git@github.com:acme/notes":    true,
		"ssh://git@host/acme/notes":    true,
		"https://github.com/acme/n":    true,
		"file:///tmp/store":            true,
		"git@host:a":                   true,
		"team":                         false,
		"me":                           false,
		"acme_team":                    false,
		"acme/org":                     false,
		"":                             false,
		"@host:x":                      false,
		"user@:x":                      false,
		"a:b":                          false,
		"weird@name":                   false,
		"s3://bucket/x":                true, // a url: CheckStoreArg refuses its scheme
		"ünïcode@hôst:owner/repo.git":  true,
		strings.Repeat("a", 300) + "x": false,
	} {
		if got := IsStoreURL(arg); got != want {
			t.Errorf("IsStoreURL(%q) = %v, want %v", arg, got, want)
		}
	}
}

// TestCheckStoreArg: team, me, a store name and an org store's url pass;
// a reserved or malformed name is E_INVALID_INPUT; a url whose scheme no
// backend takes is E_STORE_SCHEME.
func TestCheckStoreArg(t *testing.T) {
	for _, c := range []struct {
		arg  string
		code errs.Code // "" = ok
	}{
		{"team", ""}, {"me", ""}, {"acme", ""}, {"git@github.com:acme/notes", ""}, {"https://github.com/acme/notes", ""},
		{"file:///srv/store", ""},
		{"company", errs.InvalidInput}, {"repo", errs.InvalidInput}, {"Acme", errs.InvalidInput}, {"", errs.InvalidInput},
		{"acme/org", errs.InvalidInput}, {"s3://bucket/notes", errs.StoreScheme},
	} {
		err := CheckStoreArg(c.arg)
		if c.code == "" {
			if err != nil {
				t.Errorf("CheckStoreArg(%q) = %v, want ok", c.arg, err)
			}
			continue
		}
		if !errs.Has(err, c.code) {
			t.Errorf("CheckStoreArg(%q) = %v, want %s", c.arg, err, c.code)
		}
	}
}

// TestRunsWithoutRepo: the commands that need only the setup run outside
// a repo; the .lore/stores.yaml writers do not; LeavesRepo is every store
// command but those writers, and nothing outside the store noun.
func TestRunsWithoutRepo(t *testing.T) {
	for _, name := range []string{OpList, OpVisibility, OpFolderAdd, OpFolderList, OpSet, OpCompact} {
		if !RunsWithoutRepo(name) || !LeavesRepo(name) {
			t.Errorf("%s: RunsWithoutRepo %v LeavesRepo %v, want both true", name, RunsWithoutRepo(name), LeavesRepo(name))
		}
	}
	for _, name := range []string{OpInit, OpAdd, OpRemove, OpSetURL} {
		if RunsWithoutRepo(name) || LeavesRepo(name) {
			t.Errorf("%s edits .lore/stores.yaml: RunsWithoutRepo %v LeavesRepo %v, want both false", name, RunsWithoutRepo(name), LeavesRepo(name))
		}
	}
	for _, name := range []string{OpShow, OpOpen, OpForget, OpDoctor} {
		if !LeavesRepo(name) {
			t.Errorf("%s changes no repo: LeavesRepo false", name)
		}
	}
	for _, name := range []string{"memory.add", "storex.add", "team.invite", "", "store"} {
		if LeavesRepo(name) || RunsWithoutRepo(name) {
			t.Errorf("%q is not a store command that leaves the repo", name)
		}
	}
}

// TestFolderListText: one block per store, the folders as a table with
// every column; a store without folders says so and how to add one; no
// store at all names the reason.
func TestFolderListText(t *testing.T) {
	out := FolderListOut{
		Stores: []string{"acme", "me"},
		Folders: []FolderRow{
			{Store: "acme", Folder: "client_north", ID: "prj_1", CreatedBy: "Lee Lead", CreatedAt: "2026-01-05T09:00:00Z", Attached: true, Rows: 3},
			{Store: "acme", Folder: "client_south", ID: "prj_2", Rows: 0},
		},
	}
	got := out.Text()
	for _, want := range []string{"store acme folders:", "FOLDER", "ATTACHED HERE", "client_north", "prj_1", "Lee Lead", "2026-01-05T09:00:00Z", "yes", "client_south", "no",
		"store me: no folders yet (`lore store folder add --store me --folder <folder>`)"} {
		if !strings.Contains(got, want) {
			t.Errorf("Text() lacks %q:\n%s", want, got)
		}
	}
	// a missing creator or time shows "-", never an empty cell
	south := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "client_south") {
			south = l
		}
	}
	if strings.Count(south, noneShown) != 2 {
		t.Errorf("client_south line %q: want - for created by and created", south)
	}
	if got := (FolderListOut{}).Text(); !strings.Contains(got, "no stores") || !strings.Contains(got, "lore setup list") {
		t.Errorf("empty Text() = %q", got)
	}
}
