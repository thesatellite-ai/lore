package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestWhoamiJoinHintOnANewLaptop pins B-14: in a clone with a hosted
// remote on a machine with no org to switch to, whoami's "join" line
// names `lore setup join --invite` and where the store url comes from
// (a committed .lore/org.yaml names the org); a plain folder, a clone
// with no hosted remote and a machine with an org entry get no such line.
func TestWhoamiJoinHintOnANewLaptop(t *testing.T) {
	t.Parallel()
	hosted := []string{"git.example.test/acme/app"}
	withOrg := &orgconf.Config{Orgs: []orgconf.Org{{Name: "acme"}}}
	cases := []struct {
		name string
		s    orgcfg.Situation
		want string // "" = no join line
	}{
		{"new laptop", orgcfg.Situation{Remotes: hosted}, "lore setup join --invite <your org's store url>"},
		{"org.yaml names it", orgcfg.Situation{Remotes: hosted, Committed: &orgconf.OrgFile{Org: "acme"}}, "names org acme (.lore/org.yaml); join it on this machine: lore setup join --invite <acme's store url>"},
		{"plain folder", orgcfg.Situation{NotGitRepo: true}, ""},
		{"no hosted remote", orgcfg.Situation{}, ""},
		{"an org entry to switch to", orgcfg.Situation{Config: withOrg, HasConfig: true, Remotes: hosted}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := c.s
			got, ok := lineOf(whoamiLines(&s, identity.Resolved{DisplayName: "Ana", StableKey: "human:ana@example.test"}, "", nil, false, whoamiRedactor{}, nil, testMachine, ""), "join")
			switch {
			case c.want == "" && ok:
				t.Fatalf("join line %q, want none", got)
			case c.want != "" && (!ok || !strings.Contains(got, c.want) || !strings.Contains(got, "lore member add")):
				t.Fatalf("join line %q (present %v), want it to say %q and where the url comes from", got, ok, c.want)
			}
		})
	}
}
