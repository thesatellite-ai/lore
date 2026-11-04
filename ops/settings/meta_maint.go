package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
)

// Registry maintenance ops (METADATA 3.8, 3.19).
const (
	OpMetaForget = "meta.forget"
	OpMetaLint   = "meta.lint"
	OpMetaShow   = "meta.show"
)

// resourceHintValues is meta.resource_hint_values' default (MQ8, D39): past
// this many distinct values lint suggests a resource kind. The policy key
// is not registered yet, so the default is used everywhere.
const resourceHintValues = 30

// Purger is the optional Env upgrade forget needs: purge a trashed row (and
// its trashed dependents) for good, leaving tombstones (wire.App has it).
type Purger interface {
	PurgeTrash(ctx context.Context, target string) (engine.TrashResult, error)
}

// MetaForgetIn is the input of meta.forget.
type MetaForgetIn struct {
	Keys    string `json:"keys" arg:"0" rest:"true" desc:"the unused keys to remove (names or mky_ ids, space separated)"`
	Confirm bool   `json:"confirm,omitempty" flag:"confirm" desc:"required: forget removes the key rows for good (DQ2)"`
}

// MetaLintIn is the input of meta.lint.
type MetaLintIn struct {
	Fix bool `json:"fix,omitempty" flag:"fix" desc:"repair what lint can (a scope label that disagrees with its folder)"`
}

// MetaFinding is one lint finding.
type MetaFinding struct {
	Key     string `json:"key"`
	Row     string `json:"row,omitempty"`
	Problem string `json:"problem"`
	Fix     string `json:"fix,omitempty"`
	Fixed   bool   `json:"fixed,omitempty"`
}

// MetaLintOut is the output of meta.lint.
type MetaLintOut struct {
	Count int           `json:"count"`
	Data  []MetaFinding `json:"data"`
}

// Text implements op.Texter (the `meta lint` sample of 3.8).
func (o MetaLintOut) Text() string {
	if len(o.Data) == 0 {
		return "no metadata problems\n"
	}
	var b strings.Builder
	for _, f := range o.Data {
		line := f.Key + "\t"
		if f.Row != "" {
			line += f.Row + ": "
		}
		line += f.Problem
		switch {
		case f.Fixed:
			line += "   fixed"
		case f.Fix != "":
			line += "   fix: " + f.Fix
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (o ops) metaMaintOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpMetaForget, Summary: "Remove unused metadata keys for good (tombstones kept)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.MetaInUse), RESTNoun: metaREST}, o.metaForget),
		op.Define(op.Spec{Name: OpMetaLint, Summary: "List metadata that does not fit its key, and registry problems", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{When: "fix"}, RESTNoun: metaREST}, o.metaLint),
		op.Define(op.Spec{Name: OpMetaShow, Summary: "Show a row's metadata", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.metaGet),
	}
}

// metaForget is `lore meta forget <key>… --confirm` (3.8 Forget, DQ2, DQ4).
// Flow: resolve every key ─► any row (archived included) uses it?
// E_META_IN_USE with the bulk-unset command ─► no --confirm? E_NEEDS_CONFIRM
// ─► trash the key row (its values cascade) ─► purge it: tombstones stay,
// so an old branch can never bring the key back.
func (o ops) metaForget(ctx context.Context, in MetaForgetIn) (RowOut, error) {
	names := strings.Fields(in.Keys)
	if len(names) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "meta forget: name a key").WithHint("lore meta keys --unused lists them")
	}
	rows, err := o.metaRows(ctx)
	if err != nil {
		return RowOut{}, err
	}
	use := usage(rows)
	var keys []located
	for _, n := range names {
		k, kerr := o.metaKey(ctx, n)
		if kerr != nil {
			return RowOut{}, kerr
		}
		if u := use[k.Row.ID.String()]; u != nil {
			key := text(k.Row, mName)
			return RowOut{}, errs.New(errs.MetaInUse, fmt.Sprintf("%s is used on %d rows (%s)", key, u.rows, u.tableCounts())).
				WithHint(fmt.Sprintf("to stop using it: lore meta unset --where '%s?' %s --dry-run; or lore meta delete %s", key, key, key))
		}
		keys = append(keys, k)
	}
	if !in.Confirm {
		return RowOut{}, errs.New(errs.NeedsConfirm, "meta forget removes the key rows of "+strings.Join(names, ", ")+" for good (tombstones kept)").
			WithHint("lore meta forget " + strings.Join(names, " ") + " --confirm")
	}
	pg, ok := upgrade.As[Purger](o.env)
	if !ok {
		return RowOut{}, errs.New(errs.Internal, "meta forget: this surface cannot purge rows")
	}
	var done []string
	for _, k := range keys {
		if _, err := o.env.Delete(ctx, engine.Ref{Store: k.Src, Table: schema.TableMetaKeys, ID: k.Row.ID}); err != nil {
			return RowOut{}, err
		}
		if _, err := pg.PurgeTrash(ctx, k.Row.ID.String()); err != nil {
			return RowOut{}, err
		}
		done = append(done, fmt.Sprintf("%s (%s)", text(k.Row, mName), short(k.Row.ID)))
	}
	return RowOut{ID: keys[0].Row.ID.String(), Store: string(keys[0].Src),
		Human: "removed the key rows of " + strings.Join(done, ", ") + " (tombstones kept)\n"}, nil
}

// metaLint lists what does not fit (3.8): values that do not parse as their
// key's type or are outside its allowed values or 3.19 rules, rows lacking a
// required key, ids no visible key defines, retired keys still in use, keys
// past the caps, keys that look like a resource (distinct values over the
// hint), and key rows whose scope label disagrees with their folder (--fix
// rewrites the label; the folder wins).
func (o ops) metaLint(ctx context.Context, in MetaLintIn) (MetaLintOut, error) {
	keyRows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableMetaKeys)
	if err != nil {
		return MetaLintOut{}, err
	}
	rows, err := o.metaRows(ctx)
	if err != nil {
		return MetaLintOut{}, err
	}
	use := usage(rows)
	out := MetaLintOut{Data: []MetaFinding{}}
	add := func(f MetaFinding) { out.Data = append(out.Data, f) }
	byID := map[string]located{}
	valueIDs := map[string][]string{}
	for _, k := range keyRows {
		id, name := k.Row.ID.String(), text(k.Row, mName)
		byID[id] = k
		vs, verr := o.metaValues(ctx, k)
		if verr != nil {
			return MetaLintOut{}, verr
		}
		for _, v := range vs {
			valueIDs[id] = append(valueIDs[id], v.Row.ID.String())
		}
		if want := scopeOf(k.Src); text(k.Row, mScope) != want {
			f := MetaFinding{Key: name, Problem: "scope label " + quote(text(k.Row, mScope)) + " disagrees with its folder (" + want + ")", Fix: "lore meta lint --fix"}
			if in.Fix {
				sv, terr := txt(want)
				if terr != nil {
					return MetaLintOut{}, terr
				}
				if _, uerr := o.env.Update(ctx, engine.Ref{Store: k.Src, Table: schema.TableMetaKeys, ID: k.Row.ID}, map[string]value.Value{mScope: sv}); uerr != nil {
					return MetaLintOut{}, uerr
				}
				f.Fixed = true
			}
			add(f)
		}
		if len(vs) > maxMetaValues {
			add(MetaFinding{Key: name, Problem: fmt.Sprintf("%d allowed values, over the cap of %d", len(vs), maxMetaValues)})
		}
		if utf8.RuneCountInString(text(k.Row, mDescription)) > maxMetaDescription {
			add(MetaFinding{Key: name, Problem: fmt.Sprintf("description over %d characters", maxMetaDescription), Fix: "lore meta describe " + name + " \"<shorter text>\""})
		}
		u := use[id]
		if u != nil && text(k.Row, mMergedInto) != "" {
			add(MetaFinding{Key: name, Problem: fmt.Sprintf("retired (merged into %s) but still on %d rows", text(k.Row, mMergedInto), u.rows),
				Fix: "lore meta edit " + name + " --merge-into " + text(k.Row, mMergedInto) + " --yes"})
		}
		if u != nil && len(vs) == 0 && len(u.values) > resourceHintValues {
			add(MetaFinding{Key: name, Problem: fmt.Sprintf("%d distinct values; when the values need facts of their own (an owner, a contact), consider a resource kind (meta.resource_hint_values: %d)", len(u.values), resourceHintValues)})
		}
	}
	for _, r := range rows {
		rid := r.Row.ID.String()
		for kid, en := range r.entries {
			k, ok := byID[kid.String()]
			if !ok {
				add(MetaFinding{Key: en.Name, Row: rid, Problem: "key " + kid.String() + " is not defined by any visible registry", Fix: "lore sync, lore meta rehome or lore meta rebuild"})
				continue
			}
			if p := o.valueProblem(k, en, valueIDs[kid.String()]); p != "" {
				add(MetaFinding{Key: text(k.Row, mName), Row: rid, Problem: p, Fix: "lore meta set " + rid + " " + text(k.Row, mName) + "=<value>"})
			}
		}
		var have []string
		for _, en := range r.entries {
			have = append(have, en.Name)
		}
		for _, k := range keyRows {
			name := text(k.Row, mName)
			if slices.Contains(jsonTexts(k.Row, mRequiredOn), string(r.Row.Table)) && !slices.Contains(have, name) && text(k.Row, mMergedInto) == "" {
				add(MetaFinding{Key: name, Row: rid, Problem: "required on " + string(r.Row.Table) + " but missing (written before the rule)", Fix: "lore meta set " + rid + " " + name + "=<value>"})
			}
		}
	}
	slices.SortStableFunc(out.Data, func(a, b MetaFinding) int { return strings.Compare(a.Key+a.Row, b.Key+b.Row) })
	out.Count = len(out.Data)
	return out, nil
}

// valueProblem is why entry en does not fit key row k now, or "": it was
// written before the key became strict, got allowed values or rules.
func (o ops) valueProblem(k located, en meta.Entry, vals []string) string {
	typ, err := keyType(k)
	if err != nil {
		return err.Error()
	}
	if len(vals) > 0 {
		for _, s := range meta.Strings(en.V) {
			if !slices.Contains(vals, s) {
				return fmt.Sprintf("%q is not one of its allowed values", s)
			}
		}
		return ""
	}
	if typ.Kind != value.KindJSON {
		if _, cerr := meta.CoerceJSON(typ, en.V); cerr != nil {
			return fmt.Sprintf("%s does not fit the key type %s", meta.Show(en), typ)
		}
	}
	why, err := meta.Violation(text(k.Row, mName), typ, en.V, meta.Rules{Pattern: text(k.Row, mPattern), Min: text(k.Row, mMin), Max: text(k.Row, mMax)})
	if err != nil {
		return err.Error()
	}
	return why
}

// jsonTexts reads a JSON list-of-text field (required_on, default_on).
func jsonTexts(r row.Row, f string) []string { return texts(r, f) }
