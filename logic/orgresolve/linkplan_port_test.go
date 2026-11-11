package orgresolve

// The v0.1.17 PlanLink matrix (saas/pkg/aicoder/orgresolve
// link_plan_test.go TestPlanLink), ported before phase E onto the root
// PlanLink (linkplan.go), same fixture: the default plan, --store naming
// the first store is still explicit (F13, cookbook 14), a staging entry
// links on its own branch, every refusal (unknown entry, reserved folder,
// no project id, no remote, two remotes without --remote, --remote not
// ours, --own on a client repo, --store not the entry's), and --own with
// --remote choosing among several remotes.

import (
	"errors"
	"strings"
	"testing"
)

func TestPlanLinkMatrix(t *testing.T) {
	cfg := fixture()
	base := LinkRequest{Entry: "northwind", Folder: "acme", ProjectID: pid, Remotes: []string{"github.com/acme/app"}}
	p, err := PlanLink(cfg, base)
	if err != nil {
		t.Fatal(err)
	}
	if p.StoreURL != shStore || p.Branch != "main" || p.Remote != "github.com/acme/app" || p.Own || p.StoreExplicit {
		t.Fatalf("plan = %+v", p)
	}
	// --store naming the FIRST store is still explicit: the writer must be
	// able to refuse a folder living in another store (F13, cookbook 14).
	ex := base
	ex.StoreURL = "https://github.com/northwind/lore"
	if p, err := PlanLink(cfg, ex); err != nil || !p.StoreExplicit || p.StoreURL != shStore {
		t.Fatalf("explicit first store: %+v, %v", p, err)
	}
	// staging entry links on its own branch (links exist per store branch)
	st := base
	st.Entry = "northwind-stag"
	if p, err := PlanLink(cfg, st); err != nil || p.Branch != "staging" {
		t.Fatalf("staging plan = %+v, %v", p, err)
	}

	type tc struct {
		name string
		mod  func(r *LinkRequest)
		want string
		is   error
	}
	cases := []tc{
		{"unknown entry", func(r *LinkRequest) { r.Entry = "ghost" }, "no org entry", nil},
		{"reserved folder", func(r *LinkRequest) { r.Folder = "_live" }, "reserved", nil},
		{"no remote", func(r *LinkRequest) { r.Remotes = nil }, "", ErrLinkNoRemote},
		{"two remotes, no --remote", func(r *LinkRequest) { r.Remotes = []string{"github.com/acme/app", "github.com/me/fork"} }, "choose one with --remote", nil},
		{"--remote not ours", func(r *LinkRequest) { r.Remote = "github.com/x/y" }, "not a remote of this repo", nil},
		{"--own on a client repo", func(r *LinkRequest) { r.Own = true }, "", ErrLinkOwnPattern},
		{"--store not the entry's", func(r *LinkRequest) { r.StoreURL = "git@github.com:betalabs/lore" }, "not one of", nil},
	}
	for _, c := range cases {
		r := base
		c.mod(&r)
		_, err := PlanLink(cfg, r)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if c.is != nil && !errors.Is(err, c.is) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.is)
		}
		var lr *LinkRefusal
		if !errors.As(err, &lr) || lr.Next == "" {
			t.Errorf("%s: err = %#v, want a LinkRefusal naming the next step", c.name, err)
			continue
		}
		if c.want != "" && !strings.Contains(lr.Msg+"; "+lr.Next, c.want) {
			t.Errorf("%s: err = %v (next: %s), want %q", c.name, err, lr.Next, c.want)
		}
	}

	// --own on an own repo; --remote picks among several
	own := base
	own.Own, own.Remotes, own.Remote = true, []string{"github.com/northwind/tools", "github.com/me/fork"}, "git@github.com:northwind/tools.git"
	if p, err := PlanLink(cfg, own); err != nil || !p.Own || p.Remote != "github.com/northwind/tools" {
		t.Fatalf("own plan = %+v, %v", p, err)
	}
}

// TestPlanLinkWithoutProjectID: a repo with no lore data (no project id)
// is linked by its remote: the plan's id is RemoteProjectID of the picked
// remote, the same on every machine and for every spelling of the remote,
// and differs from remote to remote.
func TestPlanLinkWithoutProjectID(t *testing.T) {
	cfg := fixture()
	req := LinkRequest{Entry: "northwind", Folder: "acme", Remotes: []string{"github.com/acme/app"}}
	p, err := PlanLink(cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	want, err := RemoteProjectID("github.com/acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if !p.FromRemote || p.ProjectID != want || !strings.HasPrefix(want, "prj_") {
		t.Fatalf("plan = %+v, want FromRemote with %s", p, want)
	}
	again, _ := PlanLink(cfg, req)
	if again.ProjectID != p.ProjectID {
		t.Fatalf("not deterministic: %s then %s", p.ProjectID, again.ProjectID)
	}
	other, _ := RemoteProjectID("github.com/acme/api")
	if other == want {
		t.Fatal("two remotes share one id")
	}
	withID := req
	withID.ProjectID = pid
	if p, err = PlanLink(cfg, withID); err != nil || p.FromRemote || p.ProjectID != pid {
		t.Fatalf("a repo with a project id: %+v %v", p, err)
	}
	none := req
	none.Remotes = nil
	if _, err = PlanLink(cfg, none); !errors.Is(err, ErrLinkNoRemote) {
		t.Fatalf("no id and no remote: %v, want ErrLinkNoRemote", err)
	}
}
