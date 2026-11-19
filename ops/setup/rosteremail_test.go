package setup

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// rosterProber answers git.emails with emails and invite.found ok for the
// addresses on the roster.
type rosterProber struct {
	emails []string
	roster map[string]bool
	probed []string // the emails invite.found was asked about
}

func (p *rosterProber) Prefill(_ context.Context, id onboard.PrefillID, _ onboard.SetupEntry) (PrefillOut, error) {
	if id != onboard.PrefillGitEmails {
		return PrefillOut{}, nil
	}
	out := PrefillOut{}
	for _, e := range p.emails {
		out.Choices = append(out.Choices, onboard.Choice{Value: e, Label: e})
	}
	return out, nil
}

func (p *rosterProber) Probe(_ context.Context, reqs []ProbeRequest) []ProbeResult {
	out := make([]ProbeResult, 0, len(reqs))
	for _, r := range reqs {
		email := r.Answers[onboard.QIDEmail].Text
		p.probed = append(p.probed, email)
		st := onboard.StatusFail
		if r.Check.ID == onboard.CheckInviteFound && p.roster[email] && r.Answers[onboard.QIDInvite].Text != "" {
			st = onboard.StatusOK
		}
		out = append(out, ProbeResult{Check: r.Check.ID, Status: st})
	}
	return out
}

// rosterEnv is fakeEnv with a rosterProber.
type rosterEnv struct {
	fakeEnv
	pr *rosterProber
}

func (e rosterEnv) SetupProber(context.Context) (Prober, error) { return e.pr, nil }

// TestJoinLinePrefersTheRosterEmail (devco D-448): a join by store url on
// a machine whose global git email is personal and whose work address is
// in an includeIf file prefills the address the org's roster has; before,
// the identity line carried the global one, which the roster does not
// know. One git email reads no roster at all.
func TestJoinLinePrefersTheRosterEmail(t *testing.T) {
	const url = "git@git.acme.example:acme/team-store.git"
	for _, c := range []struct {
		name   string
		emails []string
		roster map[string]bool
		want   string
		probes int
	}{
		{"work address in an includeIf", []string{"sam@home.example", "sam@acme.example"}, map[string]bool{"sam@acme.example": true}, "sam@acme.example", 2},
		{"global address on the roster", []string{"sam@acme.example", "sam@home.example"}, map[string]bool{"sam@acme.example": true}, "sam@acme.example", 1},
		{"none on the roster", []string{"sam@home.example", "sam@other.example"}, map[string]bool{}, "", 2},
		{"one git email", []string{"sam@home.example"}, map[string]bool{"sam@home.example": true}, "", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			pr := &rosterProber{emails: c.emails, roster: c.roster}
			e := newEngine(rosterEnv{fakeEnv: newFakeEnv(), pr: pr}, onboardCatalog{})
			if got := e.rosterEmail(bg, "acme", url); got != c.want {
				t.Errorf("rosterEmail = %q, want %q", got, c.want)
			}
			if len(pr.probed) != c.probes {
				t.Errorf("invite.found asked about %v, want %d tries", pr.probed, c.probes)
			}
		})
	}
}
