package wire

// migrate_teamlock_standalone.go — the team migration lock of the binary
// built from the root module (FORMAT_EVOLUTION 7.2, 7.5; ported from the
// old binary's saas/cmd/cli/migrate_teamlock.go): `lore migrate
// run|rollback|unlock` get an opsmigrate.TeamLock built on the hold
// mechanism of the repo's signals ref (signals.DecideHold + Store.Write:
// one compare-and-swap push, never a wait), so migrate is one more caller
// of `lore hold` with the name `lore-migrate`.
//
//	Acquire ─► coord world ─► no remote: nothing to race, none taken
//	        ─► Store.Write(DecideHold lore-migrate, TTL 10m)
//	             held by another: E_MIGRATE_LOCKED (holder, host, since, renewed)
//	             remote does not exist ("Repository not found"): none
//	               taken, a line saying so; the run migrates locally
//	             offline / autopush off: E_OFFLINE
//	        ─► renew every 2m until release ─► release: Store.Write(delete)
//	Unlock  ─► expired hold: dropped · own hold from this host: dropped
//	        ─► any other live hold: E_MIGRATE_LOCKED

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

// Team lock lease (7.2): taken for 10 minutes, renewed every 2.
const (
	migrateLockName  = "lore-migrate"
	migrateLockTTL   = 10 * time.Minute
	migrateLockRenew = 2 * time.Minute
)

// MigrateTeamLock is the team migration lock of this App's repo, for the
// host to put on the context of the migrate commands (opsmigrate.WithTeamLock).
func (a *App) MigrateTeamLock() opsmigrate.TeamLock { return migrateTeamLock{a: a} }

// fixTeamLock is the team lock lore fix takes around its migrate step: the
// same lock, except that a client repo no store link names takes none and
// says how to link it (owner item 25): its rows are not shared through a
// store, so there is nobody to race, and the repo is fixed instead of
// failing. `lore migrate run` keeps refusing there (MigrateTeamLock).
func (a *App) fixTeamLock() opsmigrate.TeamLock {
	return migrateTeamLock{a: a, skipUnlinkedClient: true}
}

// migrateTeamLock implements opsmigrate.TeamLock on the repo's signals ref.
type migrateTeamLock struct {
	a *App
	// skipUnlinkedClient: a client repo with no store link takes no lock
	// (a note naming the link command) instead of E_UNSUPPORTED.
	skipUnlinkedClient bool
}

// unlinkedClientLine is the team lock line of a client repo with no store
// link under lore fix; %s is the link command.
const unlinkedClientLine = "team lock: skipped, this is a client repo with no store link, so there is no store signals ref to hold it on (nobody shares its rows through a store to race); to share it, link it: %s"

// remoteMissingLine is the team lock line of a repo whose remote does not
// exist (git answered "Repository not found" or "does not appear to be a
// git repository", signals.RemoteMissing); %s is the remote's name, then
// git's line. An unreachable remote or refused credentials stay E_OFFLINE.
const remoteMissingLine = "team lock: skipped, the remote %s does not exist (%s), so nobody shares this repo through it and there is nobody to race; migrating locally (fix or remove the remote with `git remote` to share it)"

var _ opsmigrate.TeamLock = migrateTeamLock{}

// open is the coordination world and its signals store. The lock lives
// where the checkout's holds live (forHolds): the repo's own signals ref,
// or in a client repo linked to an org store the store's signals ref under
// the link folder (D8: nothing is written to the client remote); a client
// repo with no store link is refused there. ok is false when a non-client
// repo has no remote (a solo repo: nobody to race, cookbook 1).
func (l migrateTeamLock) open(ctx context.Context) (*coordWorld, *signals.Store, bool, error) {
	w, err := standaloneCoord{a: l.a}.openWorld(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	if err = w.forHolds(ctx); err != nil {
		return w, nil, false, err // w: the caller may read why (unlinkedClient)
	}
	if w.spot.remote == nil {
		return w, nil, false, nil
	}
	return w, w.signalsStore(), true, nil
}

// lockPlace names where the team lock lives, for the run's lines: the
// signals ref, and the store folder when it is a linked store's.
func lockPlace(w *coordWorld) string {
	if w.spot.store {
		return w.spot.ref + " of the linked store (folder " + w.folder + ")"
	}
	return w.spot.ref
}

// Acquire implements opsmigrate.TeamLock.
func (l migrateTeamLock) Acquire(ctx context.Context) (func() error, string, error) {
	w, st, ok, err := l.open(ctx)
	if err != nil {
		if l.skipUnlinkedClient && w != nil && w.unlinkedClient() {
			return nil, fmt.Sprintf(unlinkedClientLine, w.linkCommand(ctx)), nil
		}
		return nil, "", err
	}
	if !ok {
		return nil, "team lock: this repo has no remote " + w.remoteName + "; nobody to race, none taken", nil
	}
	path := signals.ResourcePath(w.folder, migrateLockName)
	hold := func() (signals.Signal, error) {
		var held signals.Signal
		_, werr := st.Write(ctx, func(v signals.View) (signals.Change, error) {
			tok, terr := st.Tokens.New()
			if terr != nil {
				return signals.Change{}, terr
			}
			s, derr := signals.DecideHold(v, signals.HoldRequest{Caller: w.caller(), Folder: w.folder, Name: migrateLockName, TTL: migrateLockTTL, Token: tok})
			held = s
			return signals.Change{Put: map[string]signals.Signal{path: s}}, derr
		})
		return held, werr
	}
	held, err := hold()
	if err != nil {
		if !w.spot.store && signals.RemoteMissing(err) {
			// the repo's remote does not exist (deleted, or never this
			// person's): nobody shares the repo through it, so there is
			// nobody to race; migrate locally and say why (item 34)
			return nil, fmt.Sprintf(remoteMissingLine, w.spot.remoteName, signals.ShortCause(err)), nil
		}
		return nil, "", migrateLockError(err, l.a.opts.Wall())
	}
	line := "team lock: taken on " + lockPlace(w) + " until " + held.Expires.UTC().Format(time.RFC3339) + " (renewed every " + migrateLockRenew.String() + ")"
	if held.RenewedSeq > 0 {
		line = "team lock: renewed this machine's own hold on " + lockPlace(w)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() { // renew (7.2); a failed renewal is retried on the next tick, the lease covers several
		t := time.NewTicker(migrateLockRenew)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = hold() // a lost renewal is retried on the next tick
			}
		}
	})
	var once sync.Once
	release := func() error {
		var rerr error
		once.Do(func() {
			close(stop)
			wg.Wait()
			_, rerr = st.Write(context.WithoutCancel(ctx), func(v signals.View) (signals.Change, error) {
				p, derr := signals.DecideUnhold(v, w.caller(), w.folder, migrateLockName)
				return signals.Change{Delete: []string{p}}, derr
			})
		})
		return rerr
	}
	return release, line, nil
}

// Unlock implements opsmigrate.TeamLock (7.2: an expired hold, or this
// actor's own hold from this host; the caller holds the machine lock).
func (l migrateTeamLock) Unlock(ctx context.Context) (opsmigrate.UnlockOut, error) {
	w, st, ok, err := l.open(ctx)
	if err != nil {
		return opsmigrate.UnlockOut{}, err
	}
	if !ok {
		return opsmigrate.UnlockOut{Line: "this repo has no remote " + w.remoteName + "; there is no team lock"}, nil
	}
	host, _ := os.Hostname() // no host name: only an expired hold can be cleared
	path := signals.ResourcePath(w.folder, migrateLockName)
	out := opsmigrate.UnlockOut{Line: "no team migration lock on " + lockPlace(w)}
	_, err = st.Write(ctx, func(v signals.View) (signals.Change, error) {
		s, present := v.Board[path]
		if !present {
			return signals.Change{}, nil
		}
		who := holderName(s)
		if active, live := v.Active(path); live {
			if active.Holder != w.actorID || active.Host != host {
				return signals.Change{}, &signals.HeldError{Path: path, By: active, SameActor: active.Holder == w.actorID}
			}
			out = opsmigrate.UnlockOut{Released: true, Line: "released " + who + "'s team migration lock (" + host + ", no migration running here); run `lore migrate run` to finish"}
		} else {
			out = opsmigrate.UnlockOut{Released: true, Line: "cleared " + who + "'s expired team migration lock (last renewed " + holdRenewedAt(s).UTC().Format(time.RFC3339) + ")"}
		}
		return signals.Change{Delete: []string{path}}, nil
	})
	if err != nil {
		return opsmigrate.UnlockOut{}, migrateLockError(err, l.a.opts.Wall())
	}
	return out, nil
}

// holdRenewedAt is a hold's last renewal (Since when an older binary wrote none).
func holdRenewedAt(s signals.Signal) time.Time {
	if !s.RenewedAt.IsZero() {
		return s.RenewedAt
	}
	return s.Since
}

// migrateLockError maps a signals error to the core's codes: a live hold
// is E_MIGRATE_LOCKED naming holder, host and age (7.2); a push that could
// not be confirmed is E_OFFLINE (the hold needs the network).
func migrateLockError(err error, now time.Time) error {
	var he *signals.HeldError
	var off *signals.OfflineError
	switch {
	case errors.As(err, &he):
		s := he.By
		msg := "migrate: " + holderName(s) + " is migrating this project, started " + shortDuration(now.Sub(s.Since).Round(time.Second)) + " ago"
		if s.Host != "" {
			msg += " on " + s.Host
		}
		msg += " (lock renewed " + shortDuration(now.Sub(holdRenewedAt(s)).Round(time.Second)) + " ago)"
		return errs.New(errs.MigrateLocked, msg).
			WithHint("run `lore migrate status` in a few minutes; a crashed holder's lock expires on its own, or its owner clears it with `lore migrate unlock --team`")
	case errors.As(err, &off):
		return errs.Wrap(errs.Offline, "migrate: the team lock on "+signals.CodeRepoRef+" could not be taken: "+off.Reason, err).
			WithHint("reconnect and run again, or `lore migrate run --offline` (branch files only) / `--no-push`")
	}
	return err
}
