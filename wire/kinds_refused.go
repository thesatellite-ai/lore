package wire

// kinds_refused.go — the row files of a table that the open's refresh
// refused on import because a field value still holds conflict markers,
// read as they are on disk (RES 3.13 with STORE_SERVER_SPEC 11 rule 2).
//
//	refresh (Read) ─► rowfs refuses a marked file (never reaches the cache)
//	                  └─ ReadProblems lists it (cheap: no extra file access)
//	RefusedRows(src, table) ─► each listed file of table, Get (as stored)
//	                           └─ kept only when a field holds markers
//
// The resource ops need this: a conflicted kind or field row is refused on
// import, yet RES 3.13 keeps using the kind's last good definition for
// reads (with a warning naming the row) and refuses its writes with
// E_KIND_CONFLICT. Without the refused row the kind simply vanished
// (E_KIND_UNKNOWN), which is the silent gap 3.13 forbids.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// RefusedRows is the ops/settings refusedRower Env upgrade: the rows of
// table in source whose files the last refresh left out because a field
// value holds unresolved conflict markers, decoded from the file as it is.
// Files refused for any other reason (not JSON, wrong path, over the cap,
// markers only inside a meta entry) are not returned: they are not
// definitions anyone can read, and doctor names them (DoctorRowFiles).
func (a *App) RefusedRows(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	b, ok := st.byName[source]
	if !ok {
		return nil, nil
	}
	cb := concrete(b) // a live wrapper (WrapBackend) hides ReadProblems
	rp, ok := cb.(readProblemer)
	if !ok {
		return nil, nil
	}
	var out []row.Row
	for _, p := range rp.ReadProblems() {
		t, id, ok := rowfs.ParseRowPath(p.Path)
		if !ok || t != table {
			continue
		}
		sr, gerr := cb.Get(ctx, t, id)
		if gerr != nil {
			continue // gone or unreadable since the refresh: nothing to show
		}
		r, derr := sr.Decode()
		if derr != nil || !fieldsHoldMarkers(r) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// fieldsHoldMarkers reports a row with a field value holding a conflict
// block (the check ops/settings markedRows makes on the same rows).
func fieldsHoldMarkers(r row.Row) bool {
	for _, v := range r.Fields {
		if v != nil && merge.HasMarkers(v.String()) {
			return true
		}
	}
	return false
}
