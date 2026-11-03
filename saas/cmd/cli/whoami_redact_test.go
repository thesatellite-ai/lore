package main

import (
	"strings"
	"testing"

	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
)

// §6.5: with redaction on, no store URL (any form) and no internal folder
// survives; with it off every string passes through unchanged.
func TestWhoamiRedactor(t *testing.T) {
	cfg := &lorecfg.Config{Orgs: []lorecfg.Org{
		{Name: "northwind", Stores: []lorecfg.Store{{URL: "https://github.com/northwind/lore.git"}}},
		{Name: "acme-nda", Stores: []lorecfg.Store{{URL: "ssh://git@gitlab.example.com/northwind/lore"}}},
	}}
	s := &orgSituation{Config: cfg, Result: orgresolve.Result{Links: []orgresolve.Link{{StoreURL: "github.com/northwind/lore", Folder: "acme"}}}}
	t.Setenv(envCI, "1") // quietOutput → redaction on
	on := newWhoamiRedactor(s, false)
	if !on.on {
		t.Fatal("CI set must turn redaction on")
	}
	// The repo's own remotes and own:/repos: patterns are not internal.
	for _, keep := range []string{"own pattern github.com/betalabs/* of betalabs", "origin github.com/acme/app"} {
		if got := on.str(keep); got != keep {
			t.Errorf("str(%q) = %q; only internal names may be masked", keep, got)
		}
	}

	leaks := []string{"northwind/lore", "acme/", "secret-org-folder"}
	inputs := []string{
		"link in github.com/northwind/lore acme/, project id + remote",
		"https://github.com/northwind/lore.git on branch main",
		"git@github.com:northwind/lore.git",
		"clone at ~/.lore/clones/github.com/northwind/lore.git failed",
		"store ssh://git@gitlab.example.com/northwind/lore unreachable",
	}
	for _, in := range inputs {
		got := on.str(in)
		for _, l := range leaks {
			if strings.Contains(got, l) {
				t.Errorf("str(%q) = %q still has %q", in, got, l)
			}
		}
	}
	if got := on.folder("secret-org-folder"); got != redactedRepo {
		t.Errorf("folder = %q", got)
	}
	st := on.stores([]lorecfg.Store{{URL: "https://github.com/northwind/lore", OrgFolder: "org", Branch: "staging"}})
	if st[0].URL != redactedRepo || st[0].OrgFolder != redactedRepo || st[0].Branch != "staging" {
		t.Errorf("stores = %+v", st)
	}
	tr := on.trace([]orgresolve.RuleTrace{{Reason: "linked in northwind (github.com/northwind/lore acme/)"}})
	if strings.Contains(tr[0].Reason, "northwind/lore") || strings.Contains(tr[0].Reason, "acme/") {
		t.Errorf("trace = %q", tr[0].Reason)
	}

	off := whoamiRedactor{}
	for _, in := range inputs {
		if off.str(in) != in {
			t.Errorf("redaction off changed %q", in)
		}
	}
}
