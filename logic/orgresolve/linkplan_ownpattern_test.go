package orgresolve

import "testing"

// TestPlanLinkOwnByPattern (devco P-6): a repo the org's own: patterns
// match links as an own repo without --own (it was planned as a client
// repo, turning the leak guards on for the org's own code); a repo they
// do not match stays a client repo, and --own itself is not "by pattern".
func TestPlanLinkOwnByPattern(t *testing.T) {
	cfg := fixture()
	req := LinkRequest{Entry: "northwind", Folder: "tools", ProjectID: pid, Remotes: []string{"github.com/northwind/tools"}}
	p, err := PlanLink(cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Own || !p.OwnByPattern {
		t.Fatalf("own-pattern repo without --own: plan %+v, want Own and OwnByPattern", p)
	}
	req.Own = true
	if p, err = PlanLink(cfg, req); err != nil || !p.Own || p.OwnByPattern {
		t.Fatalf("--own: plan %+v, %v; want Own, not OwnByPattern", p, err)
	}
	client := LinkRequest{Entry: "northwind", Folder: "acme", ProjectID: pid, Remotes: []string{"github.com/acme/app"}}
	if p, err = PlanLink(cfg, client); err != nil || p.Own || p.OwnByPattern {
		t.Fatalf("client repo: plan %+v, %v; want a client link", p, err)
	}
}
