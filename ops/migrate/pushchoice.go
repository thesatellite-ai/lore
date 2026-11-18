package migrate

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
)

// PushChoice says whether a migration run (or rollback) sends its store
// commits to the stores' remotes (devco D-612).
//
// Why a choice and not a bool: the run used to push by default (opt-out
// --no-push), so a store in push mode manual got its migration commits on
// the remote nobody asked for. The owner rule is that nothing reaches a
// remote without an explicit push or sync, or a push setting the person
// turned on; the default therefore follows each store's own push mode,
// and only --push sends whatever the mode says.
type PushChoice int

const (
	// PushPerMode follows each store's push mode: a store in mode now
	// pushes, manual and batch keep the commit local and the run prints
	// the command that sends it. The default.
	PushPerMode PushChoice = iota
	// PushNow pushes every store commit of the run (--push).
	PushNow
	// PushNever keeps every store commit local (--no-push, --offline).
	PushNever
)

// pushChoice is the run's PushChoice from its flags; --push with --no-push
// (or --offline) is E_INVALID_INPUT: the two ask opposite things.
func pushChoice(push, noPush, offline bool) (PushChoice, error) {
	switch {
	case push && (noPush || offline):
		return PushNever, errs.New(errs.InvalidInput, "migrate: --push sends the store commits and --no-push/--offline keeps them local; pick one").
			WithHint("lore migrate run --push, or lore migrate run --no-push")
	case push:
		return PushNow, nil
	case noPush || offline:
		return PushNever, nil
	}
	return PushPerMode, nil
}

// teamLockNeedsPush refuses --team-lock on a run that would not push for
// sure: the lock holds teammates off only while the migrated commits reach
// the remote, so a lock around commits that stay local (push mode manual)
// guards nothing, and taking it is itself a push. Asked for with --push it
// is a deliberate remote act; without it the run says what to add instead
// of pushing silently (devco D-612).
func teamLockNeedsPush(cmd string) error {
	return errs.New(errs.InvalidInput, "migrate: --team-lock holds refs/lore/signals on the remote while the run's store commits are pushed, so it needs --push; nothing changed").
		WithHint(cmd + " --team-lock --push, or " + cmd + " without --team-lock (each store's push mode decides then)")
}

// pushChoiceKey carries a rollback's PushChoice to the host's Rollback.
type pushChoiceKey struct{}

// WithPushChoice marks ctx with c for the host's Rollback, whose signature
// predates the choice (the run passes it to Finish directly).
func WithPushChoice(ctx context.Context, c PushChoice) context.Context {
	return context.WithValue(ctx, pushChoiceKey{}, c)
}

// PushChoiceFrom is WithPushChoice's mark; PushPerMode when absent.
func PushChoiceFrom(ctx context.Context) PushChoice {
	c, _ := ctx.Value(pushChoiceKey{}).(PushChoice) // absent: each store's push mode decides
	return c
}
