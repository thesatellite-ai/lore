package merge

import (
	"bytes"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
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
