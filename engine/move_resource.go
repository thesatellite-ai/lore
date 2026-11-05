package engine

// Resource checks of a move into a client repo (RESOURCES 3.9, R2, BATCH 7).
//
//	MoveWith (dst.Client, table resources) ─► resourceIntoClient
//	   per `fields` entry: its kind_fields row (any store in view; not found
//	   = cannot prove it is safe = blocked, fail closed)
//	     ref value not a live resource of the client repo ─► blocked (R2 item 1)
//	     internal / secret_ref value ─► --drop-internal removes it (warning),
//	                                    else blocked
//	     sensitive value ─► listed; the move asks (E_NEEDS_CONFIRM) unless --yes
//	   any blocked ─► E_LEAK_BLOCKED listing every line, nothing written

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Resource row and kind_fields keys this check reads (ops/settings owns the
// shapes: fields = {fld_…: {name, v}}, kind_fields type/internal/sensitive).
const (
	resFieldsKey   = "fields"
	resEntryName   = "name"
	resEntryValue  = "v"
	kfType         = "type"
	kfInternal     = "internal"
	kfSensitive    = "sensitive"
	kfTypeRef      = string(value.KindRef)
	kfTypeSecret   = string(value.KindSecretRef)
	boolTrueString = "true"
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
	var blocked, sensitive []string
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
		fidID, perr := ids.ParseAny(fid)
		var def map[string]value.Value
		if perr == nil {
			if _, r, lerr := e.locateExcept(ctx, Ref{Table: schema.TableKindFields, ID: fidID}, ""); lerr == nil && !r.Deleted {
				def = r.Fields
			}
		}
		if def == nil {
			// R2: fail closed; a definition this machine cannot read may be internal.
			blocked = append(blocked, name+": its field definition is not visible here (cannot prove it may reach a client)")
			continue
		}
		typ := str(def[kfType])
		switch {
		case typ == kfTypeSecret || str(def[kfInternal]) == boolTrueString:
			if drop {
				delete(entries, fid)
				warns = append(warns, Warning{Code: WarnResourceDropped, Field: name, Message: "kept back: internal field " + name + " was removed (--drop-internal)"})
				continue
			}
			blocked = append(blocked, name+" (internal field) has a value; unset it, or pass --drop-internal to remove it in the move")
			continue
		case typ == kfTypeRef:
			var target string
			if json.Unmarshal(v, &target) == nil && target != "" && !e.clientHas(ctx, to, target) {
				blocked = append(blocked, name+" points at "+target+", a store resource the client cannot see; repoint or unset it first")
			}
		}
		if str(def[kfSensitive]) == boolTrueString {
			sensitive = append(sensitive, name)
		}
	}
	if len(blocked) > 0 {
		return nil, nil, errs.New(errs.LeakBlocked, "engine: moving "+id.String()+" into client repo "+string(to)+" would leak: "+strings.Join(blocked, "; ")+"; nothing written").
			WithHint("repoint or unset those fields, or pass --drop-internal for internal ones")
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
