package orgresolve

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestOwnOfferFor pins devco D-526: only an admin is offered an own:
// pattern, only for a hosted remote nothing of the org names yet, and never
// over an explicit client listing.
func TestOwnOfferFor(t *testing.T) {
	const remote = "github.com/lumen-co/atlas"
	cases := []struct {
		name    string
		org     *orgconf.Org
		links   []Link
		remotes []string
		admin   bool
		want    string
	}{
		{"admin, unlisted", &orgconf.Org{Name: "lumen"}, nil, []string{remote}, true, "github.com/lumen-co/*"},
		{"member, unlisted", &orgconf.Org{Name: "lumen"}, nil, []string{remote}, false, ""},
		{"own pattern matches", &orgconf.Org{Name: "lumen", Own: []string{"github.com/lumen-co/*"}}, nil, []string{remote}, true, ""},
		{"explicit client pattern", &orgconf.Org{Name: "lumen", Repos: []string{remote}}, nil, []string{remote}, true, ""},
		{"client link", &orgconf.Org{Name: "lumen"}, []Link{{Entry: "lumen", Remote: remote}}, []string{remote}, true, ""},
		{"own link", &orgconf.Org{Name: "lumen"}, []Link{{Entry: "lumen", Remote: remote, Own: true}}, []string{remote}, true, ""},
		{"another org's link", &orgconf.Org{Name: "lumen"}, []Link{{Entry: "other", Remote: remote}}, []string{remote}, true, "github.com/lumen-co/*"},
		{"no remote", &orgconf.Org{Name: "lumen"}, nil, nil, true, ""},
		{"not hosted shape", &orgconf.Org{Name: "lumen"}, nil, []string{"gitlab.example/group/sub/atlas"}, true, ""},
		{"no org", nil, nil, []string{remote}, true, ""},
		// devco D-706: a server by its ssh name and a path remote's key are
		// offered the folder holding the repo; a forge remote still wins,
		// and a bare host/repo is never offered the whole server
		{"dotless server", &orgconf.Org{Name: "lumen"}, nil, []string{"buildbox/srv/git/lumen/atlas"}, true, "buildbox/srv/git/lumen/*"},
		{"path remote", &orgconf.Org{Name: "lumen"}, nil, []string{"_local/srv/git/lumen/atlas"}, true, "_local/srv/git/lumen/*"},
		{"forge first", &orgconf.Org{Name: "lumen"}, nil, []string{"_local/srv/git/lumen/atlas", remote}, true, "github.com/lumen-co/*"},
		{"whole server", &orgconf.Org{Name: "lumen"}, nil, []string{"buildbox/atlas"}, true, ""},
		{"path pattern matches", &orgconf.Org{Name: "lumen", Own: []string{"_local/srv/git/lumen/*"}}, nil, []string{"_local/srv/git/lumen/atlas"}, true, ""},
	}
	for _, c := range cases {
		got, ok := OwnOfferFor(c.org, c.links, c.remotes, c.admin)
		if ok != (c.want != "") || got.Pattern != c.want {
			t.Errorf("%s: got %+v ok=%v, want %q", c.name, got, ok, c.want)
		}
		if ok && OwnOfferCommand(got) != "lore org config set --org lumen --key own --add "+c.want+" --yes" {
			t.Errorf("%s: command %q", c.name, OwnOfferCommand(got))
		}
	}
}
