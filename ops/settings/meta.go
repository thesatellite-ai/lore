package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Metadata key and value operations (CLI_REFERENCE 3.1, the registry
// half): one `meta_keys` row (mky_) per key, one `meta_values` row (val_)
// per allowed value. Writing metadata ON rows (meta set/unset, --meta) is
// the metadata logic's, not these ops'.
const (
	OpMetaAdd         = "meta.add"
	OpMetaEdit        = "meta.edit"
	OpMetaDescribe    = "meta.describe"
	OpMetaRename      = "meta.rename"
	OpMetaKeys        = "meta.keys"
	OpMetaDelete      = "meta.delete"
	OpMetaValueAdd    = "meta.value.add"
	OpMetaValueRename = "meta.value.rename"
	OpMetaSet         = "meta.set"
	OpMetaUnset       = "meta.unset"
	OpMetaGet         = "meta.get"
	// metaREST keeps the REST collection /v1/meta.
	metaREST = "meta"
)

// meta_keys / meta_values field names (schema.Builtin).
const (
	mName        = "name"
	mScope       = "scope"
	mType        = "type"
	mStrict      = "strict"
	mInternal    = "internal"
	mSensitive   = "sensitive"
	mDescription = "description"
	mvKeyID      = "key_id"
	mAddedBy     = "added_by"
	mReviewed    = "reviewed"
	mvLabel      = "label"
)

// Metadata limits (CLI_REFERENCE 3.1).
const (
	maxMetaDescription = 300 // characters (runes), not bytes: the spec counts characters
	maxMetaValues      = 100
)

// MetaAddIn is the input of meta.add.
type MetaAddIn struct {
	Name        string `json:"name" arg:"0" desc:"the key (snake_case)"`
	Type        string `json:"type" flag:"type" desc:"text | number | bool | date | datetime | list | json | …"`
	To          string `json:"to,omitempty" flag:"to" desc:"where the key row is written (repo, <store>, <store>/<folder>; default repo)"`
	Description string `json:"description,omitempty" flag:"description" desc:"what the key means (at most 300 characters)"`
	Strict      bool   `json:"strict,omitempty" flag:"strict" desc:"refuse type mismatches"`
	Values      string `json:"values,omitempty" flag:"values" desc:"allowed values, comma separated (a closed set, at most 100)"`
	Internal    bool   `json:"internal,omitempty" flag:"internal" desc:"never reaches a client repo"`
	NoInternal  bool   `json:"no_internal,omitempty" flag:"no-internal" desc:"may reach client repos"`
	Sensitive   bool   `json:"sensitive,omitempty" flag:"sensitive" desc:"hide the values from agents, render and lists (OQ2)"`
}

// MetaEditIn is the input of meta.edit.
type MetaEditIn struct {
	Key         string `json:"id" arg:"0" desc:"the key (name or mky_ id)"`
	Strict      bool   `json:"strict,omitempty" flag:"strict" desc:"turn mismatches into refusals"`
	NoStrict    bool   `json:"no_strict,omitempty" flag:"no-strict" desc:"turn mismatches back into warnings"`
	Internal    bool   `json:"internal,omitempty" flag:"internal" desc:"mark it internal"`
	NoInternal  bool   `json:"no_internal,omitempty" flag:"no-internal" desc:"mark it not internal"`
	Sensitive   bool   `json:"sensitive,omitempty" flag:"sensitive" desc:"mark it sensitive (values hidden, OQ2)"`
	NoSensitive bool   `json:"no_sensitive,omitempty" flag:"no-sensitive" desc:"mark it not sensitive"`
	Values      string `json:"values,omitempty" flag:"values" desc:"the new allowed values, comma separated"`
	// Value maintenance (3.8, OQ-AV): ids stay, only meta_values rows change.
	ValueLabel  []string `json:"value_label,omitempty" flag:"value-label" desc:"label an allowed value: name=label (repeatable)"`
	RenameValue string   `json:"rename_value,omitempty" flag:"rename-value" desc:"rename an allowed value: old=new (its val_ id stays)"`
	RemoveValue string   `json:"remove_value,omitempty" flag:"remove-value" desc:"remove an allowed value no row uses"`
	// MergeInto folds this key into another of the same type (3.8 Fold, OQ3).
	MergeInto string `json:"merge_into,omitempty" flag:"merge-into" desc:"fold this key into another key of the same type (rows rewritten, one batch)"`
	// Everyday features (3.19); "none" clears a field.
	Label        string `json:"label,omitempty" flag:"label" desc:"display label (column header); none clears it"`
	RequiredOn   string `json:"required_on,omitempty" flag:"required-on" desc:"tables a new row must carry this key on, comma separated; none clears"`
	Pattern      string `json:"pattern,omitempty" flag:"pattern" desc:"RE2 pattern text values must match; none clears"`
	Min          string `json:"min,omitempty" flag:"min" desc:"lowest number, date or datetime; none clears"`
	Max          string `json:"max,omitempty" flag:"max" desc:"highest number, date or datetime; none clears"`
	Default      string `json:"default,omitempty" flag:"default" desc:"value given on add (with --default-on); none clears"`
	DefaultOn    string `json:"default_on,omitempty" flag:"default-on" desc:"tables the default applies to, comma separated; none clears"`
	Deprecated   bool   `json:"deprecated,omitempty" flag:"deprecated" desc:"new writes warn (refused on a strict key)"`
	NoDeprecated bool   `json:"no_deprecated,omitempty" flag:"no-deprecated" desc:"not deprecated any more"`
	ReplacedBy   string `json:"replaced_by,omitempty" flag:"replaced-by" desc:"the key to use instead of a deprecated one; none clears"`
}

// MetaDescribeIn is the input of meta.describe.
type MetaDescribeIn struct {
	Key  string `json:"id" arg:"0" desc:"the key (name or mky_ id)"`
	Text string `json:"text" arg:"1" desc:"the description (at most 300 characters)"`
}

// MetaKeyRef names one key.
type MetaKeyRef struct {
	Key string `json:"id" arg:"0" desc:"the key (name or mky_ id)"`
}

// MetaKeysIn is the input of meta.keys.
type MetaKeysIn struct {
	Keys []string `json:"keys,omitempty" flag:"key" desc:"only these keys (repeatable)"`
	IDs  bool     `json:"ids,omitempty" flag:"ids" desc:"show key ids"`
	// Unreviewed lists only keys an agent registered that no person has
	// approved yet (MQ7).
	Unreviewed bool `json:"unreviewed,omitempty" flag:"unreviewed" desc:"only keys an agent added that no person reviewed yet"`
	// Unused lists keys no row uses (archived rows count as use, 3.8).
	Unused bool `json:"unused,omitempty" flag:"unused" desc:"only keys used on no row (remove with lore meta forget)"`
	// Place and Table narrow the list (CLI_REFERENCE 3.1 meta keys, XQ6).
	Place string `json:"place,omitempty" flag:"place" desc:"one place only: repo, project, org or a scope label (project:acme-hub)"`
	Table string `json:"table,omitempty" flag:"table" desc:"only keys used on rows of this table"`
}

// MetaValueAddIn is the input of meta.value.add.
type MetaValueAddIn struct {
	Key   string `json:"key" arg:"0" desc:"the key (name or mky_ id)"`
	Value string `json:"value" arg:"1" desc:"the new value (snake_case)"`
	Label string `json:"label,omitempty" flag:"label" desc:"display label"`
}

// MetaValueRenameIn is the input of meta.value.rename.
type MetaValueRenameIn struct {
	Key string `json:"key" arg:"0" desc:"the key (name or mky_ id)"`
	Old string `json:"value" arg:"1" desc:"the current value"`
	New string `json:"new_value" arg:"2" desc:"the new value (snake_case)"`
}

// MetaKey is one key as shown.
type MetaKey struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Scope       string   `json:"scope"`
	Store       string   `json:"store"`
	Type        string   `json:"type"`
	Strict      bool     `json:"strict,omitempty"`
	Internal    bool     `json:"internal,omitempty"`
	Sensitive   bool     `json:"sensitive,omitempty"`
	Description string   `json:"description,omitempty"`
	Values      []string `json:"values,omitempty"`
	AddedBy     string   `json:"added_by,omitempty"`
	Unreviewed  bool     `json:"unreviewed,omitempty"`
	Rows        int      `json:"rows"`
	Label       string   `json:"label,omitempty"`
	Retired     string   `json:"merged_into,omitempty"`
}

// MetaKeysOut is the output of meta.keys.
type MetaKeysOut struct {
	Count int       `json:"count"`
	Data  []MetaKey `json:"data"`
	ids   bool
}

// Text implements op.Texter (CLI_REFERENCE 3.1 sample).
func (o MetaKeysOut) Text() string {
	if len(o.Data) == 0 {
		return "(no metadata keys)\n"
	}
	var b strings.Builder
	head := "KEY\tSCOPE\tTYPE\tROWS\tVALUES\n"
	if o.ids {
		head = "ID\t" + head
	}
	b.WriteString(head)
	for _, k := range o.Data {
		if o.ids {
			b.WriteString(short(mustAny(k.ID)) + "\t")
		}
		vals := strings.Join(k.Values, ", ")
		if k.Retired != "" {
			vals = "(merged into " + k.Retired + ")"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\t%s\n", k.Name, k.Scope, k.Type, k.Rows, vals)
	}
	return b.String()
}

// mustAny parses a stored id for printing (the zero id on a bad one).
func mustAny(s string) ids.AnyID {
	id, err := ids.ParseAny(s)
	if err != nil {
		return ids.AnyID{}
	}
	return id
}

func (o ops) metaOps() []op.Entry {
	return append(append([]op.Entry{
		op.Define(op.Spec{Name: OpMetaAdd, Summary: "Add a metadata key to a place's registry", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST,
			Examples: []op.Example{{Title: "a closed set", CLI: "lore meta add release_window --type text --strict --values spring,summer", Want: "mky_"}}}, o.metaAdd),
		op.Define(op.Spec{Name: OpMetaEdit, Summary: "Edit a metadata key (strict, internal, allowed values)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.HasDependents, errs.MetaKey, errs.MetaType, errs.MetaInUse, errs.MetaRetired, errs.MetaConflict), Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST}, o.metaEdit),
		op.Define(op.Spec{Name: OpMetaDescribe, Summary: "Set a metadata key's description", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST}, o.metaDescribe),
		op.Define(op.Spec{Name: OpMetaRename, Summary: "Rename a metadata key (a label change; the id stays)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, RESTNoun: metaREST}, o.metaRename),
		op.Define(op.Spec{Name: OpMetaKeys, Summary: "List the metadata keys every visible place defines", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors, RESTNoun: metaREST}, o.metaKeys),
		op.Define(op.Spec{Name: OpMetaDelete, Summary: "Delete a metadata key and its allowed values", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{Always: true}, RESTNoun: metaREST}, o.metaDelete),
		op.Define(op.Spec{Name: OpMetaValueAdd, Summary: "Add an allowed value to a metadata key", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors}, o.metaValueAdd),
		op.Define(op.Spec{Name: OpMetaSet, Summary: "Set metadata on a row by id", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: metaWriteErrors, Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "one value", CLI: "lore meta set tsk_… client=acme", Want: "set client"}}}, o.metaSet),
		op.Define(op.Spec{Name: OpMetaUnset, Summary: "Remove a metadata key from a row (a parent removes its fields)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: metaWriteErrors, Confirm: op.ConfirmRule{MayAsk: true}}, o.metaUnset),
		op.Define(op.Spec{Name: OpMetaGet, Summary: "Show a row's metadata", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.metaGet),
		op.Define(op.Spec{Name: OpMetaValueRename, Summary: "Rename an allowed value (a label change; the val_ id stays)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors}, o.metaValueRename),
	}, o.metaMaintOps()...), o.metaIOOps()...)
}

func (o ops) metaKey(ctx context.Context, key string) (located, error) {
	k, err := byNameOrID(ctx, o.env, o.env.Stores(), schema.TableMetaKeys, mName, key, nil)
	if errs.Has(err, errs.NotFound) {
		return located{}, errs.New(errs.NotFound, "no metadata key "+quote(key)).WithHint("lore meta keys lists the keys")
	}
	return k, err
}

// metaValues are the live allowed-value rows of key k (beside k).
func (o ops) metaValues(ctx context.Context, k located) ([]located, error) {
	rows, err := liveRows(ctx, o.env, []store.Source{k.Src}, schema.TableMetaValues)
	if err != nil {
		return nil, err
	}
	vs := where(rows, map[string]string{mvKeyID: k.Row.ID.String()})
	slices.SortFunc(vs, func(a, b located) int { return strings.Compare(text(a.Row, mName), text(b.Row, mName)) })
	return vs, nil
}

func valueNames(vs []located) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, text(v.Row, mName))
	}
	return out
}

func checkMetaValues(vals []string) error {
	if len(vals) > maxMetaValues {
		return errs.New(errs.InvalidInput, fmt.Sprintf("a key has at most %d allowed values", maxMetaValues))
	}
	for _, v := range vals {
		if !snakeName.MatchString(v) {
			return errs.New(errs.InvalidInput, "allowed value "+quote(v)+" is not snake_case")
		}
	}
	return nil
}

func (o ops) addValue(ctx context.Context, k located, name, label string) (engine.Written, error) {
	fields := map[string]value.Value{}
	if err := optTexts(fields, map[string]string{mName: name, mvLabel: label}); err != nil {
		return engine.Written{}, err
	}
	kid, err := ref(k.Row.ID)
	if err != nil {
		return engine.Written{}, err
	}
	fields[mvKeyID] = kid
	return create(ctx, o.env, k.Src, schema.TableMetaValues, map[string]string{mvKeyID: k.Row.ID.String(), mName: name}, fields)
}

func (o ops) metaAdd(ctx context.Context, in MetaAddIn) (RowOut, error) {
	if err := checkKeyName(in.Name); err != nil {
		return RowOut{}, err
	}
	if utf8.RuneCountInString(in.Description) > maxMetaDescription {
		return RowOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("--description has at most %d characters", maxMetaDescription))
	}
	if _, err := value.ParseKind(in.Type); err != nil || in.Type == string(value.KindNull) {
		return RowOut{}, errs.New(errs.MetaType, "--type "+quote(in.Type)+" is not a metadata type")
	}
	vals := splitList(in.Values)
	if err := checkMetaValues(vals); err != nil {
		return RowOut{}, err
	}
	target := targetOf(in.To)
	// Default visibility: internal in a store registry, not in a repo.
	internal := target != store.SourceRepo
	if err := triStateBool(&internal, in.Internal, in.NoInternal, mInternal); err != nil {
		return RowOut{}, err
	}
	byWhom := addedBy(ctx)
	fields := map[string]value.Value{mStrict: value.BoolOf(in.Strict), mInternal: value.BoolOf(internal),
		mSensitive: value.BoolOf(in.Sensitive), mReviewed: value.BoolOf(byWhom == schema.MetaAddedByPerson)}
	scope := scopeOf(target)
	if err := optTexts(fields, map[string]string{mName: in.Name, mScope: scope, mType: in.Type, mDescription: in.Description, mAddedBy: byWhom}); err != nil {
		return RowOut{}, err
	}
	w, err := create(ctx, o.env, target, schema.TableMetaKeys, map[string]string{mName: in.Name, mScope: scope}, fields)
	if err != nil {
		return RowOut{}, err
	}
	k := located{Src: target, Row: w.Row}
	for _, v := range vals {
		if _, err := o.addValue(ctx, k, v, ""); err != nil {
			return RowOut{}, err
		}
	}
	extra := []string{in.Type}
	if in.Strict {
		extra = append(extra, "strict")
	}
	if len(vals) > 0 {
		extra = append(extra, "values "+strings.Join(vals, ", "))
	}
	if in.Sensitive {
		extra = append(extra, "sensitive")
	}
	vis := "not internal"
	if internal {
		vis = "internal"
	}
	human := fmt.Sprintf("added %s %s (%s) to %s (%s)\n", short(w.Row.ID), in.Name, strings.Join(extra, ", "), place(target), vis)
	if meta.TagLike(in.Name) {
		human += tagLikeNote(in.Name)
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(target), Human: human}, nil
}

// triStateBool applies a --x / --no-x pair to b.
func triStateBool(b *bool, on, off bool, name string) error {
	switch {
	case on && off:
		return errs.New(errs.InvalidInput, "give --"+name+" or --no-"+name+", not both")
	case on:
		*b = true
	case off:
		*b = false
	}
	return nil
}

func (o ops) metaDescribe(ctx context.Context, in MetaDescribeIn) (RowOut, error) {
	if utf8.RuneCountInString(in.Text) > maxMetaDescription {
		return RowOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("a description has at most %d characters", maxMetaDescription))
	}
	k, err := o.metaKey(ctx, in.Key)
	if err != nil {
		return RowOut{}, err
	}
	v, err := txt(in.Text)
	if err != nil {
		return RowOut{}, err
	}
	patch := map[string]value.Value{mDescription: v}
	// a person's description approves an agent's key (MQ7); an agent's
	// description leaves it for review
	if addedBy(ctx) == schema.MetaAddedByPerson {
		patch[mReviewed] = value.BoolOf(true)
	}
	return o.patchRow(ctx, k, patch, text(k.Row, mName)+": description set")
}

func (o ops) metaRename(ctx context.Context, in RenameIn) (RowOut, error) {
	if err := checkKeyName(in.New); err != nil {
		return RowOut{}, err
	}
	k, err := o.metaKey(ctx, in.Old)
	if err != nil {
		return RowOut{}, err
	}
	rows, err := liveRows(ctx, o.env, []store.Source{k.Src}, schema.TableMetaKeys)
	if err != nil {
		return RowOut{}, err
	}
	if len(where(rows, map[string]string{mName: in.New, mScope: text(k.Row, mScope)})) > 0 {
		return RowOut{}, errs.New(errs.MetaTaken, "a metadata key named "+quote(in.New)+" already exists there")
	}
	v, err := txt(in.New)
	if err != nil {
		return RowOut{}, err
	}
	return o.patchRow(ctx, k, map[string]value.Value{mName: v},
		"renamed "+text(k.Row, mName)+" → "+in.New+" in the registry; row labels refresh on each row's next write; no values changed")
}

func (o ops) metaKeys(ctx context.Context, in MetaKeysIn) (MetaKeysOut, error) {
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableMetaKeys)
	if err != nil {
		return MetaKeysOut{}, err
	}
	out := MetaKeysOut{Data: []MetaKey{}, ids: in.IDs}
	data, err := o.metaRows(ctx)
	if err != nil {
		return MetaKeysOut{}, err
	}
	use := usage(data)
	for _, k := range rows {
		n := 0
		if u := use[k.Row.ID.String()]; u != nil {
			n = u.rows
		}
		if in.Unused && n > 0 {
			continue
		}
		name := text(k.Row, mName)
		if len(in.Keys) > 0 && !slices.Contains(in.Keys, name) {
			continue
		}
		if !placeMatches(text(k.Row, mScope), in.Place) {
			continue
		}
		if u := use[k.Row.ID.String()]; in.Table != "" && (u == nil || u.byTable[row.Table(in.Table)] == 0) {
			continue
		}
		unreviewed := isUnreviewed(k.Row)
		if in.Unreviewed && !unreviewed {
			continue
		}
		vs, verr := o.metaValues(ctx, k)
		if verr != nil {
			return MetaKeysOut{}, verr
		}
		out.Data = append(out.Data, MetaKey{ID: k.Row.ID.String(), Name: name, Scope: text(k.Row, mScope), Store: string(k.Src),
			Type: text(k.Row, mType), Strict: flag(k.Row, mStrict), Internal: flag(k.Row, mInternal), Sensitive: flag(k.Row, mSensitive),
			Description: text(k.Row, mDescription), Values: valueNames(vs), AddedBy: text(k.Row, mAddedBy), Unreviewed: unreviewed,
			Rows: n, Label: text(k.Row, mLabel), Retired: text(k.Row, mMergedInto)})
	}
	out.Count = len(out.Data)
	return out, nil
}

// MetaDeleteIn is the input of meta.delete.
type MetaDeleteIn struct {
	Key    string `json:"id" arg:"0" desc:"the key (name or mky_ id)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why (deleted_reason on the key row, shown by lore trash)"`
}

func (o ops) metaDelete(ctx context.Context, in MetaDeleteIn) (RowOut, error) {
	if in.Reason != "" {
		ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason
	}
	k, err := o.metaKey(ctx, in.Key)
	if err != nil {
		return RowOut{}, err
	}
	// meta_values cascade with their key (schema relation).
	if _, err := o.env.Delete(ctx, engine.Ref{Store: k.Src, Table: schema.TableMetaKeys, ID: k.Row.ID}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: k.Row.ID.String(), Store: string(k.Src),
		Human: trashline.Moved("metadata key "+text(k.Row, mName)+" ("+short(k.Row.ID)+")", k.Row.ID.String())}, nil
}

func (o ops) metaValueAdd(ctx context.Context, in MetaValueAddIn) (RowOut, error) {
	if err := checkMetaValues([]string{in.Value}); err != nil {
		return RowOut{}, err
	}
	k, err := o.metaKey(ctx, in.Key)
	if err != nil {
		return RowOut{}, err
	}
	have, err := o.metaValues(ctx, k)
	if err != nil {
		return RowOut{}, err
	}
	if len(have)+1 > maxMetaValues {
		return RowOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("a key has at most %d allowed values", maxMetaValues))
	}
	w, err := o.addValue(ctx, k, in.Value, in.Label)
	if err != nil {
		return RowOut{}, err
	}
	all := append(valueNames(have), in.Value)
	return RowOut{ID: w.Row.ID.String(), Store: string(k.Src),
		Human: fmt.Sprintf("%s: added value %s (%s); allowed values %s\n", text(k.Row, mName), in.Value, short(w.Row.ID), strings.Join(all, ", "))}, nil
}

func (o ops) metaValueRename(ctx context.Context, in MetaValueRenameIn) (RowOut, error) {
	if err := checkMetaValues([]string{in.New}); err != nil {
		return RowOut{}, err
	}
	k, err := o.metaKey(ctx, in.Key)
	if err != nil {
		return RowOut{}, err
	}
	have, err := o.metaValues(ctx, k)
	if err != nil {
		return RowOut{}, err
	}
	if slices.Contains(valueNames(have), in.New) {
		return RowOut{}, errs.New(errs.InvalidInput, text(k.Row, mName)+" already has the value "+quote(in.New))
	}
	for _, v := range have {
		if text(v.Row, mName) != in.Old {
			continue
		}
		nv, terr := txt(in.New)
		if terr != nil {
			return RowOut{}, terr
		}
		if _, err := o.env.Update(ctx, engine.Ref{Store: v.Src, Table: schema.TableMetaValues, ID: v.Row.ID}, map[string]value.Value{mName: nv}); err != nil {
			return RowOut{}, err
		}
		return RowOut{ID: v.Row.ID.String(), Store: string(v.Src),
			Human: fmt.Sprintf("%s: renamed value %s %s → %s; row labels refresh on each row's next write; no values changed\n",
				text(k.Row, mName), short(v.Row.ID), in.Old, in.New)}, nil
	}
	return RowOut{}, errs.New(errs.NotFound, text(k.Row, mName)+" has no value "+quote(in.Old))
}

// metaWriteErrors are the codes of meta set / unset (METADATA 6).
var metaWriteErrors = append(slices.Clone(writeErrors), errs.MetaKey, errs.MetaReserved, errs.MetaType, errs.MetaValue,
	errs.MetaShape, errs.MetaSize, errs.MetaDepth, errs.MetaOp, errs.MetaTable, errs.MetaUnknown, errs.MetaAmbiguous,
	errs.MetaUnregistered, errs.MetaRequired, errs.MetaInUse, errs.MetaRetired)

// checkKeyName is the name rule of a registry key (3.5): dotted snake_case
// ASCII (E_META_KEY, E_META_DEPTH) and not one of lore's words
// (E_META_RESERVED, columns of the built-in tables).
func checkKeyName(name string) error {
	if err := meta.CheckName(name, meta.MaxDepth); err != nil {
		return err
	}
	return meta.Reserved(name, builtinColumns())
}

// builtinColumns are the field names of every built-in table.
func builtinColumns() map[string]bool {
	out := map[string]bool{}
	for _, t := range schema.Builtin() {
		for _, f := range t.Fields {
			out[f.Name] = true
		}
	}
	return out
}

// tagLikeNote is the MQ5 warning line for a key named like a tag.
func tagLikeNote(name string) string {
	return "warning: key " + quote(name) + " looks like a tag; lore keeps labels in tags (lore tag), not in metadata\n"
}

// addedBy is who registers a key now (MQ7).
func addedBy(ctx context.Context) string {
	if engine.AgentOf(ctx) != "" {
		return schema.MetaAddedByAgent
	}
	return schema.MetaAddedByPerson
}

// isUnreviewed: a key row an agent added that no person approved; a row
// without the field (older lore, a person's key) counts as reviewed.
func isUnreviewed(r row.Row) bool {
	b, ok := r.Fields[mReviewed].(value.Bool)
	return ok && !b.Bool()
}

// MetaSetIn is the input of meta.set. Its own field names keep it out of
// Run's generic --meta binding: the op binds the change to the row it names.
type MetaSetIn struct {
	ID   string `json:"id,omitempty" arg:"0" desc:"the row (any id); with --where, a key=value pair"`
	Pair string `json:"pair,omitempty" arg:"1" desc:"key=value, key:type=value, project:key=value"`
	// Where and DryRun are the bulk form (3.19): every row the filter
	// names, one batch.
	Where  []string `json:"where,omitempty" flag:"where" desc:"set on every row matching this filter (repeatable, ANDed; one batch)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"with --where: list the rows, write nothing"`
	All    bool     `json:"all,omitempty" flag:"all" desc:"with --dry-run: list every row, not a sample"`
	More   []string `json:"set,omitempty" flag:"set" desc:"more key=value pairs (repeatable)"`
	JSON   []string `json:"json,omitempty" flag:"meta-json" desc:"a JSON value: key=<json>, key=@file.json or key=- for stdin (repeatable)"`
	Stdin  string   `json:"stdin_key,omitempty" flag:"meta-stdin" desc:"read the value of this key from stdin"`
}

// MetaUnsetIn is the input of meta.unset.
type MetaUnsetIn struct {
	ID   string   `json:"id,omitempty" arg:"0" desc:"the row (any id); with --where, the key to remove"`
	Key  string   `json:"key,omitempty" arg:"1" desc:"the key to remove (deploy removes deploy.env too)"`
	More []string `json:"keys,omitempty" flag:"key" desc:"more keys to remove (repeatable)"`
	// Where and DryRun are the bulk form (3.19).
	Where  []string `json:"where,omitempty" flag:"where" desc:"remove from every row matching this filter (repeatable, ANDed; one batch)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"with --where: list the rows, write nothing"`
	All    bool     `json:"all,omitempty" flag:"all" desc:"with --dry-run: list every row, not a sample"`
}

// MetaGetIn is the input of meta.get.
type MetaGetIn struct {
	ID  string `json:"id" arg:"0" desc:"the row (any id)"`
	Key string `json:"key,omitempty" arg:"1" desc:"only this key"`
	IDs bool   `json:"ids,omitempty" flag:"ids" desc:"show key ids"`
}

// MetaValue is one value as shown; a sensitive value is masked and has no
// JSON (OQ2).
type MetaValue struct {
	Key       string `json:"key"`
	KeyID     string `json:"key_id"`
	Scope     string `json:"scope"`
	Value     string `json:"value"`
	JSON      string `json:"json,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// MetaGetOut is the output of meta.get.
type MetaGetOut struct {
	ID    string      `json:"id"`
	Count int         `json:"count"`
	Data  []MetaValue `json:"data"`
	ids   bool
}

// Text implements op.Texter (the `meta show` sample of 3.2).
func (o MetaGetOut) Text() string {
	if len(o.Data) == 0 {
		return "(no metadata)\n"
	}
	var b strings.Builder
	for _, v := range o.Data {
		if o.ids {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", v.KeyID, v.Key, v.Value)
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\n", v.Key, v.Value)
	}
	return b.String()
}

// rowID parses the id of meta set/unset/get.
func rowID(s string) (ids.AnyID, error) {
	id, err := ids.ParseAny(strings.TrimSpace(s))
	if err != nil {
		// The one lookup rule (ops/internal/lookup): a value that cannot be
		// the id of an existing row is E_NOT_FOUND, never E_INVALID_ID.
		return ids.AnyID{}, lookup.BadID("meta: "+quote(s)+" is not a row id", err)
	}
	return id, nil
}

// metaRowWrite runs one metadata change on the row id names (flow: bind
// the MetaWrite to the id → Env.Update with no field change → the engine
// resolves, checks and writes, R13) and reports it.
func (o ops) metaRowWrite(ctx context.Context, id ids.AnyID, mw engine.MetaWrite, done func(row.Row) string) (RowOut, error) {
	w, err := o.env.Update(engine.WithMeta(ctx, mw), engine.Ref{ID: id}, map[string]value.Value{})
	if err != nil {
		return RowOut{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n", w.Row.Table, short(w.Row.ID), done(w.Row))
	for _, x := range w.Warnings {
		b.WriteString(x.Message + "\n")
	}
	return RowOut{ID: w.Row.ID.String(), Human: b.String()}, nil
}

// setEcho is "name = value, …" of the keys a meta set wrote, read back
// from the stored row so a sensitive key echoes "= •••" (METADATA 3.2,
// 3.20; OV10) and a typed value shows as stored. A dotted (object field)
// name, or one the row does not hold, shows alone.
func setEcho(r row.Row, names []string) string {
	byName := map[string]meta.Entry{}
	for _, j := range r.Meta {
		if en, err := meta.DecodeEntry(j); err == nil {
			byName[en.Name] = en
		}
	}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		en, ok := byName[meta.TopSegment(n)]
		switch {
		case ok && en.Sensitive:
			parts = append(parts, n+" = "+meta.Show(en))
		case ok && len(meta.Segments(n)) == 1:
			parts = append(parts, n+" = "+meta.Show(en))
		default: // a field of an object key: the name alone
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, ", ")
}

// metaSet is `lore meta set <id> k=v` (3.2; MQ9, MQ12, R13).
func (o ops) metaSet(ctx context.Context, in MetaSetIn) (RowOut, error) {
	if len(in.Where) > 0 {
		return o.metaSetWhere(ctx, in)
	}
	id, err := rowID(in.ID)
	if err != nil {
		return RowOut{}, err
	}
	f := engine.MetaFlags{MetaJSON: in.JSON, MetaStdin: in.Stdin}
	if in.Pair != "" {
		f.Meta = append(f.Meta, in.Pair)
	}
	f.Meta = append(f.Meta, in.More...)
	if f.Empty() {
		return RowOut{}, errs.New(errs.InvalidInput, "meta set: nothing to set").WithHint("lore meta set <id> key=value")
	}
	mw, err := f.Write("", id, nil)
	if err != nil {
		return RowOut{}, err
	}
	var names []string
	for _, t := range append(slices.Clone(mw.Set), mw.JSON...) {
		names = append(names, t.Name())
	}
	return o.metaRowWrite(ctx, id, mw, func(r row.Row) string { return "set " + setEcho(r, names) })
}

// metaUnset is `lore meta unset <id> <key>` (3.2): E_META_UNKNOWN when the
// row lacks the key.
func (o ops) metaUnset(ctx context.Context, in MetaUnsetIn) (RowOut, error) {
	if len(in.Where) > 0 {
		return o.metaUnsetWhere(ctx, in)
	}
	id, err := rowID(in.ID)
	if err != nil {
		return RowOut{}, err
	}
	if in.Key == "" {
		return RowOut{}, errs.New(errs.InvalidInput, "meta unset: name the key").WithHint("lore meta unset <id> <key>")
	}
	keys := append([]string{in.Key}, in.More...)
	done := "removed " + strings.Join(keys, ", ")
	return o.metaRowWrite(ctx, id, engine.MetaWrite{ID: id, Unset: keys}, func(row.Row) string { return done })
}

// metaGet shows one row's metadata, sensitive values masked (OQ2).
func (o ops) metaGet(ctx context.Context, in MetaGetIn) (MetaGetOut, error) {
	id, err := rowID(in.ID)
	if err != nil {
		return MetaGetOut{}, err
	}
	src, r, err := o.env.Get(ctx, engine.Ref{ID: id})
	if err != nil {
		return MetaGetOut{}, err
	}
	if r.Deleted { // DELETION 3.2: a trashed row answers E_IN_TRASH
		return MetaGetOut{}, lookup.Gone(ctx, o.env, engine.Ref{Store: src, ID: id}, errs.New(errs.NotFound, "meta show: "+id.String()+" is deleted"))
	}
	out := MetaGetOut{ID: r.ID.String(), Data: []MetaValue{}, ids: in.IDs}
	for kid, j := range r.Meta {
		en, derr := meta.DecodeEntry(j)
		if derr != nil {
			return MetaGetOut{}, derr
		}
		if in.Key != "" && en.Name != in.Key && kid.String() != in.Key {
			continue
		}
		v := MetaValue{Key: en.Name, KeyID: kid.String(), Scope: en.Scope, Value: meta.Show(en), Sensitive: en.Sensitive}
		if !en.Sensitive {
			v.JSON = string(en.V)
		}
		out.Data = append(out.Data, v)
	}
	slices.SortFunc(out.Data, func(a, b MetaValue) int { return strings.Compare(a.Key+a.KeyID, b.Key+b.KeyID) })
	out.Count = len(out.Data)
	return out, nil
}

// placeMatches reports a key scope inside --place: a level word (repo,
// project, org) matches every scope of that level, a scope label only
// itself; "" matches all.
func placeMatches(scope, place string) bool {
	return place == "" || scope == place || strings.HasPrefix(scope, place+":")
}
