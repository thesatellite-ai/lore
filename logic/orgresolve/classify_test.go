package orgresolve

import (
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// clientListedOwn is a northwind repo the org also lists as a client's
// (an own: pattern github.com/northwind/* and a repos: entry for it).
const clientListedOwn = "github.com/northwind/shopapp"

// clientListedCfg is fixture() with northwind listing clientListedOwn
// under repos: as well.
func clientListedCfg() *orgconf.Config {
	cfg := fixture()
	o := cfg.FindOrg("northwind")
	o.Repos = append(o.Repos, clientListedOwn)
	return cfg
}

// TestLinkNeverWeakensGuards (devco D-428): a repo matching an own:
// pattern but listed under repos: links as a CLIENT repo without --own,
// --own on it is refused, and whoami's answer (F10) is client before and
// after the link, also when the store holds an own link written by an
// older lore.
func TestLinkNeverWeakensGuards(t *testing.T) {
	cfg := clientListedCfg()
	before, err := Resolve(Inputs{Config: cfg, Remotes: []string{clientListedOwn}, Links: LinkResult{Status: LinkStatusKnown}})
	if err != nil || before.Role != RoleClient {
		t.Fatalf("before the link: role %v, %v; want client", before.Role, err)
	}
	req := LinkRequest{Entry: "northwind", Folder: "shop", ProjectID: pid, Remotes: []string{clientListedOwn}}
	p, err := PlanLink(cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Own || p.OwnByPattern {
		t.Fatalf("client-listed repo linked without --own: plan %+v, want a client link", p)
	}
	req.Own = true
	if _, err := PlanLink(cfg, req); !errors.Is(err, ErrLinkClientListed) {
		t.Fatalf("--own on a client-listed repo: %v, want ErrLinkClientListed", err)
	}
	for _, own := range []bool{false, true} { // true: an own link from an older lore
		l := link("northwind", clientListedOwn, "shop")
		l.Own = own
		after, err := Resolve(Inputs{Config: cfg, ProjectID: pid, Remotes: []string{clientListedOwn},
			Links: LinkResult{Status: LinkStatusKnown, Links: []Link{l}}})
		if err != nil || after.Role != RoleClient || allOwn(after.Links) {
			t.Fatalf("after a link recorded own=%v: role %v links %+v, %v; want client", own, after.Role, after.Links, err)
		}
	}
}

// TestClassifyRemote: repos: wins over own:, own: alone is own, nothing
// is none; EffectiveOwn never turns a client link own.
func TestClassifyRemote(t *testing.T) {
	o := clientListedCfg().FindOrg("northwind")
	for remote, want := range map[string]Role{clientListedOwn: RoleClient, "github.com/northwind/tools": RoleOwn, "github.com/acme/app": RoleClient, "github.com/other/x": RoleNone} {
		if got := ClassifyRemote(o, []string{remote}).Role; got != want {
			t.Errorf("ClassifyRemote(%s) = %v, want %v", remote, got, want)
		}
	}
	if EffectiveOwn(o, clientListedOwn, true) || EffectiveOwn(o, "github.com/northwind/tools", false) || !EffectiveOwn(o, "github.com/northwind/tools", true) {
		t.Fatal("EffectiveOwn: a client-listed own link must read as client, and only a recorded own link is own")
	}
}
