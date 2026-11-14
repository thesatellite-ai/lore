package wire

// doctor_store_conflicts.go — open store sync conflicts (rows of an
// attached store both this machine and the store's remote changed, so
// `lore sync` refuses with E_STALE / E_RESOURCE_CONFLICT) in `lore doctor`
// and `lore inbox` (I-1). Before, doctor said HEALTHY with "open
// conflicts: 0" and its push-mode note said "`lore sync` pushes them"
// while sync refused, and the inbox was empty: only `lore sync conflicts`
// and `lore explain` showed them.
//
//	StoreConflicts (sync_store_conflicts.go) ─┬▶ doctor: [sync] open
//	                                          │  conflicts + one warning
//	                                          │  per store + push-mode note
//	                                          └▶ inbox: one item per row

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// storeConflictSteps names the commands that show and settle an open
// store conflict (the numbers come from `lore sync conflicts`).
const storeConflictSteps = "`lore sync conflicts` shows both versions, `lore sync resolve --id <n> --take kept|other` settles one"

// inboxConflict is the inbox kind of an open store sync conflict.
const inboxConflict = "conflict"

// openStoreConflicts counts the open store sync conflicts by store name
// (nil when there are none or they cannot be read: doctor's other checks
// report an unreadable store).
func (a *App) openStoreConflicts(ctx context.Context) (map[string]int, []StoreConflict) {
	cs, err := a.StoreConflicts(ctx)
	if err != nil || len(cs) == 0 {
		return nil, nil
	}
	by := map[string]int{}
	for _, c := range cs {
		by[c.Store]++
	}
	return by, cs
}

// doctorStoreConflicts adds the open store sync conflicts to doctor's
// [sync] open conflicts (s may be nil: no row-file repo store) and warns
// once per store with the commands that settle them. It returns, by
// store name, what its push waits for (the push-mode notes say it in
// place of "`lore sync` pushes them"): its own conflicts, or another
// folder's conflict that holds it (owner decision 7).
func (a *App) doctorStoreConflicts(ctx context.Context, s *opsdoctor.Sync, warn func(string)) map[string]string {
	by, cs := a.openStoreConflicts(ctx)
	waits := map[string]string{}
	if len(cs) > 0 {
		if s != nil {
			s.OpenConflicts += len(cs)
		}
		for _, name := range slices.Sorted(maps.Keys(by)) {
			warn(storeConflictDoctorLine(a.shownStoreName(ctx, name), by[name]))
			waits[name] = storeConflictPushNote(by[name])
		}
	}
	if recs, err := a.StorePushRefusals(ctx); err == nil {
		own, held := splitHeldRecords(recs)
		for _, r := range held {
			if _, ok := waits[r.Store]; !ok {
				on := heldOn(r, own)
				waits[r.Store] = opsstores.HeldLines(on) + "; " + heldNext(on)
			}
		}
	}
	return waits
}

// storeConflictDoctorLine is doctor's warning for n open conflicts in the
// store shown as name.
func storeConflictDoctorLine(name string, n int) string {
	return fmt.Sprintf("store %s: %d open sync conflict(s) (rows both this machine and the store's remote changed); its push waits until they are settled: %s", name, n, storeConflictSteps)
}

// storeConflictPushNote replaces the "`lore sync` pushes them" of a push
// mode line whose store has n open conflicts: sync refuses until they
// are settled.
func storeConflictPushNote(n int) string {
	return fmt.Sprintf("held by %d open sync conflict(s): %s, then `lore sync` pushes them", n, storeConflictSteps)
}

// storeConflictInbox is one inbox item per open store sync conflict:
// always shown, like a gate, until it is settled.
func (a *App) storeConflictInbox(ctx context.Context) []opscoord.InboxItem {
	_, cs := a.openStoreConflicts(ctx)
	out := make([]opscoord.InboxItem, 0, len(cs))
	for _, c := range cs {
		what := "the row"
		if len(c.Fields) > 0 {
			names := make([]string, 0, len(c.Fields))
			for _, f := range c.Fields {
				names = append(names, f.Name)
			}
			what = strings.Join(names, ", ")
		}
		out = append(out, opscoord.InboxItem{Mission: "", Kind: inboxConflict,
			Text: fmt.Sprintf("%s/%s in store %s: both sides changed %s; %s", c.Table, c.ID, a.shownStoreName(ctx, c.Store), what, storeConflictSteps)})
	}
	return out
}
