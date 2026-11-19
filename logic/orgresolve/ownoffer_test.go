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
