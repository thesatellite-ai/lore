package orgresolve

import (
	"errors"
	"strings"
	"testing"
)

func TestPlanLink(t *testing.T) {
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
		{"no project id", func(r *LinkRequest) { r.ProjectID = "" }, "", ErrLinkNoProjectID},
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
		if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}

	// --own on an own repo; --remote picks among several
	own := base
	own.Own, own.Remotes, own.Remote = true, []string{"github.com/northwind/tools", "github.com/me/fork"}, "git@github.com:northwind/tools.git"
	if p, err := PlanLink(cfg, own); err != nil || !p.Own || p.Remote != "github.com/northwind/tools" {
		t.Fatalf("own plan = %+v, %v", p, err)
	}
}
