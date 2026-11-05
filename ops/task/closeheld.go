package task

// closeheld.go — done and cancel of a claimed task (LORE_TEAM_SPEC F7,
// §7.4 "Human overrides"): the claim on the signals ref must end with the
// close, or a finished task stays held and its holder is never fenced out.
//
//	task done|cancel ─► work.TeamFrom(ctx) ─► HeldCloser?
//	   no team / no upgrade: the op alone (no claims on this surface; the
//	                         release binary wraps the command itself)
//	   done:   CloseHeldDone ─► the holder's fenced done (drops the claim),
//	           or a person's done over an expired claim (fences the holder),
//	           or E_INVALID_INPUT while someone else holds it; then the op
//	           records the done fields as always
//	   cancel: CloseHeldCancel ─► handled: the host cancelled the task
//	           through the fenced writer (E_CANCELLED for the holder) and
//	           the op prints the host's line instead of writing again
//
// P3-SIGCOORD-3: an optional upgrade, so a Team without claims is never
// forced to answer.

import "context"

// HeldCloser is an optional upgrade of the host's Team for closing a task
// that may be claimed.
type HeldCloser interface {
	// CloseHeldDone runs before task done writes. commit and pr are the
	// flags; the returned commit is the one to record (the holder's work
	// branch head when commit was empty, else commit). A task nobody
	// claims returns commit unchanged and nil.
	CloseHeldDone(ctx context.Context, taskID, commit string, pr int) (string, error)
	// CloseHeldCancel runs before task cancel writes. handled=true means
	// the task was claimed and the host cancelled it, dropping the claim;
	// line is what to print. handled=false: nobody claims it, the op
	// cancels as usual.
	CloseHeldCancel(ctx context.Context, taskID string) (line string, handled bool, err error)
}
