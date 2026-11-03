package personalplace

import (
	"errors"
	"reflect"
	"testing"

	"saas/pkg/aicoder/lorecfg"
)

// TestPlaceMatrix: org / none × --everywhere × text naming a known org /
// repo owner / nothing × TTY × --yes (§9.2 F17).
func TestPlaceMatrix(t *testing.T) {
	known := []string{"northwind", "betalabs", "acme", "acme/app"}
	type tc struct {
		org        string
		everywhere bool
		text       string
		tty, yes   bool
		folder     string
		confirm    bool
		err        error
		mentions   []string
	}
	cases := []tc{
		{org: "northwind", text: "ask Raj about betalabs creds", folder: "northwind"}, // text ignored without --everywhere
		{org: "", text: "I prefer tabs", folder: "everywhere"},
		{org: "", text: "acme is slow", folder: "everywhere"}, // no org: everywhere, no question
		{org: "northwind", everywhere: true, text: "I prefer tabs", folder: "everywhere"},
		{org: "northwind", everywhere: true, text: "betalabs invoices go to finance@", tty: true, folder: "everywhere", confirm: true, mentions: []string{"betalabs"}},
		{org: "northwind", everywhere: true, text: "betalabs invoices", err: ErrEverywhereNeedsConfirm, mentions: []string{"betalabs"}},
		{org: "northwind", everywhere: true, text: "betalabs invoices", yes: true, folder: "everywhere", mentions: []string{"betalabs"}},
		{org: "", everywhere: true, text: "Acme's portal and acme/app deploys", tty: true, folder: "everywhere", confirm: true, mentions: []string{"acme", "acme/app"}},
		{org: "", everywhere: true, text: "acmetools is great", folder: "everywhere"}, // whole words only
	}
	for i, c := range cases {
		p, err := Place(Inputs{Org: c.org, Everywhere: c.everywhere, Text: c.text, KnownNames: known, TTY: c.tty, Yes: c.yes})
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("case %d: err = %v, want %v", i, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("case %d: %v", i, err)
			continue
		}
		if p.Folder != c.folder || p.NeedsConfirm != c.confirm || p.Reason == "" {
			t.Errorf("case %d: got %+v", i, p)
		}
		if len(c.mentions) > 0 && !reflect.DeepEqual(p.Mentions, c.mentions) {
			t.Errorf("case %d: mentions %v, want %v", i, p.Mentions, c.mentions)
		}
	}
}

func TestKnownNames(t *testing.T) {
	cfg := &lorecfg.Config{Orgs: []lorecfg.Org{
		{Name: "northwind", Own: []string{"github.com/northwind/*"}, Repos: []string{"github.com/acme/app", "github.com/globex/**"}},
		{Name: "betalabs", Repos: []string{"gitlab.com/*/shared"}},
	}}
	got := KnownNames(cfg)
	want := []string{"acme", "acme/app", "betalabs", "globex", "northwind"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("KnownNames = %v, want %v", got, want)
	}
}

func TestParseAndCheckTo(t *testing.T) {
	cases := []struct {
		raw    string
		kind   TargetKind
		org    string
		folder string
		bad    bool
	}{
		{"", TargetNone, "", "", false},
		{"repo", TargetThisRepo, "", "", false},
		{"me", TargetPersonal, "", "", false},
		{"me/northwind", TargetPersonalOrg, "northwind", "", false},
		{"northwind", TargetOrg, "northwind", "", false},
		{"northwind/acme", TargetOrgFolder, "northwind", "acme", false},
		{"northwind/.", TargetOrgFolder, "northwind", ".", false}, // store root project (org_folder: .)
		{"northwind/..", 0, "", "", true},
		{"northwind/", 0, "", "", true},
		{"northwind/./x", 0, "", "", true},
		{"northwind/_root", 0, "", "", true},
		{"northwind/a/b", 0, "", "", true},
		{"me/", 0, "", "", true},
		{"me/a/b", 0, "", "", true},
		{"repo/x", 0, "", "", true},
	}
	for _, c := range cases {
		got, err := ParseTo(c.raw)
		if c.bad {
			if err == nil {
				t.Errorf("ParseTo(%q) accepted: %+v", c.raw, got)
			}
			continue
		}
		if err != nil || got.Kind != c.kind || got.Org != c.org || got.Folder != c.folder {
			t.Errorf("ParseTo(%q) = %+v, %v", c.raw, got, err)
		}
	}

	mustTo := func(s string) Target { t2, _ := ParseTo(s); return t2 }
	if err := CheckTo(mustTo("betalabs"), "northwind", false); !errors.Is(err, ErrOtherOrg) {
		t.Error("--to another org must be refused")
	}
	if err := CheckTo(mustTo("me/betalabs"), "northwind", false); !errors.Is(err, ErrOtherOrg) {
		t.Error("--to me/<other org> must be refused")
	}
	if err := CheckTo(mustTo("northwind/acme"), "", false); !errors.Is(err, ErrOtherOrg) {
		t.Error("--to <org> with no org must be refused")
	}
	if err := CheckTo(mustTo("repo"), "northwind", true); !errors.Is(err, ErrEverywhereWithoutMe) {
		t.Error("--everywhere without --to me must be refused")
	}
	for _, ok := range []string{"me", "repo", "northwind", "northwind/acme", "me/northwind", ""} {
		if err := CheckTo(mustTo(ok), "northwind", false); err != nil {
			t.Errorf("CheckTo(%q): %v", ok, err)
		}
	}
	if err := CheckTo(mustTo("me"), "", true); err != nil {
		t.Errorf("--to me --everywhere with no org: %v", err)
	}
	if !mustTo("me").IsPersonal() || mustTo("repo").IsPersonal() || mustTo("").Explicit() || !mustTo("repo").Explicit() {
		t.Error("Target helpers")
	}
}
