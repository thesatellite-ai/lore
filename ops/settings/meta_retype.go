package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
)

// mTypeHistory is the meta_keys field that keeps a key's earlier types
// (METADATA 3.3): [{type, until, by}], appended by `meta edit --type`.
const mTypeHistory = "type_history"

// retypeListMax is how many converted values a dry run lists without
// --all (METADATA 3.3 example: "31 values convert: "3" → 3, …").
const retypeListMax = 5

// metaTypeHistoryEntry is one element of meta_keys.type_history.
type metaTypeHistoryEntry struct {
	Type  string `json:"type"`
	Until string `json:"until"`
	By    string `json:"by,omitempty"`
}

// metaConv is one row value of the key in its new type.
type metaConv struct {
	r        metaRow
	from, to []byte
}

// metaRetype is `lore meta edit <key> --type <type> [--dry-run]
// [--keep-as-text] [--all]` (METADATA 3.3, R12; G-META-7). Flow:
//
//	resolve the new type ─► refuse: same type, a retired key, a key with
//	   allowed values (their names are text; G-META-7), rules (pattern,
//	   min, max, default) that do not fit the new type
//	─► convert every stored value of the key id in every attached store
//	   (branch rows; nothing is written yet)
//	─► misfits? E_META_TYPE naming each row (nothing written), unless
//	   --keep-as-text keeps them unchanged as text values (lint and
//	   filters report them as wrong-kind values, as on a non-strict key)
//	─► --dry-run: report and stop
//	─► ask (E_NEEDS_CONFIRM without --yes), one meta batch: the key row
//	   first (type + type_history; the id never changes), then every
//	   converted row (lore batch show|undo <bat_>)
//
// A sensitive key's values are never printed (3.20), only row ids.
func (o ops) metaRetype(ctx context.Context, k located, in MetaEditIn) (RowOut, error) {
	// the same type words as meta add (a key row stores a bare kind)
	if k, kerr := value.ParseKind(in.Type); kerr != nil || k == value.KindNull {
		return RowOut{}, errs.New(errs.MetaType, "--type "+quote(in.Type)+" is not a metadata type")
	}
	nt, err := meta.ParseType(in.Type)
	if err != nil {
		return RowOut{}, err
	}
	old, err := keyType(k)
	if err != nil {
		return RowOut{}, err
	}
	name := text(k.Row, mName)
	if err = o.retypeAllowed(ctx, k, name, old, nt); err != nil {
		return RowOut{}, err
	}
	kid, err := ids.Parse[ids.MetaKey](k.Row.ID.String())
	if err != nil {
		return RowOut{}, errs.Wrap(errs.ConfigInvalid, "meta key id", err)
	}
	rows, err := o.metaRows(ctx)
	if err != nil {
		return RowOut{}, err
	}
	sens := flag(k.Row, mSensitive)
	var convs []metaConv
	var bad []string
	for _, r := range rows {
		en, ok := r.entries[kid]
		if !ok {
			continue
		}
		sens = sens || en.Sensitive
		to, cerr := convertMetaValue(nt, en.V)
		if cerr != nil {
			bad = append(bad, r.Row.ID.String())
			continue
		}
		convs = append(convs, metaConv{r: r, from: en.V, to: to})
	}
	head := fmt.Sprintf("%s (%s, %s): %s → %s\n", name, short(k.Row.ID), text(k.Row, mScope), old, nt)
	var b strings.Builder
	b.WriteString(head)
	if len(bad) > 0 && !in.KeepAsText {
		return RowOut{}, errs.New(errs.MetaType, fmt.Sprintf("%s: %s not fit %s, nothing written: %s", name, plural.Of(len(bad), "value does", "values do"), nt, strings.Join(bad, ", "))).
			WithHint("fix those rows (lore meta set --id <id> " + name + "=… or lore meta unset --id <id> " + name + "), or rerun with --keep-as-text to keep them as text values")
	}
	listed := convs
	if !in.All && len(listed) > retypeListMax {
		listed = listed[:retypeListMax]
	}
	fmt.Fprintf(&b, "  %s", plural.Of(len(convs), "value converts", "values convert"))
	if !sens && len(listed) > 0 {
		pairs := make([]string, 0, len(listed))
		for _, c := range listed {
			pairs = append(pairs, string(c.from)+" → "+string(c.to))
		}
		b.WriteString(": " + strings.Join(pairs, ", "))
		if len(listed) < len(convs) {
			b.WriteString(", … (--all lists every one)")
		}
	}
	b.WriteString("\n")
	if len(bad) > 0 {
		fmt.Fprintf(&b, "  %s not fit and stay text values (--keep-as-text): %s\n", plural.Of(len(bad), "value does", "values do"), strings.Join(bad, ", "))
	}
	rowIDs := make([]string, 0, len(convs))
	for _, c := range convs {
		rowIDs = append(rowIDs, c.r.Row.ID.String())
	}
	if in.DryRun {
		b.WriteString("dry run: nothing written\n")
		return RowOut{ID: k.Row.ID.String(), Store: string(k.Src), DryRun: true, IDs: rowIDs, Human: b.String()}, nil
	}
	if err = bulk.Ask(ctx, fmt.Sprintf("change the type of %s (%s) from %s to %s and convert %s?", name, short(k.Row.ID), old, nt, plural.Count(len(convs), "value"))); err != nil {
		return RowOut{}, err
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindMeta)
	if err != nil {
		return RowOut{}, err
	}
	hist, err := o.metaTypeHistory(ctx, k, old.String())
	if err != nil {
		return RowOut{}, err
	}
	tv, err := txt(in.Type)
	if err != nil {
		return RowOut{}, err
	}
	if _, err = o.env.Update(bctx, engine.Ref{Store: k.Src, Table: schema.TableMetaKeys, ID: k.Row.ID}, map[string]value.Value{mType: tv, mTypeHistory: hist}); err != nil {
		return RowOut{}, err
	}
	for i, c := range convs {
		mw := engine.MetaWrite{ID: c.r.Row.ID, JSON: []meta.Term{{KeyID: kid, Value: string(c.to)}}}
		if _, err = o.env.Update(engine.WithMeta(bctx, mw), engine.Ref{ID: c.r.Row.ID}, map[string]value.Value{}); err != nil {
			return RowOut{}, errs.Prefix("meta edit --type: the key is "+nt.String()+" now; stopped after "+strconv.Itoa(i)+" of "+strconv.Itoa(len(convs))+
				" rows (batch "+bid.String()+"; see: lore batch show --id "+bid.String()+", or lore batch undo --id "+bid.String()+")", err)
		}
	}
	fmt.Fprintf(&b, "%s: %s converted in batch %s; id kept (see: lore batch show --id %s)\n", name, plural.Count(len(convs), "value"), bid, bid)
	return RowOut{ID: k.Row.ID.String(), Store: string(k.Src), Batch: bid.String(), IDs: rowIDs, Human: b.String()}, nil
}

// retypeAllowed refuses a type change lore cannot carry out safely.
func (o ops) retypeAllowed(ctx context.Context, k located, name string, old, nt meta.Type) error {
	switch {
	case old == nt:
		return errs.New(errs.InvalidInput, name+" is already "+nt.String())
	case text(k.Row, mMergedInto) != "":
		return errs.New(errs.MetaRetired, name+" is merged into "+text(k.Row, mMergedInto)+"; change the survivor's type")
	case nt.Kind == value.KindRef || nt.Kind == value.KindSecretRef || nt.Elem == value.KindRef || nt.Elem == value.KindSecretRef:
		return errs.New(errs.InvalidInput, "meta edit --type: "+nt.String()+" values are ids or vault entries, not converted text").
			WithHint("add a new key of that type and set it row by row")
	}
	vals, err := o.metaValues(ctx, k)
	if err != nil {
		return err
	}
	if len(vals) > 0 {
		// G-META-7: allowed values are val_ rows named in the old type; a
		// conversion would have to rename and re-match every one of them.
		return errs.New(errs.MetaType, fmt.Sprintf("%s has %d allowed values; a key with allowed values keeps its type", name, len(vals))).
			WithHint("remove them first (lore meta edit --id " + name + " --remove-value <v>), or add a new key of the new type")
	}
	rules := meta.Rules{Pattern: text(k.Row, mPattern), Min: text(k.Row, mMin), Max: text(k.Row, mMax)}
	if err := meta.CheckRuleDefs(nt, rules); err != nil {
		return errs.Prefix("meta edit --type: the rules of "+name+" do not fit "+nt.String()+" (clear them first: --pattern none, --min none, --max none)", err)
	}
	if d := text(k.Row, mDefault); d != "" {
		if _, err := meta.Coerce(nt, d); err != nil {
			return errs.Wrap(errs.MetaType, "meta edit --type: the default of "+name+" does not fit "+nt.String()+" (clear it first: --default none)", err)
		}
	}
	return nil
}

// metaTypeHistory is k's type_history with {old, until now, by the caller}
// appended.
func (o ops) metaTypeHistory(ctx context.Context, k located, old string) (value.Value, error) {
	var hist []metaTypeHistoryEntry
	// any stored shape (the cache may hand back a value other than
	// value.JSON): read it through its JSON form, as loosen reads changed_by
	if raw, ok := k.Row.Fields[mTypeHistory]; ok && raw != nil {
		b, merr := raw.MarshalJSON()
		if merr != nil {
			return nil, errs.Wrap(errs.ConfigInvalid, "meta key "+k.Row.ID.String()+": type_history", merr)
		}
		if string(b) != "null" {
			if err := json.Unmarshal(b, &hist); err != nil {
				return nil, errs.Wrap(errs.ConfigInvalid, "meta key "+k.Row.ID.String()+": type_history is not a list", err)
			}
		}
	}
	by := ""
	if a, isA := upgrade.As[actorer](o.env); isA {
		if id, aerr := a.Actor(ctx); aerr == nil && !id.IsZero() {
			by = id.String()
		}
	}
	hist = append(hist, metaTypeHistoryEntry{Type: old, Until: opclock.Now(o.env).UTC().Format(time.RFC3339), By: by})
	b, err := json.Marshal(hist)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "encode type_history", err)
	}
	return value.JSONOf(b)
}

// convertMetaValue is a stored value read as type t: json takes the value
// as it is; every other type re-reads its text (a string unquoted, a list's
// items joined with commas, a number or bool as its JSON text) the way a
// command-line value is read, so the same normalisation applies (R12).
func convertMetaValue(t meta.Type, raw []byte) ([]byte, error) {
	if t.Kind == value.KindJSON {
		c, err := meta.CoerceJSON(t, raw)
		return c.Raw, err
	}
	if canon.KindOf(raw) == canon.KindObject {
		if t.Kind != value.KindText {
			return nil, errs.New(errs.MetaType, "meta: an object does not convert to "+t.String())
		}
		c, err := meta.Coerce(t, string(raw))
		return c.Raw, err
	}
	c, err := meta.Coerce(t, strings.Join(meta.Strings(raw), ","))
	return c.Raw, err
}
