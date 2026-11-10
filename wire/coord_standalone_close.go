package wire

// coord_standalone_close.go — opstask.HeldCloser for a host without its
// own coordination machinery (cmd/lore, P3-SIGCOORD-3): task done and
// task cancel of a claimed task end the claim, as v0.1.17's guardTaskDone
// and guardTaskCancel did (saas task_done.go).
//
//	done:   the caller holds it (or held it in this session) ─► the holder's
//	        fenced done, the claim dropped (coord.HolderWrite + MarkDone)
//	        someone else holds it, lease live ─► E_INVALID_INPUT
//	        an expired claim ─► a person's override (coord.OverrideChecked),
//	        the old holder fenced out; an agent is refused
//	cancel: claimed ─► a person's override (coord.Override, E_CANCELLED for
//	        the holder); an agent is refused
//	where this host coordinates nothing (no git repo, a linked repo, no
//	agent or person): the op alone, as v0.1.17's plainRowFallback

import (
	"context"
	"fmt"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

var _ opstask.HeldCloser = standaloneTeam{}

// heldState is one task's claim as the board and this session's held
// record see it (v0.1.17 claimState).
type heldState struct {
	sig      signals.Signal
	claimed  bool
	active   bool
	mine     bool
	hadClaim bool
}

// closeWorld opens the world for a close; nil, nil where this host
// coordinates nothing (the op then runs alone). Only the two documented
// "no team here" cases mean that: no git repository, and a caller that is
// neither an agent nor a person. They are checked here directly, before
// the world opens, so that every failure of the world itself (config
// invalid, link status unknown, an unregistered agent, an unreachable
// store, a client repo's own task, an internal error) stops the close and
// a claimed task is never closed unfenced (v0.1.17 fixed rule 6,
// isNoTeamCoordination; B11).
func (t standaloneTeam) closeWorld(ctx context.Context, taskID string) (*coordWorld, error) {
	if top, err := t.a.repoTop(ctx); err != nil || top == "" {
		return nil, nil // no git repository: no signals ref, nobody can hold anything
	}
	who, err := t.a.Who(ctx)
	if err != nil {
		return nil, err
	}
	if who.Kind != identity.KindAgent && who.Kind != identity.KindHuman {
		return nil, nil // no agent or person actor: no claims of its own
	}
	return standaloneCoord(t).openWorldFor(ctx, taskID)
}

// heldOf reads task id's claim.
func (w *coordWorld) heldOf(ctx context.Context, env coord.Env, id string) (heldState, error) {
	v, err := env.Signals.Read(ctx)
	if err != nil {
		return heldState{}, coordErr(err)
	}
	var hs heldState
	p := signals.ClaimPath(w.folder, id)
	if s, ok := v.Board[p]; ok {
		_, active := v.Active(p)
		hs = heldState{sig: s, claimed: true, active: active, mine: s.Holder == w.actorID && s.Session == w.session()}
	}
	_, hs.hadClaim = w.held().Get(w.session(), id)
	return hs, nil
}

// CloseHeldDone implements opstask.HeldCloser.
func (t standaloneTeam) CloseHeldDone(ctx context.Context, taskID, commit string, pr int) (string, error) {
	w, err := t.closeWorld(ctx, taskID)
	if err != nil || w == nil {
		return commit, err
	}
	env := w.env(ctx)
	hs, err := w.heldOf(ctx, env, taskID)
	if err != nil {
		return commit, err
	}
	switch {
	case hs.mine || hs.hadClaim:
		if commit == "" {
			commit = w.workBranchHead(ctx)
		}
		if _, err = coord.HolderWrite(ctx, env, taskID, true, func(d coord.Doc, now time.Time) ([]coord.RowWrite, error) {
			return nil, coord.MarkDone(w.actorID, commit, pr)(d, now)
		}); err != nil {
			return commit, coordErr(err)
		}
	case hs.claimed && hs.active:
		return commit, errs.New(errs.InvalidInput, fmt.Sprintf("%s is held by %s until %s; only the holder can finish it",
			taskID, holderName(hs.sig), hs.sig.Expires.UTC().Format(time.Kitchen))).
			WithHint("a person can `lore task revoke " + taskID + "` first")
	case hs.claimed:
		if err = w.requireHuman("finishing a task someone else claimed"); err != nil {
			return commit, err
		}
		if _, err = coord.OverrideChecked(ctx, env, taskID, coord.OverrideRevoke, coord.MarkDone(w.actorID, commit, pr)); err != nil {
			return commit, coordErr(err)
		}
	}
	return commit, nil
}

// CloseHeldCancel implements opstask.HeldCloser.
func (t standaloneTeam) CloseHeldCancel(ctx context.Context, taskID string) (string, bool, error) {
	w, err := t.closeWorld(ctx, taskID)
	if err != nil || w == nil {
		return "", false, err
	}
	env := w.env(ctx)
	hs, err := w.heldOf(ctx, env, taskID)
	if err != nil || !hs.claimed {
		return "", false, err
	}
	if err = w.requireHuman("cancelling a claimed task"); err != nil {
		return "", false, err
	}
	if _, err = coord.Override(ctx, env, taskID, coord.OverrideCancel, func(d coord.Doc) {
		d.SetStr(fieldStatus, schema.TaskStatusCancelled)
	}); err != nil {
		return "", false, coordErr(err)
	}
	return fmt.Sprintf("%s %s cancelled (claim by %s dropped)", markOK, taskID, holderName(hs.sig)), true, nil
}

// fieldStatus is a task's status column.
const fieldStatus = "status"

// workBranchHead is the commit a holder's work ends at when it runs done
// on its work branch without --commit (v0.1.17 workBranchHead): "" off a
// work branch, and "" when HEAD is already on the fetched default branch
// or that cannot be told (trial finding F17: record nothing, fail closed).
func (w *coordWorld) workBranchHead(ctx context.Context) string {
	if w.workBranch(ctx) == "" {
		return ""
	}
	oid, ok, err := w.repo.ResolveCommit(ctx, "HEAD")
	if err != nil || !ok {
		return ""
	}
	tip, ok, err := w.repo.ResolveRef(ctx, "refs/remotes/"+w.remoteName+"/"+w.defaultBranch(ctx))
	if err != nil || !ok {
		return ""
	}
	merged, err := w.repo.IsAncestor(ctx, oid, tip)
	if err != nil || merged {
		return ""
	}
	return oid
}

var _ opstask.HolderFencer = standaloneTeam{}

// FenceHolder implements opstask.HolderFencer (TASK 7.4, PASS3-A): a
// checklist write by the task's holder (its claim on the board, or its
// held record in this session) is fenced like `task note`; anyone else
// writes unfenced. Only a holder pays for the fresh read the fence decides
// on; a failed fence is E_LOST_CLAIM. Where this host coordinates nothing,
// nothing is fenced (v0.1.17 plainRowFallback, closeWorld).
func (t standaloneTeam) FenceHolder(ctx context.Context, taskID string, rowEpoch int) error {
	w, err := t.closeWorld(ctx, taskID)
	if err != nil || w == nil {
		return err
	}
	st := w.signalsStore()
	local, err := st.Local(ctx)
	if err != nil {
		return errs.Wrap(errs.Internal, "read the signals ref", err)
	}
	_, mine := signals.FindOwn(local, w.caller(), w.folder, taskID)
	_, had := w.held().Get(w.session(), taskID)
	if !mine && !had {
		return nil
	}
	v, err := st.Read(ctx)
	if err != nil {
		return coordErr(err)
	}
	_, err = signals.CheckHolder(v, w.caller(), w.folder, taskID, rowEpoch)
	return coordErr(err)
}
