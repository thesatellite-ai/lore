package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestWhoamiRepoRowNamesThePlace: whoami's repo line says where the
// command ran. A plain folder under an org's setup, or a clone with no
// hosted remote, resolves to the client role (fail closed), and whoami
// called it "client repo" (B1-4); it now names the place and that the
// client role is the default there. A clone with a hosted remote keeps
// the lines it had.
func TestWhoamiRepoRowNamesThePlace(t *testing.T) {
	t.Parallel()
	org := &orgconf.Org{Name: "agency", Repos: []string{"github.com/client/*"}}
	hosted := []string{"github.com/client/website"}
	ownUnlisted := []string{"github.com/agency/tools"}
	cases := []struct {
		name    string
		notRepo bool
		remotes []string
		role    orgresolve.Role
		want    string
	}{
		{"client repo", false, hosted, orgresolve.RoleClient, "client repo  (guards on)"},
		{"own repo", false, hosted, orgresolve.RoleOwn, "own repo  (no client guards)"},
		// devco D-525: client by the fail-closed default alone (no own: or
		// repos: pattern, no link): the line says so and names the way out
		{"own repo never listed under own:", false, ownUnlisted, orgresolve.RoleClient, "treated as a client repo  (guards on: no own: pattern of agency matches github.com/agency/tools, so lore treats it as a client repo (fail closed); if it is agency's own repo: lore org config set --org agency --key own --add github.com/agency/tools --yes)"},
		{"no org", false, hosted, orgresolve.RoleNone, "no org  (fallback identity)"},
		{"plain folder under a setup", true, nil, orgresolve.RoleClient, "not a git repo  (treated as a client repo: guards on)"},
		{"plain folder, no org", true, nil, orgresolve.RoleNone, "not a git repo  (no org, fallback identity)"},
		{"clone with a local-path remote", false, nil, orgresolve.RoleClient, "git repo with no hosted remote  (treated as a client repo: guards on)"},
		{"own by a pin, no remote", false, nil, orgresolve.RoleOwn, "git repo with no hosted remote  (own: no client guards)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res := orgresolve.Result{Role: c.role}
			if c.role != orgresolve.RoleNone {
				res.Org = org
			}
			s := &orgcfg.Situation{Config: &orgconf.Config{}, HasConfig: true, Result: res, NotGitRepo: c.notRepo, Remotes: c.remotes}
			got, ok := lineOf(whoamiLines(s, identity.Resolved{DisplayName: "Alice", StableKey: "human:alice@agency.example"}, "", nil, false, whoamiRedactor{}, nil, testMachine, ""), "repo")
			if !ok || got != c.want {
				t.Fatalf("repo row %q (present %v), want %q", got, ok, c.want)
			}
		})
	}
}
