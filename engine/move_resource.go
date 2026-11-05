package engine

// Resource checks of a move into a client repo (RESOURCES 3.9, 3.15, R2,
// BATCH 3/5/7).
//
//	MoveWith (dst.Client, table resources) ─► resourceIntoClient
//	   per `fields` entry: its kind_fields row (any store in view; not found
//	   = cannot prove it is safe = leak, fail closed)
//	     value breaks its type / allowed values ─► quarantined (logic/resquar)
//	     ref value not a live resource of the client repo ─► leak (R2 item 1)
//	     secret_ref kept with --no-internal ─► leak (BATCH 5)
//	     internal / secret_ref value ─► --drop-internal removes it (warning),
//	                                    else internal
//	     sensitive value ─► listed; the move asks (E_NEEDS_CONFIRM) unless --yes
//	   any quarantined ─► E_RESOURCE_QUARANTINED (fix first, 3.15)
//	   any leak       ─► E_LEAK_BLOCKED listing every line (internal ones too)
//	   only internal  ─► E_RESOURCE_INTERNAL (3.19)
//	─► clientKind (after every other client check, BATCH 3): the client
//	   repo's own kind row (origin = store kind id) and the field rows the
//	   row uses, found again by origin and name or written now; the row's
//	   kind and fields keys are re-pointed at them; the store kind's copies
//	   gets "<client place>=<client kind id>" (MQ15, R2-Q1)
import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/resquar"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Resource row and kind_fields keys this check reads (ops/settings owns the
// shapes: fields = {fld_…: {name, v}}, kind_fields type/internal/sensitive).
const (
	resFieldsKey    = "fields"
	resEntryName    = "name"
	resEntryValue   = "v"
	kfType          = "type"
	kfInternal      = "internal"
	kfSensitive     = "sensitive"
	kfValues        = "values"
	kfName          = "name"
	kfKindID        = "kind_id"
	boolFalseString = "false"
	kfTypeRef       = string(value.KindRef)
	kfTypeSecret    = string(value.KindSecretRef)
	boolTrueString  = "true"
)

// WarnResourceDropped names an internal resource field --drop-internal
// removed on a move into a client repo (RES 3.9).
const WarnResourceDropped WarnCode = "W_RESOURCE_DROPPED"

// resourceIntoClient checks (and with drop strips) a resource's values for
// a move into the client repo to; it returns the fields to write.
func (e *Engine) resourceIntoClient(ctx context.Context, to store.Source, id ids.AnyID, fields map[string]value.Value, drop bool) (map[string]value.Value, []Warning, error) {
	raw, ok := value.JSONForm(fields[resFieldsKey])
	if !ok {
		return fields, nil, nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, nil, errs.Wrap(errs.ResourceInvalid, "engine: "+id.String()+": fields is not an object", err)
	}
	var leaks, internal, sensitive []string
	var quarantined []string
	var warns []Warning
	for _, fid := range slices.Sorted(maps.Keys(entries)) {
		var ent map[string]json.RawMessage
		if json.Unmarshal(entries[fid], &ent) != nil {
			continue
		}
		var name string
		_ = json.Unmarshal(ent[resEntryName], &name) // a nameless entry is listed by its id
		if name == "" {
			name = fid
		}
		v, hasV := ent[resEntryValue]
		if !hasV {
			continue
		}
		def := e.fieldDef(ctx, fid)
		if def == nil {
			// R2: fail closed; a definition this machine cannot read may be internal.
			leaks = append(leaks, name+": its field definition is not visible here (cannot prove it may reach a client)")
			continue
		}
		typ := str(def[kfType])
		// 3.15: a quarantined value is fixed first; the other checks read it.
		if q, bad := resquar.Check(resquar.Field{Name: name, Type: typ, Values: listTexts(def[kfValues])}, v); bad {
			quarantined = append(quarantined, q.Field+" ("+q.Rule+")")
			continue
		}
		isInternal := str(def[kfInternal]) == boolTrueString
		switch {
		case typ == kfTypeSecret && str(def[kfInternal]) == boolFalseString:
			// BATCH 5: a secret_ref kept with --no-internal would put the full
			// locator into the client's file.
			leaks = append(leaks, name+" holds a secret reference (secret_ref field marked --no-internal); unset it first")
			continue
		case typ == kfTypeSecret || isInternal:
			if drop {
				delete(entries, fid)
				warns = append(warns, Warning{Code: WarnResourceDropped, Field: name, Message: "kept back: internal field " + name + " was removed (--drop-internal)"})
				continue
			}
			internal = append(internal, name+" (internal field) has a value; unset it, or pass --drop-internal to remove it in the move")
			continue
		case typ == kfTypeRef:
			var target string
			if json.Unmarshal(v, &target) == nil && target != "" && !e.clientHas(ctx, to, target) {
				leaks = append(leaks, name+" points at "+target+", a store resource the client cannot see; repoint or unset it first")
			}
		}
		if str(def[kfSensitive]) == boolTrueString {
			sensitive = append(sensitive, name)
		}
	}
	if len(quarantined) > 0 {
		return nil, nil, errs.New(errs.ResourceQuarantined, "engine: "+id.String()+" has invalid fields: "+strings.Join(quarantined, "; ")+"; fix them before moving; nothing written").
			WithHint("lore resource lint shows the fix; lore resource edit <name> --set <field>=<value>")
	}
	if len(leaks) > 0 {
		// A leak and an internal value together: one refusal listing both,
		// under the leak code (internal values alone have their own, 3.19).
		return nil, nil, errs.New(errs.LeakBlocked, "engine: moving "+id.String()+" into client repo "+string(to)+" would leak: "+strings.Join(append(leaks, internal...), "; ")+"; nothing written").
			WithHint("repoint or unset those fields, or pass --drop-internal for internal ones")
	}
	if len(internal) > 0 {
		return nil, nil, errs.New(errs.ResourceInternal, "engine: moving "+id.String()+" into client repo "+string(to)+": "+strings.Join(internal, "; ")+"; nothing written").
			WithHint("lore move " + id.String() + " --to " + string(to) + " --drop-internal (removes them), or unset them first")
	}
	if len(sensitive) > 0 && !Confirmed(ctx) {
		return nil, nil, errs.New(errs.NeedsConfirm, "move "+id.String()+" into the client repo "+string(to)+"? everyone with access to it can read the sensitive field(s) "+strings.Join(sensitive, ", ")).
			WithHint("lore move " + id.String() + " --to " + string(to) + " --yes")
	}
	if len(warns) == 0 {
		return fields, nil, nil
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "engine: encode fields", err)
	}
	j, err := value.JSONOf(b)
	if err != nil {
		return nil, nil, err
	}
	out := maps.Clone(fields)
	out[resFieldsKey] = j
	return out, warns, nil
}

// clientHas reports whether target is a live resource of the client repo.
func (e *Engine) clientHas(ctx context.Context, to store.Source, target string) bool {
	tid, err := ids.ParseAny(target)
	if err != nil {
		return false
	}
	_, live, err := e.getRow(ctx, to, schema.TableResources, tid)
	return err == nil && live
}

// str is a value's text ("" for nil).
func str(v value.Value) string {
	if v == nil {
		return ""
	}
	return v.String()
}

// fieldDef is the kind_fields row fid names, from any store in view (nil
// when not visible or not an id).
func (e *Engine) fieldDef(ctx context.Context, fid string) map[string]value.Value {
	fidID, err := ids.ParseAny(fid)
	if err != nil {
		return nil
	}
	_, r, err := e.locateExcept(ctx, Ref{Table: schema.TableKindFields, ID: fidID}, "")
	if err != nil || r.Deleted {
		return nil
	}
	return r.Fields
}

// listTexts is a list-of-text value's items, the same for a value built in
// memory and one read back from a file.
func listTexts(v value.Value) []string {
	if v == nil {
		return nil
	}
	return value.ItemStrings(v)
}

// Kinds columns the client copy writes (ops/settings owns the shapes).
const (
	kindOrigin     = "origin"
	kindNameFormat = "name_format"
	kindCopies     = "copies"
	// copySep joins the client place and its kind id in a copies item.
	copySep = "="
)

// recordCopy adds "<client place>=<client kind id>" to the store kind's
// copies (MQ15, R2-Q1: the store side of the both-ways link; set union, so
// a rerun after an interrupted move adds nothing twice). A failed write
// refuses the move; the client kind already written is found again by
// origin on the next run, which then records the copy.
func (e *Engine) recordCopy(ctx context.Context, ksrc store.Source, skind row.Row, to store.Source, ckind ids.AnyID) error {
	item := e.placeOf(to) + copySep + ckind.String()
	cur := listTexts(skind.Fields[kindCopies])
	if slices.Contains(cur, item) {
		return nil
	}
	vals, err := textList(append(slices.Clone(cur), item))
	if err != nil {
		return err
	}
	_, err = e.Update(ctx, Ref{Store: ksrc, Table: schema.TableKinds, ID: skind.ID}, map[string]value.Value{kindCopies: vals})
	return err
}

// clientKind gives the client repo to its own minimal definition of the
// kind of a resource moving there (RES 3.9 "The client repo's kind
// definition is minimal", BATCH 3) and returns fields re-pointed at it:
//
//	kind already a row of to ─► unchanged
//	to has a kind with origin = the store kind ─► reuse it, else write one
//	   {name, name_format, origin} (no description: no preview showed it)
//	per kept entry: to's field of that kind with the same name, else write
//	   {kind_id, name, type, sensitive, values: only those the row uses};
//	   an existing field lacking a used value gets it added
//	the row's kind and every fields key now name to's rows
//
// Rows are written before the resource (its refs must resolve there); an
// interrupted move leaves the copy, and the next run finds it by origin.
func (e *Engine) clientKind(ctx context.Context, to store.Source, fields map[string]value.Value) (map[string]value.Value, error) {
	kv, ok := fields[resKindKey]
	if !ok || kv == nil || kv.Kind() == value.KindNull {
		return fields, nil
	}
	kid, err := ids.ParseAny(kv.String())
	if err != nil {
		return fields, nil // validation reports a bad kind id
	}
	if _, live, gerr := e.getRow(ctx, to, schema.TableKinds, kid); gerr == nil && live {
		return fields, nil
	}
	ksrc, skind, err := e.locateExcept(ctx, Ref{Table: schema.TableKinds, ID: kid}, to)
	if err != nil || skind.Deleted {
		return nil, errs.New(errs.LeakBlocked, "engine: the kind "+kid.String()+" of this resource is not visible here; the client repo's own kind cannot be written; nothing written").
			WithHint("run lore sync on a machine that reads the store, then move again")
	}
	ckind, err := e.clientKindRow(ctx, to, skind)
	if err != nil {
		return nil, err
	}
	if err = e.recordCopy(ctx, ksrc, skind, to, ckind); err != nil {
		return nil, err
	}
	cfields, err := e.clientFieldsOf(ctx, to, ckind)
	if err != nil {
		return nil, err
	}
	raw, ok := value.JSONForm(fields[resFieldsKey])
	out := maps.Clone(fields)
	kref, err := value.RefOf(ckind)
	if err != nil {
		return nil, err
	}
	out[resKindKey] = kref
	if !ok {
		return out, nil
	}
	var entries map[string]json.RawMessage
	if err = json.Unmarshal(raw, &entries); err != nil {
		return nil, errs.Wrap(errs.ResourceInvalid, "engine: fields is not an object", err)
	}
	mapped := make(map[string]json.RawMessage, len(entries))
	for _, fid := range slices.Sorted(maps.Keys(entries)) {
		def := e.fieldDef(ctx, fid)
		if def == nil {
			return nil, errs.New(errs.LeakBlocked, "engine: field "+fid+" has no visible definition; nothing written")
		}
		var ent map[string]json.RawMessage
		if err = json.Unmarshal(entries[fid], &ent); err != nil {
			return nil, errs.Wrap(errs.ResourceInvalid, "engine: fields entry "+fid, err)
		}
		used := usedValues(def, ent[resEntryValue])
		cf, ferr := e.clientField(ctx, to, ckind, cfields, def, used)
		if ferr != nil {
			return nil, ferr
		}
		mapped[cf.String()] = entries[fid]
	}
	b, err := json.Marshal(mapped)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "engine: encode fields", err)
	}
	j, err := value.JSONOf(b)
	if err != nil {
		return nil, err
	}
	out[resFieldsKey] = j
	return out, nil
}

// resKindKey is the resources column naming the row's kind.
const resKindKey = "kind"

// clientKindRow finds to's kind with origin = skind's id, or writes one.
func (e *Engine) clientKindRow(ctx context.Context, to store.Source, skind row.Row) (ids.AnyID, error) {
	rows, err := e.List(ctx, to, schema.TableKinds)
	if err != nil {
		return ids.AnyID{}, err
	}
	for _, r := range rows {
		if originID(r.Fields[kindOrigin]) == skind.ID.String() {
			return r.ID, nil
		}
	}
	org, err := value.JSONOf([]byte(strconv.Quote(skind.ID.String())))
	if err != nil {
		return ids.AnyID{}, err
	}
	kf := map[string]value.Value{kfName: skind.Fields[kfName], kindOrigin: org}
	if nf, ok := skind.Fields[kindNameFormat]; ok && nf != nil && nf.Kind() != value.KindNull {
		kf[kindNameFormat] = nf
	}
	w, err := e.create(ctx, to, schema.TableKinds, ids.AnyID{}, kf)
	if err != nil {
		return ids.AnyID{}, err
	}
	return w.Row.ID, nil
}

// originID is a kinds.origin value's id ("" when unset): a JSON string in
// memory, a text once read back from the file.
func originID(v value.Value) string {
	if v == nil || v.Kind() == value.KindNull {
		return ""
	}
	if j, ok := v.(value.JSON); ok {
		var s string
		if json.Unmarshal(j.Raw(), &s) == nil {
			return s
		}
		return ""
	}
	return v.String()
}

// clientFieldsOf maps field name to to's live kind_fields row of ckind.
func (e *Engine) clientFieldsOf(ctx context.Context, to store.Source, ckind ids.AnyID) (map[string]row.Row, error) {
	rows, err := e.List(ctx, to, schema.TableKindFields)
	if err != nil {
		return nil, err
	}
	out := map[string]row.Row{}
	for _, r := range rows {
		if str(r.Fields[kfKindID]) == ckind.String() && str(r.Fields[schema.FieldExtends]) == "" {
			out[str(r.Fields[kfName])] = r
		}
	}
	return out, nil
}

// clientField returns to's field row matching def by name (adding used
// allowed values it lacks), or writes a minimal one.
func (e *Engine) clientField(ctx context.Context, to store.Source, ckind ids.AnyID, have map[string]row.Row, def map[string]value.Value, used []string) (ids.AnyID, error) {
	name := str(def[kfName])
	if r, ok := have[name]; ok {
		cur := listTexts(r.Fields[kfValues])
		missing := false
		for _, u := range used {
			if !slices.Contains(cur, u) {
				cur, missing = append(cur, u), true
			}
		}
		if missing {
			vals, err := textList(cur)
			if err != nil {
				return ids.AnyID{}, err
			}
			if _, err = e.Update(ctx, Ref{Store: to, Table: schema.TableKindFields, ID: r.ID}, map[string]value.Value{kfValues: vals}); err != nil {
				return ids.AnyID{}, err
			}
		}
		return r.ID, nil
	}
	kref, err := value.RefOf(ckind)
	if err != nil {
		return ids.AnyID{}, err
	}
	f := map[string]value.Value{kfKindID: kref, kfName: def[kfName], kfType: def[kfType]}
	if str(def[kfSensitive]) == boolTrueString {
		f[kfSensitive] = value.BoolOf(true)
	}
	if str(def[kfType]) == kfTypeSecret {
		// only a --no-internal secret_ref could get here, and it is refused
		// earlier; keep the client's copy honest anyway.
		f[kfInternal] = value.BoolOf(false)
	}
	if len(used) > 0 {
		vals, verr := textList(used)
		if verr != nil {
			return ids.AnyID{}, verr
		}
		f[kfValues] = vals
	}
	w, err := e.create(ctx, to, schema.TableKindFields, ids.AnyID{}, f)
	if err != nil {
		return ids.AnyID{}, err
	}
	have[name] = w.Row
	return w.Row.ID, nil
}

// usedValues lists the allowed values the stored value uses (nil for a
// field without a closed set: BATCH 3 copies only the values in use).
func usedValues(def map[string]value.Value, raw json.RawMessage) []string {
	if len(listTexts(def[kfValues])) == 0 || raw == nil {
		return nil
	}
	v, err := value.Infer(raw)
	if err != nil {
		return nil
	}
	switch x := v.(type) {
	case value.Null:
		return nil
	case value.List:
		return value.ItemStrings(x)
	}
	return []string{v.String()}
}

// textList builds a list-of-text value.
func textList(items []string) (value.List, error) {
	vs := make([]value.Value, 0, len(items))
	for _, s := range items {
		t, err := value.TextOf(s)
		if err != nil {
			return value.List{}, err
		}
		vs = append(vs, t)
	}
	return value.ListOf(value.KindText, vs...)
}
