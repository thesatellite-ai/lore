package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Resource operations (CLI_REFERENCE 3.2, spec 9.6): generic ops with the
// kind as an argument; a resource row holds its values in `fields`, a JSON
// object keyed by the fld_ id of each kind field, so renaming a field never
// touches a row.
const (
	OpResAdd     = "resource.add"
	OpResEdit    = "resource.edit"
	OpResShow    = "resource.show"
	OpResList    = "resource.list"
	OpResDelete  = "resource.delete"
	OpResArchive = "resource.archive"
	OpResCheck   = "resource.check"
	OpResStale   = "resource.stale"
)

// resources field names (schema.Builtin).
const (
	rKind        = "kind"
	rName        = "name"
	rFields      = "fields"
	rArchivedAt  = "archived_at"
	rLastChecked = "last_checked"
)

// Limits and words of the resource ops.
const (
	maxResourceName = 128
	setSep          = "="
	maskedValue     = "•••"
	hoursPerDay     = 24
	dateOnly        = "2006-01-02"
)

// snakeName is a snake_case name (kinds, fields, resource names, keys).
var snakeName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ResAddIn is the input of resource.add.
type ResAddIn struct {
	Kind string   `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Name string   `json:"name" arg:"1" desc:"the resource name (snake_case, or DNS / IP under the kind's name format)"`
	Set  []string `json:"set,omitempty" flag:"set" desc:"field=value (repeatable; ref fields take resource names)"`
	To   string   `json:"to,omitempty" flag:"to" desc:"where the row is written (repo, <store>, <store>/<folder>; default repo)"`
}

// ResEditIn is the input of resource.edit.
type ResEditIn struct {
	Name  string   `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
	Set   []string `json:"set,omitempty" flag:"set" desc:"field=value (repeatable)"`
	Unset []string `json:"unset,omitempty" flag:"unset" desc:"field to remove (repeatable)"`
}

// ResRef names one resource.
type ResRef struct {
	Name          string `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
	ShowSensitive bool   `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"print sensitive values (asks first)"`
}

// ResDeleteIn is the input of resource.delete.
type ResDeleteIn struct {
	Name   string `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why (recorded in the audit log)"`
}

// ResNameIn names one resource for archive and check.
type ResNameIn struct {
	Name string `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
}

// ResListIn is the input of resource.list.
type ResListIn struct {
	Kind          string   `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Where         []string `json:"where,omitempty" flag:"where" desc:"field=value filter (repeatable; all must match)"`
	Archived      bool     `json:"archived,omitempty" flag:"archived" desc:"include archived resources"`
	ShowSensitive bool     `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"print sensitive values (asks first)"`
}

// ResStaleIn is the input of resource.stale.
type ResStaleIn struct {
	Days int `json:"days,omitempty" flag:"days" desc:"older than this many days (default 60)"`
}

// defaultStaleDays is resource.stale's --days default (the samples' 60;
// CLI_REFERENCE finding F13 names no default).
const defaultStaleDays = 60

// Resource is one resource as shown: values keyed by field NAME.
type Resource struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Store       string            `json:"store"`
	Values      map[string]string `json:"values"`
	ArchivedAt  string            `json:"archived_at,omitempty"`
	LastChecked string            `json:"last_checked,omitempty"`
}

// ResOut is the output of resource.show.
type ResOut struct {
	Data Resource `json:"data"`
}

// Text implements op.Texter.
func (o ResOut) Text() string {
	r := o.Data
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s:%s/%s\n", r.ID, r.Kind, r.Store, r.Kind, r.Name)
	keys := make([]string, 0, len(r.Values))
	for k := range r.Values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s = %s\n", k, r.Values[k])
	}
	if r.LastChecked != "" {
		fmt.Fprintf(&b, "  checked %s\n", r.LastChecked)
	}
	if r.ArchivedAt != "" {
		fmt.Fprintf(&b, "  archived %s\n", r.ArchivedAt)
	}
	return b.String()
}

// ResListOut is the output of resource.list and resource.stale.
type ResListOut struct {
	Count int        `json:"count"`
	Data  []Resource `json:"data"`
	// columns are the field names printed by Text (resource.list: the
	// kind's fields; stale: none).
	columns []string
	stale   bool
}

// Text implements op.Texter: the CLI_REFERENCE 3.2 tables.
func (o ResListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no resources)\n"
	}
	var b strings.Builder
	if o.stale {
		b.WriteString("KIND\tNAME\tLAST_CHECKED\tAGE\n")
		for _, r := range o.Data {
			age := "never"
			if t, err := time.Parse(time.RFC3339Nano, r.LastChecked); err == nil {
				age = strconv.Itoa(int(time.Since(t).Hours()/hoursPerDay)) + "d"
			}
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.Kind, r.Name, dayOf(r.LastChecked), age)
		}
		return b.String()
	}
	head := []string{"KIND", "NAME"}
	for _, c := range o.columns {
		head = append(head, strings.ToUpper(c))
	}
	b.WriteString(strings.Join(append(head, "CHECKED"), "\t") + "\n")
	for _, r := range o.Data {
		cells := []string{r.Kind, r.Name}
		for _, c := range o.columns {
			cells = append(cells, r.Values[c])
		}
		b.WriteString(strings.Join(append(cells, dayOf(r.LastChecked)), "\t") + "\n")
	}
	return b.String()
}

// dayOf is the date part of an RFC 3339 time ("" stays "").
func dayOf(s string) string {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(dateOnly)
	}
	return s
}

func (o ops) resourceOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpResAdd, Summary: "Add a resource of a kind", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceInvalid, errs.KindUnknown), Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "add a host", CLI: "lore resource add host hub_prod --set provider=hetzner", Want: "res_"}}}, o.resAdd),
		op.Define(op.Spec{Name: OpResEdit, Summary: "Edit a resource's values (stamps last_checked)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceInvalid), Confirm: op.ConfirmRule{MayAsk: true}}, o.resEdit),
		op.Define(op.Spec{Name: OpResShow, Summary: "Show one resource", Permission: op.PermRead, Access: op.AccessRead,
			Errors: append(slices.Clone(readErrors), errs.NeedsConfirm), Confirm: op.ConfirmRule{When: "show_sensitive"}}, o.resShow),
		op.Define(op.Spec{Name: OpResList, Summary: "List resources of a kind", Permission: op.PermRead, Access: op.AccessRead,
			Errors: append(slices.Clone(readErrors), errs.KindUnknown, errs.NeedsConfirm), Confirm: op.ConfirmRule{When: "show_sensitive"}}, o.resList),
		op.Define(op.Spec{Name: OpResDelete, Summary: "Delete a resource (a tombstone is kept)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.HasDependents), Confirm: op.ConfirmRule{Always: true}}, o.resDelete),
		op.Define(op.Spec{Name: OpResArchive, Summary: "Archive a resource (refs stay; new refs are refused)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors}, o.resArchive),
		op.Define(op.Spec{Name: OpResCheck, Summary: "Stamp last_checked: a person or agent looked and the facts are right", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors}, o.resCheck),
		op.Define(op.Spec{Name: OpResStale, Summary: "Resources whose last_checked is older than --days, or never set", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.resStale),
	}
}

// decodeValues reads a resource's fields object (fld_ id → JSON value).
func decodeValues(raw []byte) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errs.Wrap(errs.ConfigInvalid, "resource: fields is not a JSON object", err)
	}
	return out, nil
}

// valuesPatch builds the `fields` patch from a values map.
func valuesPatch(vals map[string]json.RawMessage) (map[string]value.Value, error) {
	b, err := json.Marshal(vals)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "resource: encode fields", err)
	}
	j, err := value.JSONOf(b)
	if err != nil {
		return nil, err
	}
	return map[string]value.Value{rFields: j}, nil
}

// fromJSONRaw infers a stored value (nil when it is not JSON).
func fromJSONRaw(raw json.RawMessage) value.Value {
	v, err := value.Infer(raw)
	if err != nil {
		return nil
	}
	return v
}

// kindDef is a kind with its fields indexed by name.
type kindDef struct {
	kind   located
	byName map[string]located
	byID   map[string]located
}

func (o ops) kindDef(ctx context.Context, key string) (kindDef, error) {
	k, err := o.kindByName(ctx, key)
	if err != nil {
		return kindDef{}, err
	}
	fs, err := o.fieldsOf(ctx, k)
	if err != nil {
		return kindDef{}, err
	}
	d := kindDef{kind: k, byName: map[string]located{}, byID: map[string]located{}}
	for _, f := range fs {
		d.byName[text(f.Row, kName)] = f
		d.byID[f.Row.ID.String()] = f
	}
	return d, nil
}

func (d kindDef) name() string { return text(d.kind.Row, kName) }

func (d kindDef) fieldNames() []string {
	out := make([]string, 0, len(d.byName))
	for n := range d.byName {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func invalid(d kindDef, name, msg string) error {
	return errs.New(errs.ResourceInvalid, d.name()+" "+name+": "+msg).WithHint("lore resource kind show " + d.name())
}

// applySets parses --set field=value pairs into vals (keyed by fld_ id).
func (o ops) applySets(ctx context.Context, d kindDef, name string, sets []string, vals map[string]json.RawMessage) error {
	for _, s := range sets {
		f, raw, ok := strings.Cut(s, setSep)
		if !ok || f == "" {
			return errs.New(errs.InvalidInput, "--set "+quote(s)+": want field=value")
		}
		if raw == "" {
			return errs.New(errs.InvalidInput, "--set "+f+"=: an empty value is refused").WithHint("use --unset " + f)
		}
		def, ok := d.byName[f]
		if !ok {
			return invalid(d, name, "unknown field "+f+" (fields: "+strings.Join(d.fieldNames(), ", ")+")")
		}
		v, err := o.fieldValue(ctx, d, name, def, raw)
		if err != nil {
			return err
		}
		b, err := v.MarshalJSON()
		if err != nil {
			return errs.Wrap(errs.Internal, "resource: encode "+f, err)
		}
		vals[def.Row.ID.String()] = b
	}
	return nil
}

// fieldValue parses raw by the field's type; a ref takes a resource name
// (or res_ id) of the ref kind and stores its id.
func (o ops) fieldValue(ctx context.Context, d kindDef, name string, def located, raw string) (value.Value, error) {
	fname := text(def.Row, kName)
	if vs := texts(def.Row, fValues); len(vs) > 0 && !slices.Contains(vs, raw) {
		return nil, invalid(d, name, fname+" "+quote(raw)+" is not one of "+strings.Join(vs, ", "))
	}
	k, err := value.ParseKind(text(def.Row, fType))
	if err != nil {
		return nil, errs.Wrap(errs.ConfigInvalid, "kind field "+def.Row.ID.String()+" has an unknown type", err)
	}
	if k == value.KindRef {
		target, rerr := o.resourceByName(ctx, raw, text(def.Row, fRefKind))
		if rerr != nil {
			return nil, invalid(d, name, fname+": "+rerr.Error())
		}
		if text(target.Row, rArchivedAt) != "" {
			return nil, invalid(d, name, fname+": "+raw+" is archived")
		}
		return ref(target.Row.ID)
	}
	v, err := parseAs(k, raw)
	if err != nil {
		return nil, invalid(d, name, fname+" "+quote(raw)+" is not a "+string(k))
	}
	return v, nil
}

// resourceByName finds a live resource by name or id, optionally of one
// kind id.
func (o ops) resourceByName(ctx context.Context, key, kindID string) (located, error) {
	var extra map[string]string
	if kindID != "" {
		extra = map[string]string{rKind: kindID}
	}
	return byNameOrID(ctx, o.env, o.env.Stores(), schema.TableResources, rName, key, extra)
}

// requireFields refuses a resource missing a required field.
func requireFields(d kindDef, name string, vals map[string]json.RawMessage) error {
	for _, n := range d.fieldNames() {
		f := d.byName[n]
		if _, ok := vals[f.Row.ID.String()]; flag(f.Row, fRequired) && !ok {
			return invalid(d, name, "required field "+n+" is missing")
		}
	}
	return nil
}

func checkResourceName(d kindDef, name string) error {
	if len(name) > maxResourceName {
		return invalid(d, name, "a name has at most "+strconv.Itoa(maxResourceName)+" characters")
	}
	var err error
	switch text(d.kind.Row, kNameFormat) {
	case NameFormatDNS:
		_, err = value.DNSOf(name)
	case NameFormatIP:
		_, err = value.IPOf(name)
	case NameFormatText:
		_, err = value.TextOf(name)
	default:
		if !snakeName.MatchString(name) {
			err = errs.New(errs.InvalidInput, "not snake_case")
		}
	}
	if err != nil {
		return invalid(d, name, "the name does not fit the kind's name format")
	}
	return nil
}

func (o ops) resAdd(ctx context.Context, in ResAddIn) (RowOut, error) {
	d, err := o.kindDef(ctx, in.Kind)
	if err != nil {
		return RowOut{}, err
	}
	if err = checkResourceName(d, in.Name); err != nil {
		return RowOut{}, err
	}
	target := targetOf(in.To)
	if string(target) == storeMe {
		return RowOut{}, errs.New(errs.InvalidInput, "--to me: resources are team facts, not personal ones").WithHint("--to repo or --to <store>[/<folder>]")
	}
	rows, err := liveRows(ctx, o.env, []store.Source{target}, schema.TableResources)
	if err != nil {
		return RowOut{}, err
	}
	if hit := where(rows, map[string]string{rKind: d.kind.Row.ID.String(), rName: in.Name}); len(hit) > 0 {
		return RowOut{}, errs.New(errs.NameTaken, d.name()+" "+in.Name+" already exists in "+string(target)+" ("+hit[0].Row.ID.String()+")")
	}
	vals := map[string]json.RawMessage{}
	if err = o.applySets(ctx, d, in.Name, in.Set, vals); err != nil {
		return RowOut{}, err
	}
	if err = requireFields(d, in.Name, vals); err != nil {
		return RowOut{}, err
	}
	fields, err := valuesPatch(vals)
	if err != nil {
		return RowOut{}, err
	}
	if fields[rKind], err = ref(d.kind.Row.ID); err != nil {
		return RowOut{}, err
	}
	if fields[rName], err = txt(in.Name); err != nil {
		return RowOut{}, err
	}
	w, err := o.env.Create(ctx, target, schema.TableResources, fields)
	if err != nil {
		return RowOut{}, err
	}
	human := fmt.Sprintf("created %s  %s  %s:%s/%s\n", short(w.Row.ID), d.name(), scopeOf(target), d.name(), in.Name)
	if line := o.valuesLine(ctx, d, vals, false); line != "" {
		human += "  " + line + "\n"
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(target), Human: human}, nil
}

// valuesLine prints "f = v; …" (sensitive values masked unless shown).
func (o ops) valuesLine(ctx context.Context, d kindDef, vals map[string]json.RawMessage, show bool) string {
	m := o.named(ctx, d, vals, show)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" = "+m[k])
	}
	return strings.Join(parts, "; ")
}

// named turns fld_-keyed values into name-keyed display text.
func (o ops) named(ctx context.Context, d kindDef, vals map[string]json.RawMessage, show bool) map[string]string {
	out := map[string]string{}
	for id, raw := range vals {
		f, ok := d.byID[id]
		name := id
		if ok {
			name = text(f.Row, kName)
		}
		v := fromJSONRaw(raw)
		s := display(v)
		switch {
		case ok && flag(f.Row, fSensitive) && !show:
			s = maskedValue
		case ok && text(f.Row, fType) == string(value.KindRef):
			if r, err := o.resourceByName(ctx, s, ""); err == nil {
				s = text(r.Row, rName)
			}
		}
		out[name] = s
	}
	return out
}

func (o ops) resEdit(ctx context.Context, in ResEditIn) (RowOut, error) {
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return RowOut{}, err
	}
	d, err := o.kindDef(ctx, text(r.Row, rKind))
	if err != nil {
		return RowOut{}, err
	}
	if len(in.Set) == 0 && len(in.Unset) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "resource edit: nothing to change").WithHint("give --set field=value or --unset field")
	}
	vals, err := decodeValues(fieldsRaw(r))
	if err != nil {
		return RowOut{}, err
	}
	name := text(r.Row, rName)
	if err = o.applySets(ctx, d, name, in.Set, vals); err != nil {
		return RowOut{}, err
	}
	for _, f := range in.Unset {
		def, ok := d.byName[f]
		if !ok {
			return RowOut{}, invalid(d, name, "unknown field "+f)
		}
		delete(vals, def.Row.ID.String())
	}
	if err = requireFields(d, name, vals); err != nil {
		return RowOut{}, err
	}
	patch, err := valuesPatch(vals)
	if err != nil {
		return RowOut{}, err
	}
	patch[rLastChecked] = value.DateTimeFrom(time.Now())
	return o.patchRow(ctx, r, patch, "edited "+d.name()+" "+name)
}

// fieldsRaw is a resource row's `fields` JSON bytes.
func fieldsRaw(r located) []byte {
	switch v := r.Row.Fields[rFields].(type) {
	case value.JSON:
		return v.Raw()
	case nil:
		return nil
	default:
		b, err := v.MarshalJSON()
		if err != nil {
			return nil
		}
		return b
	}
}

func (o ops) view(ctx context.Context, r located, show bool) (Resource, error) {
	d, err := o.kindDef(ctx, text(r.Row, rKind))
	if err != nil {
		return Resource{}, err
	}
	vals, err := decodeValues(fieldsRaw(r))
	if err != nil {
		return Resource{}, err
	}
	return Resource{ID: r.Row.ID.String(), Kind: d.name(), Name: text(r.Row, rName), Store: string(r.Src),
		Values: o.named(ctx, d, vals, show), ArchivedAt: text(r.Row, rArchivedAt), LastChecked: text(r.Row, rLastChecked)}, nil
}

func (o ops) resShow(ctx context.Context, in ResRef) (ResOut, error) {
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return ResOut{}, err
	}
	v, err := o.view(ctx, r, in.ShowSensitive)
	return ResOut{Data: v}, err
}

func (o ops) resList(ctx context.Context, in ResListIn) (ResListOut, error) {
	d, err := o.kindDef(ctx, in.Kind)
	if err != nil {
		return ResListOut{}, err
	}
	filters := map[string]string{}
	for _, w := range in.Where {
		f, v, ok := strings.Cut(w, setSep)
		if !ok {
			return ResListOut{}, errs.New(errs.InvalidInput, "--where "+quote(w)+": want field=value")
		}
		if _, known := d.byName[f]; !known {
			return ResListOut{}, invalid(d, "--where", "unknown field "+f)
		}
		filters[f] = v
	}
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableResources)
	if err != nil {
		return ResListOut{}, err
	}
	out := ResListOut{Data: []Resource{}, columns: d.fieldNames()}
	for _, r := range where(rows, map[string]string{rKind: d.kind.Row.ID.String()}) {
		if text(r.Row, rArchivedAt) != "" && !in.Archived {
			continue
		}
		v, verr := o.view(ctx, r, in.ShowSensitive)
		if verr != nil {
			return ResListOut{}, verr
		}
		keep := true
		for f, want := range filters {
			if v.Values[f] != want {
				keep = false
			}
		}
		if keep {
			out.Data = append(out.Data, v)
		}
	}
	slices.SortFunc(out.Data, func(a, b Resource) int { return strings.Compare(a.Name, b.Name) })
	out.Count = len(out.Data)
	return out, nil
}

func (o ops) resDelete(ctx context.Context, in ResDeleteIn) (RowOut, error) {
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return RowOut{}, err
	}
	referrers, err := o.referrers(ctx, r.Row.ID)
	if err != nil {
		return RowOut{}, err
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: r.Src, Table: schema.TableResources, ID: r.Row.ID}); err != nil {
		return RowOut{}, err
	}
	human := fmt.Sprintf("✓ %s (%s) deleted\n", short(r.Row.ID), text(r.Row, rName))
	if len(referrers) > 0 {
		human += "  still pointing at it: " + strings.Join(referrers, ", ") + "; they show the old id until you repoint them\n"
	}
	return RowOut{ID: r.Row.ID.String(), Store: string(r.Src), Human: human}, nil
}

// referrers names the live resources holding a ref to id.
func (o ops) referrers(ctx context.Context, id ids.AnyID) ([]string, error) {
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableResources)
	if err != nil {
		return nil, err
	}
	needle := quote(id.String())
	var out []string
	for _, r := range rows {
		if r.Row.ID != id && strings.Contains(string(fieldsRaw(r)), needle) {
			out = append(out, text(r.Row, rName))
		}
	}
	return out, nil
}

func (o ops) resArchive(ctx context.Context, in ResNameIn) (RowOut, error) {
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return RowOut{}, err
	}
	return o.patchRow(ctx, r, map[string]value.Value{rArchivedAt: value.DateTimeFrom(time.Now())}, "archived "+text(r.Row, rName))
}

func (o ops) resCheck(ctx context.Context, in ResNameIn) (RowOut, error) {
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return RowOut{}, err
	}
	return o.patchRow(ctx, r, map[string]value.Value{rLastChecked: value.DateTimeFrom(time.Now())}, "checked "+text(r.Row, rName)+" now")
}

func (o ops) resStale(ctx context.Context, in ResStaleIn) (ResListOut, error) {
	days := in.Days
	if days <= 0 {
		days = defaultStaleDays
	}
	cut := time.Now().Add(-time.Duration(days) * hoursPerDay * time.Hour)
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableResources)
	if err != nil {
		return ResListOut{}, err
	}
	out := ResListOut{Data: []Resource{}, stale: true}
	for _, r := range rows {
		if text(r.Row, rArchivedAt) != "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, text(r.Row, rLastChecked)); err == nil && t.After(cut) {
			continue
		}
		v, verr := o.view(ctx, r, false)
		if verr != nil {
			return ResListOut{}, verr
		}
		out.Data = append(out.Data, v)
	}
	out.Count = len(out.Data)
	return out, nil
}
