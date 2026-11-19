package orgresolve

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestResolveLocalRemote pins devco D-706: a repo whose only remote is a
// path on this machine is matched by its _local/<path> key. An own: (or
// repos:) pattern naming that key decides; with no pattern naming it the
// repo resolves exactly as a repo with no remote (the test rigs that use
// path remotes keep their role).
func TestResolveLocalRemote(t *testing.T) {
	const key = "_local/srv/git/acme/tools"
	setup := SetupHint{Name: "acme-admin", Org: "acme", Why: "test"}
	cases := []struct {
		name  string
		org   orgconf.Org
		local []string
		setup SetupHint
		want  Role
		step  Step
	}{
		{"own pattern names the path", orgconf.Org{Name: "acme", Own: []string{"_local/srv/git/acme/*"}}, []string{key}, SetupHint{}, RoleOwn, StepPattern},
		{"repos pattern names the path", orgconf.Org{Name: "acme", Own: []string{"_local/srv/git/**"}, Repos: []string{key}}, []string{key}, SetupHint{}, RoleClient, StepPattern},
		{"setup, own pattern names the path", orgconf.Org{Name: "acme", Own: []string{key}}, []string{key}, setup, RoleOwn, StepPattern},
		{"setup, no pattern: client as with no remote", orgconf.Org{Name: "acme", Own: []string{"github.com/acme-co/*"}}, []string{key}, setup, RoleClient, StepSetup},
		{"setup, no remote at all", orgconf.Org{Name: "acme", Own: []string{"github.com/acme-co/*"}}, nil, setup, RoleClient, StepSetup},
		{"no setup, no pattern: none", orgconf.Org{Name: "acme"}, []string{key}, SetupHint{}, RoleNone, StepNone},
	}
	for _, c := range cases {
		cfg := &orgconf.Config{Orgs: []orgconf.Org{c.org}}
		r, err := Resolve(Inputs{Config: cfg, LocalRemotes: c.local, Setup: c.setup})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if r.Role != c.want || r.Step != c.step {
			t.Errorf("%s: role %v step %v, want %v %v (%s)", c.name, r.Role, r.Step, c.want, c.step, r.MatchedBy)
		}
	}
}
