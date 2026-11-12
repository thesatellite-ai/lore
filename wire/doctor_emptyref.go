package wire

// doctor_emptyref.go — `lore doctor` names the rows whose ref fields hold
// the empty string lore 0.1.17 wrote for "none" (owner report 2026-11-12:
// 36 adopted tasks with tasklist_id "", one with assigned_to_actor_id "").
// Reads and edits already treat them as null (engine/emptyref.go); this
// check says where they are and `lore doctor --fix` writes null, every row
// of a store in one store commit.
//
//	no such row                 ──▶ nothing
//	rows in a writable store    ──▶ note per store naming the fields and
//	                                counts; --fix: one commit per store
//	rows in a read-only store   ──▶ note: fixed where it is writable

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckEmptyRef names the empty-string-ref check.
const DoctorCheckEmptyRef DoctorCheck = "empty_ref"

// emptyRefFix is the finding's fix.
const emptyRefFix = "lore doctor --fix"

// emptyRefRow is one row holding empty-string refs.
type emptyRefRow struct {
	ref    engine.Ref
	fields []string
}

// DoctorEmptyRefs reports (and with fix repairs) the empty-string refs of
// every attached store.
func (a *App) DoctorEmptyRefs(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	raw := engine.WithStoredRefs(ctx)
	reg := st.eng.Registry()
	var labels func(store.Source) string // made on the first finding: it walks the org's stores
	var out []DoctorFinding
	for _, src := range st.eng.Stores() {
		var found []emptyRefRow
		for _, def := range reg.Tables() {
			if !hasRefField(def) {
				continue
			}
			rows, lerr := st.eng.List(raw, src, def.Name)
			if lerr != nil {
				continue // an unreadable store: the store checks report it
			}
			for _, r := range rows {
				if names := engine.EmptyRefFields(reg, r); len(names) > 0 {
					found = append(found, emptyRefRow{engine.Ref{Store: src, Table: def.Name, ID: r.ID}, names})
				}
			}
		}
		if len(found) == 0 {
			continue
		}
		if labels == nil {
			labels = a.storeLabels(ctx, st)
		}
		label := labels(src)
		f := DoctorFinding{Check: DoctorCheckEmptyRef, Level: DoctorNote, Store: src,
			Message: emptyRefMessage(label, found), Fix: emptyRefFix}
		switch {
		case st.eng.ReadOnly(src):
			f.Fix = "run `lore doctor --fix` where " + label + " is writable"
		case fix:
			b := a.bulk(raw, "doctor --fix: write null for empty refs", nil) // raw: the edit sees the stored "" and writes null
			for _, x := range found {
				patch := map[string]value.Value{}
				for _, n := range x.fields {
					patch[n] = value.Null{}
				}
				b.update(x.ref, patch)
			}
			_ = b.commit() // a failed store write is in b.failed
			if ferr := b.failedErr("doctor: empty_ref", emptyRefFix); ferr != nil {
				f.Message += " (not fixed: " + ferr.Error() + ")"
			} else {
				f.Fixed = true
				f.Message += "; wrote null in " + plural(b.total(), "row") + " (one store commit)"
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// hasRefField reports whether def has a ref field.
func hasRefField(def schema.TableDef) bool {
	return slices.ContainsFunc(def.Fields, func(f schema.FieldDef) bool { return f.Kind == value.KindRef })
}

// emptyRefMessage is the finding's line: counts per table.field.
func emptyRefMessage(label string, found []emptyRefRow) string {
	per := map[string]int{}
	for _, x := range found {
		for _, n := range x.fields {
			per[string(x.ref.Table)+"."+n]++
		}
	}
	parts := make([]string, 0, len(per))
	for _, k := range slices.Sorted(maps.Keys(per)) {
		parts = append(parts, k+" "+strconv.Itoa(per[k]))
	}
	return plural(len(found), "row") + " in " + label + " hold an empty string in a ref field (lore 0.1.17 wrote it for \"none\"): " +
		strings.Join(parts, ", ") + "; reads and edits treat it as none, `lore doctor --fix` writes null"
}
