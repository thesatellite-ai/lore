// Package merge is the one field-level three-way merge of lore rows
// (STORE_SERVER_SPEC 11, rules 1-3, 5). The engine calls it when a checked
// write comes back stale (spec 5.3), the git backend calls it through Driver
// from its merge driver, and a later server will call the same function, so
// two backends can never settle the same clash differently.
//
// Layer L1: pure, no I/O; imports model and schema only.
//
// Ported from saas/pkg/aicoder/merge3/merge3.go (field merge, conflict
// markers) and saas/pkg/aicoder/lsync/mergedriver.go (row-level settling),
// with two deliberate changes: every row is ordered by its _hlc (never by
// updated_at), and an edit newer than a tombstone no longer brings the row
// back; it is reported instead (spec 11 rule 3, DQ4: tombstones are kept
// forever so no old copy can resurrect a row).
//
// Flow:
//
//	Rows ─ order (HLC, then bytes) ─ tombstone rules ─ fields ─ settleTrash (soft deletes, trash.go):
//	  each field: pick (3-way) ─ clash? ─ strategyMerge (schema.FieldDef.Merge)
//	                                       list_by_id │ set_union │ url_set │ entries_by_id
//	                                     └ else: text → Mark, other → newer side
//	  meta: StrategyMeta (entry by entry, objects per field, lists union,
//	        strict clash → {conflict}), reserved, _o (mergeOrigin)
//
// Invariants:
//   - Rows is deterministic, and for every field that is not free text its
//     outcome does not depend on which side is called "ours" (the fuzz test
//     pins this): the newer side is chosen by HLC, ties by document bytes.
//   - A clash on a free-text field never silently drops either text: both
//     are kept inside conflict markers and the field is listed in
//     Result.Conflicts.
package merge

import (
	"bytes"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Conflict markers, shaped like git's so editors highlight them. The label
// after the opening and closing marker is the HLC of the side that follows
// (newer side first), so both machines of a merge write the same bytes.
const (
	MarkerOpen  = "<<<<<<< "
	MarkerSplit = "======="
	MarkerClose = ">>>>>>> "
)

// Policy says which fields are free text: a clash on a free-text field gets
// conflict markers; a clash on any other field takes the newer side, unless
// Strategy names a per-field strategy (schema.FieldDef.Merge), which then
// settles the clash instead (StrategyListByID).
//
// Invariant: IsText is never nil on a Policy built by PolicyFor or
// UnknownTable; the zero Policy treats every field as non-text. A nil
// Strategy means every field uses the row rule.
type Policy struct {
	IsText   func(field string) bool
	Strategy func(field string) schema.MergeStrategy
}

// PolicyFor builds the policy of a known table: text fields with no closed
// value set (memories.body, not memories.kind) are free text. A status-like
// field takes the newer side, as v0.1.17 did (StrategyNewest).
func PolicyFor(def schema.TableDef) Policy {
	text := map[string]bool{}
	strategies := map[string]schema.MergeStrategy{}
	for _, f := range def.Fields {
		if f.Kind == value.KindText && len(f.Values) == 0 {
			text[f.Name] = true
		}
		if f.Merge != schema.MergeDefault {
			strategies[f.Name] = f.Merge
		}
	}
	return Policy{
		IsText:   func(field string) bool { return text[field] },
		Strategy: func(field string) schema.MergeStrategy { return strategies[field] },
	}
}

// UnknownTable is the policy for a row whose table this binary does not
// know (a kind defined after this build, a newer table): every string value
// is treated as free text, so a clash stays loud instead of being settled
// by a rule nobody declared (fail closed).
func UnknownTable() Policy {
	return Policy{IsText: func(string) bool { return true }}
}

// FieldConflict is one free-text field both sides changed differently.
// Newer and Older are the two texts, newer side by HLC first; the merged
// row holds them inside conflict markers.
type FieldConflict struct {
	Field string
	Newer value.Value
	Older value.Value
}

// Result is the outcome of one merge.
type Result struct {
	// Row is the merged row. Its HLC is the larger of the two sides; a
	// caller that writes it as a new edit must stamp a fresh HLC above both.
	Row row.Row
	// Conflicts lists the free-text fields that now hold conflict markers,
	// sorted by field. Empty means the merge is clean.
	Conflicts []FieldConflict
	// NewerWins lists fields (and "meta.<key>" entries) both sides changed
	// where the newer side was taken, sorted. Reported, never a conflict.
	NewerWins []string
	// EditAfterDelete is set when one side is a tombstone and the other
	// side holds an edit with a higher HLC: the tombstone is kept (DQ4: rows are
	// never resurrected) and the edit's HLC is reported here for lore doctor
	// (spec 11 rule 3). Zero when nothing was overruled.
	EditAfterDelete hlc.HLC
	// Trash is the soft-delete rule that settled the row (DELETION 3.7,
	// trash.go), "" when none applied.
	Trash TrashOutcome
}

// Clean reports whether the merge needs no person.
func (r Result) Clean() bool { return len(r.Conflicts) == 0 }

// Absent is the base of a merge with no common ancestor (both sides created
// the row): the zero row.
var Absent = row.Row{}

// Rows merges ours and theirs against base. base is Absent (zero ID) when
// there is no common ancestor. ours and theirs must name the same table and
// id; base, when present, too.
func Rows(p Policy, base, ours, theirs row.Row) (Result, error) {
	if ours.Table != theirs.Table || ours.ID != theirs.ID {
		return Result{}, errs.New(errs.InvalidInput, "merge: the two sides are different rows")
	}
	if !base.ID.IsZero() && (base.Table != ours.Table || base.ID != ours.ID) {
		return Result{}, errs.New(errs.InvalidInput, "merge: the base is a different row")
	}
	if r, ok := replaceRebuilt(ours, theirs); ok {
		return Result{Row: r}, nil // METADATA 3.18: a real key row replaces a rebuilt one whole (rebuilt.go)
	}
	newer, older, oursNewer := order(ours, theirs)
	switch {
	case newer.Deleted && older.Deleted:
		// DEL-TOMB2 (DELETION 3.7): both sides purged: the EARLIER tombstone
		// (lower HLC; order() breaks an HLC tie by bytes) is kept, so the
		// first purge's purger and time stay on record whatever the side.
		return Result{Row: older}, nil
	case newer.Deleted:
		// The tombstone is newer than the edit: it wins (rule 3).
		return Result{Row: newer}, nil
	case older.Deleted:
		// The edit is newer than the tombstone, but rows never come back
		// (DQ4: an old branch must not revive a deleted row).
		return Result{Row: older, EditAfterDelete: newer.HLC}, nil
	}
	if base.Deleted {
		base = Absent // both sides revived a tombstone: no usable ancestor
	}
	ns := newerTheirs
	if oursNewer {
		ns = newerOurs
	}
	res := fields(p, base, ours, theirs, newer, older, ns)
	settleTrash(&res, base, ours, theirs, newer) // DELETION 3.7: soft-delete rules (trash.go)
	return res, nil
}

// order returns (newer, older) by HLC; equal HLCs (which only one edit can
// produce) fall back to the document bytes so the choice never depends on
// the argument order.
func order(a, b row.Row) (newer, older row.Row, aNewer bool) {
	switch c := a.HLC.Compare(b.HLC); {
	case c > 0:
		return a, b, true
	case c < 0:
		return b, a, false
	}
	ab, aerr := row.Encode(a)
	bb, berr := row.Encode(b)
	if aerr == nil && berr == nil && bytes.Compare(ab, bb) < 0 {
		return b, a, false
	}
	return a, b, true
}

// fields merges two live rows field by field.
func fields(p Policy, base, ours, theirs, newer, older row.Row, newerSide side) Result {
	isText := p.IsText
	if isText == nil {
		isText = func(string) bool { return false }
	}
	out := row.Row{
		Table: newer.Table, ID: newer.ID, HLC: newer.HLC,
		UpdatedAt: newer.UpdatedAt, UpdatedBy: newer.UpdatedBy,
	}
	out.CreatedAt, out.CreatedBy = created(base, ours, theirs)
	var res Result

	merged := map[string]value.Value{}
	for _, k := range keys(base.Fields, ours.Fields, theirs.Fields) {
		v, how := pick(base.Fields, ours.Fields, theirs.Fields, newer.Fields, k)
		if how == pickedNewer {
			st := schema.MergeDefault
			if p.Strategy != nil {
				st = p.Strategy(k)
			}
			if st == schema.MergeDefault && k == fieldSharedAs {
				// shared_as is a v0.1.17 bookkeeping list on any table,
				// declared by none: always a union (PROVENANCE_DESIGN 5.4, 7).
				st = schema.MergeSetUnion
			}
			// A per-field strategy settles the clash. Should it refuse the
			// values (a corrupt list), the row rule below still applies, so
			// the merge never fails on one bad field; a corrupt entries
			// object is one conflict on the whole field instead (R10).
			sv, entryConflicts, ok, err := strategyMerge(st, base.Fields[k], ours.Fields[k], theirs.Fields[k], newerSide)
			switch {
			case ok && err == nil:
				merged[k] = sv
				for _, id := range entryConflicts {
					res.Conflicts = append(res.Conflicts, FieldConflict{Field: k + "." + id, Newer: newer.Fields[k], Older: older.Fields[k]})
				}
				continue
			case ok && st == schema.MergeEntriesByID:
				merged[k] = newer.Fields[k]
				res.Conflicts = append(res.Conflicts, FieldConflict{Field: k, Newer: newer.Fields[k], Older: older.Fields[k]})
				continue
			}
		}
		switch {
		case how == pickedNewer && isText(k) && isTextValue(newer.Fields[k]) && isTextValue(older.Fields[k]):
			n, o := newer.Fields[k], older.Fields[k]
			// Both sides are valid UTF-8 text, so the joined text is too;
			// should TextOf ever refuse it, the newer text is kept and the
			// clash is still listed, so nothing is dropped silently.
			merged[k] = n
			if marked, err := value.TextOf(Mark(newer.HLC, n.String(), older.HLC, o.String())); err == nil {
				merged[k] = marked
			}
			res.Conflicts = append(res.Conflicts, FieldConflict{Field: k, Newer: n, Older: o})
			continue
		case how == pickedNewer:
			res.NewerWins = append(res.NewerWins, k)
		}
		if v != nil {
			merged[k] = v
		}
	}
	if len(merged) > 0 {
		out.Fields = merged
	}

	// meta: entry by entry (StrategyMeta, METADATA 3.12). Should the
	// sides not merge (a corrupt entry), the newer meta is kept and the
	// whole meta is one conflict for a person (R10), never dropped.
	meta, metaConflicts, metaWins, merr := StrategyMeta(base.Meta, ours.Meta, theirs.Meta, newerSide)
	switch {
	case merr != nil:
		meta = newer.Meta
		res.Conflicts = append(res.Conflicts, FieldConflict{Field: FieldMeta})
	default:
		for _, id := range metaConflicts {
			res.Conflicts = append(res.Conflicts, FieldConflict{Field: metaField(id), Newer: newer.Meta[mustMetaID(id)], Older: older.Meta[mustMetaID(id)]})
		}
		for _, id := range metaWins {
			res.NewerWins = append(res.NewerWins, metaField(id))
		}
	}
	if len(meta) > 0 {
		out.Meta = meta
	}

	reserved := map[string]value.Value{}
	for _, k := range keys(base.Reserved, ours.Reserved, theirs.Reserved) {
		v, how := pick(base.Reserved, ours.Reserved, theirs.Reserved, newer.Reserved, k)
		if how == pickedNewer {
			// An underscore key this lore does not model (a future "_w")
			// is settled like any non-text field, and reported the same
			// way, so the older side's value never vanishes silently.
			res.NewerWins = append(res.NewerWins, k)
		}
		if v != nil {
			reserved[k] = v
		}
	}
	if len(reserved) > 0 {
		out.Reserved = reserved
	}

	out.Origin = mergeOrigin(base, ours, theirs)

	slices.SortFunc(res.Conflicts, func(a, b FieldConflict) int { return strings.Compare(a.Field, b.Field) })
	slices.Sort(res.NewerWins)
	res.Row = out
	return res
}

// picked says how pick chose a value.
type picked int

const (
	pickedSame  picked = iota // both sides agree (or only one changed)
	pickedNewer               // both changed differently: newer side taken
)

// pick is the three-way rule for one key: equal sides agree; a side equal
// to base did not change, so the other side wins; otherwise the newer side.
// A nil result means the key is absent in the merge.
func pick(b, o, t, newer map[string]value.Value, k string) (value.Value, picked) {
	bv, ov, tv := b[k], o[k], t[k]
	switch {
	case value.Equal(ov, tv):
		return ov, pickedSame
	case value.Equal(ov, bv):
		return tv, pickedSame
	case value.Equal(tv, bv):
		return ov, pickedSame
	}
	return newer[k], pickedNewer
}

// created keeps the creation stamp of the base, else the earlier side's.
func created(base, ours, theirs row.Row) (time.Time, ids.ID[ids.Actor]) {
	if !base.ID.IsZero() && !base.CreatedAt.IsZero() {
		return base.CreatedAt, base.CreatedBy
	}
	a, b := ours, theirs
	switch {
	case a.CreatedAt.IsZero():
		return b.CreatedAt, b.CreatedBy
	case b.CreatedAt.IsZero():
		return a.CreatedAt, a.CreatedBy
	case b.CreatedAt.Before(a.CreatedAt),
		b.CreatedAt.Equal(a.CreatedAt) && b.CreatedBy.String() < a.CreatedBy.String():
		return b.CreatedAt, b.CreatedBy
	}
	return a.CreatedAt, a.CreatedBy
}

// keys returns the sorted union of the maps' keys.
func keys(ms ...map[string]value.Value) []string {
	seen := map[string]struct{}{}
	for _, m := range ms {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// mustMetaID parses an entry id StrategyMeta returned (it came from a
// valid key of a side; the zero id, which no map holds, otherwise).
func mustMetaID(s string) ids.ID[ids.MetaKey] {
	id, err := ids.Parse[ids.MetaKey](s)
	if err != nil {
		return ids.ID[ids.MetaKey]{}
	}
	return id
}

// isTextValue reports a present text value.
func isTextValue(v value.Value) bool { return v != nil && v.Kind() == value.KindText }

// Mark renders a text clash with conflict markers, the newer side first.
func Mark(newerHLC hlc.HLC, newer string, olderHLC hlc.HLC, older string) string {
	return MarkerOpen + newerHLC.String() + "\n" + newer + "\n" + MarkerSplit + "\n" + older + "\n" + MarkerClose + olderHLC.String()
}

// HasMarkers reports whether s holds an unresolved conflict block (a Mark
// block or a raw git hunk). Writers refuse such text so marker text is never
// stored as knowledge (spec 11 rule 2: "refused on import until resolved").
func HasMarkers(s string) bool {
	var open, split, closed bool
	for line := range strings.SplitSeq(s, "\n") {
		switch {
		case strings.HasPrefix(line, MarkerOpen):
			open = true
		case line == MarkerSplit:
			split = true
		case strings.HasPrefix(line, MarkerClose):
			closed = true
		}
	}
	return open && split && closed
}
