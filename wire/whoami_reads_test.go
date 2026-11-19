package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestWhoamiNamesTheFolderItReadsD596: whoami in a repo linked to a store
// folder printed "(org folder org)" and the link count, never the place
// the repo reads (devco D-596). The stores row names the org folder as
// <org>/<folder> (the org itself for an org folder at the store top), and
// the reads rows sit in front of the links row in the <org>/<folder>
// spelling, a second store of the org by its short name.
func TestWhoamiNamesTheFolderItReadsD596(t *testing.T) {
	t.Parallel()
	org := &orgconf.Org{Name: "acme", Stores: []orgconf.Store{
		{URL: "git@github.com:acme-co/lore-team.git", OrgFolder: "org"},
		{URL: "git@github.com:acme-co/lore-ops.git"},
	}}
	links := []orgresolve.Link{
		{StoreURL: "github.com/acme-co/lore-team", Folder: "web"},
		{StoreURL: "github.com/acme-co/lore-ops", Folder: "infra"},
	}
	s := &orgcfg.Situation{Config: &orgconf.Config{}, HasConfig: true, Remotes: []string{"github.com/acme-co/web"},
		Result: orgresolve.Result{Role: orgresolve.RoleOwn, Org: org, Links: links, LinkStatus: orgresolve.LinkStatusKnown, LinkDetail: "2 link(s) for this repo"}}
	lines := whoamiLines(s, identity.Resolved{DisplayName: "Ada", StableKey: "human:ada@acme.example"}, "", nil, false, whoamiRedactor{}, nil, testMachine, "")
	var reads []string
	for _, l := range links {
		reads = append(reads, orgPlace(org, l.StoreURL, l.Folder)+" (this repo's link)")
	}
	got := strings.Join(withReadsRows(lines, reads), "\n")
	for _, want := range []string{
		"(org folder acme/org: roster, teams, company rules)",
		"reads      acme/web (this repo's link)\n           lore-ops/infra (this repo's link)\nlinks      known",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("whoami lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "(org folder org)") {
		t.Fatalf("whoami names the org folder by its bare name:\n%s", got)
	}

	org.Stores[0].OrgFolder = "."
	got = strings.Join(whoamiLines(s, identity.Resolved{DisplayName: "Ada", StableKey: "human:ada@acme.example"}, "", nil, false, whoamiRedactor{}, nil, testMachine, ""), "\n")
	if !strings.Contains(got, "(org folder acme: roster") {
		t.Fatalf("an org folder at the store top is the org itself:\n%s", got)
	}
}
