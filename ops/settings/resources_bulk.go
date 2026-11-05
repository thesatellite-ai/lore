package settings

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/schema"
)

// Bulk edit and check (RESOURCES 3.20, XQ1/XQ2/XQ7):
//
//	resource edit  --kind K --where F --set|--unset|--archive [--dry-run]
//	resource check --kind K --where F [--dry-run]
//	  └─ matchWhere      the one --where grammar (logic/filter), live rows of K
//	  └─ --dry-run       per-row preview, nothing written
//	  └─ bulk.Preview    asks above batch.preview_rows (XQ2)
//	  └─ bulk.Begin      one bat_ id (kind resource) on every row's event (XQ1)
//	  └─ per row         the single-row path (resEdit / archive / check stamp),
//	                     so validation, refs and last_checked are identical

// BulkWhere are the flags of a bulk form shared by edit and check.
type BulkWhere struct {
	Kind   string   `json:"kind,omitempty" flag:"kind" desc:"bulk: the kind of the rows --where matches"`
	Where  []string `json:"where,omitempty" flag:"where" desc:"bulk: filter on a field, as resource list --where (repeatable; all must match)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"bulk: list what would change; write nothing"`
}

// bulk reports whether the bulk form was asked for.
func (b BulkWhere) bulk() bool { return b.Kind != "" || len(b.Where) > 0 || b.DryRun }

// ResCheckIn is the input of resource.check: one resource, or the bulk
// form --kind --where.
type ResCheckIn struct {
	Name string `json:"id,omitempty" arg:"0" desc:"the resource (name or res_ id); leave out for the bulk form --where"`
	BulkWhere
}

// matchWhere is every live, unarchived resource of b.Kind matching b.Where,
// sorted by name (archived rows are left alone by a bulk edit: archive or
// check them by name).
func (o ops) matchWhere(ctx context.Context, b BulkWhere, verb string) (kindDef, []located, error) {
	if b.Kind == "" || len(b.Where) == 0 {
		return kindDef{}, nil, errs.New(errs.InvalidInput, "resource "+verb+": the bulk form needs both --kind and --where").
			WithHint("lore resource " + verb + " --kind <kind> --where <field>=<value> …")
	}
	d, err := o.kindDef(ctx, b.Kind)
	if err != nil {
		return kindDef{}, nil, err
	}
	m, err := resourceMatcher(d, b.Where)
	if err != nil {
		return kindDef{}, nil, err
	}
	rows, err := liveRows(ctx, o.env, o.resStores(), schema.TableResources)
	if err != nil {
		return kindDef{}, nil, err
	}
	var hits []located
	for _, r := range where(rows, map[string]string{rKind: d.kind.Row.ID.String()}) {
		if text(r.Row, rArchivedAt) != "" {
			continue
		}
		stored, derr := decodeValues(r)
		if derr != nil {
			return kindDef{}, nil, derr
		}
		if m.Match(o.resourceGetter(ctx, d, stored)) {
			hits = append(hits, r)
		}
	}
	slices.SortFunc(hits, func(a, b located) int { return strings.Compare(text(a.Row, rName), text(b.Row, rName)) })
	return d, hits, nil
}

// bulkRun previews, asks and runs one(r) for every hit under one batch.
// change is the per-row preview text ("" for the check stamp).
func (o ops) bulkRun(ctx context.Context, d kindDef, hits []located, dry bool, what string, change func(located) string, one func(context.Context, located) error) (RowOut, error) {
	out := RowOut{DryRun: dry, IDs: make([]string, 0, len(hits))}
	items := make([]bulk.Item, 0, len(hits))
	var b strings.Builder
	for _, r := range hits {
		out.IDs = append(out.IDs, r.Row.ID.String())
		items = append(items, bulk.Item{ID: r.Row.ID.String(), Label: text(r.Row, rName)})
		fmt.Fprintf(&b, "  %s:%s/%s  %s\n", scopeOf(r.Src), d.name(), text(r.Row, rName), change(r))
	}
	if len(hits) == 0 {
		out.Human = "no " + d.name() + " resources match; nothing changed\n"
		return out, nil
	}
	if dry {
		out.Human = "would " + what + " " + strconv.Itoa(len(hits)) + " " + d.name() + " resources (dry run, nothing written):\n" + b.String()
		return out, nil
	}
	if err := bulk.Preview(ctx, o.env, hits[0].Src, "resource "+what, items); err != nil {
		return RowOut{}, err
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindResource)
	if err != nil {
		return RowOut{}, err
	}
	out.Batch = bid.String()
	for i, r := range hits {
		if err = one(bctx, r); err != nil {
			return RowOut{}, errors.Join(err, errs.New(errs.ResourceInvalid, "resource "+what+": stopped after "+strconv.Itoa(i)+
				" of "+strconv.Itoa(len(hits))+" rows; the rows written are in batch "+out.Batch+" (undo: lore batch undo "+out.Batch+")"))
		}
	}
	out.Human = fmt.Sprintf("%s %d %s resources; batch %s (undo: lore batch undo %s)\n", what, len(hits), d.name(), out.Batch, out.Batch)
	return out, nil
}

// resEditWhere is resource edit --kind --where (RESOURCES 3.20): --set and
// --unset go through resEdit row by row, --archive stamps archived_at.
func (o ops) resEditWhere(ctx context.Context, in ResEditIn) (RowOut, error) {
	sets, err := valueSets(in.Set, in.SetJSON, in.SetStdin)
	if err != nil {
		return RowOut{}, err
	}
	// The engine binds --meta to ONE write (engine.metaFor), so a bulk form
	// would tag only its first row: refused rather than half applied.
	if !in.Empty() {
		return RowOut{}, errs.New(errs.InvalidInput, "resource edit: --meta is not taken by the bulk form --where; nothing written").
			WithHint("edit metadata row by row: lore resource edit <name> --meta key=value")
	}
	if in.Archive && (len(sets) > 0 || len(in.Unset) > 0) {
		return RowOut{}, errs.New(errs.InvalidInput, "resource edit: --archive takes no --set or --unset; run them apart")
	}
	if !in.Archive && len(sets) == 0 && len(in.Unset) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "resource edit: nothing to change").WithHint("give --set, --unset or --archive")
	}
	d, hits, err := o.matchWhere(ctx, in.BulkWhere, "edit")
	if err != nil {
		return RowOut{}, err
	}
	if in.Archive {
		return o.bulkRun(ctx, d, hits, in.DryRun, "archive", func(located) string { return "archive" },
			func(c context.Context, r located) error {
				_, perr := o.patchRow(c, r, map[string]value.Value{rArchivedAt: value.DateTimeFrom(opclock.Now(o.env))}, "archived "+text(r.Row, rName))
				return perr
			})
	}
	change := func(r located) string {
		stored, derr := decodeValues(r)
		if derr != nil {
			return "(unreadable fields)"
		}
		before := o.named(ctx, d, stored.vals, false)
		var parts []string
		for _, s := range sets {
			f, v, _ := strings.Cut(s, setSep)
			if fd, ok := d.byName[f]; ok && flag(fd.Row, fSensitive) {
				v = maskedValue
			}
			parts = append(parts, f+" "+orUnset(before[f])+" → "+v)
		}
		for _, f := range in.Unset {
			parts = append(parts, f+" "+orUnset(before[f])+" → (unset)")
		}
		return strings.Join(parts, "; ")
	}
	return o.bulkRun(ctx, d, hits, in.DryRun, "edit", change, func(c context.Context, r located) error {
		one := in
		one.Name, one.BulkWhere = r.Row.ID.String(), BulkWhere{}
		// the stdin value was read once above; every row gets the same pairs
		one.Set, one.SetJSON, one.SetStdin = sets, nil, ""
		_, eerr := o.resEdit(c, one)
		return eerr
	})
}

// orUnset is s, or "(unset)" when empty.
func orUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

// resCheckWhere is resource check --kind --where: the manual stamp on every
// matching row, one batch (RESOURCES 3.20, RQ8).
func (o ops) resCheckWhere(ctx context.Context, in ResCheckIn) (RowOut, error) {
	d, hits, err := o.matchWhere(ctx, in.BulkWhere, "check")
	if err != nil {
		return RowOut{}, err
	}
	by, err := txt(CheckedManual)
	if err != nil {
		return RowOut{}, err
	}
	return o.bulkRun(ctx, d, hits, in.DryRun, "check", func(located) string { return "last_checked now (manual)" },
		func(c context.Context, r located) error {
			_, perr := o.patchRow(c, r, map[string]value.Value{rLastChecked: value.DateTimeFrom(opclock.Now(o.env)), rCheckedBy: by}, "checked "+text(r.Row, rName)+" now")
			return perr
		})
}
