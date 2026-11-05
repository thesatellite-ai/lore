package engine

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// projectClientAgents is the one place agent ids are kept out of client
// repos (decision P2B-OLD-2, AGENT_IDENTITY Q11/F18): for every attached
// store that is Client and AgentsInternal, it wraps the store's ActorAs so
// the mapping first applies the store's own ActorAs (wire maps this App's
// agent to its owner) and then projects any agent id that is left to its
// owner, or to nobody. Every client write already runs through ActorAs
// (stampAs: created_by, updated_by, the HLC actor, every field at any
// depth via actorFieldsAs; auditView: the audit actor), so wrapping it
// covers create, update, merge, move, delete, trash, restore and purge
// alike. Internal stores (Client false) are never touched: there an agent
// keeps its own id.
func (e *Engine) projectClientAgents() {
	for name, a := range e.stores {
		if !a.Client || !a.AgentsInternal {
			continue
		}
		inner := a.ActorAs
		a.ActorAs = func(id ids.ID[ids.Actor]) ids.ID[ids.Actor] {
			if inner != nil {
				id = inner(id)
			}
			if id.IsZero() {
				return id
			}
			return e.agentOwner(context.Background(), id)
		}
		e.stores[name] = a
	}
}

// agentOwner is the id a client store records for id: id itself for a
// person (or any non-agent actor, or an actor no store knows), the owner
// of an agent, or the zero id (nobody) for an agent without an owner.
//
// The actor's row is looked up in the internal (non-client) stores first,
// in precedence order, because the org roster there is the truth; a client
// store's minimal actor row (written while agent_details was client) is
// only a fallback. A store that fails to answer makes the result nobody
// unless another store answers: fail closed, a client repo never gets an
// id we could not prove is not an agent. The ActorAs contract asks for
// idempotence: an owner is a person and maps to itself; nobody stays
// nobody (the caller skips the zero id).
//
// It reads with context.Background() because ActorAs carries no context;
// the reads are local row-file lookups by id (Backend.Get).
func (e *Engine) agentOwner(ctx context.Context, id ids.ID[ids.Actor]) ids.ID[ids.Actor] {
	failed := false
	for _, clientPass := range []bool{false, true} {
		for _, name := range e.order {
			if e.stores[name].Client != clientPass {
				continue
			}
			sr, _, err := e.getRow(ctx, name, schema.TableActors, id.Any())
			if errors.Is(err, store.ErrNotFound) || (err == nil && sr.Deleted) {
				continue
			}
			if err != nil {
				failed = true
				continue
			}
			r, err := sr.Decode()
			if err != nil {
				failed = true
				continue
			}
			return ownerOrSelf(r, id)
		}
	}
	if failed {
		return ids.ID[ids.Actor]{}
	}
	return id
}

// ownerOrSelf reads one actors row: a non-agent is itself, an agent is its
// owner_actor_id (the zero id, nobody, when unset or unreadable).
func ownerOrSelf(r row.Row, id ids.ID[ids.Actor]) ids.ID[ids.Actor] {
	kind := r.Fields[schema.FieldActorKind]
	if kind == nil || kind.String() != schema.ActorKindAgent {
		return id
	}
	o := r.Fields[schema.FieldActorOwner]
	if o == nil {
		return ids.ID[ids.Actor]{}
	}
	owner, err := ids.Parse[ids.Actor](o.String())
	if err != nil || owner == id {
		return ids.ID[ids.Actor]{}
	}
	return owner
}
