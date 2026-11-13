package leakcheck

// asclient.go — the resource rules of `lore check leaks --as-client` (RES
// 3.9 "A repo that becomes client-readable"): an own repo about to be
// handed over is read as the client would read it. Pure: the CLI gathers
// the repo's resource rows, the field definitions it can see and the
// resource blobs of git history; nothing here reads a store or prints a
// value (findings name row, field, why and fix only).
//
//	AsClientResources(rows, defs, repo resources)
//	   row kind not a repo kind          ─► store kind id in a client file
//	   per entry with a value:
//	     no visible definition           ─► fail closed (R2 item 1)
//	     definition in a store           ─► store field id in a client file
//	     secret_ref / internal           ─► must be unset
//	     ${secret:…} in the value        ─► full locator (BATCH 5)
//	     ref not a repo resource         ─► points at a store row
//	     sensitive                       ─► readable by the client
//	AsClientHistory(versions, current, defs)
//	   an older blob holding an internal / sensitive / secret_ref value that
//	   the current file no longer holds ─► stays in git history (lore does
//	   not rewrite it)

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// Field types the rules read (kind_fields.type words).
const (
	resTypeRef       = "ref"
	resTypeSecretRef = "secret_ref"
	secretRefMarker  = "${secret:"
)

// ResField is one kind_fields definition as the check sees it.
type ResField struct {
	Name string
	Type string
	// Internal is the definition's internal flag; InternalSet says it was
	// written at all (a secret_ref is internal unless set false, RES 3.1).
	Internal, InternalSet bool
	Sensitive             bool
	// InRepo: the definition is a row of this repo's own store.
	InRepo bool
}

// internal reports the effective internal flag.
func (f ResField) internal() bool {
	if f.Type == resTypeSecretRef && !f.InternalSet {
		return true
	}
	return f.Internal
}

// ResEntry is one `fields` entry of a resource row.
type ResEntry struct {
	FieldID string
	Name    string
	Value   json.RawMessage
}

// ResRow is one resource row of the repo (or one historic version of it).
type ResRow struct {
	ID, Name, KindID string
	// KindInRepo: the kind is a row of this repo's own store.
	KindInRepo bool
	Entries    []ResEntry
}

// AsClientFinding is one thing a hand-over would make readable. Never
// carries a value.
type AsClientFinding struct {
	Row, Name, Field string
	Why, Fix         string
}

// AsClientResources applies the resource rules to the repo's live
// resource rows. defs maps field id to its definition (any store this
// machine reads); repoRes holds the ids of the repo's live resources.
func AsClientResources(rows []ResRow, defs map[string]ResField, repoRes map[string]bool) []AsClientFinding {
	var out []AsClientFinding
	for _, r := range rows {
		edit := "lore resource edit --id " + r.Name + " --unset "
		if r.KindID != "" && !r.KindInRepo {
			out = append(out, AsClientFinding{Row: r.ID, Name: r.Name, Field: "kind",
				Why: "its kind " + r.KindID + " is a store definition; the file would name a store id",
				Fix: "move it to the store (lore move --id " + r.ID + " --to <store>), and after the hand-over move it back (lore move --id " + r.ID + " --to repo writes the repo's own kind)"})
		}
		for _, e := range r.Entries {
			if emptyRaw(e.Value) {
				continue
			}
			name := e.Name
			if name == "" {
				name = e.FieldID
			}
			def, ok := defs[e.FieldID]
			if !ok {
				// R2: a definition this machine cannot read may be internal.
				out = append(out, AsClientFinding{Row: r.ID, Name: r.Name, Field: name,
					Why: "its field definition is not visible here (cannot prove it may reach a client)",
					Fix: "run lore sync on a machine that reads the store, or " + edit + name})
				continue
			}
			if !def.InRepo {
				out = append(out, AsClientFinding{Row: r.ID, Name: r.Name, Field: name,
					Why: "its field definition " + e.FieldID + " is a store row; the file would name a store id",
					Fix: "define the field in this repo's own kind, or " + edit + name})
			}
			switch {
			case def.Type == resTypeSecretRef || def.internal():
				out = append(out, AsClientFinding{Row: r.ID, Name: r.Name, Field: name,
					Why: "internal field with a value (" + typeWord(def) + ")",
					Fix: edit + name})
				continue
			case bytes.Contains(e.Value, []byte(secretRefMarker)):
				out = append(out, AsClientFinding{Row: r.ID, Name: r.Name, Field: name,
					Why: "holds a secret reference; the client would read its full locator (BATCH 5)",
					Fix: edit + name})
				continue
			case def.Type == resTypeRef:
				var target string
				if json.Unmarshal(e.Value, &target) == nil && target != "" && !repoRes[target] {
					out = append(out, AsClientFinding{Row: r.ID, Name: r.Name, Field: name,
						Why: "points at " + target + ", which is not a resource of this repo",
						Fix: "repoint it at a repo resource, or " + edit + name})
				}
			}
			if def.Sensitive {
				out = append(out, AsClientFinding{Row: r.ID, Name: r.Name, Field: name,
					Why: "sensitive value; the client could read it",
					Fix: edit + name + "   (or keep it internal: lore move --id " + r.ID + " --to <store>)"})
			}
		}
	}
	sortAsClient(out)
	return out
}

// typeWord names why a field counts as internal.
func typeWord(f ResField) string {
	if f.Type == resTypeSecretRef {
		return "secret_ref"
	}
	return "marked internal"
}

// AsClientHistory lists values of internal, sensitive or secret_ref
// fields that older versions in git history hold and the current file
// does not (a deleted row counts with all its values). current maps row id
// to field id to the current raw value; a row missing from current is
// deleted now. Each (row, field) is listed once.
func AsClientHistory(versions []ResRow, current map[string]map[string]json.RawMessage, defs map[string]ResField) []AsClientFinding {
	seen := map[string]bool{}
	var out []AsClientFinding
	for _, r := range versions {
		cur, live := current[r.ID]
		for _, e := range r.Entries {
			if emptyRaw(e.Value) {
				continue
			}
			def, ok := defs[e.FieldID]
			guarded := !ok || def.internal() || def.Sensitive || def.Type == resTypeSecretRef || bytes.Contains(e.Value, []byte(secretRefMarker))
			if !guarded {
				continue
			}
			if live && sameRaw(cur[e.FieldID], e.Value) {
				continue // the live rules already list it
			}
			key := r.ID + "\x00" + e.FieldID
			if seen[key] {
				continue
			}
			seen[key] = true
			name := e.Name
			if name == "" {
				name = e.FieldID
			}
			why := "an older version in git history holds this field's value"
			if !live {
				why = "a deleted resource; its file in git history holds this field's value"
			}
			out = append(out, AsClientFinding{Row: r.ID, Name: r.Name, Field: name, Why: why,
				Fix: "rewrite the history without it or re-create the repo before the hand-over (lore does not rewrite history)"})
		}
	}
	sortAsClient(out)
	return out
}

// ParseResourceRow reads a resource row file's id, name, kind and fields
// entries (ok false for a file that is not a live resource row). Works on
// the flat `_v: 2` row file (columns beside id; a tombstone has _deleted).
func ParseResourceRow(b []byte) (ResRow, bool) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(b, &doc) != nil {
		return ResRow{}, false
	}
	body := doc // row files are flat: columns sit beside id and _v
	var del bool
	_ = json.Unmarshal(doc["_deleted"], &del) // absent: live
	if del {
		return ResRow{}, false
	}
	var r ResRow
	_ = json.Unmarshal(doc["id"], &r.ID) // a missing id leaves "" and is skipped below
	_ = json.Unmarshal(body["name"], &r.Name)
	_ = json.Unmarshal(body["kind"], &r.KindID)
	if r.ID == "" {
		return ResRow{}, false
	}
	var entries map[string]map[string]json.RawMessage
	if raw, ok := body["fields"]; ok {
		if json.Unmarshal(raw, &entries) != nil {
			// a JSON value stored as a string (the file form of a json column)
			var s string
			if json.Unmarshal(raw, &s) == nil {
				_ = json.Unmarshal([]byte(s), &entries) // unreadable: no entries
			}
		}
	}
	ids := make([]string, 0, len(entries))
	for k := range entries {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, fid := range ids {
		ent := entries[fid]
		var n string
		_ = json.Unmarshal(ent["name"], &n) // nameless entries are named by id
		r.Entries = append(r.Entries, ResEntry{FieldID: fid, Name: n, Value: ent["v"]})
	}
	return r, true
}

// emptyRaw reports an absent, null or empty-string value.
func emptyRaw(v json.RawMessage) bool {
	t := strings.TrimSpace(string(v))
	return t == "" || t == "null" || t == `""` || t == "[]"
}

// sameRaw compares two JSON values ignoring layout.
func sameRaw(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

// sortAsClient orders findings by row, field, reason.
func sortAsClient(fs []AsClientFinding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Row != fs[j].Row {
			return fs[i].Row < fs[j].Row
		}
		if fs[i].Field != fs[j].Field {
			return fs[i].Field < fs[j].Field
		}
		return fs[i].Why < fs[j].Why
	})
}
