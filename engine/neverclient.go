package engine

import (
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
)

// refuseNeverClient answers E_LEAK_BLOCKED when table is a NeverClient
// table (schema.NeverClient, TEAM_PROFILES 2.8, TS-3) and st is a
// client-class store. It runs on every engine write path beside forStore
// (create, update, merge, move, delete, trash, restore, purge), before
// anything is staged or written, so nothing of the row reaches the store.
//
// Why at the engine and not only in the profile ops (whose roster target
// already refuses a client store, wire rosterState): `lore move <acn_…>
// --to repo`, a generic write through REST, or a future op would otherwise
// put a contact, period or membership row (a phone, a personal email, a
// leave reason; TP-6 keeps them plain) into files the client reads (TP-7).
// A purge is refused too: it writes a tombstone over the file first, and a
// leaked file is removed with git (the commit check names it).
//
// It also marks the engine as having written (RebindActor's invariant):
// every write path calls it before its first stamp is stored.
func (e *Engine) refuseNeverClient(st Attached, table row.Table) error {
	e.written.mark()
	if !st.Client || e.reg == nil {
		return nil
	}
	def, ok := e.reg.Table(table)
	if !ok || !def.NeverClient {
		return nil
	}
	return errs.New(errs.LeakBlocked, ""+string(table)+" rows never go into client store "+string(st.Name)+
		": team profile and invite rows live only in the org store (TP-7, ONBOARDING 4.5)").
		WithHint("write it to the org roster store; lore actor show --id <actor> reads it there")
}
