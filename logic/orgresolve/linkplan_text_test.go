package orgresolve

import (
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestPlanLinkRefusalTexts pins every refusal of `lore org link`'s plan:
// the cause in user words (no package name: the v0.1.17 texts began with
// "orgresolve:") and the next step, each exactly as the CLI prints them.
func TestPlanLinkRefusalTexts(t *testing.T) {
	t.Parallel()
	cfg := fixture()
	cfg.Orgs = append(cfg.Orgs, orgconf.Org{Name: "nostores", Identity: orgconf.OrgIdentity{Key: "human:alice@example.com"}})
	base := LinkRequest{Entry: "northwind", Folder: "acme", ProjectID: pid, Remotes: []string{"github.com/acme/app"}}
	cases := []struct {
		name, msg, next string
		mod             func(r *LinkRequest)
		cfg             *orgconf.Config
	}{
		{name: "unknown entry", mod: func(r *LinkRequest) { r.Entry = "ghost" },
			msg:  `no org entry named "ghost" in ~/.lore/config.yaml`,
			next: "name one of the org entries: betalabs, northwind, northwind-stag, nostores"},
		{name: "unknown entry, no entries", mod: func(r *LinkRequest) { r.Entry = "ghost" }, cfg: &orgconf.Config{},
			msg:  `no org entry named "ghost" in ~/.lore/config.yaml`,
			next: "import the org first: lore org import <store url>"},
		{name: "reserved folder", mod: func(r *LinkRequest) { r.Folder = "_live" },
			msg:  `--project "_live" is not a folder name lore can use: folder names starting with "_" are reserved for lore (_root, _live)`,
			next: "pick another folder name: letters, digits, '.', '_' or '-'"},
		{name: "no stores", mod: func(r *LinkRequest) { r.Entry = "nostores" },
			msg:  "org entry nostores lists no store",
			next: "run lore org refresh nostores to copy its stores from the team's lore-org.yaml, or add one under stores: in ~/.lore/config.yaml"},
		{name: "--store not the entry's", mod: func(r *LinkRequest) { r.StoreURL = "git@github.com:betalabs/lore" },
			msg:  "--store github.com/betalabs/lore is not one of northwind's stores",
			next: "pass one of them: git@github.com:northwind/lore"},
		{name: "--own on a client repo", mod: func(r *LinkRequest) { r.Own = true },
			msg:  "--own is only for repos matching the org entry's own: patterns: github.com/acme/app does not match northwind's own: patterns (github.com/northwind/*)",
			next: "link it without --own (as a client repo), or add a matching pattern to own: in northwind's lore-org.yaml and run lore org refresh northwind"},
		{name: "no remote", mod: func(r *LinkRequest) { r.Remotes = nil },
			msg:  "this repo has no hosted remote; a link names a project id and a remote",
			next: "add the repo's remote first: git remote add origin <url>"},
		{name: "two remotes", mod: func(r *LinkRequest) { r.Remotes = []string{"github.com/acme/app", "github.com/me/fork"} },
			msg:  "this repo has 2 remotes (github.com/acme/app, github.com/me/fork)",
			next: "choose one with --remote <url>"},
		{name: "--remote not ours", mod: func(r *LinkRequest) { r.Remote = "github.com/x/y" },
			msg:  "--remote github.com/x/y is not a remote of this repo",
			next: "pass one of this repo's remotes: github.com/acme/app"},
		{name: "--remote not ours, no remotes", mod: func(r *LinkRequest) { r.Remote, r.Remotes = "github.com/x/y", nil },
			msg:  "--remote github.com/x/y is not a remote of this repo",
			next: "add it first: git remote add origin github.com/x/y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, use := base, cfg
			c.mod(&r)
			if c.cfg != nil {
				use = c.cfg
			}
			_, err := PlanLink(use, r)
			var lr *LinkRefusal
			if !errors.As(err, &lr) {
				t.Fatalf("err = %#v, want a LinkRefusal", err)
			}
			if lr.Msg != c.msg || err.Error() != c.msg {
				t.Errorf("message\n got: %s\nwant: %s", lr.Msg, c.msg)
			}
			if lr.Next != c.next {
				t.Errorf("next step\n got: %s\nwant: %s", lr.Next, c.next)
			}
		})
	}
}
