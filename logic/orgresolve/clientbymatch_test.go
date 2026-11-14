package orgresolve

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestClientByMatch pins I-13's line between a client repo an org claims
// (a client link, or its repos: pattern) and one that is client only by
// the fail-closed default (an own repo nobody listed under own:): only
// the first is steered away from lore init.
func TestClientByMatch(t *testing.T) {
	o := &orgconf.Org{Name: "acme", Repos: []string{"github.com/client-co/*"}}
	cases := []struct {
		name    string
		links   []Link
		remotes []string
		want    bool
	}{
		{"repos: pattern", nil, []string{"github.com/client-co/shop"}, true},
		{"client link", []Link{{Entry: "acme", Folder: "web"}}, []string{"github.com/acme/web"}, true},
		{"own link", []Link{{Entry: "acme", Folder: "web", Own: true}}, []string{"github.com/acme/web"}, false},
		{"another org's link", []Link{{Entry: "other", Folder: "web"}}, []string{"github.com/acme/web"}, false},
		{"unlisted repo (client by default)", nil, []string{"github.com/acme/app"}, false},
	}
	for _, c := range cases {
		if got := ClientByMatch(o, c.links, c.remotes); got != c.want {
			t.Errorf("%s: ClientByMatch = %v, want %v", c.name, got, c.want)
		}
	}
	if ClientByMatch(nil, nil, []string{"github.com/client-co/shop"}) {
		t.Error("no org is never a client match")
	}
}
