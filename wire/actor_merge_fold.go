package wire

// actor_merge_fold.go — a merge's edit that would land on a row the kept
// actor already holds (devco:D-993, owner report 2026-11-25: two `lore
// member add` the same day, then `lore actor merge` stopped at
// actor_periods E_NAME_TAKEN "started_on already exists", and its printed
// rerun failed the same).
//
//	actorRefEdits, per store and table with a natural key (the first
//	unique index: actor_periods (actor_id, started_on), actor_contacts
//	(actor_id, type, value_norm), project_members (project_id, actor_id,
//	started_on), any table whose key names an actor)
//	   edit whose new key a live row already holds ─▶ folded:
//	      the holder (the kept actor's row) takes the duplicate's fields
//	      (foldFields: an open end beats a dated one, else the later end;
//	      the earlier start; lists unioned; a true flag kept; a blank
//	      filled), the duplicate is deleted (its tombstone keeps history)
//	   actor_contacts ─▶ one primary per (actor, type) after the merge
//	      (TP-3): the kept actor's own primary stays, a moved one is
//	      edited to primary false

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Fields foldFields reads by name: a stint's start and end (actor_periods,
// project_members) and why it ended, which an open end clears.
const (
	foldStartedOn = schema.FieldPeriodStartedOn
	foldEndedOn   = schema.FieldPeriodEndedOn
	foldEndReason = schema.FieldPeriodEndReason
)

// naturalKeyFields is the first unique index of def (the engine's natural
// key, which an edit may not move onto another live row's); nil when the
// table has none.
func naturalKeyFields(def schema.TableDef) []string {
	for _, ix := range def.Indexes {
		if ix.Unique && len(ix.Fields) > 0 {
			return ix.Fields
		}
	}
	return nil
}

// foldKey is fields' value of every key field joined, as the engine
// compares them (sameKey: String of each); ok is false when one is unset.
func foldKey(fields map[string]value.Value, keyFields []string) (string, bool) {
	parts := make([]string, 0, len(keyFields))
	for _, f := range keyFields {
		v := fields[f]
		if blankValue(v) {
			return "", false
		}
		parts = append(parts, v.String())
	}
	return strings.Join(parts, "\x00"), true
}

// blankValue reports whether v holds nothing (unset, null or empty text).
func blankValue(v value.Value) bool {
	return v == nil || v.Kind() == value.KindNull || v.String() == ""
}

// overlay is fields with patch applied (a new map).
func overlay(fields, patch map[string]value.Value) map[string]value.Value {
	out := make(map[string]value.Value, len(fields)+len(patch))
	for k, v := range fields {
		out[k] = v
	}
	for k, v := range patch {
		out[k] = v
	}
	return out
}

// foldActorEdits folds every edit of one table's rows (live: the table's
// rows in one store) whose new natural key another live row holds into
// that row: the holder gets foldFields' patch, the edit becomes a delete.
// An edit no row collides with is kept. actor_contacts then gets one
// primary per actor and type (oneContactPrimary). The result is sorted
// by id.
func foldActorEdits(def schema.TableDef, live []row.Row, edits []actorEdit) []actorEdit {
	if len(edits) == 0 {
		return edits
	}
	keyFields := naturalKeyFields(def)
	byID := map[ids.AnyID]row.Row{}
	for _, r := range live {
		if !r.Deleted {
			byID[r.ID] = r
		}
	}
	edited := map[ids.AnyID]int{}
	for i, e := range edits {
		edited[e.ref.ID] = i
	}
	// whole is each live row as the merge leaves it (edits applied)
	whole := map[ids.AnyID]map[string]value.Value{}
	for id, r := range byID {
		whole[id] = r.Fields
	}
	for _, e := range edits {
		whole[e.ref.ID] = overlay(byID[e.ref.ID].Fields, e.patch)
	}
	// extra is the patch of a holder no edit names yet
	extra := map[ids.AnyID]map[string]value.Value{}
	if keyFields != nil {
		holder := map[string]ids.AnyID{}
		unedited := make([]ids.AnyID, 0, len(byID))
		for id := range byID {
			if _, ok := edited[id]; !ok {
				unedited = append(unedited, id)
			}
		}
		slices.SortFunc(unedited, func(x, y ids.AnyID) int { return strings.Compare(x.String(), y.String()) })
		for _, id := range unedited {
			if k, ok := foldKey(whole[id], keyFields); ok {
				if _, taken := holder[k]; !taken {
					holder[k] = id
				}
			}
		}
		order := slices.Clone(edits)
		slices.SortFunc(order, func(x, y actorEdit) int { return strings.Compare(x.ref.ID.String(), y.ref.ID.String()) })
		for _, e := range order {
			k, ok := foldKey(whole[e.ref.ID], keyFields)
			if !ok {
				continue
			}
			h, taken := holder[k]
			if !taken {
				holder[k] = e.ref.ID
				continue
			}
			p := foldFields(def, keyFields, whole[h], whole[e.ref.ID])
			whole[h] = overlay(whole[h], p)
			if i, ok := edited[h]; ok {
				edits[i].patch = overlay(edits[i].patch, p)
			} else if len(p) > 0 {
				extra[h] = overlay(extra[h], p)
			}
			i := edited[e.ref.ID]
			edits[i].patch, edits[i].del, edits[i].foldedInto = nil, true, h
			delete(whole, e.ref.ID)
		}
	}
	if def.Name == schema.TableActorContacts {
		for id, p := range oneContactPrimary(byID, whole, edited, edits) {
			if i, ok := edited[id]; ok {
				edits[i].patch = overlay(edits[i].patch, p)
				continue
			}
			extra[id] = overlay(extra[id], p)
		}
	}
	for id, p := range extra {
		if len(p) == 0 {
			continue
		}
		src := edits[0].ref
		src.ID = id
		edits = append(edits, actorEdit{ref: src, patch: p})
	}
	slices.SortFunc(edits, func(x, y actorEdit) int { return strings.Compare(x.ref.ID.String(), y.ref.ID.String()) })
	return edits
}

// foldFields is the patch that makes keep hold dup's facts too (keep is
// the row that stays; key fields are equal and left alone). A stint's end
// (ended_on) is open when either is open, else the later day, with the
// end reason of the end kept; its start is the earlier day; a list is the
// union; a flag set on either is set; any other blank field of keep takes
// dup's value.
func foldFields(def schema.TableDef, keyFields []string, keep, dup map[string]value.Value) map[string]value.Value {
	p := map[string]value.Value{}
	for _, f := range def.Fields {
		n := f.Name
		if slices.Contains(keyFields, n) || n == foldEndReason {
			continue
		}
		kv, dv := keep[n], dup[n]
		switch {
		case n == foldEndedOn:
			switch {
			case blankValue(kv):
			case blankValue(dv):
				p[n] = value.Null{}
				if !blankValue(keep[foldEndReason]) {
					p[foldEndReason] = value.Null{}
				}
			case dv.String() > kv.String():
				p[n] = dv
				if r := dup[foldEndReason]; !blankValue(r) {
					p[foldEndReason] = r
				} else if !blankValue(keep[foldEndReason]) {
					p[foldEndReason] = value.Null{}
				}
			}
		case blankValue(dv):
		case blankValue(kv):
			p[n] = dv
		case n == foldStartedOn && dv.String() < kv.String():
			p[n] = dv
		case f.Kind == value.KindBool:
			if dv.String() == "true" && kv.String() != "true" {
				p[n] = dv
			}
		case f.Kind == value.KindList:
			if u, ok := unionList(kv, dv); ok {
				p[n] = u
			}
		}
	}
	return p
}

// unionList is keep's items followed by dup's items keep lacks; ok is
// false when dup adds none (or either is not a list).
func unionList(keep, dup value.Value) (value.Value, bool) {
	kl, ok1 := keep.(value.List)
	dl, ok2 := dup.(value.List)
	if !ok1 || !ok2 {
		return nil, false
	}
	items := kl.Items()
	added := false
	for _, it := range dl.Items() {
		if !slices.ContainsFunc(items, func(x value.Value) bool { return value.Equal(x, it) }) {
			items, added = append(items, it), true
		}
	}
	if !added {
		return nil, false
	}
	l, err := value.ListOf(kl.Elem(), items...)
	if err != nil {
		return nil, false
	}
	return l, true
}

// oneContactPrimary is the patches that leave at most one primary contact
// per actor and type after the merge (TP-3): of several, a row that was
// primary before and is not moved by the merge stays (the kept actor's
// choice), else the first by id; the others are edited to primary false.
func oneContactPrimary(before map[ids.AnyID]row.Row, whole map[ids.AnyID]map[string]value.Value, edited map[ids.AnyID]int, edits []actorEdit) map[ids.AnyID]map[string]value.Value {
	groups := map[string][]ids.AnyID{}
	for id, f := range whole {
		if f[schema.FieldContactPrimary] == nil || f[schema.FieldContactPrimary].String() != "true" {
			continue
		}
		k := f[schema.FieldContactActor].String() + "\x00" + f[schema.FieldContactType].String()
		groups[k] = append(groups[k], id)
	}
	moved := func(id ids.AnyID) bool {
		i, ok := edited[id]
		return ok && edits[i].patch[schema.FieldContactActor] != nil
	}
	out := map[ids.AnyID]map[string]value.Value{}
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		slices.SortFunc(g, func(x, y ids.AnyID) int { return strings.Compare(x.String(), y.String()) })
		keep := g[0]
		for _, id := range g {
			if p := before[id].Fields[schema.FieldContactPrimary]; p != nil && p.String() == "true" && !moved(id) {
				keep = id
				break
			}
		}
		for _, id := range g {
			if id != keep {
				out[id] = map[string]value.Value{schema.FieldContactPrimary: value.BoolOf(false)}
			}
		}
	}
	return out
}
