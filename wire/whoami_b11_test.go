package wire

// B11 ports of saas/cmd/cli whoami_redact_test.go (TestWhoamiRedactor) and
// org_remotes_test.go (TestOrgUseHint): the whoami redactor and the "org
// none" hint now live in wire/whoami_standalone.go.

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestWhoamiRedactor: store urls in every form, links and folders are
// printed as they are, with a CI marker set too (a store's plain address
// is no secret: whoami showed "matched by: link in <redacted>"); only a
// credential inside a url is masked.
func TestWhoamiRedactor(t *testing.T) {
	t.Setenv(ciMarkers[0], "1") // CI used to turn redaction on
	red := whoamiRedactor{}
	for _, keep := range []string{
		"own pattern github.com/betalabs/* of betalabs",
		"origin github.com/acme/app",
		"link in github.com/northwind/lore acme/, project id + remote",
		"https://github.com/northwind/lore.git on branch main",
		"git@github.com:northwind/lore.git",
		"clone at ~/.lore/clones/github.com/northwind/lore.git failed",
		"store ssh://git@gitlab.example.com/northwind/lore unreachable",
		"linked in northwind (github.com/northwind/lore acme/)", // a rule trace reason
	} {
		if got := red.str(keep); got != keep {
			t.Errorf("str(%q) = %q; a plain address must be printed as it is", keep, got)
		}
	}
	if got := red.folder("client-folder"); got != "client-folder" {
		t.Errorf("folder = %q, want it as it is", got)
	}
	pw := "pass" + "word1"
	in := "store https://mira:" + pw + "@gitlab.example.com/northwind/lore.git unreachable"
	want := "store https://mira:" + redactedText + "@gitlab.example.com/northwind/lore.git unreachable"
	if got := red.str(in); got != want {
		t.Errorf("str(%q) = %q, want %q", in, got, want)
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
