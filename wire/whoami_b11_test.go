package wire

// B11 ports of saas/cmd/cli whoami_redact_test.go (TestWhoamiRedactor) and
// org_remotes_test.go (TestOrgUseHint): the whoami redactor and the "org
// none" hint now live in wire/whoami_standalone.go.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestWhoamiRedactor (§6.5): with redaction on (a CI marker set), no store
// URL in any form and no internal folder survives; the repo's own remotes
// and own:/repos: patterns are kept; with it off every string passes
// through unchanged.
func TestWhoamiRedactor(t *testing.T) {
	cfg := &orgconf.Config{Orgs: []orgconf.Org{
		{Name: "northwind", Stores: []orgconf.Store{{URL: "https://github.com/northwind/lore.git"}}},
		{Name: "acme-nda", Stores: []orgconf.Store{{URL: "ssh://git@gitlab.example.com/northwind/lore"}}},
	}}
	s := &orgcfg.Situation{Config: cfg, Result: orgresolve.Result{Links: []orgresolve.Link{{StoreURL: "github.com/northwind/lore", Folder: "acme"}}}}
	t.Setenv(ciMarkers[0], "1") // quietOutput → redaction on
	on := newWhoamiRedactor(s, false)
	if !on.on {
		t.Fatal("CI set must turn redaction on")
	}
	for _, keep := range []string{"own pattern github.com/betalabs/* of betalabs", "origin github.com/acme/app"} {
		if got := on.str(keep); got != keep {
			t.Errorf("str(%q) = %q; only internal names may be masked", keep, got)
		}
	}
	leaks := []string{"northwind/lore", "acme/"}
	inputs := []string{
		"link in github.com/northwind/lore acme/, project id + remote",
		"https://github.com/northwind/lore.git on branch main",
		"git@github.com:northwind/lore.git",
		"clone at ~/.lore/clones/github.com/northwind/lore.git failed",
		"store ssh://git@gitlab.example.com/northwind/lore unreachable",
		"linked in northwind (github.com/northwind/lore acme/)", // a rule trace reason
	}
	for _, in := range inputs {
		got := on.str(in)
		for _, l := range leaks {
			if strings.Contains(got, l) {
				t.Errorf("str(%q) = %q still has %q", in, got, l)
			}
		}
	}
	if got := on.folder("secret-org-folder"); got != redactedText {
		t.Errorf("folder = %q, want %q", got, redactedText)
	}
	if got := on.folder(""); got != "" {
		t.Errorf("empty folder = %q, want empty", got)
	}
	off := whoamiRedactor{}
	for _, in := range inputs {
		if off.str(in) != in || off.folder(in) != in {
			t.Errorf("redaction off changed %q", in)
		}
	}
}

// TestOrgUseHint: whoami's "org none" hint names only entries a user may
// pick casually: never a staging entry (Q-a), the entry itself when there
// is one.
func TestOrgUseHint(t *testing.T) {
	t.Parallel()
	prod := orgconf.Org{Name: "northwind", Stores: []orgconf.Store{{URL: "https://github.com/northwind/lore"}}}
	stag := orgconf.Org{Name: "northwind-stag", Stores: []orgconf.Store{{URL: "https://github.com/northwind/lore", Branch: "staging"}}}
	beta := orgconf.Org{Name: "betalabs", Stores: []orgconf.Store{{URL: "https://github.com/betalabs/lore"}}}
	cases := []struct {
		name string
		cfg  *orgconf.Config
		want string
	}{
		{"no config", nil, ""},
		{"no orgs", &orgconf.Config{}, ""},
		{"only staging", &orgconf.Config{Orgs: []orgconf.Org{stag}}, ""},
		{"one plus staging", &orgconf.Config{Orgs: []orgconf.Org{prod, stag}}, "lore org use northwind"},
		{"two", &orgconf.Config{Orgs: []orgconf.Org{prod, stag, beta}}, "lore org use <entry> (northwind, betalabs)"},
	}
	for _, c := range cases {
		if got := orgUseHint(c.cfg); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
