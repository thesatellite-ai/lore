package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestImportKeepsTypedStoreSpelling (devco D-680): the org entry an import
// makes names the store the person imported by in the spelling they gave
// (https, ssh://, an ssh alias the ssh config lore reads does not name),
// not the shared file's git@github.com:… spelling, which may need a key
// this machine lacks; the org's other stores keep the file's spelling.
func TestImportKeepsTypedStoreSpelling(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.ssh/config: an alias stays as typed
	const team, ops = "git@github.com:acme-co/lore-team.git", "git@github.com:acme-co/lore-ops.git"
	sg := orgcfg.Suggestion{Folder: "org", Doc: orgcfg.OrgFileDoc{Name: "acme",
		Stores: []orgcfg.OrgFileStore{{URL: team, OrgFolder: "org"}, {URL: ops}}}}
	for _, typed := range []string{
		"https://github.com/acme-co/lore-team.git",
		"https://github.com/acme-co/lore-team",
		"ssh://git@github.com/acme-co/lore-team.git",
		"git@github-acme:acme-co/lore-team.git",
		team,
	} {
		entry := orgconf.Org{Name: "acme"}
		suggestedStores(context.Background(), &entry, sg, typed)
		if len(entry.Stores) != 2 {
			t.Fatalf("%s: stores %+v", typed, entry.Stores)
		}
		if got := entry.Stores[0].URL; got != typed {
			t.Errorf("imported by %s: the entry names the store %s", typed, got)
		}
		if got := entry.Stores[1].URL; got != ops {
			t.Errorf("imported by %s: the org's other store became %s, want %s", typed, got, ops)
		}
	}
}
