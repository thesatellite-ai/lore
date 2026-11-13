package merge

import (
	"bytes"
	"maps"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Driver merges row documents byte for byte: decode the three sides, Rows,
// encode the result. It satisfies the git backend's Merger interface
// (adapters/backend/git: Merge(base, ours, theirs []byte) ([]byte, bool,
// error)) structurally, without this package importing the adapter, so the
// git merge driver and the engine run the very same merge (spec 11 rule 5).
//
// Registry picks the per-table Policy; a table it does not hold (or a nil
// Registry) uses UnknownTable, which keeps every string clash loud.
// Report, when set, receives every Result (lore doctor's edit-after-delete
// list, the sync report's merged fields); it must not retain the rows.
type Driver struct {
	Registry *schema.Registry
	Report   func(Result)
	// OursFirst is Policy.OursFirst for every table (the git merge
	// driver of a worktree merge).
	OursFirst bool
}

// Merge implements the git backend's Merger. An empty or whitespace-only
// base means the row was added on both sides. conflict is true when a
// free-text field holds conflict markers (the merged bytes are still a
// valid row document; the importer refuses marker text until resolved). An
// edit newer than a tombstone is NOT a conflict: the tombstone is written
// and the case goes to Report.
func (d Driver) Merge(base, ours, theirs []byte) (merged []byte, conflict bool, err error) {
	b := Absent
	if len(bytes.TrimSpace(base)) > 0 {
		if b, err = row.Decode(base); err != nil {
			return nil, false, errs.Wrap(errs.InvalidInput, "merge: base is not a row document", err)
		}
	}
	o, err := row.Decode(ours)
	if err != nil {
		return nil, false, errs.Wrap(errs.InvalidInput, "merge: ours is not a row document", err)
	}
	t, err := row.Decode(theirs)
	if err != nil {
		return nil, false, errs.Wrap(errs.InvalidInput, "merge: theirs is not a row document", err)
	}
	p := UnknownTable()
	if d.Registry != nil {
		if def, ok := d.Registry.Table(o.Table); ok {
			p = PolicyFor(def)
		}
	}
	p.OursFirst = d.OursFirst
	res, err := Rows(p, b, o, t)
	if err != nil {
		return nil, false, err
	}
	if d.Report != nil {
		d.Report(res)
	}
	out, err := row.Encode(res.Row)
	if err != nil {
		return nil, false, err
	}
	return out, !res.Clean(), nil
}

// MergePrefer merges like Merge, then settles every conflict with one
// side's value: theirs when preferTheirs, else ours. Only the clashing
// fields (and meta entries) take that side; a field only one side changed
// keeps that change, as in any clean merge (bug hunt B2-22: settling a
// store conflict used to take one side's whole row and silently drop the
// other side's independent edits). The git backend calls it for a row a
// person settled (lore sync resolve --take, or an edit after the refusal).
func (d Driver) MergePrefer(base, ours, theirs []byte, preferTheirs bool) ([]byte, error) {
	b := Absent
	var err error
	if len(bytes.TrimSpace(base)) > 0 {
		if b, err = row.Decode(base); err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "merge: base is not a row document", err)
		}
	}
	o, err := row.Decode(ours)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "merge: ours is not a row document", err)
	}
	t, err := row.Decode(theirs)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "merge: theirs is not a row document", err)
	}
	p := UnknownTable()
	if d.Registry != nil {
		if def, ok := d.Registry.Table(o.Table); ok {
			p = PolicyFor(def)
		}
	}
	res, err := Rows(p, b, o, t)
	if err != nil {
		return nil, err
	}
	side := o
	if preferTheirs {
		side = t
	}
	res.Row = Prefer(res, side)
	if d.Report != nil {
		d.Report(res)
	}
	return row.Encode(res.Row)
}

// Prefer is res.Row with each of res.Conflicts settled to side's value:
// a text field (or a whole entries field, "field.<entry>") takes side's
// field, a meta entry ("meta.<id>") side's entry, a meta that did not
// merge at all ("meta") side's whole meta. Everything else stays merged.
func Prefer(res Result, side row.Row) row.Row {
	out := res.Row
	out.Fields = maps.Clone(out.Fields)
	out.Meta = maps.Clone(out.Meta)
	for _, c := range res.Conflicts {
		switch {
		case c.Field == FieldMeta:
			out.Meta = maps.Clone(side.Meta)
		case strings.HasPrefix(c.Field, FieldMeta+"."):
			id := mustMetaID(strings.TrimPrefix(c.Field, FieldMeta+"."))
			if out.Meta == nil {
				out.Meta = map[ids.ID[ids.MetaKey]]value.JSON{}
			}
			if v, ok := side.Meta[id]; ok {
				out.Meta[id] = v
			} else {
				delete(out.Meta, id)
			}
		default:
			k, _, _ := strings.Cut(c.Field, ".")
			if out.Fields == nil {
				out.Fields = map[string]value.Value{}
			}
			if v, ok := side.Fields[k]; ok {
				out.Fields[k] = v
			} else {
				delete(out.Fields, k)
			}
		}
	}
	if len(out.Meta) == 0 {
		out.Meta = nil
	}
	return out
}
