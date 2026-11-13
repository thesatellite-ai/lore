package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCheckUnusedOrgStoreSamePlace (B-FLOW4-1): `lore store init|add
// <name>` refuses a name an org store answers (G-CORE-8) only when the org
// store is a different place. The org's own store made or attached by its
// org name (the team docs' `lore store init --name agency --url git@github.com:agency/notes`
// in the agency's own repo) is the same place: same canonical url (any
// spelling git accepts), same branch, same folder (the root spelled "",
// "." or "/"). A name stores.yaml already uses is refused whatever the
// place (CFG2), and an unused name is free.
func TestCheckUnusedOrgStoreSamePlace(t *testing.T) {
	t.Parallel()
	const (
		scp   = "git@github.com:agency/notes"
		other = "git@github.com:agency/other"
	)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{
			{Name: "agency", URL: scp},
			{Name: "agency/org", URL: scp, Folder: "org"},
			{Name: "stag", URL: scp, Branch: "staging"},
		}, nil
	}
	repo := gitRepo(t)
	a := openWith(t, repo, Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	ctx := background()

	cases := []struct {
		name  string
		use   string
		place attach.Store
		ok    bool
	}{
		{"root, same url as configured", "agency", attach.Store{URL: scp}, true},
		{"root, https spelling", "agency", attach.Store{URL: "https://github.com/agency/notes"}, true},
		{"root, .git suffix", "agency", attach.Store{URL: scp + ".git"}, true},
		{"root spelled .", "agency", attach.Store{URL: scp, Folder: "."}, true},
		{"root spelled /", "agency", attach.Store{URL: scp, Folder: "/"}, true},
		{"org folder, same folder", "agency/org", attach.Store{URL: scp, Folder: "org"}, true},
		{"org folder, trailing slash", "agency/org", attach.Store{URL: scp, Folder: "org/"}, true},
		{"branch entry, same branch", "stag", attach.Store{URL: scp, Branch: "staging"}, true},
		{"unused name", "fresh", attach.Store{URL: other}, true},
		{"different url", "agency", attach.Store{URL: other}, false},
		{"same url, other folder", "agency", attach.Store{URL: scp, Folder: "client"}, false},
		{"org folder name at the root", "agency/org", attach.Store{URL: scp}, false},
		{"same url, a branch the org entry has not", "agency", attach.Store{URL: scp, Branch: "staging"}, false},
		{"branch entry, default branch", "stag", attach.Store{URL: scp}, false},
		{"unparsable place", "agency", attach.Store{URL: "::"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := a.checkUnused(ctx, c.use, c.place)
			if c.ok && err != nil {
				t.Fatalf("checkUnused(%s, %+v) = %v, want allowed", c.use, c.place, err)
			}
			if !c.ok && !errs.Has(err, errs.InvalidInput) {
				t.Fatalf("checkUnused(%s, %+v) = %v, want E_INVALID_INPUT", c.use, c.place, err)
			}
		})
	}

	// stores.yaml wins on name alone: the same place is still a clash there.
	repo2 := gitRepo(t)
	writeStoresYAML(t, repo2, "stores:\n  - name: agency\n    url: "+scp+"\n")
	b := openWith(t, repo2, Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err := b.checkUnused(ctx, "agency", attach.Store{URL: scp}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("a stores.yaml name at the same place: %v, want E_INVALID_INPUT", err)
	}
}
