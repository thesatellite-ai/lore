package migrate

// The team lock (FORMAT_EVOLUTION 7.2, 7.5): a `lore-migrate` hold on the
// project's signals ref, so two teammates never migrate at once.
//
//	run ─► M (LockMigration, machine) ─► T (TeamLock.Acquire; only with
//	       --team-lock, never for --dry-run, --no-push, --offline) ─► … ─►
//	       release T ─► release M
//	rollback ─► M ─► T (--team-lock) ─► …        (7.2: a rollback cannot race a run)
//
// T writes to the remote (a hold on refs/lore/signals), so it is taken
// only when the person names --team-lock (owner rule 1: nothing reaches a
// remote without an explicit push, sync or flag); a plain run holds only
// the machine lock M and says so (localLockLine).
//	unlock --team ─► clear a stale M ─► M ─► TeamLock.Unlock ─► release M
//
// The signals machinery lives in the host (the CLI), which the core cannot
// import, so the host supplies a TeamLock per command with WithTeamLock,
// like the work Team port. A surface without one (REST) takes no team lock
// and says so in the run's journal lines.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TeamLock is the host's team migration lock.
type TeamLock interface {
	// Acquire takes the hold (one compare-and-swap push, never waits, 7.5
	// rule 1) and keeps renewing it until release is called. A hold that
	// someone else keeps alive is E_MIGRATE_LOCKED naming holder, host and
	// age; an unreachable remote is E_OFFLINE. line says what happened
	// ("" when there is nothing to say). A nil release with a nil error
	// means no hold was taken (a repo with no remote: nobody to race), so
	// the run never reports releasing one.
	Acquire(ctx context.Context) (release func() error, line string, err error)
	// Unlock clears a stale hold: one that expired, or this actor's own
	// hold from this machine (the caller already holds the machine lock,
	// so no migration runs here). Any other live hold is E_MIGRATE_LOCKED.
	Unlock(ctx context.Context) (UnlockOut, error)
}

// teamLockKey is the context key of the command's TeamLock.
type teamLockKey struct{}

// WithTeamLock returns ctx carrying l for the migrate operations under it.
func WithTeamLock(ctx context.Context, l TeamLock) context.Context {
	return context.WithValue(ctx, teamLockKey{}, l)
}

// teamLockFrom returns the host's TeamLock, if any.
func teamLockFrom(ctx context.Context) (TeamLock, bool) {
	l, ok := ctx.Value(teamLockKey{}).(TeamLock)
	return l, ok && l != nil
}

// noTeamLockLine is the journal line of a run on a surface with no team lock.
const noTeamLockLine = "team lock: not available on this surface (no signals ref here); run `lore migrate run` from the CLI to guard against a teammate migrating at the same time"

// localLockLine is the journal line of a run that would push but was not
// asked for --team-lock: only this machine's lock guarded it, and nothing
// was written to the remote for the lock (A-7: the lock used to push
// refs/lore/signals to origin on every run).
const localLockLine = "team lock: none (this machine's migration lock only; nothing written to the remote for it); `--team-lock` also holds refs/lore/signals on the remote while the run lasts, so a teammate cannot migrate at the same time"

// teamLockReleasedLine ends the journal of a run that took T.
const teamLockReleasedLine = "team lock released"

// teamHold is what takeTeamLock did: the release func (never nil), the
// journal line ("" for none) and whether a hold was really taken (only
// then does the run print teamLockReleasedLine).
type teamHold struct {
	release func() error
	line    string
	taken   bool
}

// takeTeamLock takes T when the person asked for it (asked: --team-lock),
// the host gives one and the run will push. A run that pushes without
// --team-lock gets localLockLine and writes nothing to the remote.
func takeTeamLock(ctx context.Context, pushes, asked bool) (teamHold, error) {
	none := func() error { return nil }
	if !pushes {
		return teamHold{release: none}, nil
	}
	if !asked {
		return teamHold{release: none, line: localLockLine}, nil
	}
	l, ok := teamLockFrom(ctx)
	if !ok {
		return teamHold{release: none, line: noTeamLockLine}, nil
	}
	release, line, err := l.Acquire(ctx)
	if err != nil {
		return teamHold{}, err
	}
	if release == nil { // the host took no hold (no remote): nothing to release
		return teamHold{release: none, line: line}, nil
	}
	return teamHold{release: release, line: line, taken: true}, nil
}

// unlockTeam is `lore migrate unlock --team` (7.2): clear this machine's
// stale machine lock (refused while its process lives), take M so no
// migration can start here meanwhile, then let the host clear the hold.
func unlockTeam(ctx context.Context, rw Rewriter, j Journal) (UnlockOut, error) {
	l, ok := teamLockFrom(ctx)
	if !ok {
		return UnlockOut{}, errs.New(errs.InvalidInput, "migrate: the team lock lives on the signals ref, which this surface cannot reach").
			WithHint("run `lore migrate unlock --team` from the CLI")
	}
	if _, err := j.Unlock(ctx); err != nil {
		return UnlockOut{}, err
	}
	unlock, err := rw.LockMigration(ctx)
	if err != nil {
		return UnlockOut{}, err
	}
	defer func() { _ = unlock() }() // a failed release is reported by the next run
	return l.Unlock(ctx)
}
