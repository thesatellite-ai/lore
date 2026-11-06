package settings

// Last good definition of a resource kind (RES 3.13, OV35-LASTGOOD).
//
//	read op (show, list, export, report, lint, git history)
//	  └─ kindDefRead(key)
//	       ├─ kindDef(key)                    strict: refuses a conflicted kind
//	       └─ on E_KIND_CONFLICT (markers):   LastGoodKind(src, kind id)
//	            ├─ found  → that definition, read-only, d.note warns
//	            └─ absent → the strict error (never seen good: load nothing)
//
// Writes keep calling kindDef: a kind or field row holding conflict
// markers refuses every write of that kind until the row is resolved
// (STORE_SERVER_SPEC 11 rule 2; P2-RES-4).

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// lastGooder is the optional Env upgrade wire's engine implements
// (engine.LastGoodKinds, the cache's _kinds_good table).
type lastGooder interface {
	LastGoodKind(ctx context.Context, source store.Source, kindID string) (kind row.Row, fields []row.Row, ok bool, err error)
}

// refusedRower is the optional Env upgrade wire implements (wire
// RefusedRows): the rows of a table whose files the import refused because
// a field value holds conflict markers, read as they are on disk. A marked
// kind or field row never reaches the cache (STORE_SERVER_SPEC 11 rule 2),
// so without it a conflicted kind would vanish (E_KIND_UNKNOWN) instead of
// falling back to its last good definition (RES 3.13).
type refusedRower interface {
	RefusedRows(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

// withRefused adds src's refused-on-import rows of table to rows (its live
// rows), replacing a cached row of the same id: the file on disk is the
// truth, and its markers make markedRows name it.
func withRefused(ctx context.Context, env Env, src store.Source, table row.Table, rows []located) ([]located, error) {
	rr, ok := upgrade.As[refusedRower](env)
	if !ok {
		return rows, nil
	}
	refused, err := rr.RefusedRows(ctx, src, table)
	if err != nil || len(refused) == 0 {
		return rows, err
	}
	out := slices.Clone(rows)
	for _, r := range refused {
		if i := slices.IndexFunc(out, func(l located) bool { return l.Row.ID == r.ID }); i >= 0 {
			out[i] = located{Src: src, Row: r}
			continue
		}
		out = append(out, located{Src: src, Row: r})
	}
	return out, nil
}

// liveDefRows is liveRows for kinds and kind_fields: each store's live
// rows with its refused-on-import marked rows added (withRefused), so
// every reader of a kind definition sees a conflicted row and names it.
func liveDefRows(ctx context.Context, env Env, srcs []store.Source, table row.Table) ([]located, error) {
	var out []located
	for _, src := range srcs {
		got, err := liveRows(ctx, env, []store.Source{src}, table)
		if err != nil {
			return nil, err
		}
		if got, err = withRefused(ctx, env, src, table, got); err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// markedRows lists the ids of rows among k and fs whose values hold
// unresolved conflict markers.
func markedRows(k located, fs []located) []string {
	var out []string
	for _, l := range append([]located{k}, fs...) {
		for _, v := range l.Row.Fields {
			if v != nil && merge.HasMarkers(v.String()) {
				out = append(out, l.Row.ID.String())
				break
			}
		}
	}
	return out
}

// conflicted is the strict refusal of a kind with marked rows.
func conflicted(k located, marked []string) error {
	return errs.New(errs.KindConflict, "resource kind "+text(k.Row, kName)+": "+strings.Join(marked, ", ")+
		" holds conflict markers; writes of this kind are refused until it is resolved").
		WithHint("resolve the conflict in the row file, then run lore sync")
}

// kindDefRead is kindDef for read ops: a conflicted kind falls back to
// its last good definition, read-only, with a warning in d.note.
func (o ops) kindDefRead(ctx context.Context, key string) (kindDef, error) {
	d, err := o.kindDef(ctx, key)
	if err == nil || !errs.Has(err, errs.KindConflict) {
		return d, err
	}
	k, kerr := o.kindByName(ctx, key)
	if kerr != nil {
		return kindDef{}, err
	}
	lg, isLG := upgrade.As[lastGooder](o.env)
	if !isLG {
		return kindDef{}, err
	}
	kr, frs, ok, lerr := lg.LastGoodKind(ctx, k.Src, k.Row.ID.String())
	if lerr != nil {
		return kindDef{}, errors.Join(err, lerr)
	}
	if !ok {
		return kindDef{}, err // 3.13: never seen good here → load nothing
	}
	raw := make([]located, 0, len(frs))
	for _, f := range frs {
		raw = append(raw, located{Src: k.Src, Row: f})
	}
	fs, ferr := effectiveFields(raw)
	if ferr != nil {
		return kindDef{}, errors.Join(err, ferr)
	}
	good := kindDef{kind: located{Src: k.Src, Row: kr}, byName: map[string]located{}, byID: map[string]located{}}
	for _, f := range fs {
		good.byName[text(f.Row, kName)] = f
		good.byID[f.Row.ID.String()] = f
	}
	var msg string
	var e *errs.Error
	if errors.As(err, &e) {
		msg = e.Message
	}
	good.note = "warning: " + msg + "; showing its last good definition (read-only)"
	return good, nil
}

// fieldList is d's fields as rows, sorted by name.
func (d kindDef) fieldList() []located {
	out := make([]located, 0, len(d.byName))
	for _, n := range d.fieldNames() {
		out = append(out, d.byName[n])
	}
	return out
}

// noteLines is d's warning as list notes (none when the kind is clean).
func (d kindDef) noteLines() []string {
	if d.note == "" {
		return nil
	}
	return slices.Clone([]string{d.note})
}
