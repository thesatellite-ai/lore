package task

// claimshow.go — `lore task show <id> --signals` (LORE_TEAM_SPEC F7 Debug,
// CLI_REFERENCE task show, PENDING-60): after the task, the claim on it as
// this clone sees it now (holder, session, token prefix, epoch, expires,
// renewed_seq, work branch, owned resources), read from the signals ref
// the task is coordinated on.
//
//	task show --signals ─► work.TeamFrom(ctx) ─► ClaimShower?
//	   yes: its text goes after the body (ShowDetail.Tail), human only:
//	        --json keeps the row's shape, as v0.1.17's --json did
//	   no team / no upgrade: E_UNSUPPORTED (notYet), never a silent
//	        "no claim" a reader would take for an answer
//
// The signals machinery is the host's (the core imports no signals
// adapter), so the text is the host's too: an optional upgrade of Team,
// like ShowDetailer and HeldCloser.

import "context"

// ClaimShower is an optional upgrade of the host's Team: the claim view
// `task show --signals` prints.
type ClaimShower interface {
	// ClaimText is the claim on taskID as seen now: whole lines, each
	// ending in a newline, starting with a blank line (v0.1.17's layout:
	// "  claim:    …"). A task nobody claims is a "claim: none on <ref>"
	// line, not an error.
	ClaimText(ctx context.Context, taskID string) (string, error)
}

// flagSignals is task show's --signals.
const flagSignals = "signals"

// claimShow asks the context's Team for the claim text of taskID.
func claimShow(ctx context.Context, taskID string) (string, error) {
	t, err := team(ctx, flagSignals, "the claim is on the repo's refs/lore/signals: lore task list shows its STATE")
	if err != nil {
		return "", err
	}
	cs, ok := t.(ClaimShower)
	if !ok {
		return "", notYet(flagSignals, "the claim is on the repo's refs/lore/signals: lore task list shows its STATE")
	}
	return cs.ClaimText(ctx, taskID)
}
