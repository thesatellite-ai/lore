package settings

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
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
	kAddedBy     = "added_by" // MQ7, as meta_keys
	kReviewed    = "reviewed"
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
	// SecretManager is the manager a bare secret_ref path means for this
	// kind (RES 3.1); it replaces the fixed gopass default of RES-SR1.
	SecretManager string `json:"secret_manager,omitempty" flag:"secret-manager" desc:"the secret manager a bare secret_ref path means (gopass, op, vault, …; default from policy)"`
	To            string `json:"to,omitempty" flag:"to" desc:"where the kind row is written (repo, <store>, <store>/<folder>; default repo)"`
}

// KindEditIn is the input of resource.kind.edit.
type KindEditIn struct {
	Kind          string `json:"id" arg:"0" desc:"the kind (name or knd_ id)"`
	Description   string `json:"description,omitempty" flag:"description" desc:"what the kind is"`
	NameFormat    string `json:"name_format,omitempty" flag:"name-format" desc:"id | dns | ip | text"`
	Render        string `json:"render,omitempty" flag:"render" desc:"how a row renders"`
	SecretManager string `json:"secret_manager,omitempty" flag:"secret-manager" desc:"the secret manager a bare secret_ref path means"`
	// Reviewed approves an agent-added kind with no other change; any edit
	// by a person approves it too (MQ7, as meta keys).
	Reviewed bool `json:"reviewed,omitempty" flag:"reviewed" desc:"mark an agent-added kind reviewed"`
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
type KindsIn struct {
	// Unreviewed lists only kinds an agent added that no person reviewed
	// yet (CLI 3.2, mirrors meta keys --unreviewed, MQ7).
	Unreviewed bool `json:"unreviewed,omitempty" flag:"unreviewed" desc:"only kinds an agent added that no person reviewed yet"`
}

// FieldAddIn is the input of resource.field.add.
type FieldAddIn struct {
	Kind        string `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Field       string `json:"field" arg:"1" desc:"the field name (snake_case)"`
	Type        string `json:"type" flag:"type" desc:"the value type (text, number, bool, date, datetime, duration, ip, cidr, dns, url, email, json, list, ref, secret_ref)"`
	Required    bool   `json:"required,omitempty" flag:"required" desc:"a resource of this kind must set it"`
	Sensitive   bool   `json:"sensitive,omitempty" flag:"sensitive" desc:"printed as ••• unless --show-sensitive"`
	Internal    bool   `json:"internal,omitempty" flag:"internal" desc:"never reaches a client repo"`
	NoInternal  bool   `json:"no_internal,omitempty" flag:"no-internal" desc:"with --type secret_ref: not internal (a secret_ref is internal by default)"`
	RefKind     string `json:"ref_kind,omitempty" flag:"ref-kind" desc:"with --type ref: the kind the value points at"`
	Values      string `json:"values,omitempty" flag:"values" desc:"allowed values, comma separated (a closed set)"`
	Description string `json:"description,omitempty" flag:"description" desc:"what the field means"`
	Label       string `json:"label,omitempty" flag:"label" desc:"display label"`
	To          string `json:"to,omitempty" flag:"to" desc:"write the field at this place (the kind's place or a nearer one; default beside the kind)"`
	FieldRuleFlags
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
	RenameValues []string `json:"rename_values,omitempty" flag:"rename-value" desc:"rename an allowed value, <old>=<new> (repeatable); rows holding it are rewritten"`
	ValueLabels  []string `json:"value_labels,omitempty" flag:"value-label" desc:"label an allowed value, <name>=<text> (repeatable; input then also takes the label; OQ-AV)"`
	Label        string   `json:"label,omitempty" flag:"label" desc:"display label"`
	Description  string   `json:"description,omitempty" flag:"description" desc:"what the field means"`
	NoStrict     bool     `json:"no_strict,omitempty" flag:"no-strict" desc:"back to newer-wins on a both-sides change (asks)"`
	NoDeprecated bool     `json:"no_deprecated,omitempty" flag:"no-deprecated" desc:"not deprecated any more"`
	To           string   `json:"to,omitempty" flag:"to" desc:"tighten the field for a nearer place only (an extension row there; RES 3.6)"`
	Clear        []string `json:"clear,omitempty" flag:"clear" desc:"remove a rule: default, pattern, min, max, replaced_by, remind_before (repeatable; clearing pattern/min/max asks)"`
	FieldRuleFlags
}

// FieldRenameIn is the input of resource.field.rename.
type FieldRenameIn struct {
	Kind string `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Old  string `json:"field" arg:"1" desc:"the current field name"`
	New  string `json:"new_name" arg:"2" desc:"the new field name"`
	// DryRun previews (CLI_REFERENCE 3.2): checks run, nothing is written.
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would change; write nothing"`
	// RewriteRows writes the new field name into every resource's stored
	// label now, in one batch (R4 item 6; mirrors meta rename, FA-RES-4).
	RewriteRows bool `json:"rewrite_rows,omitempty" flag:"rewrite-rows" desc:"also rewrite the field's stored name in every resource now (one batch)"`
}

// FieldRemoveIn is the input of resource.field.remove.
type FieldRemoveIn struct {
	Kind  string `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Field string `json:"field" arg:"1" desc:"the field name"`
	Drop  bool   `json:"drop,omitempty" flag:"drop" desc:"also remove the values rows hold (else refused while rows use it)"`
	// Reason is the deleted_reason of the removed field row: required while
	// delete.require_reason is true (E_REASON_REQUIRED), as for every other
	// delete; without it a field could never be removed under that policy.
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why the field is removed (needed while delete.require_reason is true)"`
}

// RowOut is the output of a settings write: the row id, its store and the
// human line.
type RowOut struct {
	ID    string `json:"id"`
	Store string `json:"store"`
	// Batch, DryRun and IDs are set by the bulk forms (resource edit|check
	// --where, RESOURCES 3.20): the bat_ id and every matched row.
	Batch  string   `json:"batch,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
	IDs    []string `json:"ids,omitempty"`
	Human  string   `json:"-"`
}

// Text implements op.Texter.
func (o RowOut) Text() string { return o.Human }

// Field is one kind field as shown.
type Field struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Required  bool     `json:"required,omitempty"`
	Sensitive bool     `json:"sensitive,omitempty"`
	Internal  bool     `json:"internal,omitempty"`
	RefKind   string   `json:"ref_kind,omitempty"`
	Values    []string `json:"values,omitempty"`
	// ValueIDs are the allowed values with their val_ ids and labels (OQ-AV).
	ValueIDs    []ValueEntry `json:"value_ids,omitempty"`
	Label       string       `json:"label,omitempty"`
	Description string       `json:"description,omitempty"`
	// RES 3.1 options (OV32); ReplacedBy is the replacing field's name.
	Default      string `json:"default,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	Min          string `json:"min,omitempty"`
	Max          string `json:"max,omitempty"`
	Strict       bool   `json:"strict,omitempty"`
	Deprecated   bool   `json:"deprecated,omitempty"`
	ReplacedBy   string `json:"replaced_by,omitempty"`
	RemindBefore string `json:"remind_before,omitempty"`
}

// Kind is one kind as shown, with its fields.
type Kind struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Store         string  `json:"store"`
	Description   string  `json:"description,omitempty"`
	NameFormat    string  `json:"name_format,omitempty"`
	Render        string  `json:"render,omitempty"`
	SecretManager string  `json:"secret_manager,omitempty"`
	AddedBy       string  `json:"added_by,omitempty"`
	Unreviewed    bool    `json:"unreviewed,omitempty"`
	Count         int     `json:"count"`
	Fields        []Field `json:"fields"`
	// Note warns that the kind or one of its fields holds conflict
	// markers: the view is the last good definition, read-only, or the
	// raw rows when this machine never saw one (RES 3.13, P3-RES-2).
	Note string `json:"note,omitempty"`
}

// KindOut is the output of resource.kind.show.
type KindOut struct {
	Data Kind `json:"data"`
}

// Text implements op.Texter.
func (o KindOut) Text() string {
	k := o.Data
	var b strings.Builder
	if k.Note != "" {
		b.WriteString(k.Note + "\n")
	}
	fmt.Fprintf(&b, "%s  %s  (%s)\n", k.ID, k.Name, k.Store)
	if k.Unreviewed {
		b.WriteString("  added by an agent, not reviewed yet (lore resource kind edit " + k.Name + " --reviewed)\n")
	}
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
	}{{f.Required, "required"}, {f.Sensitive, "sensitive"}, {f.Internal, "internal"}, {f.Strict, "strict"}, {f.Deprecated, "deprecated"}} {
		if x.on {
			parts = append(parts, x.word)
		}
	}
	for _, x := range [][2]string{{"default", f.Default}, {"pattern", f.Pattern}, {"min", f.Min}, {"max", f.Max}, {"replaced by", f.ReplacedBy}, {"remind before", f.RemindBefore}} {
		if x[1] != "" {
			parts = append(parts, x[0]+" "+x[1])
		}
	}
	if len(f.Values) > 0 {
		parts = append(parts, "values "+strings.Join(valueWords(f), ", "))
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
	for _, k := range o.Data {
		if k.Note != "" {
			b.WriteString(k.Note + "\n")
		}
	}
	b.WriteString("ID\tKIND\tSTORE\tROWS\tFIELDS\n")
	for _, k := range o.Data {
		names := make([]string, 0, len(k.Fields))
		for _, f := range k.Fields {
			names = append(names, f.Name)
		}
		name := k.Name
		if k.Unreviewed {
			name += " (unreviewed)"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\t%s\n", k.ID, name, k.Store, k.Count, strings.Join(names, ", "))
	}
	return b.String()
}

// kindOps returns the kind and field operations.
// nameErrors are the write errors of an op that takes a kind or field
// name: a bad one is E_RESOURCE_NAME (RES 3.1).
var nameErrors = append(slices.Clone(layerErrors), errs.ResourceName)

// layerErrors are the write errors of a kind or field op under kind
// layering (RES 3.6): a conflicting definition, missing store kind rows.
var layerErrors = append(slices.Clone(writeErrors), errs.KindConflict, errs.ResourceStale, errs.KindLimit)

func (o ops) kindOps() []op.Entry {
	return append([]op.Entry{
		op.Define(op.Spec{Name: OpKindAdd, Summary: "Add a resource kind (a kinds settings row)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: nameErrors, Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "add a kind", CLI: `lore resource kind add host --description "a server"`, Want: "knd_"}}}, o.kindAdd),
		op.Define(op.Spec{Name: OpKindEdit, Summary: "Edit a resource kind", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: layerErrors, Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "describe a kind", CLI: `lore resource kind edit database --description "A database server"`}}}, o.kindEdit),
		op.Define(op.Spec{Name: OpKindShow, Summary: "Show a resource kind and its fields", Permission: op.PermRead, Access: op.AccessRead,
			Errors: append(slices.Clone(readErrors), errs.KindConflict, errs.ResourceStale), Examples: []op.Example{{Title: "a kind and its fields", CLI: "lore resource kind show database"}}}, o.kindShow),
		op.Define(op.Spec{Name: OpKindRename, Summary: "Rename a resource kind (a label change; ids stay)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: nameErrors, Examples: []op.Example{{Title: "rename a kind", CLI: "lore resource kind rename database db_server"}}}, o.kindRename),
		op.Define(op.Spec{Name: OpKinds, Summary: "List resource kinds with their fields and row counts", Permission: op.PermRead, Access: op.AccessRead,
			Errors: append(slices.Clone(readErrors), errs.KindConflict, errs.ResourceStale), Examples: []op.Example{{Title: "every kind this repo sees", CLI: "lore resource kinds"}}}, o.kindList),
		op.Define(op.Spec{Name: OpFieldAdd, Summary: "Add a field to a resource kind (a kind_fields settings row)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: nameErrors, Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "closed set", CLI: "lore resource field add host provider --type text --required --values hetzner,aws", Want: "fld_"}}}, o.fieldAdd),
		op.Define(op.Spec{Name: OpFieldEdit, Summary: "Edit a field of a resource kind", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(layerErrors), errs.ResourceType), Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "label a field", CLI: "lore resource field edit database host --label Host"}}}, o.fieldEdit),
		op.Define(op.Spec{Name: OpFieldRename, Summary: "Rename a field (a label change: rows key values by fld_ id)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: nameErrors, Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "rename a field and its values", CLI: "lore resource field rename database hostname host"}}}, o.fieldRename),
		op.Define(op.Spec{Name: OpFieldRemove, Summary: "Remove a field from a resource kind", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(layerErrors), errs.ReasonRequired), Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "stop using a field", CLI: "lore resource field remove database legacy_port"}, {Title: "without a terminal", CLI: "lore resource field remove database legacy_port --drop --yes"}}}, o.fieldRemove),
	}, o.layerOps()...)
}

// kindByName finds a live kind by name or id in every attached store.
// (layering rules in lookupKind, RES 3.6).
func (o ops) kindByName(ctx context.Context, key string) (located, error) {
	return o.lookupKind(ctx, key)
}

// fieldsOf are the live fields of kind k as this view sees them (from
// every store: a field row may live beside its kind or at a nearer place;
// nearer extension rows are folded into their base, RES 3.6).
func (o ops) fieldsOf(ctx context.Context, k located) ([]located, error) {
	raw, err := o.rawFieldsOf(ctx, k)
	if err != nil {
		return nil, err
	}
	return effectiveFields(raw)
}

// fieldMap are k's live fields by name.
func (o ops) fieldMap(ctx context.Context, k located) (map[string]located, error) {
	fs, err := o.fieldsOf(ctx, k)
	if err != nil {
		return nil, err
	}
	m := make(map[string]located, len(fs))
	for _, f := range fs {
		m[text(f.Row, kName)] = f
	}
	return m, nil
}

// fieldByName is k's defining field row named name, as stored (not folded
// with nearer extensions: edits address the defining row, RES 3.6).
func (o ops) fieldByName(ctx context.Context, k located, name string) (located, error) {
	fs, err := o.rawFieldsOf(ctx, k)
	if err != nil {
		return located{}, err
	}
	for _, f := range fs {
		if schema.ExtendsOf(f.Row) != "" {
			continue
		}
		if text(f.Row, kName) == name || f.Row.ID.String() == name {
			return f, nil
		}
	}
	return located{}, errs.New(errs.NotFound, "kind "+text(k.Row, kName)+" has no field "+quote(name)).
		WithHint("lore resource kind show " + text(k.Row, kName))
}

// checkName is the kind and field name format: E_RESOURCE_NAME (RES 3.1,
// CLI_REFERENCE 3.2 errors).
func checkName(what, s string) error {
	if !snakeName.MatchString(s) {
		return errs.New(errs.ResourceName, what+" "+quote(s)+" is not snake_case (a-z, 0-9, _; starting with a letter)")
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
	if err := o.checkKindRoute(in.Kind); err != nil {
		return RowOut{}, err
	}
	if err := checkOneOf("name-format", in.NameFormat, NameFormats()); err != nil {
		return RowOut{}, err
	}
	if err := checkSecretManager(in.SecretManager); err != nil {
		return RowOut{}, err
	}
	// MQ7 (as meta keys): an agent's kind waits for a person's review.
	byWhom := addedBy(ctx)
	fields := map[string]value.Value{kReviewed: value.BoolOf(byWhom == schema.MetaAddedByPerson)}
	if err := optTexts(fields, map[string]string{kName: in.Kind, kDescription: in.Description, kNameFormat: in.NameFormat, kRender: in.Render, kSecretMgr: in.SecretManager, kAddedBy: byWhom}); err != nil {
		return RowOut{}, err
	}
	target := targetOf(in.To)
	if err := personalPlaceRefused("resource kind add", "a kind", "kinds live in the repo (or an org store) so resources can use them: drop --to me", target); err != nil {
		return RowOut{}, err
	}
	if err := o.kindNameClash(ctx, in.Kind, target); err != nil {
		return RowOut{}, err
	}
	// RES 3.1 cap: kinds per place (FA-RES-5).
	if ks, lerr := liveRows(ctx, o.env, []store.Source{target}, schema.TableKinds); lerr != nil {
		return RowOut{}, lerr
	} else if len(ks) >= maxKindsPerPlace {
		return RowOut{}, kindLimit(place(target)+" has "+strconv.Itoa(len(ks))+" kinds", maxKindsPerPlace)
	}
	w, err := create(ctx, o.env, target, schema.TableKinds, map[string]string{kName: in.Kind}, fields)
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
	if err := checkSecretManager(in.SecretManager); err != nil {
		return RowOut{}, err
	}
	patch := map[string]value.Value{}
	if err := optTexts(patch, map[string]string{kDescription: in.Description, kNameFormat: in.NameFormat, kRender: in.Render, kSecretMgr: in.SecretManager}); err != nil {
		return RowOut{}, err
	}
	// MQ7: a person's edit approves an agent-added kind, and so does
	// --reviewed from anyone: a coding agent reviews for its person (owner
	// rule 2026-11-11; the row records the agent as the writer). A team
	// that keeps reviews to people lists resource.kind.edit.reviewed in
	// agents.person_only, which the engine checks before this runs.
	person := addedBy(ctx) == schema.MetaAddedByPerson
	if (person || in.Reviewed) && isUnreviewed(k.Row) {
		patch[kReviewed] = value.BoolOf(true)
	}
	if len(patch) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "resource kind edit: nothing to change").WithHint("give --description, --name-format, --render, --secret-manager or --reviewed")
	}
	return o.patchRow(ctx, k, patch, "edited kind "+text(k.Row, kName))
}

// patchRow updates one located row and prints "<what> (<short id>)".
func (o ops) patchRow(ctx context.Context, l located, patch map[string]value.Value, what string) (RowOut, error) {
	if _, hasFields := patch[rFields]; hasFields && l.Row.Table == schema.TableResources {
		d, derr := o.kindDef(ctx, text(l.Row, rKind))
		if derr != nil {
			return RowOut{}, derr
		}
		if derr = o.withHistory(ctx, d, &l, patch); derr != nil { // RES 3.22
			return RowOut{}, derr
		}
	}
	w, err := o.env.Update(ctx, engine.Ref{Store: l.Src, Table: l.Row.Table, ID: l.Row.ID}, patch)
	if err != nil {
		return RowOut{}, err
	}
	human := fmt.Sprintf("✓ %s (%s)\n", what, short(w.Row.ID))
	for _, x := range w.Warnings { // OV10: --meta notes like meta set
		human += metalist.NoteLine(string(x.Code), x.Message)
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(l.Src), Human: human}, nil
}

func (o ops) kindView(ctx context.Context, k located) (Kind, error) {
	fs, err := o.fieldsOf(ctx, k)
	if err != nil {
		return Kind{}, err
	}
	// RES 3.13: a kind or field row with conflict markers shows the last
	// good definition with its warning; never seen good here, the raw rows
	// with the warning, so a person sees what to resolve (P3-RES-2).
	var note string
	if marked := markedRows(k, fs); len(marked) > 0 {
		note = "warning: " + errs.EnvelopeOf(conflicted(k, marked)).Error.Message + "; no last good definition on this machine (showing the rows as stored)"
		if d, derr := o.kindDefRead(ctx, k.Row.ID.String()); derr == nil && d.note != "" {
			k, fs, note = d.kind, d.fieldList(), d.note
		}
	}
	res, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableResources)
	if err != nil {
		return Kind{}, err
	}
	out := Kind{ID: k.Row.ID.String(), Name: text(k.Row, kName), Store: string(k.Src), Description: text(k.Row, kDescription),
		NameFormat: text(k.Row, kNameFormat), Render: text(k.Row, kRender), SecretManager: text(k.Row, kSecretMgr), Fields: []Field{},
		AddedBy: text(k.Row, kAddedBy), Unreviewed: isUnreviewed(k.Row), Note: note,
		Count: len(where(res, map[string]string{rKind: k.Row.ID.String()}))}
	for _, f := range fs {
		fv := Field{ID: f.Row.ID.String(), Name: text(f.Row, kName), Type: text(f.Row, fType), Required: flag(f.Row, fRequired),
			Sensitive: flag(f.Row, fSensitive), Internal: flag(f.Row, fInternal), Values: texts(f.Row, fValues), ValueIDs: valueEntries(f.Row),
			Label: text(f.Row, fLabel), Description: text(f.Row, fDescription),
			Default: text(f.Row, fDefault), Pattern: text(f.Row, fPattern), Min: text(f.Row, fMin), Max: text(f.Row, fMax),
			Strict: flag(f.Row, fStrict), Deprecated: flag(f.Row, fDeprecated), RemindBefore: text(f.Row, fRemindBefore)}
		if rb := text(f.Row, fReplacedBy); rb != "" {
			fv.ReplacedBy = rb
			for _, s := range fs {
				if s.Row.ID.String() == rb {
					fv.ReplacedBy = text(s.Row, kName)
				}
			}
		}
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

func (o ops) kindList(ctx context.Context, in KindsIn) (KindsOut, error) {
	ks, err := liveDefRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableKinds)
	if err != nil {
		return KindsOut{}, err
	}
	out := KindsOut{Data: []Kind{}}
	for _, k := range ks {
		if in.Unreviewed && !isUnreviewed(k.Row) {
			continue
		}
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
	if err := o.checkKindRoute(in.New); err != nil {
		return RowOut{}, err
	}
	k, err := o.kindByName(ctx, in.Old)
	if err != nil {
		return RowOut{}, err
	}
	if _, err = o.kindByName(ctx, in.New); err == nil {
		return RowOut{}, errs.New(errs.NameTaken, "a kind named "+quote(in.New)+" already exists")
	}
	v, err := txt(in.New)
	if err != nil {
		return RowOut{}, err
	}
	return o.patchRow(ctx, k, map[string]value.Value{kName: v}, "renamed kind "+text(k.Row, kName)+" → "+in.New+"; ids unchanged")
}

// personalPlaceRefused refuses a resource definition (a kind, a field, an
// adopted set of kinds) written to the personal store (--to me): it could
// serve no resource, because resources may not live there (resAdd) and a
// repo or store resource cannot name a personal row. Nothing documents
// personal kinds as adoptable. cmd names the command, what the row ("a
// kind"), hint the place to use instead. Any other place: nil.
func personalPlaceRefused(cmd, what, hint string, target store.Source) error {
	if string(target) != storeMe {
		return nil
	}
	return errs.New(errs.InvalidInput, cmd+" --to me: "+what+" in your personal store could not be used by any resource").WithHint(hint)
}

// fieldPlaceHint is the next step of a refused field add/edit --to me.
const fieldPlaceHint = "a field lives beside its kind, or at a nearer repo or org place: drop --to me"

func (o ops) fieldAdd(ctx context.Context, in FieldAddIn) (RowOut, error) {
	if err := personalPlaceRefused("resource field add", "a field", fieldPlaceHint, targetOf(in.To)); err != nil {
		return RowOut{}, err
	}
	if err := checkName("field", in.Field); err != nil {
		return RowOut{}, err
	}
	if _, err := value.ParseKind(in.Type); err != nil || in.Type == string(value.KindNull) {
		return RowOut{}, errs.New(errs.InvalidInput, "--type "+quote(in.Type)+" is not a value type").
			WithHint("one of text, number, bool, date, datetime, duration, ip, cidr, dns, url, email, json, list, ref, secret_ref")
	}
	isSecretRef := in.Type == string(value.KindSecretRef)
	if err := credentialName(in.Field, isSecretRef); err != nil {
		return RowOut{}, err
	}
	internal, err := secretRefInternal(isSecretRef, in.Internal, in.NoInternal)
	if err != nil {
		return RowOut{}, err
	}
	k, err := o.kindByName(ctx, in.Kind)
	if err != nil {
		return RowOut{}, err
	}
	if _, err = o.fieldByName(ctx, k, in.Field); err == nil {
		return RowOut{}, errs.New(errs.InvalidInput, "kind "+in.Kind+" already has a field "+quote(in.Field))
	}
	fields := map[string]value.Value{fRequired: value.BoolOf(in.Required), fSensitive: value.BoolOf(in.Sensitive), fInternal: value.BoolOf(internal)}
	if err = optTexts(fields, map[string]string{kName: in.Field, fType: in.Type, fLabel: in.Label, fDescription: in.Description}); err != nil {
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
	vs := splitList(in.Values)
	if err = checkValuesCap(in.Kind+"."+in.Field, vs); err != nil {
		return RowOut{}, err
	}
	if len(vs) > 0 {
		if fields[fValues], err = textList(vs); err != nil {
			return RowOut{}, err
		}
		// OQ-AV: each allowed value gets its permanent val_ id.
		es, verr := syncValueIDs(in.Kind+"."+in.Field, nil, vs, nil, nil)
		if verr != nil {
			return RowOut{}, verr
		}
		if fields[fValueIDs], err = valueEntriesValue(es); err != nil {
			return RowOut{}, err
		}
	}
	// RES 3.1 options: checked against the type and the allowed values.
	siblings, err := o.fieldMap(ctx, k)
	if err != nil {
		return RowOut{}, err
	}
	// RES 3.1 cap: fields per effective kind (FA-RES-5).
	if len(siblings) >= maxFieldsPerKind {
		return RowOut{}, kindLimit("kind "+in.Kind+" has "+strconv.Itoa(len(siblings))+" fields", maxFieldsPerKind)
	}
	if err = fieldOptions(in.Type, vs, siblings, in.FieldRuleFlags, fields); err != nil {
		return RowOut{}, err
	}
	// The field row lives beside its kind, so the definition travels as
	// one, unless --to names a nearer place extending it (RES 3.6).
	at := k.Src
	if in.To != "" {
		at = targetOf(in.To)
		if err = o.checkFieldPlace(k, at); err != nil {
			return RowOut{}, err
		}
	}
	w, err := create(ctx, o.env, at, schema.TableKindFields, map[string]string{fKindID: k.Row.ID.String(), kName: in.Field}, fields)
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(at),
		Human: fmt.Sprintf("added field %s %s.%s (%s)%s\n", short(w.Row.ID), text(k.Row, kName), in.Field, in.Type, secretRefNote(isSecretRef, internal))}, nil
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
	if err := personalPlaceRefused("resource field edit", "a field", fieldPlaceHint, targetOf(in.To)); err != nil {
		return RowOut{}, err
	}
	k, err := o.kindByName(ctx, in.Kind)
	if err != nil {
		return RowOut{}, err
	}
	f, err := o.fieldByName(ctx, k, in.Field)
	if err != nil {
		return RowOut{}, err
	}
	// RES 3.6: --to a nearer place tightens through an extension row.
	if in.To != "" && targetOf(in.To) != f.Src {
		return o.extendField(ctx, k, f, targetOf(in.To), in)
	}
	patch := map[string]value.Value{}
	for _, t := range []struct {
		f       string
		on, off bool
	}{{fRequired, in.Required, in.NoRequired}, {fSensitive, in.Sensitive, in.NoSensitive}, {fInternal, in.Internal, in.NoInternal},
		{fStrict, in.Strict, in.NoStrict}, {fDeprecated, in.Deprecated, in.NoDeprecated}} {
		if err = triState(patch, t.f, t.on, t.off); err != nil {
			return RowOut{}, err
		}
	}
	// OV32: --clear removes a rule (null); clearing pattern/min/max is a
	// loosening and asks below (RES 3.14).
	if err = clearRules(f, in, patch); err != nil {
		return RowOut{}, err
	}
	// RES 3.14: a loosening asks first.
	loosened, err := loosening(ctx, f, in)
	if err != nil {
		return RowOut{}, err
	}
	// RES 3.14: a confirmed loosening records who and when in the field row.
	if loosened {
		if err = o.stampChange(ctx, patch); err != nil {
			return RowOut{}, err
		}
	}
	siblings, err := o.fieldMap(ctx, k)
	if err != nil {
		return RowOut{}, err
	}
	rules := in.FieldRuleFlags
	rules.Strict, rules.Deprecated = false, in.Deprecated || (flag(f.Row, fDeprecated) && !in.NoDeprecated) // strict/deprecated went through triState
	if err = fieldOptions(text(f.Row, fType), texts(f.Row, fValues), siblings, rules, patch); err != nil {
		return RowOut{}, err
	}
	if !in.Deprecated {
		delete(patch, fDeprecated)
		if in.NoDeprecated {
			patch[fDeprecated] = value.BoolOf(false)
		}
	}
	if err = optTexts(patch, map[string]string{fLabel: in.Label, fDescription: in.Description}); err != nil {
		return RowOut{}, err
	}
	var renames []valueRename
	if len(in.AddValues) > 0 || len(in.RemoveValues) > 0 || len(in.RenameValues) > 0 || len(in.ValueLabels) > 0 {
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
				return RowOut{}, errs.New(errs.ResourceInUse, fmt.Sprintf("%s.%s value %s is used by %s", in.Kind, in.Field, quote(v), plural.Count(n, "resource"))).
					WithHint("change those resources first; nothing written")
			}
			vals = slices.DeleteFunc(vals, func(x string) bool { return x == v })
		}
		if renames, vals, err = renamePairs(in.Kind+"."+in.Field, in.RenameValues, vals); err != nil {
			return RowOut{}, err
		}
		if err = checkValuesCap(in.Kind+"."+in.Field, vals); err != nil {
			return RowOut{}, err
		}
		if patch[fValues], err = textList(vals); err != nil {
			return RowOut{}, err
		}
		// OQ-AV: ids follow the values (a rename keeps the id); labels last.
		es, verr := syncValueIDs(in.Kind+"."+in.Field, valueEntries(f.Row), vals, renames, in.ValueLabels)
		if verr != nil {
			return RowOut{}, verr
		}
		if patch[fValueIDs], err = valueEntriesValue(es); err != nil {
			return RowOut{}, err
		}
	}
	if len(patch) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "resource field edit: nothing to change")
	}
	if err = o.checkOuterEdit(ctx, k, f, patch); err != nil {
		return RowOut{}, err
	}
	out, err := o.patchRow(ctx, f, patch, "edited field "+in.Kind+"."+in.Field)
	if err != nil || len(renames) == 0 {
		return out, err
	}
	// CLI_REFERENCE 3.2: --rename-value rewrites every row holding the value.
	n, err := o.rewriteValueRows(ctx, k, f, renames)
	if err != nil {
		return out, err
	}
	out.Human += fmt.Sprintf("  renamed %s; %s rewritten\n", plural.Count(len(renames), "value"), plural.Count(n, "resource"))
	return out, nil
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
	if _, err = o.fieldByName(ctx, k, in.New); err == nil {
		return RowOut{}, errs.New(errs.InvalidInput, "kind "+in.Kind+" already has a field "+quote(in.New))
	}
	// RES 3.1: a rename is the other way to give a field a credential name;
	// only a secret_ref may carry one (as on field add).
	if err = credentialName(in.New, text(f.Row, fType) == string(value.KindSecretRef)); err != nil {
		return RowOut{}, err
	}
	v, err := txt(in.New)
	if err != nil {
		return RowOut{}, err
	}
	if in.DryRun {
		using, uerr := o.resourcesUsing(ctx, k, f)
		if uerr != nil {
			return RowOut{}, uerr
		}
		return RowOut{ID: f.Row.ID.String(), Store: string(f.Src), Human: fmt.Sprintf(
			"would rename field %s.%s → %s (%s); %d resources hold values (keyed by fld_ id, their stored name refreshes on their next write)\ndry run: nothing written\n",
			in.Kind, in.Old, in.New, short(f.Row.ID), len(using))}, nil
	}
	out, err := o.patchRow(ctx, f, map[string]value.Value{kName: v}, "renamed field "+in.Kind+"."+in.Old+" → "+in.New+"; no values changed")
	if err != nil || !in.RewriteRows {
		return out, err
	}
	n, batch, err := o.relabelFieldRows(ctx, k, f)
	if err != nil {
		return out, err
	}
	out.Human += fmt.Sprintf("  rewrote the stored name in %s%s\n", plural.Count(n, "resource"), batch)
	return out, nil
}

// relabelFieldRows writes field f's current name into the `fields` entry
// of every live resource holding a value for it, one batch (--rewrite-rows).
func (o ops) relabelFieldRows(ctx context.Context, k, f located) (int, string, error) {
	using, err := o.resourcesUsing(ctx, k, f)
	if err != nil || len(using) == 0 {
		return 0, "", err
	}
	d, err := o.kindDef(ctx, k.Row.ID.String())
	if err != nil {
		return 0, "", err
	}
	res, err := o.bulkRun(ctx, d, using, false, "relabel", func(located) string { return "stored field name → " + text(d.byID[f.Row.ID.String()].Row, kName) },
		func(c context.Context, r located) error {
			stored, derr := d.decode(r)
			if derr != nil {
				return derr
			}
			patch, perr := o.valuesPatch(ctx, d, stored.vals, stored)
			if perr != nil {
				return perr
			}
			_, uerr := o.env.Update(c, engine.Ref{Store: r.Src, Table: r.Row.Table, ID: r.Row.ID}, patch)
			return uerr
		})
	if err != nil {
		return 0, "", err
	}
	return len(using), " (batch " + res.Batch + ")", nil
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
		return RowOut{}, errs.New(errs.ResourceInUse, fmt.Sprintf("%s.%s has values on %s", in.Kind, in.Field, plural.Count(len(using), "row"))).
			WithHint("pass --drop to remove the values in the same commit")
	}
	d, err := o.kindDef(ctx, k.Row.ID.String())
	if err != nil {
		return RowOut{}, err
	}
	// One confirm rule: every remove asks unless --yes, once the field is
	// found and the in-use check passed.
	if err := bulk.Ask(ctx, "remove field "+in.Kind+"."+in.Field+" and drop its values from "+plural.Count(len(using), "row")+"?"); err != nil {
		return RowOut{}, err
	}
	for _, r := range using {
		stored, verr := d.decode(r)
		if verr != nil {
			return RowOut{}, verr
		}
		delete(stored.vals, f.Row.ID.String())
		delete(stored.conflicts, f.Row.ID.String())
		patch, perr := o.valuesPatch(ctx, d, stored.vals, stored)
		if perr != nil {
			return RowOut{}, perr
		}
		if _, err := o.env.Update(ctx, engine.Ref{Store: r.Src, Table: schema.TableResources, ID: r.Row.ID}, patch); err != nil {
			return RowOut{}, err
		}
	}
	if _, err := o.env.Delete(engine.WithDeleteReason(ctx, in.Reason), engine.Ref{Store: f.Src, Table: schema.TableKindFields, ID: f.Row.ID}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: f.Row.ID.String(), Store: string(f.Src),
		Human: fmt.Sprintf("✓ removed field %s.%s (%s); values dropped from %s\n", in.Kind, in.Field, short(f.Row.ID), plural.Count(len(using), "row"))}, nil
}

// resourcesUsing are the live resources of kind k holding a value for f.
func (o ops) resourcesUsing(ctx context.Context, k located, f located) ([]located, error) {
	rows, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableResources)
	if err != nil {
		return nil, err
	}
	var out []located
	for _, r := range where(rows, map[string]string{rKind: k.Row.ID.String()}) {
		stored, verr := decodeValues(r)
		if verr != nil {
			return nil, verr
		}
		if _, ok := stored.vals[f.Row.ID.String()]; ok {
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
		stored, verr := decodeValues(r)
		if verr != nil {
			return 0, verr
		}
		cur := stored.vals[f.Row.ID.String()]
		if ids, ok := stored.valIDs[f.Row.ID.String()]; ok { // OQ-AV: by id
			if names, ok := namesOf(f.Row, ids); ok {
				cur = names
			}
		}
		if display(fromJSONRaw(cur)) == v {
			n++
		}
	}
	return n, nil
}
