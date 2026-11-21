package wire

// directpolicy.go — agents.person_only on the direct paths of App.run
// (devco D-877, bug hunt H-136).
//
//	App.run ─▶ a direct path (hostRunOp roster verbs, task next / heartbeat
//	           / hold on an org store, actor merge outside a repo, sync and
//	           sync status from the setup, personal store init)
//	        ─▶ runDirectAs: the caller's agent (writerAgent), the decoded
//	           input and the op's spec ride on ctx (directCall)
//	        ─▶ runDirect ─▶ the op opens whichever store it needs
//	        ─▶ every App state it reaches (state, rowState) runs
//	           directPolicy first: that store's policy, engine.PersonOnly,
//	           once per opened state; sync and sync status from the setup
//	           read each store's policy first (directStoresPolicy)
//
// Why: the main path checks agents.person_only in engine.Run, but these
// paths invoke the operation themselves (Entry.Invoke, no engine.Run) and
// pick their store inside it, so the policy was never read for them: an
// agent with hold.claim listed for people only claimed a hold from a
// folder with no code repo, registered a bot in a project repo, synced
// the setup. The check sits where the store opens because only there is
// the store the command reaches known (an org store by --store, each
// store of the setup, the roster's org folder); the result is kept per
// opened state, so an op that ignores an open error and opens again still
// gets the refusal. wire's TestDirectRunsCheckPersonOnly keeps every
// direct call of App.run on runDirectAs.

import (
	"context"
	"sync"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// directCallKey carries the *directCall of a direct path on ctx.
type directCallKey struct{}

// directCall is what directPolicy needs of the command a direct path runs:
// its spec and input (an agents.person_only item may name a flag), the
// caller with its agent filled, and the answer per opened state.
type directCall struct {
	spec   op.Spec
	in     op.Input
	caller op.Caller

	mu   sync.Mutex
	seen map[*state]error // nil value while the policy read runs (re-entry passes)
}

// runDirectAs runs e directly (runDirect) for caller c with the policy
// check of directPolicy armed for each store the operation opens. The
// agent is c's when a surface fixed it, named one or set an explicit
// actor, else the identity chain's with detection, as runAsCaller does.
func (a *App) runDirectAs(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	return runDirect(a.directContext(ctx, e, d, c), e, d)
}

// directContext is ctx carrying the directCall of e for c; ctx itself for
// a person (agents.person_only never refuses one) or an input that does not
// decode (runDirect reports that error).
func (a *App) directContext(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) context.Context {
	if !c.AgentFixed && c.Agent == "" && c.Actor.IsZero() {
		c.Agent = a.writerAgent(ctx)
	}
	if c.Agent == "" {
		return ctx
	}
	in, err := e.Decode(d)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, directCallKey{}, &directCall{spec: e.Spec(), in: in, caller: c, seen: map[*state]error{}})
}

// directPolicy refuses the direct call ctx carries when agents.person_only
// of st's default target (the store the op opened: the repo's, or the one
// store of a store-only open) lists it for this agent. A policy that cannot
// be read refuses, as in engine.Run. nil outside a direct call.
func directPolicy(ctx context.Context, st *state) error {
	dc, ok := ctx.Value(directCallKey{}).(*directCall)
	if !ok || dc == nil || st == nil || st.eng == nil {
		return nil
	}
	dc.mu.Lock()
	if err, done := dc.seen[st]; done {
		dc.mu.Unlock()
		return err
	}
	dc.seen[st] = nil // the policy read may open this state again: pass it
	dc.mu.Unlock()
	pol, err := st.eng.Policy(ctx, st.defaultTarget)
	if err == nil {
		err = engine.PersonOnly(pol, dc.spec, dc.in, dc.caller)
	}
	dc.mu.Lock()
	dc.seen[st] = err
	dc.mu.Unlock()
	return err
}

// withoutDirectCall is ctx with the direct call's check off: for a lookup
// that opens stores only to find the one a command acts on (actor merge's
// org stores), which then runs directPolicy on that one itself.
func withoutDirectCall(ctx context.Context) context.Context {
	return context.WithValue(ctx, directCallKey{}, (*directCall)(nil))
}

// directStoresPolicy is directPolicy for a direct path that acts on stores
// without an App state of its own first (`lore sync` and `lore sync
// status` from the setup): each store is opened store-only for the policy
// read, its org's company folder beside it (where an org-level policy row
// lives; the store root alone does not attach it). The read makes no
// clone (WithNoClone): a store with none here is left to the sync, which
// reports it. Only a refusal or a malformed policy row stops the command;
// a store that does not open is the sync's to report.
func (a *App) directStoresPolicy(ctx context.Context, stores []attach.Store) error {
	if dc, ok := ctx.Value(directCallKey{}).(*directCall); !ok || dc == nil {
		return nil
	}
	ctx = gitbackend.WithNoClone(ctx)
	for _, one := range stores {
		if err := a.directStorePolicy(ctx, one); errs.Has(err, errs.PolicyDenied) || errs.Has(err, errs.ConfigInvalid) {
			return err
		}
	}
	return nil
}

// directStorePolicy is directStoresPolicy for store one: a store-only App
// on it (and its org's company folder), closed after the read.
func (a *App) directStorePolicy(ctx context.Context, one attach.Store) error {
	x, err := New(detachedOpts(a.opts))
	if err != nil {
		return err
	}
	defer func() { _ = x.Close() }()
	if err = x.openStoreOnlyOn(ctx, one, "", cloneCompanyFolder(cloneOrgEntry(a.opts.Home, one.URL), one)...); err != nil {
		return err
	}
	_, err = x.rowState(ctx) // runs directPolicy on the opened state
	return err
}
