package main

// The CLI's team migration lock (FORMAT_EVOLUTION 7.2, 7.5): the core's
// `lore migrate run|rollback|unlock --team` get an opsmigrate.TeamLock in
// their context, built on the v0.1.17 hold mechanism of the signals ref
// (signals.DecideHold + Store.Write: one compare-and-swap push, never a
// wait), so migrate is one more caller of `lore hold` with the name
// `lore-migrate`.
//
//	Acquire ─► openCoordWorld ─► no remote: nothing to race, skipped
//	        ─► Store.Write(DecideHold lore-migrate, TTL 10m)
//	             held by another: E_MIGRATE_LOCKED (holder, host, since, renewed)
//	             offline / autopush off: E_OFFLINE (hint --offline / --no-push)
//	        ─► renew every 2m until release ─► release: Store.Write(delete)
//	Unlock  ─► expired hold: dropped · own hold from this host: dropped
//	        ─► any other live hold: E_MIGRATE_LOCKED

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"

	"saas/pkg/aicoder/signals"
)

// Team lock lease (7.2): taken for 10 minutes, renewed every 2.
const (
	migrateLockName  = "lore-migrate"
	migrateLockTTL   = 10 * time.Minute
	migrateLockRenew = 2 * time.Minute
)

// migrateLockVerbs are the core migrate commands that take or clear T.
var migrateLockVerbs = []string{"run", "rollback", "unlock"}

// withMigrateTeamLock gives the core-mounted migrate commands the CLI's
// team lock in their context (core_mount.go calls it after cli.Mount).
func withMigrateTeamLock(root *cobra.Command) {
	for _, verb := range migrateLockVerbs {
		c, _, err := root.Find([]string{"migrate", verb})
		if err != nil || c.Name() != verb || c.RunE == nil {
			continue
		}
		run := c.RunE
		c.RunE = func(cmd *cobra.Command, args []string) error {
			cmd.SetContext(migrateTestHooks(opsmigrate.WithTeamLock(cmd.Context(), cliMigrateLock{}))) // hooks: test builds only
			return run(cmd, args)
		}
	}
}

// cliMigrateLock implements opsmigrate.TeamLock on the repo's signals ref.
type cliMigrateLock struct{}

// open gathers the coordination world and the signals store; ok is false
// when the repo has no remote (a solo repo: nobody to race, cookbook 1).
func (cliMigrateLock) open(ctx context.Context) (*coordWorld, *signals.Store, bool, error) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	if w.Remote == nil {
		return w, nil, false, nil
	}
	if err = w.requireActor(); err != nil {
		return nil, nil, false, err
	}
	st, err := w.repoSignalsChecked()
	if err != nil {
		return nil, nil, false, err
	}
	return w, st, true, nil
}

// Acquire implements opsmigrate.TeamLock.
func (l cliMigrateLock) Acquire(ctx context.Context) (func() error, string, error) {
	w, st, ok, err := l.open(ctx)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "team lock: this repo has no remote " + w.RemoteName + "; nobody to race, none taken", nil
	}
	path := signals.ResourcePath(w.Folder, migrateLockName)
	hold := func() (signals.Signal, error) {
		var held signals.Signal
		_, werr := st.Write(ctx, func(v signals.View) (signals.Change, error) {
			tok, terr := st.Tokens.New()
			if terr != nil {
				return signals.Change{}, terr
			}
			s, derr := signals.DecideHold(v, signals.HoldRequest{Caller: w.Caller(), Folder: w.Folder, Name: migrateLockName, TTL: migrateLockTTL, Token: tok})
			held = s
			return signals.Change{Put: map[string]signals.Signal{path: s}}, derr
		})
		return held, werr
	}
	held, err := hold()
	if err != nil {
		return nil, "", migrateLockError(err, w.Clock.Now())
	}
	line := "team lock: taken on " + signals.CodeRepoRef + " until " + held.Expires.UTC().Format(time.RFC3339) + " (renewed every " + migrateLockRenew.String() + ")"
	if held.RenewedSeq > 0 {
		line = "team lock: renewed this machine's own hold on " + signals.CodeRepoRef
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // renew (7.2); a failed renewal is retried on the next tick, the lease covers several
		defer wg.Done()
		t := time.NewTicker(migrateLockRenew)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = hold()
			}
		}
	}()
	var once sync.Once
	release := func() error {
		var rerr error
		once.Do(func() {
			close(stop)
			wg.Wait()
			_, rerr = st.Write(context.WithoutCancel(ctx), func(v signals.View) (signals.Change, error) {
				p, derr := signals.DecideUnhold(v, w.Caller(), w.Folder, migrateLockName)
				return signals.Change{Delete: []string{p}}, derr
			})
		})
		return rerr
	}
	return release, line, nil
}

// Unlock implements opsmigrate.TeamLock (7.2: an expired hold, or this
// actor's own hold from this host; the caller holds the machine lock).
func (l cliMigrateLock) Unlock(ctx context.Context) (opsmigrate.UnlockOut, error) {
	w, st, ok, err := l.open(ctx)
	if err != nil {
		return opsmigrate.UnlockOut{}, err
	}
	if !ok {
		return opsmigrate.UnlockOut{Line: "this repo has no remote " + w.RemoteName + "; there is no team lock"}, nil
	}
	host, _ := os.Hostname()
	path := signals.ResourcePath(w.Folder, migrateLockName)
	out := opsmigrate.UnlockOut{Line: "no team migration lock on " + signals.CodeRepoRef}
	_, err = st.Write(ctx, func(v signals.View) (signals.Change, error) {
		s, present := v.Board[path]
		if !present {
			return signals.Change{}, nil
		}
		who := holderName(s)
		if active, live := v.Active(path); live {
			if active.Holder != w.ActorID || active.Host != host {
				return signals.Change{}, &signals.HeldError{Path: path, By: active, SameActor: active.Holder == w.ActorID}
			}
			out = opsmigrate.UnlockOut{Released: true, Line: "released " + who + "'s team migration lock (" + host + ", no migration running here); run `lore migrate run` to finish"}
		} else {
			out = opsmigrate.UnlockOut{Released: true, Line: "cleared " + who + "'s expired team migration lock (last renewed " + renewedAt(s).UTC().Format(time.RFC3339) + ")"}
		}
		return signals.Change{Delete: []string{path}}, nil
	})
	if err != nil {
		return opsmigrate.UnlockOut{}, migrateLockError(err, w.Clock.Now())
	}
	return out, nil
}

// holderName is how a message names a hold's holder.
func holderName(s signals.Signal) string {
	if s.HolderLabel != "" {
		return s.HolderLabel
	}
	return s.Holder
}

// renewedAt is the hold's last renewal (Since when an older binary wrote none).
func renewedAt(s signals.Signal) time.Time {
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
		msg += " (lock renewed " + shortDuration(now.Sub(renewedAt(s)).Round(time.Second)) + " ago)"
		return errs.New(errs.MigrateLocked, msg).
			WithHint("run `lore migrate status` in a few minutes; a crashed holder's lock expires on its own, or its owner clears it with `lore migrate unlock --team`")
	case errors.As(err, &off):
		return errs.Wrap(errs.Offline, "migrate: the team lock on "+signals.CodeRepoRef+" could not be taken: "+off.Reason, err).
			WithHint("reconnect and run again, or `lore migrate run --offline` (branch files only) / `--no-push`")
	}
	return err
}
