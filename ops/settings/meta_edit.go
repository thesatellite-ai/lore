package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// meta_keys fields of the 3.19 everyday features and the 3.12 fold.
const (
	mLabel      = "label"
	mRequiredOn = "required_on"
	mPattern    = "pattern"
	mMin        = "min"
	mMax        = "max"
	mDefault    = "default"
	mDefaultOn  = "default_on"
	mDeprecated = "deprecated"
	mReplacedBy = "replaced_by"
	mMergedInto = "merged_into"
	// clearWord clears a text or list field of `meta edit` (`--pattern none`).
	clearWord = "none"
	// pairSep splits old=new and name=label flag values.
	pairSep = "="
)

// metaEdit is `lore meta edit <key>` (METADATA 3.8, 3.19). Flow:
//
//	resolve the key ─► --merge-into? fold (its own path, one batch)
//	   ─► flags (strict/internal/sensitive) + 3.19 fields, checked against the type
//	   ─► allowed values: --values, --value-label, --rename-value, --remove-value (E_META_IN_USE)
//	   ─► a loosening (--no-internal/--no-sensitive/--no-strict on a set flag)?
//	      --no-internal asks (3.11); each gets a changed_by entry and a
//	      Lore-Meta trailer on the commit (RX7, OV58)
//	   ─► one patch of the key row
func (o ops) metaEdit(ctx context.Context, in MetaEditIn) (RowOut, error) {
	k, err := o.metaKey(ctx, in.Key)
	if err != nil {
		return RowOut{}, err
	}
	if in.MergeInto != "" {
		return o.metaMergeInto(ctx, k, in.MergeInto)
	}
	name := text(k.Row, mName)
	patch := map[string]value.Value{}
	for _, ts := range []struct {
		f       string
		on, off bool
	}{{mStrict, in.Strict, in.NoStrict}, {mInternal, in.Internal, in.NoInternal}, {mSensitive, in.Sensitive, in.NoSensitive},
		{mDeprecated, in.Deprecated, in.NoDeprecated}} {
		if err = triState(patch, ts.f, ts.on, ts.off); err != nil {
			return RowOut{}, err
		}
	}
	if err = o.everydayPatch(k, in, patch); err != nil {
		return RowOut{}, err
	}
	var notes []string
	vnotes, err := o.editValues(ctx, k, in)
	if err != nil {
		return RowOut{}, err
	}
	notes = append(notes, vnotes...)
	if len(patch) == 0 && len(notes) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "meta edit: nothing to change").
			WithHint("give --strict, --internal, --sensitive (or --no-…), --values, --value-label, --rename-value, --remove-value, --merge-into, --label, --required-on, --pattern, --min, --max, --default, --default-on, --deprecated or --replaced-by")
	}
	human := strings.Join(notes, "")
	if len(patch) > 0 {
		if ctx, err = o.loosen(ctx, k, patch); err != nil {
			return RowOut{}, err
		}
		out, perr := o.patchRow(ctx, k, patch, "edited "+name)
		if perr != nil {
			return RowOut{}, perr
		}
		out.Human = human + out.Human
		return out, nil
	}
	return RowOut{ID: k.Row.ID.String(), Store: string(k.Src), Human: human}, nil
}

// Loosening record (RX7): changed_by entry fields and the trailer.
const (
	mChangedBy     = "changed_by"
	loreMetaPrefix = "Lore-Meta: no-"
)

// changedByEntry is one changed_by item; id is the list-by-id merge key.
type changedByEntry struct {
	ID    string `json:"id"`
	Field string `json:"field"`
	To    bool   `json:"to"`
	By    string `json:"by,omitempty"`
	At    string `json:"at"`
}

// loosen records every protection patch turns off on key k (RX7): a flag
// k has set (internal, sensitive, strict) patched to false gets a
// changed_by entry, and ctx carries a "Lore-Meta: no-<field> <key id>"
// trailer for the commit of that write, so the pre-commit and landing
// checks can tell lore's own loosening from a hand edit. Turning internal
// off asks first (3.11, E_NEEDS_CONFIRM without --yes); the ask counts
// trashed rows too.
func (o ops) loosen(ctx context.Context, k located, patch map[string]value.Value) (context.Context, error) {
	var fields []string
	for _, f := range []string{mInternal, mSensitive, mStrict} {
		b, ok := patch[f].(value.Bool)
		if ok && !b.Bool() && flag(k.Row, f) {
			fields = append(fields, f)
		}
	}
	if len(fields) == 0 {
		return ctx, nil
	}
	name := text(k.Row, mName)
	if slices.Contains(fields, mInternal) {
		rows, err := o.usedRows(ctx)
		if err != nil {
			return ctx, err
		}
		n := 0
		if u := usage(rows)[k.Row.ID.String()]; u != nil {
			n = u.rows
		}
		if err = bulk.Ask(ctx, fmt.Sprintf("%s (%s) values will travel with every row moved into a client repo. It is used on %d rows. Make it shareable?", name, short(k.Row.ID), n)); err != nil {
			return ctx, err
		}
	}
	var list []changedByEntry
	if raw, ok := k.Row.Fields[mChangedBy]; ok {
		if b, merr := raw.MarshalJSON(); merr == nil && string(b) != "null" {
			if uerr := json.Unmarshal(b, &list); uerr != nil {
				return ctx, errs.Wrap(errs.ConfigInvalid, "meta key "+k.Row.ID.String()+": changed_by is not a list of entries", uerr)
			}
		}
	}
	by := ""
	if a, isA := upgrade.As[actorer](o.env); isA {
		if id, aerr := a.Actor(ctx); aerr == nil && !id.IsZero() {
			by = id.String()
		}
	}
	at := opclock.Now(o.env).UTC().Format(time.RFC3339)
	trailers := make([]string, 0, len(fields))
	for _, f := range fields {
		id, err := ids.New[resHistTag]()
		if err != nil {
			return ctx, err
		}
		list = append(list, changedByEntry{ID: id.String(), Field: f, To: false, By: by, At: at})
		trailers = append(trailers, loreMetaPrefix+f+" "+k.Row.ID.String())
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return ctx, errs.Wrap(errs.Internal, "meta: encode changed_by", err)
	}
	if patch[mChangedBy], err = value.JSONOf(raw); err != nil {
		return ctx, errs.Wrap(errs.Internal, "meta: changed_by", err)
	}
	return store.WithCommitTrailers(ctx, trailers...), nil
}

// keyType is the registered type of key row k.
func keyType(k located) (meta.Type, error) {
	t, err := meta.ParseType(text(k.Row, mType))
	if err != nil {
		return meta.Type{}, errs.Wrap(errs.ConfigInvalid, "meta key "+k.Row.ID.String()+" has an unknown type", err)
	}
	return t, nil
}

// everydayPatch adds the 3.19 fields of in to patch, each checked: tables
// must take metadata, a pattern compiles, bounds parse as the key's type
// (and min <= max with the stored other bound), a default coerces to it.
func (o ops) everydayPatch(k located, in MetaEditIn, patch map[string]value.Value) error {
	typ, err := keyType(k)
	if err != nil {
		return err
	}
	for f, v := range map[string]string{mLabel: in.Label, mPattern: in.Pattern, mMin: in.Min, mMax: in.Max, mDefault: in.Default, mReplacedBy: in.ReplacedBy} {
		switch v {
		case "":
		case clearWord:
			patch[f] = value.Null{}
		default:
			tv, terr := txt(v)
			if terr != nil {
				return terr
			}
			patch[f] = tv
		}
	}
	for f, v := range map[string]string{mRequiredOn: in.RequiredOn, mDefaultOn: in.DefaultOn} {
		switch v {
		case "":
		case clearWord:
			patch[f] = value.Null{}
		default:
			tables := splitList(v)
			for _, t := range tables {
				if !engine.AcceptsMeta(row.Table(t)) {
					return errs.New(errs.InvalidInput, "--"+strings.ReplaceAll(f, "_", "-")+": "+quote(t)+" is not a table that takes metadata")
				}
			}
			j, jerr := jsonList(tables)
			if jerr != nil {
				return jerr
			}
			patch[f] = j
		}
	}
	// the rules as they will stand, checked against the type
	pick := func(f string) string {
		if v, ok := patch[f]; ok {
			if _, isNull := v.(value.Null); isNull {
				return ""
			}
			return v.String()
		}
		return text(k.Row, f)
	}
	rules := meta.Rules{Pattern: pick(mPattern), Min: pick(mMin), Max: pick(mMax)}
	if err := meta.CheckRuleDefs(typ, rules); err != nil {
		return err
	}
	if d := pick(mDefault); d != "" && in.Default != "" {
		if _, cerr := meta.Coerce(typ, d); cerr != nil {
			return cerr
		}
	}
	if in.ReplacedBy != "" && in.ReplacedBy != clearWord {
		if err := checkKeyName(in.ReplacedBy); err != nil {
			return err
		}
	}
	return nil
}

// jsonList is a JSON list of text values for a JSON field.
func jsonList(xs []string) (value.Value, error) {
	items := make([][]byte, 0, len(xs))
	for _, x := range xs {
		items = append(items, canon.String(x))
	}
	raw, err := canon.Compact([]byte("[" + string(joinJSON(items)) + "]"))
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "meta: build a list", err)
	}
	return value.JSONOf(raw)
}

func joinJSON(items [][]byte) []byte {
	var out []byte
	for i, it := range items {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, it...)
	}
	return out
}

// editValues applies the allowed-value flags (3.8 OQ-AV) and returns one
// note line per change. Values are their own rows, so no data row changes.
func (o ops) editValues(ctx context.Context, k located, in MetaEditIn) ([]string, error) {
	name := text(k.Row, mName)
	var notes []string
	if in.Values != "" {
		want := splitList(in.Values)
		if err := checkMetaValues(want); err != nil {
			return nil, errs.New(errs.MetaKey, err.Error()).WithHint("allowed values are snake_case names; give display text with --value-label name=Text")
		}
		have, err := o.metaValues(ctx, k)
		if err != nil {
			return nil, err
		}
		for _, v := range have {
			if !slices.Contains(want, text(v.Row, mName)) {
				if err := o.valueNotUsed(ctx, k, v); err != nil {
					return nil, err
				}
				if _, err := o.env.Delete(ctx, engine.Ref{Store: v.Src, Table: schema.TableMetaValues, ID: v.Row.ID}); err != nil {
					return nil, err
				}
			}
		}
		for _, v := range want {
			if !slices.Contains(valueNames(have), v) {
				if _, err := o.addValue(ctx, k, v, ""); err != nil {
					return nil, err
				}
			}
		}
		notes = append(notes, name+": allowed values "+strings.Join(want, ", ")+"\n")
	}
	if in.RenameValue != "" {
		old, nw, ok := strings.Cut(in.RenameValue, pairSep)
		if !ok {
			return nil, errs.New(errs.InvalidInput, "--rename-value takes old=new")
		}
		out, err := o.metaValueRename(ctx, MetaValueRenameIn{Key: k.Row.ID.String(), Old: old, New: nw})
		if err != nil {
			return nil, err
		}
		notes = append(notes, out.Human)
	}
	for _, pair := range in.ValueLabel {
		vn, label, ok := strings.Cut(pair, pairSep)
		if !ok {
			return nil, errs.New(errs.InvalidInput, "--value-label takes name=label")
		}
		note, err := o.labelValue(ctx, k, vn, label)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	if in.RemoveValue != "" {
		v, err := o.valueByName(ctx, k, in.RemoveValue)
		if err != nil {
			return nil, err
		}
		if err := o.valueNotUsed(ctx, k, v); err != nil {
			return nil, err
		}
		if _, err := o.env.Delete(ctx, engine.Ref{Store: v.Src, Table: schema.TableMetaValues, ID: v.Row.ID}); err != nil {
			return nil, err
		}
		notes = append(notes, fmt.Sprintf("%s: removed value %s (%s)\n", name, in.RemoveValue, short(v.Row.ID)))
	}
	return notes, nil
}

// valueByName finds key k's allowed value named n.
func (o ops) valueByName(ctx context.Context, k located, n string) (located, error) {
	have, err := o.metaValues(ctx, k)
	if err != nil {
		return located{}, err
	}
	for _, v := range have {
		if text(v.Row, mName) == n {
			return v, nil
		}
	}
	return located{}, errs.New(errs.NotFound, text(k.Row, mName)+" has no value "+quote(n)+" (values: "+strings.Join(valueNames(have), ", ")+")")
}

// labelValue sets one value's display label; a label may not equal another
// value's name, so input by name or label is never ambiguous (OQ-AV).
func (o ops) labelValue(ctx context.Context, k located, vn, label string) (string, error) {
	v, err := o.valueByName(ctx, k, vn)
	if err != nil {
		return "", err
	}
	have, err := o.metaValues(ctx, k)
	if err != nil {
		return "", err
	}
	for _, other := range have {
		if other.Row.ID != v.Row.ID && text(other.Row, mName) == label {
			return "", errs.New(errs.MetaTaken, "label "+quote(label)+" is the name of another value of "+text(k.Row, mName))
		}
	}
	lv, err := txt(label)
	if err != nil {
		return "", err
	}
	if _, err := o.env.Update(ctx, engine.Ref{Store: v.Src, Table: schema.TableMetaValues, ID: v.Row.ID}, map[string]value.Value{mvLabel: lv}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: value %s (%s) labelled %q\n", text(k.Row, mName), vn, short(v.Row.ID), label), nil
}

// valueNotUsed refuses removing allowed value v of key k while a row
// stores it (E_META_IN_USE, 3.8).
func (o ops) valueNotUsed(ctx context.Context, k located, v located) error {
	rows, err := o.usedRows(ctx) // OV58: trashed rows count as uses
	if err != nil {
		return err
	}
	n := 0
	for _, r := range rows {
		for kid, en := range r.entries {
			if kid.String() == k.Row.ID.String() && slices.Contains(meta.Strings(en.V), v.Row.ID.String()) {
				n++
			}
		}
	}
	if n == 0 {
		return nil
	}
	key, vn := text(k.Row, mName), text(v.Row, mName)
	return errs.New(errs.MetaInUse, fmt.Sprintf("%s (%s) is used on %d rows", vn, short(v.Row.ID), n)).
		WithHint(fmt.Sprintf("to change them: lore meta set --where '%s=%s' %s=<value> --dry-run", key, vn, key))
}

// metaMergeInto folds key k into survivor (3.8 Fold, OQ3), the only way two
// keys become one. Flow:
//
//	survivor resolved; neither retired (E_META_RETIRED), same type (E_META_TYPE)
//	─► a row with different values under both keys refuses (E_META_CONFLICT)
//	─► rows using k counted; ask first (MayAsk; E_NEEDS_CONFIRM without --yes)
//	─► survivor takes the flags of both and the union of allowed values by name
//	─► every row's k entry rewritten to the survivor in ONE meta batch (bat_)
//	─► k's row keeps merged_into: <survivor id> (retired; names skip it)
func (o ops) metaMergeInto(ctx context.Context, k located, into string) (RowOut, error) {
	sv, err := o.metaKey(ctx, into)
	if err != nil {
		return RowOut{}, err
	}
	name, sname := text(k.Row, mName), text(sv.Row, mName)
	switch {
	case sv.Row.ID == k.Row.ID:
		return RowOut{}, errs.New(errs.InvalidInput, "meta edit: a key cannot be folded into itself")
	case text(sv.Row, mMergedInto) != "":
		return RowOut{}, errs.New(errs.MetaRetired, sname+" is retired (merged into "+text(sv.Row, mMergedInto)+")").
			WithHint("fold into the survivor: lore meta edit " + name + " --merge-into " + text(sv.Row, mMergedInto))
	case text(k.Row, mMergedInto) != "":
		return RowOut{}, errs.New(errs.MetaRetired, name+" is already merged into "+text(k.Row, mMergedInto))
	case text(sv.Row, mType) != text(k.Row, mType):
		return RowOut{}, errs.New(errs.MetaType, fmt.Sprintf("%s is %s, %s is %s; a fold needs one type", name, text(k.Row, mType), sname, text(sv.Row, mType))).
			WithHint("lore meta edit " + name + " --type " + text(sv.Row, mType))
	}
	rows, err := o.metaRows(ctx)
	if err != nil {
		return RowOut{}, err
	}
	kid, err := ids.Parse[ids.MetaKey](k.Row.ID.String())
	if err != nil {
		return RowOut{}, errs.Wrap(errs.ConfigInvalid, "meta key id", err)
	}
	sid, err := ids.Parse[ids.MetaKey](sv.Row.ID.String())
	if err != nil {
		return RowOut{}, errs.Wrap(errs.ConfigInvalid, "meta key id", err)
	}
	var hits []metaRow
	var clash []string
	for _, r := range rows {
		en, ok := r.entries[kid]
		if !ok {
			continue
		}
		hits = append(hits, r)
		// a row with values under both keys: the fold would overwrite one
		// (safest: refuse until a person settles each row)
		if have, both := r.entries[sid]; both && !slices.Equal(meta.Names(have), meta.Names(en)) {
			clash = append(clash, r.Row.ID.String())
		}
	}
	if len(clash) > 0 {
		return RowOut{}, errs.New(errs.MetaConflict, fmt.Sprintf("%d rows hold different values under both %s and %s: %s", len(clash), name, sname, strings.Join(clash, ", "))).
			WithHint("settle each row first: lore meta unset <id> " + name + " (or set " + sname + " to the value to keep), then fold")
	}
	if err = bulk.Ask(ctx, fmt.Sprintf("fold %s (%s) into %s (%s)? %d rows are rewritten", name, short(k.Row.ID), sname, short(sv.Row.ID), len(hits))); err != nil {
		return RowOut{}, err
	}
	// survivor flags: protective if either side was (3.8)
	patch := map[string]value.Value{}
	for _, f := range []string{mInternal, mSensitive, mStrict} {
		if flag(k.Row, f) && !flag(sv.Row, f) {
			patch[f] = value.BoolOf(true)
		}
	}
	if text(sv.Row, mDescription) == "" && text(k.Row, mDescription) != "" {
		patch[mDescription] = k.Row.Fields[mDescription]
	}
	if len(patch) > 0 {
		if _, err = o.env.Update(ctx, engine.Ref{Store: sv.Src, Table: schema.TableMetaKeys, ID: sv.Row.ID}, patch); err != nil {
			return RowOut{}, err
		}
	}
	oldVals, err := o.metaValues(ctx, k)
	if err != nil {
		return RowOut{}, err
	}
	svVals, err := o.metaValues(ctx, sv)
	if err != nil {
		return RowOut{}, err
	}
	for _, v := range oldVals {
		if !slices.Contains(valueNames(svVals), text(v.Row, mName)) {
			if _, err = o.addValue(ctx, sv, text(v.Row, mName), text(v.Row, mvLabel)); err != nil {
				return RowOut{}, err
			}
		}
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindMeta)
	if err != nil {
		return RowOut{}, err
	}
	for i, r := range hits {
		en := r.entries[kid]
		raw := en.V
		if len(en.VName) > 0 {
			raw = en.VName // allowed values move by name; the survivor maps them to its val_ ids
		}
		mw := engine.MetaWrite{ID: r.Row.ID, Unset: []string{kid.String()}}
		if _, both := r.entries[sid]; !both {
			mw.JSON = []meta.Term{{KeyID: sid, Value: string(raw)}}
		}
		if _, err = o.env.Update(engine.WithMeta(bctx, mw), engine.Ref{ID: r.Row.ID}, map[string]value.Value{}); err != nil {
			return RowOut{}, errs.Wrap(errs.CodeOf(err), "meta edit --merge-into: stopped after "+strconv.Itoa(i)+" of "+strconv.Itoa(len(hits))+
				" rows (batch "+bid.String()+"; see: lore batch show "+bid.String()+")", err)
		}
	}
	mi, err := txt(sv.Row.ID.String())
	if err != nil {
		return RowOut{}, err
	}
	if _, err := o.env.Update(bctx, engine.Ref{Store: k.Src, Table: schema.TableMetaKeys, ID: k.Row.ID}, map[string]value.Value{mMergedInto: mi}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: sv.Row.ID.String(), Store: string(sv.Src), Human: fmt.Sprintf("folded %s (%s) into %s (%s): %d rows rewritten in batch %s (see: lore batch show %s)\n",
		name, short(k.Row.ID), sname, short(sv.Row.ID), len(hits), bid, bid)}, nil
}
