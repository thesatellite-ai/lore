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
// this machine lacks. The org's other stores on the same host take the
// same spelling (an https member syncs the ops store over https too); a
// store on another host keeps the file's.
func TestImportKeepsTypedStoreSpelling(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.ssh/config: an alias stays as typed
	const team, ops = "git@github.com:acme-co/lore-team.git", "git@github.com:acme-co/lore-ops.git"
	const elsewhere = "git@git.other.example:acme-co/lore-archive.git"
	sg := orgcfg.Suggestion{Folder: "org", Doc: orgcfg.OrgFileDoc{Name: "acme",
		Stores: []orgcfg.OrgFileStore{{URL: team, OrgFolder: "org"}, {URL: ops}, {URL: elsewhere}}}}
	for typed, wantOps := range map[string]string{
		"https://github.com/acme-co/lore-team.git":   "https://github.com/acme-co/lore-ops.git",
		"https://github.com/acme-co/lore-team":       "https://github.com/acme-co/lore-ops",
		"ssh://git@github.com/acme-co/lore-team.git": "ssh://git@github.com/acme-co/lore-ops.git",
		"git@github-acme:acme-co/lore-team.git":      "git@github-acme:acme-co/lore-ops.git",
		team:                                         ops,
	} {
		entry := orgconf.Org{Name: "acme"}
		suggestedStores(context.Background(), &entry, sg, typed)
		if len(entry.Stores) != 3 {
			t.Fatalf("%s: stores %+v", typed, entry.Stores)
		}
		if got := entry.Stores[0].URL; got != typed {
			t.Errorf("imported by %s: the entry names the store %s", typed, got)
		}
		if got := entry.Stores[1].URL; got != wantOps {
			t.Errorf("imported by %s: the org's ops store is %s, want %s", typed, got, wantOps)
		}
		if got := entry.Stores[2].URL; got != elsewhere {
			t.Errorf("imported by %s: the store on another host became %s, want %s", typed, got, elsewhere)
		}
	}
}
