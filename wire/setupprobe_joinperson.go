package wire

// setupprobe_joinperson.go — the roster entry of the person joining
// (devco D-487), so `lore setup join` prefills their name and GitHub
// login from the roster rather than global git. The roster is read
// exactly where the join step's invite.found check reads it
// (setupprobe_memberjoin.go):
//
//	answer ─▶ org entry here (name, or its store url) ─▶ local roster
//	       ─▶ a store url no entry has ─▶ org.import with Admit reading the
//	          roster, then always taken back (errProbeAdmitted)
//	       ─▶ anything else ─▶ not found (git's values stay)

import (
	"context"
	"errors"
	"strings"

	"github.com/thesatellite-ai/lore/ops/orgcmd"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/team"
)

var _ opssetup.JoinPersonFinder = setupProber{}

// JoinPerson implements opssetup.JoinPersonFinder.
func (sp setupProber) JoinPerson(ctx context.Context, setup, answer, email string) (opssetup.JoinPerson, bool) {
	if sp.a == nil || answer == "" || !strings.Contains(email, "@") {
		return opssetup.JoinPerson{}, false
	}
	var (
		got   team.RosterPerson
		found bool
		org   string // the entry the roster was read under (D-645)
	)
	read := func(ctx context.Context, entry string) error {
		p, ok, err := team.FindRosterPerson(ctx, orgRosterStore{a: sp.a, org: entry}, []string{email})
		got, found, org = p, ok, entry
		return err
	}
	ctx = team.WithJoinSetup(ctx, setup)
	switch {
	case !strings.ContainsAny(answer, ":/"):
		if _, err := sp.a.TeamOrigin(ctx, answer); err != nil || read(ctx, answer) != nil {
			return opssetup.JoinPerson{}, false
		}
	case orgForStoreURL(sp.a.opts.Home, answer) != "":
		if read(ctx, orgForStoreURL(sp.a.opts.Home, answer)) != nil {
			return opssetup.JoinPerson{}, false
		}
	default:
		host, ok := sp.a.OrgHost(ctx)
		if !ok {
			return opssetup.JoinPerson{}, false
		}
		name, _, _ := strings.Cut(email, "@")
		_, err := host.Import(ctx, orgcmd.ImportIn{URL: answer, Key: humanKeyOf(email), Name: name, SkipRegister: true,
			Admit: func(ctx context.Context, entry string) error {
				if rerr := read(ctx, entry); rerr != nil {
					return rerr
				}
				return errProbeAdmitted // nothing stays in the config
			}}, true)
		if err != nil && !errors.Is(err, errProbeAdmitted) {
			return opssetup.JoinPerson{}, false
		}
	}
	if !found {
		return opssetup.JoinPerson{Org: org}, false
	}
	return opssetup.JoinPerson{Name: got.Name, GitHub: got.GitHub, Org: org}, true
}
