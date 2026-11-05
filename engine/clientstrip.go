package engine

import (
	"maps"

	"github.com/thesatellite-ai/lore/model/row"
)

// forStore is the one place a row is shaped for the store it is written
// into, called by every engine write (create, update, merge, move, delete,
// trash, restore, purge) just before the row is stamped and encoded:
//
//   - stampAs: actor ids through the store's ActorAs (agent ids never reach
//     a client store, P2B-OLD-2);
//   - stripInternal: in a client-class store, every field the table's
//     schema marks Internal() is dropped (PC-LEAK-1, AGENT_IDENTITY Q11,
//     TQ3). Why here and not per op: a direct write such as `task done`
//     or a time span stop into a client repo would otherwise commit
//     time_entries, time_open, claim_log, approved_hash, an actor's
//     stable_key, … to files the client reads; lore move already keeps
//     them back (clientTime, with its warnings) and passes here too, which
//     is a no-op for it.
//
// Internal stores (Client false) keep every field.
func (e *Engine) forStore(st Attached, r row.Row) row.Row {
	return e.stripInternal(st, stampAs(st, r))
}

// stripInternal drops def.Internal fields of r's table when st is a client
// store. The field map is copied before the first deletion, so the
// caller's row is never mutated. A table the registry does not know has no
// internal fields to drop (the write itself reports an unknown table).
func (e *Engine) stripInternal(st Attached, r row.Row) row.Row {
	if !st.Client || e.reg == nil || len(r.Fields) == 0 {
		return r
	}
	def, ok := e.reg.Table(r.Table)
	if !ok {
		return r
	}
	copied := false
	for _, f := range def.Fields {
		if !f.Internal {
			continue
		}
		if _, has := r.Fields[f.Name]; !has {
			continue
		}
		if !copied {
			r.Fields, copied = maps.Clone(r.Fields), true
		}
		delete(r.Fields, f.Name)
	}
	return r
}
