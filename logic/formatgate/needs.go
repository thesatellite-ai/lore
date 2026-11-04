package formatgate

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/row"
)

// Table names the first-use rule looks at. They are file format (the
// schema package declares the same strings: schema.TablePolicies,
// schema.TablePlans, schema.KindTablePrefix; schema_test pins them equal,
// because logic may not import schema at the same layer rank).
const (
	TablePolicies   = "policies"
	TablePlans      = "plans"
	KindTablePrefix = "res_"
	// FieldDeletedAt marks a row in the trash (logic/trash.FieldDeletedAt;
	// same layer rank, so the string is repeated, not imported).
	FieldDeletedAt = "deleted_at"
)

// Needs lists the features a row written by this binary uses, sorted
// (FORMAT_EVOLUTION rule 2, "How features is set the first time"): hlc for
// every row (the _v: 2 format), meta for a row with metadata, trash for a
// tombstone, policy for a policies row, resources for a kind row, and
// plan_status for a plans row (its status is the closed TSQ9 set).
func Needs(r row.Row) []string {
	out := []string{HLC}
	if len(r.Meta) > 0 {
		out = append(out, Meta)
	}
	if _, trashed := r.Fields[FieldDeletedAt]; r.Deleted || trashed {
		out = append(out, Trash) // a tombstone, or a row in the trash (DELETION 3.1)
	}
	switch t := r.Table.String(); {
	case t == TablePolicies:
		out = append(out, Policy)
	case t == TablePlans:
		out = append(out, PlanStatus)
	case strings.HasPrefix(t, KindTablePrefix):
		out = append(out, Resources)
	}
	slices.Sort(out)
	return out
}

// NeedsAll is the sorted union of Needs over rows.
func NeedsAll(rows []row.Row) []string {
	var out []string
	for _, r := range rows {
		for _, f := range Needs(r) {
			if !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	slices.Sort(out)
	return out
}
