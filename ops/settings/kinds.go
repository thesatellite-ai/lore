package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// Resource kind and field operations (CLI_REFERENCE 3.2 "lore resource
// kind and lore resource field"): kinds are `kinds` rows (knd_), their
// fields `kind_fields` rows (fld_), in the place --to names.
const (
	OpKindAdd     = "resource.kind.add"
	OpKindEdit    = "resource.kind.edit"
	OpKindShow    = "resource.kind.show"
	OpKindRename  = "resource.kind.rename"
	OpKinds       = "resource.kinds"
	OpFieldAdd    = "resource.field.add"
	OpFieldEdit   = "resource.field.edit"
	OpFieldRename = "resource.field.rename"
	OpFieldRemove = "resource.field.remove"
)

// kinds / kind_fields field names (schema.Builtin).
const (
	kName        = "name"
	kDescription = "description"
	kNameFormat  = "name_format"
	kRender      = "render"
	fKindID      = "kind_id"
	fType        = "type"
	fRequired    = "required"
	fSensitive   = "sensitive"
	fInternal    = "internal"
	fRefKind     = "ref_kind"
	fLabel       = "label"
	fValues      = "values"
	fDescription = "description"
)

// Name formats of a kind (CLI_REFERENCE 3.2 --name-format).
const (
	NameFormatID   = "id"
	NameFormatDNS  = "dns"
	NameFormatIP   = "ip"
	NameFormatText = "text"
)

// NameFormats lists the name formats.
func NameFormats() []string {
	return []string{NameFormatID, NameFormatDNS, NameFormatIP, NameFormatText}
}

// KindAddIn is the input of resource.kind.add.
type KindAddIn struct {
	Kind        string `json:"kind" arg:"0" desc:"the kind name (snake_case)"`
	Description string `json:"description,omitempty" flag:"description" desc:"what the kind is"`
	NameFormat  string `json:"name_format,omitempty" flag:"name-format" desc:"id | dns | ip | text (default id)"`
	Render      string `json:"render,omitempty" flag:"render" desc:"how a row renders (summary, …)"`
	To          string `json:"to,omitempty" flag:"to" desc:"where the kind row is written (repo, <store>, <store>/<folder>; default repo)"`
}

// KindEditIn is the input of resource.kind.edit.
type KindEditIn struct {
	Kind        string `json:"id" arg:"0" desc:"the kind (name or knd_ id)"`
	Description string `json:"description,omitempty" flag:"description" desc:"what the kind is"`
	NameFormat  string `json:"name_format,omitempty" flag:"name-format" desc:"id | dns | ip | text"`
	Render      string `json:"render,omitempty" flag:"render" desc:"how a row renders"`
}

// KindRef names one kind.
type KindRef struct {
	Kind string `json:"id" arg:"0" desc:"the kind (name or knd_ id)"`
}

// RenameIn renames a settings row (old → new).
type RenameIn struct {
	Old string `json:"id" arg:"0" desc:"the current name (or id)"`
	New string `json:"new_name" arg:"1" desc:"the new name"`
}

// KindsIn is the input of resource.kinds.
type KindsIn struct{}

// FieldAddIn is the input of resource.field.add.
type FieldAddIn struct {
	Kind        string `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Field       string `json:"field" arg:"1" desc:"the field name (snake_case)"`
	Type        string `json:"type" flag:"type" desc:"the value type (text, number, bool, date, datetime, duration, ip, cidr, dns, url, email, json, list, ref)"`
	Required    bool   `json:"required,omitempty" flag:"required" desc:"a resource of this kind must set it"`
	Sensitive   bool   `json:"sensitive,omitempty" flag:"sensitive" desc:"printed as ••• unless --show-sensitive"`
	Internal    bool   `json:"internal,omitempty" flag:"internal" desc:"never reaches a client repo"`
	RefKind     string `json:"ref_kind,omitempty" flag:"ref-kind" desc:"with --type ref: the kind the value points at"`
	Values      string `json:"values,omitempty" flag:"values" desc:"allowed values, comma separated (a closed set)"`
	Description string `json:"description,omitempty" flag:"description" desc:"what the field means"`
	Label       string `json:"label,omitempty" flag:"label" desc:"display label"`
}

// FieldEditIn is the input of resource.field.edit.
type FieldEditIn struct {
	Kind         string   `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Field        string   `json:"field" arg:"1" desc:"the field name"`
	Required     bool     `json:"required,omitempty" flag:"required" desc:"make it required"`
	NoRequired   bool     `json:"no_required,omitempty" flag:"no-required" desc:"make it optional"`
	Sensitive    bool     `json:"sensitive,omitempty" flag:"sensitive" desc:"mark it sensitive"`
	NoSensitive  bool     `json:"no_sensitive,omitempty" flag:"no-sensitive" desc:"mark it not sensitive"`
	Internal     bool     `json:"internal,omitempty" flag:"internal" desc:"mark it internal"`
	NoInternal   bool     `json:"no_internal,omitempty" flag:"no-internal" desc:"mark it not internal"`
	AddValues    []string `json:"add_values,omitempty" flag:"add-value" desc:"add an allowed value (repeatable)"`
	RemoveValues []string `json:"remove_values,omitempty" flag:"remove-value" desc:"remove an allowed value (repeatable; refused while rows use it)"`
	Label        string   `json:"label,omitempty" flag:"label" desc:"display label"`
	Description  string   `json:"description,omitempty" flag:"description" desc:"what the field means"`
}

// FieldRenameIn is the input of resource.field.rename.
type FieldRenameIn struct {
	Kind string `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Old  string `json:"field" arg:"1" desc:"the current field name"`
	New  string `json:"new_name" arg:"2" desc:"the new field name"`
}

// FieldRemoveIn is the input of resource.field.remove.
type FieldRemoveIn struct {
	Kind  string `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Field string `json:"field" arg:"1" desc:"the field name"`
	Drop  bool   `json:"drop,omitempty" flag:"drop" desc:"also remove the values rows hold (else refused while rows use it)"`
}

// RowOut is the output of a settings write: the row id, its store and the
// human line.
type RowOut struct {
	ID    string `json:"id"`
	Store string `json:"store"`
	Human string `json:"-"`
}

// Text implements op.Texter.
func (o RowOut) Text() string { return o.Human }

// Field is one kind field as shown.
type Field struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required,omitempty"`
	Sensitive   bool     `json:"sensitive,omitempty"`
	Internal    bool     `json:"internal,omitempty"`
	RefKind     string   `json:"ref_kind,omitempty"`
	Values      []string `json:"values,omitempty"`
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Kind is one kind as shown, with its fields.
type Kind struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Store       string  `json:"store"`
	Description string  `json:"description,omitempty"`
	NameFormat  string  `json:"name_format,omitempty"`
	Render      string  `json:"render,omitempty"`
	Count       int     `json:"count"`
	Fields      []Field `json:"fields"`
}

// KindOut is the output of resource.kind.show.
type KindOut struct {
	Data Kind `json:"data"`
}

// Text implements op.Texter.
func (o KindOut) Text() string {
	k := o.Data
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  (%s)\n", k.ID, k.Name, k.Store)
	if k.Description != "" {
		fmt.Fprintf(&b, "  %s\n", k.Description)
	}
	for _, f := range k.Fields {
		b.WriteString("  " + fieldLine(f) + "\n")
	}
	if len(k.Fields) == 0 {
		b.WriteString("  (no fields)\n")
	}
	return b.String()
}

func fieldLine(f Field) string {
	parts := []string{f.Name, f.Type}
	if f.RefKind != "" {
		parts[1] += "→" + f.RefKind
	}
	for _, x := range []struct {
		on   bool
		word string
	}{{f.Required, "required"}, {f.Sensitive, "sensitive"}, {f.Internal, "internal"}} {
		if x.on {
			parts = append(parts, x.word)
		}
	}
	if len(f.Values) > 0 {
		parts = append(parts, "values "+strings.Join(f.Values, ", "))
	}
	return strings.Join(parts, "  ")
}

// KindsOut is the output of resource.kinds.
type KindsOut struct {
	Count int    `json:"count"`
	Data  []Kind `json:"data"`
}

// Text implements op.Texter.
func (o KindsOut) Text() string {
	if len(o.Data) == 0 {
		return "(no kinds)\n"
	}
	var b strings.Builder
	b.WriteString("ID\tKIND\tSTORE\tROWS\tFIELDS\n")
	for _, k := range o.Data {
		names := make([]string, 0, len(k.Fields))
		for _, f := range k.Fields {
			names = append(names, f.Name)
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\t%s\n", k.ID, k.Name, k.Store, k.Count, strings.Join(names, ", "))
	}
	return b.String()
}

// kindOps returns the kind and field operations.
func (o ops) kindOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpKindAdd, Summary: "Add a resource kind (a kinds settings row)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "add a kind", CLI: `lore resource kind add host --description "a server"`, Want: "knd_"}}}, o.kindAdd),
		op.Define(op.Spec{Name: OpKindEdit, Summary: "Edit a resource kind", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true}}, o.kindEdit),
		op.Define(op.Spec{Name: OpKindShow, Summary: "Show a resource kind and its fields", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.kindShow),
		op.Define(op.Spec{Name: OpKindRename, Summary: "Rename a resource kind (a label change; ids stay)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors}, o.kindRename),
		op.Define(op.Spec{Name: OpKinds, Summary: "List resource kinds with their fields and row counts", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.kindList),
		op.Define(op.Spec{Name: OpFieldAdd, Summary: "Add a field to a resource kind (a kind_fields settings row)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "closed set", CLI: "lore resource field add host provider --type text --required --values hetzner,aws", Want: "fld_"}}}, o.fieldAdd),
		op.Define(op.Spec{Name: OpFieldEdit, Summary: "Edit a field of a resource kind", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true}}, o.fieldEdit),
		op.Define(op.Spec{Name: OpFieldRename, Summary: "Rename a field (a label change: rows key values by fld_ id)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors}, o.fieldRename),
		op.Define(op.Spec{Name: OpFieldRemove, Summary: "Remove a field from a resource kind", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.HasDependents)}, o.fieldRemove),
	}
}

// kindByName finds a live kind by name or id in every attached store.
func (o ops) kindByName(ctx context.Context, key string) (located, error) {
	k, err := byNameOrID(ctx, o.env, o.env.Stores(), schema.TableKinds, kName, key, nil)
	if errs.Has(err, errs.NotFound) {
		return located{}, errs.New(errs.KindUnknown, "no resource kind "+quote(key)).WithHint("run `lore resource kinds` to see the kinds")
	}
	return k, err
}

// fieldsOf are the live fields of kind k (from every store: a field row
// may live beside its kind or at a nearer place).
func (o ops) fieldsOf(ctx context.Context, k located) ([]located, error) {
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableKindFields)
	if err != nil {
		return nil, err
	}
	return where(rows, map[string]string{fKindID: k.Row.ID.String()}), nil
}

func (o ops) fieldByName(ctx context.Context, k located, name string) (located, error) {
	fs, err := o.fieldsOf(ctx, k)
	if err != nil {
		return located{}, err
	}
	for _, f := range fs {
		if text(f.Row, kName) == name || f.Row.ID.String() == name {
			return f, nil
		}
	}
	return located{}, errs.New(errs.NotFound, "kind "+text(k.Row, kName)+" has no field "+quote(name)).
		WithHint("lore resource kind show " + text(k.Row, kName))
}

func checkName(what, s string) error {
	if !snakeName.MatchString(s) {
		return errs.New(errs.InvalidInput, what+" "+quote(s)+" is not snake_case (a-z, 0-9, _; starting with a letter)")
	}
	return nil
}

func checkOneOf(flagName, v string, allowed []string) error {
	if v != "" && !slices.Contains(allowed, v) {
		return errs.New(errs.InvalidInput, "--"+flagName+" "+quote(v)+" is not one of "+strings.Join(allowed, ", "))
	}
	return nil
}

// optTexts fills fields from the non-empty entries of m.
func optTexts(fields map[string]value.Value, m map[string]string) error {
	for f, s := range m {
		if s == "" {
			continue
		}
		v, err := txt(s)
		if err != nil {
			return err
		}
		fields[f] = v
	}
	return nil
}

func (o ops) kindAdd(ctx context.Context, in KindAddIn) (RowOut, error) {
	if err := checkName("kind", in.Kind); err != nil {
		return RowOut{}, err
	}
	if err := checkOneOf("name-format", in.NameFormat, NameFormats()); err != nil {
		return RowOut{}, err
	}
	fields := map[string]value.Value{}
	if err := optTexts(fields, map[string]string{kName: in.Kind, kDescription: in.Description, kNameFormat: in.NameFormat, kRender: in.Render}); err != nil {
		return RowOut{}, err
	}
	target := targetOf(in.To)
	w, err := create(ctx, o.env, target, schema.TableKinds, map[string]string{kName: in.Kind}, fields, freshID[ids.Kind]())
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(target),
		Human: fmt.Sprintf("added kind %s %s in %s\n", short(w.Row.ID), in.Kind, place(target))}, nil
}

func (o ops) kindEdit(ctx context.Context, in KindEditIn) (RowOut, error) {
	k, err := o.kindByName(ctx, in.Kind)
	if err != nil {
		return RowOut{}, err
	}
	if err := checkOneOf("name-format", in.NameFormat, NameFormats()); err != nil {
		return RowOut{}, err
	}
	patch := map[string]value.Value{}
	if err := optTexts(patch, map[string]string{kDescription: in.Description, kNameFormat: in.NameFormat, kRender: in.Render}); err != nil {
		return RowOut{}, err
	}
	if len(patch) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "resource kind edit: nothing to change").WithHint("give --description, --name-format or --render")
	}
	return o.patchRow(ctx, k, patch, "edited kind "+text(k.Row, kName))
}

// patchRow updates one located row and prints "<what> (<short id>)".
func (o ops) patchRow(ctx context.Context, l located, patch map[string]value.Value, what string) (RowOut, error) {
	w, err := o.env.Update(ctx, engine.Ref{Store: l.Src, Table: l.Row.Table, ID: l.Row.ID}, patch)
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(l.Src), Human: fmt.Sprintf("✓ %s (%s)\n", what, short(w.Row.ID))}, nil
}

func (o ops) kindView(ctx context.Context, k located) (Kind, error) {
	fs, err := o.fieldsOf(ctx, k)
	if err != nil {
		return Kind{}, err
	}
	res, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableResources)
	if err != nil {
		return Kind{}, err
	}
	out := Kind{ID: k.Row.ID.String(), Name: text(k.Row, kName), Store: string(k.Src), Description: text(k.Row, kDescription),
		NameFormat: text(k.Row, kNameFormat), Render: text(k.Row, kRender), Fields: []Field{},
		Count: len(where(res, map[string]string{rKind: k.Row.ID.String()}))}
	for _, f := range fs {
		fv := Field{ID: f.Row.ID.String(), Name: text(f.Row, kName), Type: text(f.Row, fType), Required: flag(f.Row, fRequired),
			Sensitive: flag(f.Row, fSensitive), Internal: flag(f.Row, fInternal), Values: texts(f.Row, fValues),
			Label: text(f.Row, fLabel), Description: text(f.Row, fDescription)}
		if rk := text(f.Row, fRefKind); rk != "" {
			fv.RefKind = o.kindName(ctx, rk)
		}
		out.Fields = append(out.Fields, fv)
	}
	slices.SortFunc(out.Fields, func(a, b Field) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// kindName is a kind id's name (the id when it is not found).
func (o ops) kindName(ctx context.Context, id string) string {
	if k, err := o.kindByName(ctx, id); err == nil {
		return text(k.Row, kName)
	}
	return id
}

func (o ops) kindShow(ctx context.Context, in KindRef) (KindOut, error) {
	k, err := o.kindByName(ctx, in.Kind)
	if err != nil {
		return KindOut{}, err
	}
	v, err := o.kindView(ctx, k)
	return KindOut{Data: v}, err
}

func (o ops) kindList(ctx context.Context, _ KindsIn) (KindsOut, error) {
	ks, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableKinds)
	if err != nil {
		return KindsOut{}, err
	}
	out := KindsOut{Data: []Kind{}}
	for _, k := range ks {
		v, err := o.kindView(ctx, k)
		if err != nil {
			return KindsOut{}, err
		}
		out.Data = append(out.Data, v)
	}
	out.Count = len(out.Data)
	return out, nil
}

func (o ops) kindRename(ctx context.Context, in RenameIn) (RowOut, error) {
	if err := checkName("kind", in.New); err != nil {
		return RowOut{}, err
	}
	k, err := o.kindByName(ctx, in.Old)
	if err != nil {
		return RowOut{}, err
	}
	if _, err := o.kindByName(ctx, in.New); err == nil {
		return RowOut{}, errs.New(errs.InvalidInput, "a kind named "+quote(in.New)+" already exists")
	}
	v, err := txt(in.New)
	if err != nil {
		return RowOut{}, err
	}
	return o.patchRow(ctx, k, map[string]value.Value{kName: v}, "renamed kind "+text(k.Row, kName)+" → "+in.New+"; ids unchanged")
}

func (o ops) fieldAdd(ctx context.Context, in FieldAddIn) (RowOut, error) {
	if err := checkName("field", in.Field); err != nil {
		return RowOut{}, err
	}
	if _, err := value.ParseKind(in.Type); err != nil || in.Type == string(value.KindNull) {
		return RowOut{}, errs.New(errs.InvalidInput, "--type "+quote(in.Type)+" is not a value type").
			WithHint("one of text, number, bool, date, datetime, duration, ip, cidr, dns, url, email, json, list, ref")
	}
	k, err := o.kindByName(ctx, in.Kind)
	if err != nil {
		return RowOut{}, err
	}
	if _, err := o.fieldByName(ctx, k, in.Field); err == nil {
		return RowOut{}, errs.New(errs.InvalidInput, "kind "+in.Kind+" already has a field "+quote(in.Field))
	}
	fields := map[string]value.Value{fRequired: value.BoolOf(in.Required), fSensitive: value.BoolOf(in.Sensitive), fInternal: value.BoolOf(in.Internal)}
	if err := optTexts(fields, map[string]string{kName: in.Field, fType: in.Type, fLabel: in.Label, fDescription: in.Description}); err != nil {
		return RowOut{}, err
	}
	if fields[fKindID], err = ref(k.Row.ID); err != nil {
		return RowOut{}, err
	}
	switch {
	case in.Type == string(value.KindRef) && in.RefKind == "":
		return RowOut{}, errs.New(errs.InvalidInput, "--type ref needs --ref-kind <kind>")
	case in.RefKind != "":
		if in.Type != string(value.KindRef) {
			return RowOut{}, errs.New(errs.InvalidInput, "--ref-kind needs --type ref")
		}
		rk, rerr := o.kindByName(ctx, in.RefKind)
		if rerr != nil {
			return RowOut{}, rerr
		}
		if fields[fRefKind], err = ref(rk.Row.ID); err != nil {
			return RowOut{}, err
		}
	}
	if vs := splitList(in.Values); len(vs) > 0 {
		if fields[fValues], err = textList(vs); err != nil {
			return RowOut{}, err
		}
	}
	// The field row lives beside its kind, so the definition travels as one.
	w, err := create(ctx, o.env, k.Src, schema.TableKindFields, map[string]string{fKindID: k.Row.ID.String(), kName: in.Field}, fields, freshID[ids.KindField]())
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(k.Src),
		Human: fmt.Sprintf("added field %s %s.%s (%s)\n", short(w.Row.ID), text(k.Row, kName), in.Field, in.Type)}, nil
}

// triState folds a --x / --no-x pair into the patch.
func triState(patch map[string]value.Value, f string, on, off bool) error {
	switch {
	case on && off:
		return errs.New(errs.InvalidInput, "give --"+f+" or --no-"+f+", not both")
	case on:
		patch[f] = value.BoolOf(true)
	case off:
		patch[f] = value.BoolOf(false)
	}
	return nil
}

func (o ops) fieldEdit(ctx context.Context, in FieldEditIn) (RowOut, error) {
	k, err := o.kindByName(ctx, in.Kind)
	if err != nil {
		return RowOut{}, err
	}
	f, err := o.fieldByName(ctx, k, in.Field)
	if err != nil {
		return RowOut{}, err
	}
	patch := map[string]value.Value{}
	for _, t := range []struct {
		f       string
		on, off bool
	}{{fRequired, in.Required, in.NoRequired}, {fSensitive, in.Sensitive, in.NoSensitive}, {fInternal, in.Internal, in.NoInternal}} {
		if err := triState(patch, t.f, t.on, t.off); err != nil {
			return RowOut{}, err
		}
	}
	if err := optTexts(patch, map[string]string{fLabel: in.Label, fDescription: in.Description}); err != nil {
		return RowOut{}, err
	}
	if len(in.AddValues) > 0 || len(in.RemoveValues) > 0 {
		vals := texts(f.Row, fValues)
		for _, v := range in.AddValues {
			if !slices.Contains(vals, v) {
				vals = append(vals, v)
			}
		}
		for _, v := range in.RemoveValues {
			n, uerr := o.valueUses(ctx, k, f, v)
			if uerr != nil {
				return RowOut{}, uerr
			}
			if n > 0 {
				return RowOut{}, errs.New(errs.HasDependents, fmt.Sprintf("%s.%s value %s is used by %d resources", in.Kind, in.Field, quote(v), n)).
					WithHint("change those resources first; nothing written")
			}
			vals = slices.DeleteFunc(vals, func(x string) bool { return x == v })
		}
		if patch[fValues], err = textList(vals); err != nil {
			return RowOut{}, err
		}
	}
	if len(patch) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "resource field edit: nothing to change")
	}
	return o.patchRow(ctx, f, patch, "edited field "+in.Kind+"."+in.Field)
}

func (o ops) fieldRename(ctx context.Context, in FieldRenameIn) (RowOut, error) {
	if err := checkName("field", in.New); err != nil {
		return RowOut{}, err
	}
	k, err := o.kindByName(ctx, in.Kind)
	if err != nil {
		return RowOut{}, err
	}
	f, err := o.fieldByName(ctx, k, in.Old)
	if err != nil {
		return RowOut{}, err
	}
	if _, err := o.fieldByName(ctx, k, in.New); err == nil {
		return RowOut{}, errs.New(errs.InvalidInput, "kind "+in.Kind+" already has a field "+quote(in.New))
	}
	v, err := txt(in.New)
	if err != nil {
		return RowOut{}, err
	}
	return o.patchRow(ctx, f, map[string]value.Value{kName: v}, "renamed field "+in.Kind+"."+in.Old+" → "+in.New+"; no values changed")
}

func (o ops) fieldRemove(ctx context.Context, in FieldRemoveIn) (RowOut, error) {
	k, err := o.kindByName(ctx, in.Kind)
	if err != nil {
		return RowOut{}, err
	}
	f, err := o.fieldByName(ctx, k, in.Field)
	if err != nil {
		return RowOut{}, err
	}
	using, err := o.resourcesUsing(ctx, k, f)
	if err != nil {
		return RowOut{}, err
	}
	if len(using) > 0 && !in.Drop {
		return RowOut{}, errs.New(errs.HasDependents, fmt.Sprintf("%s.%s has values on %d rows", in.Kind, in.Field, len(using))).
			WithHint("pass --drop to remove the values in the same commit")
	}
	for _, r := range using {
		vals, verr := decodeValues(fieldsRaw(r))
		if verr != nil {
			return RowOut{}, verr
		}
		delete(vals, f.Row.ID.String())
		patch, perr := valuesPatch(vals)
		if perr != nil {
			return RowOut{}, perr
		}
		if _, err := o.env.Update(ctx, engine.Ref{Store: r.Src, Table: schema.TableResources, ID: r.Row.ID}, patch); err != nil {
			return RowOut{}, err
		}
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: f.Src, Table: schema.TableKindFields, ID: f.Row.ID}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: f.Row.ID.String(), Store: string(f.Src),
		Human: fmt.Sprintf("✓ removed field %s.%s (%s); values dropped from %d rows\n", in.Kind, in.Field, short(f.Row.ID), len(using))}, nil
}

// resourcesUsing are the live resources of kind k holding a value for f.
func (o ops) resourcesUsing(ctx context.Context, k located, f located) ([]located, error) {
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableResources)
	if err != nil {
		return nil, err
	}
	var out []located
	for _, r := range where(rows, map[string]string{rKind: k.Row.ID.String()}) {
		vals, verr := decodeValues(fieldsRaw(r))
		if verr != nil {
			return nil, verr
		}
		if _, ok := vals[f.Row.ID.String()]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// valueUses counts resources of k whose field f holds v.
func (o ops) valueUses(ctx context.Context, k, f located, v string) (int, error) {
	using, err := o.resourcesUsing(ctx, k, f)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range using {
		vals, verr := decodeValues(fieldsRaw(r))
		if verr != nil {
			return 0, verr
		}
		if display(fromJSONRaw(vals[f.Row.ID.String()])) == v {
			n++
		}
	}
	return n, nil
}
