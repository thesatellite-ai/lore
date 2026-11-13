package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// TestJoinStepReadsItsAnswer (B-10): the join step's store checks probe
// the store url the person answered (they skipped with "no store url
// yet"), and its invite checks say there is no invite line to check on a
// join by store url (they said "not checked here yet").
func TestJoinStepReadsItsAnswer(t *testing.T) {
	const url = "git@git.acme.example:acme/team-store.git"
	req := func(id onboard.CheckID, answer string) opssetup.ProbeRequest {
		return opssetup.ProbeRequest{Check: onboard.Check{ID: id}, Answers: onboard.Answers{onboard.QIDInvite: {Text: answer}}}
	}
	if got := (setupProber{}).storeURL(context.Background(), req(onboard.CheckStoreRead, url)); got != url {
		t.Fatalf("store.read of a join by url probes %q, want %q", got, url)
	}
	for _, id := range []onboard.CheckID{onboard.CheckInviteParse, onboard.CheckInviteStatus, onboard.CheckInviteEmail} {
		res, ok := joinInviteCheck(req(id, url))
		if !ok || res.Status != onboard.StatusSkip || res.Detail != skipNoInviteLine {
			t.Errorf("%s on a join by url = %+v (handled %v), want skip %q", id, res, ok, skipNoInviteLine)
		}
	}
	if res, ok := joinInviteCheck(req(onboard.CheckInviteParse, onboard.InvitePrefix+"garbage")); !ok || res.Status != onboard.StatusFail {
		t.Errorf("invite.parse of a bad invite line = %+v (handled %v), want fail", res, ok)
	}
	if _, ok := joinInviteCheck(req(onboard.CheckInviteFound, url)); ok {
		t.Error("invite.found must stay memberJoinFound's")
	}
}
