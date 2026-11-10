package infra

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// The org roster verbs (LORE_TEAM_SPEC F18, §7.2): `lore actor register`
// puts the caller (or an agent the caller owns) on the roster, `lore actor
// deactivate <@handle>` marks someone gone, `lore actor delete` always
// refuses (BATCH2 #8, DELETION 4.1: actors are never deleted).
//
// Flow:
//
//	actor.register ──▶ Roster.ActorRegister ──▶ "✓ <id> <name> (@h) registered in <where>"
//	actor.deactivate ──▶ Roster.ActorDeactivate ──▶ "✓ <id> <name> deactivated (…)"
//	actor.delete ──▶ E_REFUSED naming `lore actor deactivate` (no Env call)
//
// Which roster a command writes (the org folder of the org's store, or the
// repo's own actors with no org, Q4), whose identity registers, and the
// store-only open outside a repo (BUG-T3) are the composition root's
// decisions: the operations validate the input and render the answer.

// Roster operation names.
const (
	OpActorRegister   = "actor.register"
	OpActorDeactivate = "actor.deactivate"
	OpActorDelete     = "actor.delete"
)

// Actor kinds a registration takes (schema.ActorKinds' human and agent).
const (
	RegisterKindHuman = "human"
	RegisterKindAgent = "agent"
)

// handleMark prefixes a roster handle in input and output.
const handleMark = "@"

// labelForbidden are the characters an agent label may not hold: it is one
// word, the tail of the agent's stable key agent:<label> (F18).
const labelForbidden = " \t:/@"

// Roster does the roster work; wire.App implements it (an upgrade of Env:
// an Env without it simply has no roster verbs).
type Roster interface {
	// ActorRegister creates or refreshes the caller's own actor (in.Kind
	// human) or the agent in.Label owned by in.Owner (@handle, default the
	// caller) on the roster this repo belongs to. A handle another active
	// actor or team holds is refused (E_INVALID_INPUT).
	ActorRegister(ctx context.Context, in ActorRegisterIn) (ActorRegisterOut, error)
	// ActorDeactivate marks the active actor @handle gone (active false,
	// left_at now); the row stays so old rows still resolve.
	ActorDeactivate(ctx context.Context, handle string) (ActorDeactivateOut, error)
}

// RosterOps are the roster operations bound to r.
func RosterOps(r Roster) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpActorRegister, Summary: "Put yourself (or, with --kind agent, an agent you own) on the org roster", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append([]errs.Code{errs.InvalidInput, errs.NotAStore}, writeErrors...),
			Examples: []op.Example{
				{Title: "register yourself", CLI: "lore actor register", Want: "registered in"},
				{Title: "register an agent you own", CLI: "lore actor register --kind agent --label agent-frontend --owner @alice", Want: "agent agent-frontend registered"},
			}},
			func(ctx context.Context, in ActorRegisterIn) (ActorRegisterOut, error) {
				return actorRegister(ctx, r, in)
			}),
		op.Define(op.Spec{Name: OpActorDeactivate, Summary: "Mark someone as gone (active=false, left_at); actors are never deleted", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors:   append([]errs.Code{errs.InvalidInput}, writeErrors...),
			Examples: []op.Example{{Title: "someone left the team", CLI: "lore actor deactivate @carol", Want: "deactivated"}}},
			func(ctx context.Context, in ActorDeactivateIn) (ActorDeactivateOut, error) {
				return actorDeactivate(ctx, r, in)
			}),
	}
}

// ActorOps returns the roster verbs when env offers Roster (PROV-style
// upgrade), and actor.delete always (F18, BATCH2 #8). The composition root
// registers them beside Ops; they are separate because the host runs them
// outside the repo engine (IsRosterOp).
func ActorOps(env Env) []op.Entry {
	entries := []op.Entry{actorDeleteOp()}
	if r, ok := upgrade.As[Roster](env); ok {
		entries = append(entries, RosterOps(r)...)
	}
	return entries
}

// IsRosterOp reports whether name is a roster operation. The host runs them
// on the roster's own App (the org store folder, store-only outside a
// repo, BUG-T3), not through the repo engine's Run.
func IsRosterOp(name string) bool {
	return name == OpActorRegister || name == OpActorDeactivate || name == OpActorDelete
}

// ActorRegisterIn is the input of actor.register.
type ActorRegisterIn struct {
	Kind  string `json:"kind,omitempty" flag:"kind" desc:"human (default) or agent"`
	Label string `json:"label,omitempty" flag:"label" desc:"with --kind agent: the session label"`
	Owner string `json:"owner,omitempty" flag:"owner" desc:"with --kind agent: the owning human as @handle (default: you)"`
}

// ActorRegisterOut is the registration.
type ActorRegisterOut struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"display_name"`
	Handle string `json:"handle,omitempty"`
	Label  string `json:"label,omitempty"`
	// Created is false when the actor was already registered (refreshed).
	Created bool `json:"created"`
	// Where names the roster: "agency org/" or "this repo".
	Where string `json:"where"`
	// ViaContact is set when the caller's key is an identity contact of
	// this existing actor (TEAM_PROFILES 6.3, TP-4): nothing was written,
	// the caller is that person already. It names the contact (type:value).
	ViaContact string `json:"via_contact,omitempty"`
}

// Text is v0.1.17's line: a human "✓ <id> <name> (@h) registered in
// <where>" ("already registered (refreshed)" the second time), an agent
// "✓ <id> agent <label> registered in <where>".
func (o ActorRegisterOut) Text() string {
	if o.Kind == RegisterKindAgent {
		return work.Success + " " + o.ID + " agent " + o.Label + " registered in " + o.Where + "\n"
	}
	verb := "registered"
	switch {
	case o.ViaContact != "":
		verb = "already registered as " + o.Name + " via identity contact " + o.ViaContact
	case !o.Created:
		verb = "already registered (refreshed)"
	}
	h := ""
	if o.Handle != "" {
		h = " (" + handleMark + o.Handle + ")"
	}
	return work.Success + " " + o.ID + " " + o.Name + h + " " + verb + " in " + o.Where + "\n"
}

// ActorDeactivateIn is the input of actor.deactivate.
type ActorDeactivateIn struct {
	Handle string `json:"handle" arg:"0" desc:"the person's @handle"`
}

// ActorDeactivateOut is the deactivated actor.
type ActorDeactivateOut struct {
	ID   string `json:"id"`
	Name string `json:"display_name"`
	// handle is the @handle the caller named (text only): the offboard
	// hint names the person the way they were named.
	handle string
}

// Text is v0.1.17's line, then the TEAM_PROFILES 5.3 hint: deactivate
// ends no company period and no project membership, offboard does.
func (o ActorDeactivateOut) Text() string {
	who := o.handle
	if who == "" {
		who = o.ID
	}
	return work.Success + " " + o.ID + " " + o.Name + " deactivated (kept on the roster; old rows still resolve)\n" +
		"  to end their projects and stint too: lore actor offboard " + who + "\n"
}

// ActorDeleteIn is the input of actor.delete (the id is optional: the
// answer is the same refusal either way).
type ActorDeleteIn struct {
	ID string `json:"id,omitempty" arg:"0" desc:"the actor id (refused either way)"`
}

// ActorDeleteOut is never returned: actor.delete always refuses.
type ActorDeleteOut struct{}

// actorDeleteOp is `lore actor delete [<id>]`: always E_REFUSED (BATCH2 #8).
func actorDeleteOp() op.Entry {
	return op.Define(op.Spec{Name: OpActorDelete, Summary: "Refused: actors are never deleted (use `lore actor deactivate`)", Permission: op.PermWrite, Access: op.AccessRead,
		Errors:   []errs.Code{errs.Refused},
		Examples: []op.Example{{Title: "someone left: mark them gone instead", CLI: "lore actor deactivate @carol", Want: "deactivated"}}},
		func(_ context.Context, in ActorDeleteIn) (ActorDeleteOut, error) {
			return ActorDeleteOut{}, actorDeleteRefusal(in.ID)
		})
}

// actorDeleteRefusal is BATCH2 #8's answer: every *_by field of every row
// points at an actor, so deleting one would orphan history.
func actorDeleteRefusal(id string) error {
	what := "an actor"
	if id = strings.TrimSpace(id); id != "" {
		what = "actor " + id
	}
	return errs.New(errs.Refused, what+" cannot be deleted: actors are never deleted, every *_by field points at them").
		WithHint("deactivate the person instead: lore actor deactivate <@handle> (the row stays, marked inactive)")
}

// actorRegister validates in and registers.
func actorRegister(ctx context.Context, r Roster, in ActorRegisterIn) (ActorRegisterOut, error) {
	switch in.Kind {
	case "", RegisterKindHuman:
		// v0.1.17 ignores --label/--owner without --kind agent.
		in.Kind, in.Label, in.Owner = RegisterKindHuman, "", ""
	case RegisterKindAgent:
		if in.Label == "" || strings.ContainsAny(in.Label, labelForbidden) {
			return ActorRegisterOut{}, errs.New(errs.InvalidInput, "an agent label is one word (no spaces, ':', '/', '@')")
		}
	default:
		return ActorRegisterOut{}, errs.New(errs.InvalidInput, "--kind is human (default) or agent")
	}
	return r.ActorRegister(ctx, in)
}

// actorDeactivate validates the handle and deactivates.
func actorDeactivate(ctx context.Context, r Roster, in ActorDeactivateIn) (ActorDeactivateOut, error) {
	h := strings.TrimPrefix(strings.TrimSpace(in.Handle), handleMark)
	if h == "" {
		return ActorDeactivateOut{}, errs.New(errs.InvalidInput, "name the person as @handle")
	}
	out, err := r.ActorDeactivate(ctx, handleMark+h)
	out.handle = handleMark + h
	return out, err
}
