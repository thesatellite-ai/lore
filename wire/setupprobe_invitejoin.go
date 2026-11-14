package wire

// setupprobe_invitejoin.go — invite.found, invite.status and invite.email
// of a join step answered with an invite line (J-9). They skipped with
// "not checked here yet", so `lore setup check --step join` said ready
// for an invite apply then refused (already accepted, for another
// address). They now run apply's own check (team.CheckInviteJoin), on the
// org entry here or through an org.import that is always taken back, as
// memberJoinFound does for a join by the org's name or url:
//
//	invite line ─▶ entry here for its org ─▶ CheckInviteJoin (a re-run)
//	            ─▶ no entry ─▶ org.import, Admit = CheckInviteJoin, then
//	               errProbeAdmitted takes the entry out again
//	the error's code ─▶ the one check it belongs to fails; the others pass

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/orgcmd"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/team"
)

// inviteCheckOf is the invite check an invite join's refusal belongs to.
var inviteCheckOf = map[errs.Code]onboard.CheckID{
	errs.InviteNotFound: onboard.CheckInviteFound,
	errs.InviteRevoked:  onboard.CheckInviteStatus,
	errs.InviteUsed:     onboard.CheckInviteStatus,
	errs.InviteExpired:  onboard.CheckInviteStatus,
	errs.InviteInvalid:  onboard.CheckInviteStatus,
	errs.InviteEmail:    onboard.CheckInviteEmail,
	errs.ContactTaken:   onboard.CheckInviteEmail,
}

// inviteJoinMu serialises the invite checks: setup check runs a step's
// checks at once, and each one's import adds and takes back the same org
// entry. Racing, one check saw another's entry, took the join for a
// re-run done here, and said ok for an invite apply refuses.
var inviteJoinMu sync.Mutex

// inviteJoinCheck answers invite.found / invite.status / invite.email of
// a join answered with an invite line; ok false for any other request.
func (sp setupProber) inviteJoinCheck(ctx context.Context, r opssetup.ProbeRequest) (opssetup.ProbeResult, bool) {
	switch r.Check.ID {
	case onboard.CheckInviteFound, onboard.CheckInviteStatus, onboard.CheckInviteEmail:
	default:
		return opssetup.ProbeResult{}, false
	}
	answer := joinAnswer(r)
	if !isInviteLine(answer) {
		return opssetup.ProbeResult{}, false
	}
	inv, err := onboard.DecodeInvite(answer)
	if err != nil {
		return skip("the invite line does not read (invite.parse)"), true
	}
	ans, aerr := setupApplier{a: sp.a}.setupAnswers(ctx, r.Setup, r.Answers)
	if aerr != nil {
		return result(onboard.StatusError, readFailure(aerr), nil), true
	}
	var emails []string
	for _, e := range append([]string{answerText(ans, onboard.QIDEmail)}, ans[onboard.QIDEmails].List...) {
		if e = strings.TrimSpace(e); strings.Contains(e, "@") && !slices.Contains(emails, e) {
			emails = append(emails, e)
		}
	}
	if len(emails) == 0 {
		return skip(skipNoEmail), true
	}
	inviteJoinMu.Lock()
	defer inviteJoinMu.Unlock()
	check := func(ctx context.Context, entry string, rerun bool) error {
		return team.CheckInviteJoin(ctx, orgRosterStore{a: sp.a, org: entry}, inv, emails, rerun)
	}
	if _, oerr := sp.a.TeamOrigin(ctx, inv.Org); oerr == nil {
		err = check(ctx, inv.Org, true)
	} else {
		host, ok := sp.a.OrgHost(ctx)
		if !ok {
			return skip(notHereDetail(r.Check.ID)), true
		}
		name, _, _ := strings.Cut(emails[0], "@")
		_, err = host.Import(ctx, orgcmd.ImportIn{URL: inv.Store, Key: humanKeyOf(emails[0]), Name: cmp.Or(answerText(ans, onboard.QIDName), name),
			Handle: answerText(ans, onboard.QIDHandle), As: inv.Org, SkipRegister: true,
			Admit: func(ctx context.Context, entry string) error {
				if cerr := check(ctx, entry, false); cerr != nil {
					return cerr
				}
				return errProbeAdmitted
			}}, true)
		if errors.Is(err, errProbeAdmitted) {
			err = nil
		}
	}
	if err == nil {
		return result(onboard.StatusOK, "", nil), true
	}
	owner, known := inviteCheckOf[errs.CodeOf(err)]
	switch {
	case !known:
		return result(onboard.StatusError, readFailure(err), nil), true
	case owner == r.Check.ID:
		return result(onboard.StatusFail, readFailure(err), nil), true
	}
	return skip("see " + string(owner)), true
}
