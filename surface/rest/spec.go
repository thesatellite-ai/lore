package rest

import (
	"encoding/json"
	"net/http"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// SpecDoc is the GET /v1/spec body (spec 9.8): the whole registry plus the
// attached stores' kinds, with every UI hint resolved, so any UI or tool can
// build itself from it. Field names are a contract: only added.
type SpecDoc struct {
	LoreVersion string     `json:"lore_version"`
	Operations  []SpecOp   `json:"operations"`
	Kinds       []SpecKind `json:"kinds"`
}

// SpecOp is one non-hidden operation.
type SpecOp struct {
	Name       string         `json:"name"`
	Summary    string         `json:"summary"`
	Permission op.Permission  `json:"permission"`
	Confirm    op.ConfirmRule `json:"confirm"`
	Errors     []errs.Code    `json:"errors"`
	REST       SpecRoute      `json:"rest"`
	Input      []SpecField    `json:"input"`
}

// SpecRoute is an operation's REST route.
type SpecRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// SpecField is one input field with its hints resolved (label, widget,
// in_list defaults of spec 9.8 applied).
type SpecField struct {
	Name     string        `json:"name"`
	Type     op.FieldType  `json:"type"`
	Required bool          `json:"required"`
	Desc     string        `json:"desc,omitempty"`
	Label    string        `json:"label"`
	Widget   schema.Widget `json:"widget"`
	InList   bool          `json:"in_list"`
}

// SpecKind is one dynamic kind.
type SpecKind struct {
	ID     ids.ID[ids.Kind] `json:"id"`
	Name   string           `json:"name"`
	Source string           `json:"source"`
	Fields []SpecKindField  `json:"fields"`
}

// SpecKindField is one field of a kind with its hints resolved. Field rows
// carry no id in schema.FieldDef today, so none is reported.
type SpecKindField struct {
	Name     string        `json:"name"`
	Type     value.Kind    `json:"type"`
	Required bool          `json:"required"`
	Label    string        `json:"label"`
	Widget   schema.Widget `json:"widget"`
	InList   bool          `json:"in_list"`
}

// BuildSpec assembles the /v1/spec document. Pure: the handler and tests
// call it with the registry and the kinds.
func BuildSpec(reg *op.Registry, version string, kinds []Kind) SpecDoc {
	doc := SpecDoc{LoreVersion: version, Operations: []SpecOp{}, Kinds: []SpecKind{}}
	for _, e := range reg.Entries() {
		s := e.Spec()
		rt, ok := op.RouteOf(s, e.InputFields())
		if !ok {
			continue
		}
		codes := append([]errs.Code{}, s.Errors...)
		doc.Operations = append(doc.Operations, SpecOp{
			Name: s.Name, Summary: s.Summary, Permission: s.Permission, Confirm: s.Confirm,
			Errors: codes, REST: SpecRoute{Method: rt.Method, Path: rt.Path}, Input: inputFields(e.InputFields()),
		})
	}
	for _, k := range kinds {
		ui := schema.ResolveFields(k.Def.Fields)
		sk := SpecKind{ID: k.Def.Kind, Name: string(k.Def.Name), Source: k.Source, Fields: []SpecKindField{}}
		for i, f := range k.Def.Fields {
			sk.Fields = append(sk.Fields, SpecKindField{Name: f.Name, Type: f.Kind, Required: f.Required, Label: ui[i].Label, Widget: ui[i].Widget, InList: ui[i].InList})
		}
		doc.Kinds = append(doc.Kinds, sk)
	}
	return doc
}

// inputFields resolves the UI hints of an operation's inputs through the
// same schema.ResolveFields rule kinds use, so a form looks the same
// whether it comes from a struct or a kind row.
func inputFields(fields []op.FieldInfo) []SpecField {
	defs := make([]schema.FieldDef, len(fields))
	for i, f := range fields {
		defs[i] = schema.FieldDef{Name: f.Name, Kind: valueKindOf(f.Type), Required: f.Required, UI: f.UI}
	}
	ui := schema.ResolveFields(defs)
	out := make([]SpecField, len(fields))
	for i, f := range fields {
		out[i] = SpecField{Name: f.Name, Type: f.Type, Required: f.Required, Desc: f.Desc, Label: ui[i].Label, Widget: ui[i].Widget, InList: ui[i].InList}
	}
	return out
}

// valueKindOf is the value kind whose default widget fits an operation
// field type.
func valueKindOf(t op.FieldType) value.Kind {
	switch t {
	case op.TypeBool:
		return value.KindBool
	case op.TypeInt, op.TypeUint, op.TypeFloat:
		return value.KindNumber
	case op.TypeDuration:
		return value.KindDuration
	case op.TypeStrings:
		return value.KindList
	case op.TypeStringMap, op.TypeObject, op.TypeList:
		return value.KindJSON
	case op.TypeString, op.TypeText:
	}
	return value.KindText
}

// spec serves GET /v1/spec.
func (h *handlers) spec(w http.ResponseWriter, r *http.Request) {
	kinds, err := h.kinds(r)
	if err != nil {
		writeError(w, err)
		return
	}
	b, err := json.Marshal(BuildSpec(h.reg, h.opts.LoreVersion, kinds))
	if err != nil {
		writeError(w, errs.Wrap(errs.Internal, "rest: encode spec", err))
		return
	}
	writeJSON(w, http.StatusOK, b)
}
