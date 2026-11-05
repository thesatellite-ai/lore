// Package actorproject rewrites every actor id a row's fields hold through
// one mapping, at any depth: a ref or text field, every item of a list, and
// every string inside a JSON value (list items, object members, nested).
//
// Why it exists (AGENT_IDENTITY Q11/F18, decision P2B-OLD-2): while an
// org's agents.agent_details is internal, a client repo must never hold an
// agent's actor id. Agent ids reach a task row in many places, top-level
// (done_by, assignees, created_by_actor_id) and nested (history[].by,
// time_entries[].actor_id/created_by/updated_by, checklist[].done_by,
// claim_log[].actor_id), and new nested records keep appearing. A list of
// field names would miss the next one, so the projection is driven by the
// value itself: a string is an actor id exactly when ids.Parse[ids.Actor]
// accepts it (the `act_` prefix plus 32 hex digits), which no free text,
// label or other id kind does. Free text that merely mentions an id inside
// a longer sentence is not rewritten.
//
// Layer L1: imports model only; pure, no I/O.
package actorproject

import (
	"encoding/json"
	"maps"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Mapper maps one actor id to the id the row is stored with. Returning the
// same id keeps it; returning the zero id means "nobody": a ref or text
// field holding it is removed, a list item or JSON array element holding it
// is dropped, and a JSON object member holding it becomes "" (the zero id's
// JSON form, which ids.ID decodes back to the zero id). A Mapper must be
// idempotent (a mapped id maps to itself), because a stored row is mapped
// again on every later write.
type Mapper func(ids.ID[ids.Actor]) ids.ID[ids.Actor]

// Fields returns fields with every actor id mapped through as. fields is
// never mutated: a changed map is a clone, an unchanged one is returned as
// is. A value this package cannot rebuild (which only a malformed value
// could cause) is left unchanged rather than failing the write.
func Fields(as Mapper, fields map[string]value.Value) map[string]value.Value {
	if as == nil {
		return fields
	}
	out, copied := fields, false
	for k, v := range fields {
		nv, drop, changed := Value(as, v)
		if !changed {
			continue
		}
		if !copied {
			out, copied = maps.Clone(fields), true
		}
		if drop {
			delete(out, k)
			continue
		}
		out[k] = nv
	}
	return out
}

// Value maps the actor ids v holds. drop reports a scalar that now names
// nobody (the caller removes it); changed reports any difference.
func Value(as Mapper, v value.Value) (nv value.Value, drop, changed bool) {
	if v == nil {
		return v, false, false
	}
	switch v.Kind() {
	case value.KindText, value.KindRef:
		return scalar(as, v)
	case value.KindList:
		l, ok := v.(value.List)
		if !ok {
			return v, false, false
		}
		return list(as, l)
	case value.KindJSON:
		j, ok := v.(value.JSON)
		if !ok || j.IsZero() {
			return v, false, false
		}
		raw, jchanged, err := mapJSON(as, j.Raw())
		if err != nil || !jchanged {
			return v, false, false
		}
		nj, err := value.JSONOf(raw)
		if err != nil {
			return v, false, false
		}
		return nj, false, true
	case value.KindNumber, value.KindBool, value.KindDate, value.KindDateTime, value.KindDuration,
		value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL, value.KindEmail, value.KindNull, value.KindSecretRef:
		// these kinds never hold an actor id
		return v, false, false
	}
	return v, false, false
}

// scalar maps one text or ref value.
func scalar(as Mapper, v value.Value) (value.Value, bool, bool) {
	id, err := ids.Parse[ids.Actor](v.String())
	if err != nil {
		return v, false, false
	}
	mapped := as(id)
	switch {
	case mapped == id:
		return v, false, false
	case mapped.IsZero():
		return nil, true, true
	case v.Kind() == value.KindRef:
		ref, rerr := value.RefOf(mapped.Any())
		if rerr != nil {
			return v, false, false
		}
		return ref, false, true
	default:
		txt, terr := value.TextOf(mapped.String())
		if terr != nil {
			return v, false, false
		}
		return txt, false, true
	}
}

// list maps every item; an item naming nobody is dropped.
func list(as Mapper, l value.List) (value.Value, bool, bool) {
	items := l.Items()
	out := make([]value.Value, 0, len(items))
	changed := false
	for _, it := range items {
		nv, drop, ch := Value(as, it)
		changed = changed || ch
		if drop {
			continue
		}
		out = append(out, nv)
	}
	if !changed {
		return l, false, false
	}
	nl, err := value.ListOf(l.Elem(), out...)
	if err != nil {
		return l, false, false
	}
	return nl, false, true
}

// mapJSON walks one canonical JSON value. A string naming nobody becomes
// "" as an object member and is dropped as an array element.
func mapJSON(as Mapper, raw []byte) ([]byte, bool, error) {
	switch canon.KindOf(raw) {
	case canon.KindString:
		nv, drop, changed, err := jsonString(as, raw)
		if err != nil || !changed {
			return raw, false, err
		}
		if drop {
			return []byte(`""`), true, nil
		}
		return nv, true, nil
	case canon.KindArray:
		return mapArray(as, raw)
	case canon.KindObject:
		return mapObject(as, raw)
	case canon.KindInvalid, canon.KindNull, canon.KindBool, canon.KindNumber:
		// no string inside: nothing to map
		return raw, false, nil
	}
	return raw, false, nil
}

// jsonString maps a JSON string literal that is an actor id.
func jsonString(as Mapper, raw []byte) (nv []byte, drop, changed bool, err error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return raw, false, false, err
	}
	id, perr := ids.Parse[ids.Actor](s)
	if perr != nil {
		return raw, false, false, nil
	}
	mapped := as(id)
	switch {
	case mapped == id:
		return raw, false, false, nil
	case mapped.IsZero():
		return nil, true, true, nil
	}
	return canon.String(mapped.String()), false, true, nil
}

func mapArray(as Mapper, raw []byte) ([]byte, bool, error) {
	elems, err := canon.Elements(raw)
	if err != nil {
		return raw, false, err
	}
	out := []byte{'['}
	n, changed := 0, false
	for _, e := range elems {
		var ne []byte
		if canon.KindOf(e) == canon.KindString {
			nv, drop, ch, serr := jsonString(as, e)
			if serr != nil {
				return raw, false, serr
			}
			changed = changed || ch
			if drop {
				continue
			}
			ne = nv
		} else {
			nv, ch, verr := mapJSON(as, e)
			if verr != nil {
				return raw, false, verr
			}
			changed = changed || ch
			ne = nv
		}
		if n > 0 {
			out = append(out, ',')
		}
		out = append(out, ne...)
		n++
	}
	if !changed {
		return raw, false, nil
	}
	return append(out, ']'), true, nil
}

func mapObject(as Mapper, raw []byte) ([]byte, bool, error) {
	members, err := canon.Members(raw)
	if err != nil {
		return raw, false, err
	}
	changed := false
	for i, m := range members {
		nv, ch, verr := mapJSON(as, m.Raw)
		if verr != nil {
			return raw, false, verr
		}
		if ch {
			members[i].Raw, changed = nv, true
		}
	}
	if !changed {
		return raw, false, nil
	}
	doc, err := canon.Object(members)
	if err != nil {
		return raw, false, err
	}
	c, err := canon.Compact(doc)
	return c, err == nil, err
}
