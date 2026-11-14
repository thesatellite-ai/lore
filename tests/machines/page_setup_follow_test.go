//go:build machines && !windows

package machines

// page_setup_follow_test.go — the setup page follows the setup it just
// saved (devco J-16). A person with no lore config opens `lore setup web`
// in their home folder and saves a whole admin setup there (the team step
// makes the org). On the same page, without restarting it, whoami answers
// that org and the person of the setup, and team.invite writes the invite.
// Before the fix the page kept the org and identity it read at start: org
// "" and the OS login, and team.invite failed "this repo has no org" until
// the page was restarted.

import (
	"encoding/json"
	"strings"
	"testing"
)

// pfOrg / pfSetup / pfEmail are the made-up team, setup and person.
const (
	pfOrg   = "probeorg"
	pfSetup = "probeorg"
	pfEmail = "diego@example.test"
)

func TestSetupPageFollowsTheSetupItSaved(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	lsGHHosts(t, m)
	spBare(t, m, "team", lsTeamURL)
	p := m.StartPage(t)
	steps := []struct {
		id      string
		answers map[string]any
	}{
		{"kind", map[string]any{"kind": kindAdmin, "name": pfSetup}},
		{"identity", map[string]any{"name": PersonName, "email": pfEmail}},
		{"github", map[string]any{"github_account": lsAlpha.login, "host_alias": lsAlpha.alias}},
		{"personal_store", map[string]any{"personal_store": "local"}},
		{"team", map[string]any{"org": pfOrg, "store_url": lsTeamURL}},
		{"folders", map[string]any{"folders": []string{}, "default": true}},
	}
	for _, s := range steps {
		in := map[string]any{"setup": pfSetup, "step": s.id, "answers": s.answers}
		if err := p.Call("setup.apply", in, nil); err != nil {
			t.Fatalf("setup.apply %s: %v\n%s", s.id, err, p.Output())
		}
	}
	var who json.RawMessage
	if err := p.Call("whoami.show", map[string]any{}, &who); err != nil {
		t.Fatalf("whoami.show: %v", err)
	}
	for _, want := range []string{`"org":"` + pfOrg + `"`, pfEmail} {
		if !strings.Contains(string(who), want) {
			t.Errorf("whoami on the page right after the setup was saved lacks %s (the page kept what it read at start):\n%s", want, who)
		}
	}
	var inv json.RawMessage
	if err := p.Call("team.invite", map[string]any{"email": "pat@example.test"}, &inv); err != nil {
		t.Fatalf("team.invite on the same page after the admin setup was saved: %v", err)
	}
	if !strings.Contains(string(inv), "lore-invite:") {
		t.Errorf("team.invite wrote no invite: %s", inv)
	}
}
