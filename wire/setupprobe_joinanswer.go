package wire

// setupprobe_joinanswer.go — the join step's checks read its one answer
// (B-10). The join step asks only "Org or invite"; before this, store.read
// and store.write looked for a store_url answer the step never has and
// skipped with "no store url yet", and invite.parse / invite.status /
// invite.email skipped with "not checked here yet", even when the answer
// was the org's store url itself:
//
//	answer ─▶ invite line ─▶ invite.parse decodes it; store.* probe its store
//	       ─▶ store url   ─▶ store.* probe it; invite.parse/status/email skip:
//	                         no invite line (invite.found checks the roster)
//	       ─▶ org name    ─▶ store.* probe that org entry's store

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// skipNoInviteLine is the skip detail of an invite check on a join by the
// org's store url or name: there is no invite to check, the roster is.
const skipNoInviteLine = "no invite line: an admin added you (lore member add); invite.found checks the roster"

// joinAnswer is the join step's answer, "" when r is not a join step's
// request (no invite question answered).
func joinAnswer(r opssetup.ProbeRequest) string {
	return strings.TrimSpace(answerText(r.Answers, onboard.QIDInvite))
}

// isInviteLine reports whether a join answer is an invite line.
func isInviteLine(answer string) bool { return strings.Contains(answer, onboard.InvitePrefix) }

// joinStoreURL is the store a join answer names: the invite's store, the
// url itself, or the store of the org entry of that name ("" when none).
func (sp setupProber) joinStoreURL(ctx context.Context, setup onboard.SetupEntry, answer string) string {
	switch {
	case answer == "":
		return ""
	case isInviteLine(answer):
		inv, err := onboard.DecodeInvite(answer)
		if err != nil {
			return ""
		}
		return inv.Store
	case looksLikeURL(answer):
		return answer
	}
	return sp.namedStoreURL(ctx, setup, answer)
}

// joinInviteCheck answers invite.parse, invite.status and invite.email of
// a join step; ok false when r is not one (the caller's default applies).
func joinInviteCheck(r opssetup.ProbeRequest) (opssetup.ProbeResult, bool) {
	answer := joinAnswer(r)
	if answer == "" {
		return opssetup.ProbeResult{}, false
	}
	if !isInviteLine(answer) {
		switch r.Check.ID {
		case onboard.CheckInviteParse, onboard.CheckInviteStatus, onboard.CheckInviteEmail:
			return skip(skipNoInviteLine), true
		}
		return opssetup.ProbeResult{}, false
	}
	if r.Check.ID != onboard.CheckInviteParse {
		return opssetup.ProbeResult{}, false
	}
	if _, err := onboard.DecodeInvite(answer); err != nil {
		return result(onboard.StatusFail, readFailure(err), nil), true
	}
	return result(onboard.StatusOK, "", nil), true
}
