package engine

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// InputTarget is the json name of the input field that names a write's
// target store ("--to agency", "to": "agency"). An operation whose input has
// no such field writes to the default target.
const InputTarget = "to"

// Confirmer asks a person a yes/no question (a terminal prompt). It is
// consulted only for an interactive caller without yes.
type Confirmer interface {
	Confirm(ctx context.Context, question string) (bool, error)
}

// call is what Run knows about the running operation, carried in the
// context so the write API applies the same caller to every row the
// operation writes.
type call struct {
	op     string
	caller op.Caller
	target store.Source
}

// callKey keys the call in a context.
type callKey struct{}

// callOf returns the running call. A write API used outside Run (tests,
// embedders) gets a non-interactive caller without yes: every question is
// E_NEEDS_CONFIRM, never a silent yes.
func (e *Engine) callOf(ctx context.Context) call {
	if c, ok := ctx.Value(callKey{}).(call); ok {
		return c
	}
	return call{target: e.target}
}

// Run implements op.Runner (spec 9.2): decode the input, resolve the target
// store, check the permission (a write needs a writable store:
// E_STORE_READ_ONLY), ask for the operation's confirmation
// (E_NEEDS_CONFIRM for a non-interactive caller without yes), then invoke
// the operation with the call in its context. The secret scan, policy,
// stamping, backend write and audit happen per row in the write API the
// operation calls, so a row an operation computes is checked exactly like
// one typed by a person.
func (e *Engine) Run(ctx context.Context, entry op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	spec := entry.Spec()
	in, err := entry.Decode(d)
	if err != nil {
		return op.Result{}, err
	}
	cl := call{op: spec.Name, caller: c, target: e.target}
	for _, tv := range in.Texts() {
		if tv.Field == InputTarget && tv.Text != "" {
			cl.target = store.Source(tv.Text)
		}
	}
	if _, err := e.attached(cl.target); err != nil {
		return op.Result{}, err
	}
	if spec.Access == op.AccessWrite {
		if _, err := e.writable(cl.target); err != nil {
			return op.Result{}, err
		}
	}
	if c.Agent != "" {
		// POLICY (spec 9.2): agents.person_only names commands, so it is
		// checked here, before the confirmation, for CLI and REST alike.
		// Only an agent caller pays the settings read.
		pol, _, perr := e.settings.Policy(cl.target)
		if perr != nil {
			return op.Result{}, perr
		}
		if perr = personOnly(pol, spec, in, c); perr != nil {
			return op.Result{}, perr
		}
	}
	if needsConfirm(spec, in) {
		if err := e.confirm(ctx, cl, "run "+strings.ReplaceAll(spec.Name, ".", " ")+"?"); err != nil {
			return op.Result{}, err
		}
	}
	// --meta / --meta-json / --meta-stdin of any input (engine/meta.go, R13)
	ctx, metaUsed, merr := bindInputMeta(ctx, in)
	if merr != nil {
		return op.Result{}, merr
	}
	res, ierr := entry.Invoke(context.WithValue(ctx, callKey{}, cl), in)
	if ierr == nil {
		ierr = metaUsed()
	}
	return res, ierr
}

// needsConfirm applies the spec's ConfirmRule to the decoded input.
func needsConfirm(spec op.Spec, in op.Input) bool {
	if spec.Confirm.Always {
		return true
	}
	if spec.Confirm.When != "" {
		v, ok := in.Bool(spec.Confirm.When)
		return ok && v
	}
	return false
}

// confirm settles one question: yes passes; an interactive caller with a
// Confirmer is asked; anyone else gets E_NEEDS_CONFIRM carrying the
// question, so an agent can relay it to its person (XQ3: no terminal
// means --yes is required; DQ2: --yes for every non-permanent confirm).
func (e *Engine) confirm(ctx context.Context, cl call, question string) error {
	if cl.caller.Yes {
		return nil
	}
	if cl.caller.Interactive && e.confirmer != nil {
		ok, err := e.confirmer.Confirm(ctx, question)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		return errs.New(errs.Cancelled, "engine: not confirmed: "+question)
	}
	return errs.New(errs.NeedsConfirm, "engine: "+question).WithHint("ask the person, then rerun with --yes")
}

// actorOf is the actor a write is stamped with: the caller's when a surface
// named one, else the engine's.
func (e *Engine) actorOf(cl call) ids.ID[ids.Actor] {
	if !cl.caller.Actor.IsZero() {
		return cl.caller.Actor
	}
	return e.actor
}

var _ op.Runner = (*Engine)(nil)
