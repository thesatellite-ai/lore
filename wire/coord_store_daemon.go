package wire

// coord_store_daemon.go — `task heartbeat --daemon` with no code repo
// (devco:D-671): from the home folder, a plain folder or a store clone it
// keeps alive every store claim this session holds (the claims task claim
// --push and task next --claim --push took from home or in the clone, whose
// held records are each store clone's). Each round opens one world per
// store held (eachHeldStore: the App reopens store-only on that store, so
// the worlds run one after another, never side by side) and renews the
// claims there; a store renewal pushes the store's signals ref as a plain
// heartbeat does (the D-580 rule asks for --push only on a new claim: a
// renewal changes only what the person already published).
//
// The rules are the code repo daemon's (heartbeatDaemon): renew at once,
// then each lease.renew_every; stop quietly when ctx ends or the parent
// process exits; a claim fenced out or gone stops the daemon with that
// error; offline or contended renewals are retried until the held expiry
// minus the lease grace, then E_LOST_CLAIM.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// storeHeartbeatDaemon is heartbeatDaemon for the store claims this
// session holds (id: that one claim only). The claims are the ones held
// when it starts: one released or fenced later stops it with that error,
// as the code repo daemon does.
func (h standaloneCoord) storeHeartbeatDaemon(ctx context.Context, id string) (opscoord.Report, error) {
	var want []string
	if err := h.eachHeldStore(ctx, func(_ *coordWorld, recs []coord.HeldRecord) error {
		for _, r := range recs {
			if id == "" || r.Task == id {
				want = append(want, r.Task)
			}
		}
		return nil
	}); err != nil {
		return opscoord.Report{}, err
	}
	if len(want) == 0 {
		if id != "" {
			return opscoord.Report{}, errs.New(errs.NotFound, id+" is not a claim this session holds on an org store of this machine, and here is no code repo whose claims it could be").
				WithHint("`lore task claim --id " + id + " --push` claims it; `lore task heartbeat --daemon` keeps every claim you hold alive")
		}
		return opscoord.Report{Lines: []string{"no claims held by this session"}}, nil
	}
	parent := os.Getppid()
	tick := time.NewTicker(daemonParentPoll)
	defer tick.Stop()
	next := h.a.Now()
	renewals := 0
	var every time.Duration
	for {
		if !h.a.Now().Before(next) {
			n, d, err := h.storeDaemonRound(ctx, want)
			renewals += n
			if ctx.Err() != nil {
				return storeDaemonStopped(renewals), nil
			}
			if err != nil {
				return opscoord.Report{}, err
			}
			if d > 0 {
				every = d
			}
			next = h.a.Now().Add(every)
			daemonHook(daemonRound)
		}
		select {
		case <-ctx.Done():
			return storeDaemonStopped(renewals), nil
		case <-tick.C:
			if os.Getppid() != parent {
				return storeDaemonStopped(renewals), nil // the session that started us is gone
			}
		}
	}
}

// storeDaemonRound renews each claim of want once, store by store: the
// renewals made, the shortest lease.renew_every of the stores (the next
// round's wait), and the error that stops the daemon. A claim of want no
// store holds any more was released or lost: stop work on it.
func (h standaloneCoord) storeDaemonRound(ctx context.Context, want []string) (int, time.Duration, error) {
	renewals := 0
	var every time.Duration
	found := map[string]bool{}
	err := h.eachHeldStore(ctx, func(w *coordWorld, recs []coord.HeldRecord) error {
		if every == 0 || w.lease.RenewEvery < every {
			every = w.lease.RenewEvery
		}
		for _, rec := range recs {
			if !slices.Contains(want, rec.Task) {
				continue
			}
			found[rec.Task] = true
			daemonHook(daemonRenewing)
			if _, err := w.renewStoreHeld(ctx, rec); err != nil {
				if ctx.Err() != nil {
					return nil // stopped during the renewal: a quiet stop
				}
				if !errs.Has(err, errs.Offline) && !errs.Has(err, errs.Contended) {
					return err
				}
				if w.a.Now().After(rec.Expires.Add(-w.lease.Grace)) {
					return errs.Wrap(errs.LostClaim, fmt.Sprintf("%s: renewals kept failing; the lease ends at %s, stop work", rec.Task, rec.Expires.UTC().Format(time.Kitchen)), err)
				}
				continue
			}
			renewals++
		}
		return nil
	})
	if err != nil || ctx.Err() != nil {
		return renewals, every, err
	}
	for _, id := range want {
		if !found[id] {
			return renewals, every, errs.New(errs.LostClaim, id+": this session holds its claim on no org store any more (released, or taken over); stop work").
				WithHint("`lore task show --id " + id + "` shows who holds it now")
		}
	}
	return renewals, every, nil
}

// storeDaemonStopped is the store daemon's closing line.
func storeDaemonStopped(renewals int) opscoord.Report {
	return opscoord.Report{Lines: []string{fmt.Sprintf("heartbeat daemon stopped after %d renewal(s)", renewals)}}
}

// repoDaemonScope is what a code repo's `task heartbeat --daemon` keeps
// alive (devco:D-673). One daemon holds one open world: the App reopens
// store-only per store, which closes the repo's, so it cannot renew the
// repo's claims and the store claims in the same rounds. store=true: run
// the store daemon instead (--id names a store claim this repo does not
// hold, or the repo holds none and the stores hold some); else uncovered
// lists the store claims this daemon leaves to expire, read from the store
// clones' held records without opening them (repoIDs: the repo's claims).
func (h standaloneCoord) repoDaemonScope(ctx context.Context, w *coordWorld, id string, repoIDs []string) (store bool, uncovered []string) {
	if id != "" {
		_, inRepo := w.held().Get(w.session(), id)
		return !inRepo && slices.Contains(h.heldStoreTasks(ctx, w.session()), id), nil
	}
	held := h.heldStoreTasks(ctx, w.session())
	if len(repoIDs) == 0 {
		return len(held) > 0, nil
	}
	return false, slices.DeleteFunc(held, func(t string) bool { return slices.Contains(repoIDs, t) })
}

// heldStoreTasks lists the store claims session holds on this machine's
// org store clones, from their held records alone (no world opened).
func (h standaloneCoord) heldStoreTasks(ctx context.Context, session string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range h.a.everyOrgFolders(ctx).Folders {
		k := placeKey(f.URL, f.Branch)
		if seen[k] || f.Dir == "" {
			continue
		}
		seen[k] = true
		for _, r := range (coord.HeldStore{Path: filepath.Join(gitbackend.StateDirOf(f.Dir), signalsStateDir, heldFile)}).List(session) {
			if !slices.Contains(out, r.Task) {
				out = append(out, r.Task)
			}
		}
	}
	return out
}

// uncoveredStoreClaims is the code repo daemon's start notice for the
// store claims it does not renew, with the command that does, which works
// right here (an --id of a store claim runs the store daemon).
func uncoveredStoreClaims(ids []string) string {
	var cmds []string
	for _, id := range ids {
		cmds = append(cmds, "`lore task heartbeat --daemon --id "+id+"`")
	}
	return "note: this daemon renews this repo's claims only; the org store claims " + strings.Join(ids, ", ") + " you hold are not renewed by it: run " + strings.Join(cmds, " and ") + " beside it (or `lore task heartbeat --daemon` from the home folder, which renews them all)"
}
