package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/filter"
	"github.com/thesatellite-ai/lore/logic/sortspec"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
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
	rReplacedBy  = "replaced_by"
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
	resValueFlags
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags
}

// resValueFlags are the two other ways to give a value (RES-OQ4).
type resValueFlags struct {
	SetJSON  []string `json:"set_json,omitempty" flag:"set-json" desc:"field=<json> (repeatable; for list and json fields)"`
	SetStdin string   `json:"set_stdin,omitempty" flag:"set-stdin" desc:"read this field's value from stdin (keeps it out of shell history)"`
}

// ResEditIn is the input of resource.edit.
type ResEditIn struct {
	Name  string   `json:"id,omitempty" arg:"0" desc:"the resource (name or res_ id); leave out for the bulk form --where"`
	Set   []string `json:"set,omitempty" flag:"set" desc:"field=value (repeatable)"`
	Unset []string `json:"unset,omitempty" flag:"unset" desc:"field to remove (repeatable)"`
	bulkWhere
	Archive bool `json:"archive,omitempty" flag:"archive" desc:"bulk: archive every matching resource"`
	resValueFlags
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags
}

// ResRef names one resource.
type ResRef struct {
	Name          string `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
	ShowSensitive bool   `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"print sensitive values (asks first)"`
}

// ResDeleteIn is the input of resource.delete: one resource by name, or
// the bulk form --kind with --where (CLI_REFERENCE 3.2, XQ1, XQ2).
type ResDeleteIn struct {
	Name   string   `json:"id,omitempty" arg:"0" desc:"the resource (name or res_ id); leave out for the bulk form --kind --where"`
	Reason string   `json:"reason,omitempty" flag:"reason" desc:"why (recorded in the audit log)"`
	Kind   string   `json:"kind,omitempty" flag:"kind" desc:"bulk: delete every resource of this kind matching --where (one batch)"`
	Where  []string `json:"where,omitempty" flag:"where" desc:"bulk: filter on a field, as resource list --where (repeatable; all must match)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"bulk: list what would be deleted; write nothing"`
}

// ResDeleteOut is the output of resource.delete: the one row's id and
// store (the single form, unchanged), or the batch and its rows (bulk).
type ResDeleteOut struct {
	ID     string   `json:"id,omitempty"`
	Store  string   `json:"store,omitempty"`
	Batch  string   `json:"batch,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
	Count  int      `json:"count,omitempty"`
	IDs    []string `json:"ids,omitempty"`
	Human  string   `json:"-"`
}

// Text implements op.Texter.
func (o ResDeleteOut) Text() string { return o.Human }

// ResNameIn names one resource for archive and check.
type ResNameIn struct {
	Name string `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
}

// ResListIn is the input of resource.list.
type ResListIn struct {
	Kind          string   `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Where         []string `json:"where,omitempty" flag:"where" desc:"filter on a field: = != ~= > >= < <= ? !? or field:a,b (repeatable; all must match)"`
	Sort          []string `json:"sort,omitempty" flag:"sort" desc:"sort by name, last_checked, archived_at or a field: <key>, <key>:asc or <key>:desc (repeatable; default name)"`
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
	CheckedBy   string            `json:"checked_by,omitempty"`
	// ReplacedBy is "<kind> <name> (<res_ id>)" of the resource repoint put
	// in this one's place (RESOURCES 3.20); "" when none.
	ReplacedBy string `json:"replaced_by,omitempty"`
	// Conflicts are the fields a merge left for a person (by field name).
	Conflicts []Conflict `json:"conflicts,omitempty"`
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
	for _, c := range r.Conflicts {
		fmt.Fprintf(&b, "  %s CONFLICT  ours %s | theirs %s | base %s  (settle: lore resource edit %s --set %s=…)\n", c.Field, c.Ours, c.Theirs, c.Base, r.Name, c.Field)
	}
	if r.LastChecked != "" {
		fmt.Fprintf(&b, "  checked %s%s\n", r.LastChecked, byWho(r.CheckedBy))
	}
	if r.ArchivedAt != "" {
		fmt.Fprintf(&b, "  archived %s\n", r.ArchivedAt)
	}
	if r.ReplacedBy != "" {
		fmt.Fprintf(&b, "  replaced by  %s\n", r.ReplacedBy)
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

// byWho is " (by <how>)" for a checked_by value, "" when unset (RQ8).
func byWho(by string) string {
	if by == "" {
		return ""
	}
	return " (by " + by + ")"
}

// dayOf is the date part of an RFC 3339 time ("" stays "").
func dayOf(s string) string {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(dateOnly)
	}
	return s
}

func (o ops) resourceOps() []op.Entry {
	return append([]op.Entry{
		op.Define(op.Spec{Name: OpResAdd, Summary: "Add a resource of a kind", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceInvalid, errs.KindUnknown, errs.ResourcePlace), Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "add a host", CLI: "lore resource add host hub_prod --set provider=hetzner", Want: "res_"}}}, o.resAdd),
		op.Define(op.Spec{Name: OpResEdit, Summary: "Edit a resource's values (stamps last_checked)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceInvalid, errs.KindUnknown), Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "bulk edit", CLI: "lore resource edit --kind service --where runs_on=hub_prod --set runs_on=hub_prod_2 --yes", Want: "batch bat_"}}}, o.resEdit),
		op.Define(op.Spec{Name: OpResShow, Summary: "Show one resource", Permission: op.PermRead, Access: op.AccessRead,
			// XQ3: --show-sensitive always confirms; without a terminal it needs --yes.
			Errors: append(slices.Clone(readErrors), errs.NeedsConfirm), Confirm: op.ConfirmRule{When: "show_sensitive"}}, o.resShow),
		op.Define(op.Spec{Name: OpResList, Summary: "List resources of a kind", Permission: op.PermRead, Access: op.AccessRead,
			Errors: append(slices.Clone(readErrors), errs.KindUnknown, errs.NeedsConfirm), Confirm: op.ConfirmRule{When: "show_sensitive"}}, o.resList),
		// XQ2: MayAsk, not Always: the bulk form must count its rows before
		// it may ask (a preview above batch.preview_rows); the one-row form
		// still always asks, in resDelete (bulk.Ask).
		op.Define(op.Spec{Name: OpResDelete, Summary: "Delete a resource, or every resource of a kind matching --where (a tombstone is kept)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.HasDependents, errs.KindUnknown), Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "bulk delete without a terminal", CLI: "lore resource delete --kind host --where provider=linode --yes", Want: "batch bat_"}}}, o.resDelete),
		op.Define(op.Spec{Name: OpResArchive, Summary: "Archive a resource (refs stay; new refs are refused)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors}, o.resArchive),
		op.Define(op.Spec{Name: OpResCheck, Summary: "Stamp last_checked: a person or agent looked and the facts are right", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.KindUnknown), Confirm: op.ConfirmRule{MayAsk: true}}, o.resCheck),
		op.Define(op.Spec{Name: OpResStale, Summary: "Resources whose last_checked is older than --days, or never set", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.resStale),
	}, o.resourceIOOps()...) // import, export, repoint, clone (resources_io.go)
}

// fieldEntry is one entry of a resource's `fields` object, the stored
// shape of STORE_SERVER_SPEC 4.4:
//
//	"fields": { "fld_…": { "name": "ip", "v": "203.0.113.10" } }
//
// keyed by the field's permanent fld_ id (a rename keeps the key, so the
// value survives), with the field's name carried beside the value so a row
// file reads on its own. The cache projection (adapters/cache/sqlite
// project.go entryValue) reads exactly this shape; a flat {fld: value}
// object would project as NULL columns, which is why a reader refuses it.
type fieldEntry struct {
	Name string          `json:"name"`
	V    json.RawMessage `json:"v"`
}

// resValues is a decoded `fields` object: the values by fld_ id (what every
// reader works on) and the stored name of each entry (kept for an id the
// kind no longer lists, so a re-write never loses it).
type resValues struct {
	vals  map[string]json.RawMessage
	names map[string]string
	// conflicts are the entries a strict merge clash left for a person
	// (logic/merge entries_by_id, RESOURCES 3.13/R4): {"name", "strict":
	// true, "conflict": {base, ours, theirs}} with no "v". The field reads
	// as unset; a re-write keeps the entry as is until --set (or --unset)
	// on that field settles it.
	conflicts map[string]json.RawMessage
}

// Conflict is one unsettled field of a resource as shown: the three sides
// of a strict merge clash, each as display text (sensitive ones masked).
type Conflict struct {
	Field  string `json:"field"`
	Base   string `json:"base"`
	Ours   string `json:"ours"`
	Theirs string `json:"theirs"`
}

// conflictBody is the "conflict" member of a conflict entry.
type conflictBody struct {
	Base   json.RawMessage `json:"base"`
	Ours   json.RawMessage `json:"ours"`
	Theirs json.RawMessage `json:"theirs"`
}

// entryConflictKey is the member of a conflict entry (logic/merge).
const entryConflictKey = "conflict"

// decodeValues reads resource r's `fields` object. Any entry that is not
// {name, v} (a flat value, a missing "v") is E_RESOURCE_INVALID naming the
// row: showing it as unset would hide a broken row.
func decodeValues(r located) (resValues, error) {
	out := resValues{vals: map[string]json.RawMessage{}, names: map[string]string{}, conflicts: map[string]json.RawMessage{}}
	raw := fieldsRaw(r)
	if len(raw) == 0 {
		return out, nil
	}
	bad := func(why string, err error) error {
		e := errs.New(errs.ResourceInvalid, "resource "+r.Row.ID.String()+" ("+text(r.Row, rName)+"): fields "+why)
		if err != nil {
			e = errs.Wrap(errs.ResourceInvalid, e.Error(), err)
		}
		return e.WithHint(`each entry must be {"name": <field>, "v": <value>} (STORE_SERVER_SPEC 4.4)`)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return resValues{}, bad("is not a JSON object", err)
	}
	for id, e := range entries {
		var probe map[string]json.RawMessage
		if json.Unmarshal(e, &probe) != nil {
			return resValues{}, bad("entry "+id+" is not a {name, v} object", nil)
		}
		v, hasV := probe[entryValueKey]
		var name string
		if _, isConflict := probe[entryConflictKey]; isConflict && !hasV {
			if err := json.Unmarshal(probe[entryNameKey], &name); err != nil || name == "" {
				return resValues{}, bad("conflict entry "+id+" lacks its name", nil)
			}
			out.conflicts[id], out.names[id] = e, name
			continue
		}
		if err := json.Unmarshal(probe[entryNameKey], &name); err != nil || !hasV || name == "" {
			return resValues{}, bad("entry "+id+" lacks its name or value", nil)
		}
		out.vals[id], out.names[id] = v, name
	}
	return out, nil
}

// Keys of one fieldEntry (the json tags above).
const (
	entryNameKey  = "name"
	entryValueKey = "v"
)

// valuesPatch builds the `fields` patch from vals in the spec shape: each
// entry named after d's field with that fld_ id, or, for an id d no longer
// lists, the name the row stored (prev); an id with neither is a bug of the
// caller and refused.
//
// Conflict entries of prev whose field vals does not set are kept as they
// are (a merge clash stays for a person until that field is set).
func valuesPatch(d kindDef, vals map[string]json.RawMessage, prev resValues) (map[string]value.Value, error) {
	entries := make(map[string]json.RawMessage, len(vals)+len(prev.conflicts))
	for id, c := range prev.conflicts {
		if _, set := vals[id]; !set {
			entries[id] = c
		}
	}
	for id, v := range vals {
		name := prev.names[id]
		if f, ok := d.byID[id]; ok {
			name = text(f.Row, kName)
		}
		if name == "" {
			return nil, errs.New(errs.Internal, "resource: no field name for "+id)
		}
		e, err := json.Marshal(fieldEntry{Name: name, V: v})
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "resource: encode field "+id, err)
		}
		entries[id] = e
	}
	b, err := json.Marshal(entries)
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
	if k == value.KindSecretRef {
		sr, serr := secretRefValue(raw)
		if serr != nil {
			return nil, errs.Wrap(errs.CodeOf(serr), d.name()+" "+name+": field "+fname, serr)
		}
		return sr, nil
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
		return RowOut{}, errs.New(errs.ResourcePlace, "--to me: resources are team facts, not personal ones").WithHint("--to repo or --to <store>[/<folder>]")
	}
	// RQ2: resources.allowed_in names the places a resource may live.
	if err = checkPlace(ctx, o.env, target); err != nil {
		return RowOut{}, err
	}
	sets, err := valueSets(in.Set, in.SetJSON, in.SetStdin)
	if err != nil {
		return RowOut{}, err
	}
	rows, err := liveRows(ctx, o.env, []store.Source{target}, schema.TableResources)
	if err != nil {
		return RowOut{}, err
	}
	if hit := where(rows, map[string]string{rKind: d.kind.Row.ID.String(), rName: in.Name}); len(hit) > 0 {
		return RowOut{}, errs.New(errs.NameTaken, d.name()+" "+in.Name+" already exists in "+string(target)+" ("+hit[0].Row.ID.String()+")")
	}
	vals := map[string]json.RawMessage{}
	if err = o.applySets(ctx, d, in.Name, sets, vals); err != nil {
		return RowOut{}, err
	}
	if err = requireFields(d, in.Name, vals); err != nil {
		return RowOut{}, err
	}
	fields, err := valuesPatch(d, vals, resValues{})
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
	human += sizeWarning(ctx, o.env, target, w.Row.Fields) // RQ4
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
	if in.bulk() || in.Archive {
		if in.Name != "" {
			return RowOut{}, errs.New(errs.InvalidInput, "resource edit: name one resource or use --kind with --where, not both")
		}
		return o.resEditWhere(ctx, in)
	}
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return RowOut{}, err
	}
	d, err := o.kindDef(ctx, text(r.Row, rKind))
	if err != nil {
		return RowOut{}, err
	}
	sets, err := valueSets(in.Set, in.SetJSON, in.SetStdin)
	if err != nil {
		return RowOut{}, err
	}
	if len(sets) == 0 && len(in.Unset) == 0 && in.Empty() {
		return RowOut{}, errs.New(errs.InvalidInput, "resource edit: nothing to change").WithHint("give --set field=value, --unset field or --meta key=value")
	}
	stored, err := decodeValues(r)
	if err != nil {
		return RowOut{}, err
	}
	vals := stored.vals
	name := text(r.Row, rName)
	if err = o.applySets(ctx, d, name, sets, vals); err != nil {
		return RowOut{}, err
	}
	for _, f := range in.Unset {
		def, ok := d.byName[f]
		if !ok {
			return RowOut{}, invalid(d, name, "unknown field "+f)
		}
		delete(vals, def.Row.ID.String())
		delete(stored.conflicts, def.Row.ID.String()) // --unset settles a clash too
	}
	if err = requireFields(d, name, vals); err != nil {
		return RowOut{}, err
	}
	patch, err := valuesPatch(d, vals, stored)
	if err != nil {
		return RowOut{}, err
	}
	// RQ8: every edit stamps last_checked, and records that an edit did it.
	patch[rLastChecked] = value.DateTimeFrom(opclock.Now(o.env))
	if patch[rCheckedBy], err = txt(CheckedEdit); err != nil {
		return RowOut{}, err
	}
	out, err := o.patchRow(ctx, r, patch, "edited "+d.name()+" "+name)
	if err == nil {
		all := maps.Clone(r.Row.Fields)
		maps.Copy(all, patch)
		out.Human += sizeWarning(ctx, o.env, r.Src, all) // RQ4
	}
	return out, err
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
	stored, err := decodeValues(r)
	if err != nil {
		return Resource{}, err
	}
	vals := stored.vals
	return Resource{ID: r.Row.ID.String(), Kind: d.name(), Name: text(r.Row, rName), Store: string(r.Src),
		Values: o.named(ctx, d, vals, show), ArchivedAt: text(r.Row, rArchivedAt), LastChecked: text(r.Row, rLastChecked),
		CheckedBy: text(r.Row, rCheckedBy), Conflicts: o.conflictsView(d, stored, show), ReplacedBy: o.successor(ctx, r)}, nil
}

// successor is "<kind> <name> (<id>)" of r's replaced_by, the bare id when
// that row is not visible here, "" when unset (RESOURCES 3.20).
func (o ops) successor(ctx context.Context, r located) string {
	id := text(r.Row, rReplacedBy)
	if id == "" {
		return ""
	}
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableResources)
	if err != nil {
		return id
	}
	for _, s := range rows {
		if s.Row.ID.String() == id {
			return o.kindName(ctx, text(s.Row, rKind)) + " " + text(s.Row, rName) + " (" + id + ")"
		}
	}
	return id
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
	m, err := resourceMatcher(d, in.Where)
	if err != nil {
		return ResListOut{}, err
	}
	specs, err := resourceSort(d, in.Sort)
	if err != nil {
		return ResListOut{}, err
	}
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableResources)
	if err != nil {
		return ResListOut{}, err
	}
	out := ResListOut{Data: []Resource{}, columns: d.fieldNames()}
	type hit struct {
		res Resource
		row located
		got resValues
	}
	var hits []hit
	for _, r := range where(rows, map[string]string{rKind: d.kind.Row.ID.String()}) {
		if text(r.Row, rArchivedAt) != "" && !in.Archived {
			continue
		}
		stored, derr := decodeValues(r)
		if derr != nil {
			return ResListOut{}, derr
		}
		// XQ7: the one --where grammar; values compare as stored (a
		// sensitive field still matches, it only prints masked: RES 3.12).
		if !m.Match(o.resourceGetter(ctx, d, stored)) {
			continue
		}
		v, verr := o.view(ctx, r, in.ShowSensitive)
		if verr != nil {
			return ResListOut{}, verr
		}
		hits = append(hits, hit{res: v, row: r, got: stored})
	}
	slices.SortFunc(hits, func(a, b hit) int { return strings.Compare(a.res.Name, b.res.Name) })
	// XQ10: --sort key[:asc|:desc]; ties keep the name order (stable).
	slices.SortStableFunc(hits, func(a, b hit) int {
		return sortspec.Compare(specs, resourceSortValue(d, a.row, a.got), resourceSortValue(d, b.row, b.got))
	})
	for _, h := range hits {
		out.Data = append(out.Data, h.res)
	}
	out.Count = len(out.Data)
	return out, nil
}

// resourceMatcher binds --where to a kind's fields (XQ7): the key part of
// a resource filter is a field name of the kind (CLI_REFERENCE 4.3); a
// metadata key is --meta's job, so meta.<key> is an unknown field here.
// Exported to the bulk ops of this package (resource delete --where).
func resourceMatcher(d kindDef, where []string) (filter.Matcher, error) {
	f, err := filter.Parse(where)
	if err != nil {
		return filter.Matcher{}, err
	}
	return f.Bind(func(k filter.Key) (filter.Field, error) {
		def, ok := d.byName[k.Name]
		if !ok || k.Meta {
			return filter.Field{}, invalid(d, "--where", "unknown field "+k.String()+" (fields: "+strings.Join(d.fieldNames(), ", ")+")")
		}
		kind, perr := value.ParseKind(text(def.Row, fType))
		if perr != nil {
			return filter.Field{}, errs.Wrap(errs.ConfigInvalid, "kind field "+def.Row.ID.String()+" has an unknown type", perr)
		}
		return filter.Field{Kind: kind, Elem: value.KindText, Values: texts(def.Row, fValues)}, nil
	})
}

// resourceGetter reads a resource's stored values for the matcher: a list
// gives its items, a ref gives the target id and, when it resolves, the
// target name (a ref filter takes a resource name or res_ id).
func (o ops) resourceGetter(ctx context.Context, d kindDef, stored resValues) filter.Getter {
	return func(k filter.Key) []string {
		def := d.byName[k.Name]
		raw, ok := stored.vals[def.Row.ID.String()]
		if !ok {
			return nil
		}
		v := fromJSONRaw(raw)
		if v == nil || v.Kind() == value.KindNull {
			return nil
		}
		if b, isJSON := value.JSONForm(v); isJSON && canon.KindOf(b) == canon.KindArray {
			return value.ItemStrings(v)
		}
		s := display(v)
		if text(def.Row, fType) == string(value.KindRef) {
			if r, err := o.resourceByName(ctx, s, ""); err == nil {
				return []string{s, text(r.Row, rName)}
			}
		}
		return []string{s}
	}
}

// Resource sort keys besides the kind's fields.
const (
	sortName = "name"
)

// resourceSortKeys are the keys resource list --sort accepts.
func resourceSortKeys(d kindDef) []string {
	return append([]string{sortName, rLastChecked, rArchivedAt}, d.fieldNames()...)
}

// resourceSort parses and checks --sort (XQ10).
func resourceSort(d kindDef, raw []string) ([]sortspec.Spec, error) {
	specs, err := sortspec.Parse(raw)
	if err != nil {
		return nil, err
	}
	return specs, sortspec.Check(specs, resourceSortKeys(d))
}

// resourceSortValue types a resource's value for sorting: numbers
// numerically, dates and datetimes by instant, the rest as text.
func resourceSortValue(d kindDef, r located, stored resValues) func(string) sortspec.Value {
	return func(key string) sortspec.Value {
		switch key {
		case sortName:
			return sortspec.Text(text(r.Row, rName))
		case rLastChecked, rArchivedAt:
			t, _ := time.Parse(time.RFC3339Nano, text(r.Row, key))
			return sortspec.Time(t)
		}
		def := d.byName[key]
		raw, ok := stored.vals[def.Row.ID.String()]
		if !ok {
			return sortspec.Missing
		}
		v := fromJSONRaw(raw)
		if v == nil {
			return sortspec.Missing
		}
		s := display(v)
		switch value.Kind(text(def.Row, fType)) {
		case value.KindNumber:
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				return sortspec.Number(n)
			}
		case value.KindDate, value.KindDateTime:
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return sortspec.Time(t)
			}
			if t, err := time.Parse(dateOnly, s); err == nil {
				return sortspec.Time(t)
			}
		default:
			// text and text-like kinds sort as shown.
		}
		return sortspec.Text(s)
	}
}

func (o ops) resDelete(ctx context.Context, in ResDeleteIn) (ResDeleteOut, error) {
	bulkForm := in.Kind != "" || len(in.Where) > 0
	switch {
	case bulkForm && in.Name != "":
		return ResDeleteOut{}, errs.New(errs.InvalidInput, "resource delete: name one resource or use --kind with --where, not both")
	case bulkForm:
		return o.resDeleteWhere(ctx, in)
	case in.Name == "":
		return ResDeleteOut{}, errs.New(errs.InvalidInput, "resource delete: name the resource, or use --kind <kind> --where <filter>")
	case in.DryRun:
		return ResDeleteOut{}, errs.New(errs.InvalidInput, "resource delete: --dry-run belongs to the bulk form --kind --where")
	}
	// A single delete always asks (CLI_REFERENCE 2.12), as before.
	if err := bulk.Ask(ctx, "run resource delete?"); err != nil {
		return ResDeleteOut{}, err
	}
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return ResDeleteOut{}, err
	}
	referrers, err := o.referrers(ctx, r.Row.ID)
	if err != nil {
		return ResDeleteOut{}, err
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: r.Src, Table: schema.TableResources, ID: r.Row.ID}); err != nil {
		return ResDeleteOut{}, err
	}
	human := fmt.Sprintf("✓ %s (%s) deleted\n", short(r.Row.ID), text(r.Row, rName))
	if len(referrers) > 0 {
		human += "  still pointing at it: " + strings.Join(referrers, ", ") + "; they show the old id until you repoint them\n"
	}
	return ResDeleteOut{ID: r.Row.ID.String(), Store: string(r.Src), Human: human}, nil
}

// resDeleteWhere is the bulk form: every live resource of --kind matching
// --where (the one grammar, XQ7) is deleted in ONE batch of kind delete
// (XQ1: one bat_ id on every row's audit event), after a preview that asks
// above batch.preview_rows (XQ2). Archived resources match too: a delete
// by filter means every row the filter names.
func (o ops) resDeleteWhere(ctx context.Context, in ResDeleteIn) (ResDeleteOut, error) {
	if in.Kind == "" || len(in.Where) == 0 {
		return ResDeleteOut{}, errs.New(errs.InvalidInput, "resource delete: the bulk form needs both --kind and --where").
			WithHint("lore resource delete --kind <kind> --where <field>=<value>")
	}
	d, err := o.kindDef(ctx, in.Kind)
	if err != nil {
		return ResDeleteOut{}, err
	}
	m, err := resourceMatcher(d, in.Where)
	if err != nil {
		return ResDeleteOut{}, err
	}
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableResources)
	if err != nil {
		return ResDeleteOut{}, err
	}
	var hits []located
	for _, r := range where(rows, map[string]string{rKind: d.kind.Row.ID.String()}) {
		stored, derr := decodeValues(r)
		if derr != nil {
			return ResDeleteOut{}, derr
		}
		if m.Match(o.resourceGetter(ctx, d, stored)) {
			hits = append(hits, r)
		}
	}
	slices.SortFunc(hits, func(a, b located) int { return strings.Compare(text(a.Row, rName), text(b.Row, rName)) })
	out := ResDeleteOut{DryRun: in.DryRun, Count: len(hits), IDs: make([]string, 0, len(hits))}
	items := make([]bulk.Item, 0, len(hits))
	for _, r := range hits {
		out.IDs = append(out.IDs, r.Row.ID.String())
		items = append(items, bulk.Item{ID: r.Row.ID.String(), Label: text(r.Row, rName)})
	}
	if len(hits) == 0 {
		out.Human = "no " + d.name() + " resources match; nothing deleted\n"
		return out, nil
	}
	if in.DryRun {
		var b strings.Builder
		b.WriteString("would delete " + strconv.Itoa(len(hits)) + " " + d.name() + " resources (dry run, nothing written)\n")
		for _, it := range items {
			b.WriteString("  " + it.String() + "\n")
		}
		out.Human = b.String()
		return out, nil
	}
	if err = bulk.Preview(ctx, o.env, hits[0].Src, "resource delete", items); err != nil {
		return ResDeleteOut{}, err
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindDelete)
	if err != nil {
		return ResDeleteOut{}, err
	}
	out.Batch = bid.String()
	for i, r := range hits {
		if _, derr := o.env.Delete(bctx, engine.Ref{Store: r.Src, Table: schema.TableResources, ID: r.Row.ID}); derr != nil {
			return ResDeleteOut{}, errors.Join(derr, errs.New(errs.InvalidInput, "resource delete: stopped after "+strconv.Itoa(i)+
				" of "+strconv.Itoa(len(hits))+" resources; the ones deleted are in batch "+out.Batch))
		}
	}
	out.Human = fmt.Sprintf("✓ %d %s resources deleted (batch %s)\n", len(hits), d.name(), out.Batch)
	return out, nil
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
	return o.patchRow(ctx, r, map[string]value.Value{rArchivedAt: value.DateTimeFrom(opclock.Now(o.env))}, "archived "+text(r.Row, rName))
}

func (o ops) resCheck(ctx context.Context, in ResCheckIn) (RowOut, error) {
	if in.bulk() {
		if in.Name != "" {
			return RowOut{}, errs.New(errs.InvalidInput, "resource check: name one resource or use --kind with --where, not both")
		}
		return o.resCheckWhere(ctx, in)
	}
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return RowOut{}, err
	}
	by, err := txt(CheckedManual) // RQ8: a person or agent looked
	if err != nil {
		return RowOut{}, err
	}
	return o.patchRow(ctx, r, map[string]value.Value{rLastChecked: value.DateTimeFrom(opclock.Now(o.env)), rCheckedBy: by}, "checked "+text(r.Row, rName)+" now")
}

func (o ops) resStale(ctx context.Context, in ResStaleIn) (ResListOut, error) {
	days := in.Days
	if days <= 0 {
		days = defaultStaleDays
	}
	cut := opclock.Now(o.env).Add(-time.Duration(days) * hoursPerDay * time.Hour)
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

// conflictsView shows a row's conflict entries by field name; a sensitive
// field's sides are masked unless shown, like its value.
func (o ops) conflictsView(d kindDef, stored resValues, show bool) []Conflict {
	if len(stored.conflicts) == 0 {
		return nil
	}
	out := make([]Conflict, 0, len(stored.conflicts))
	for id, raw := range stored.conflicts {
		var e struct {
			Conflict conflictBody `json:"conflict"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		name := stored.names[id]
		f, known := d.byID[id]
		if known {
			name = text(f.Row, kName)
		}
		side := func(b json.RawMessage) string {
			if len(b) == 0 {
				return "(unset)"
			}
			if known && flag(f.Row, fSensitive) && !show {
				return maskedValue
			}
			return display(fromJSONRaw(b))
		}
		out = append(out, Conflict{Field: name, Base: side(e.Conflict.Base), Ours: side(e.Conflict.Ours), Theirs: side(e.Conflict.Theirs)})
	}
	slices.SortFunc(out, func(a, b Conflict) int { return strings.Compare(a.Field, b.Field) })
	return out
}
