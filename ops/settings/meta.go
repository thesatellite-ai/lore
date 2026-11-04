package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
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
}

// MetaEditIn is the input of meta.edit.
type MetaEditIn struct {
	Key        string `json:"id" arg:"0" desc:"the key (name or mky_ id)"`
	Strict     bool   `json:"strict,omitempty" flag:"strict" desc:"turn mismatches into refusals"`
	NoStrict   bool   `json:"no_strict,omitempty" flag:"no-strict" desc:"turn mismatches back into warnings"`
	Internal   bool   `json:"internal,omitempty" flag:"internal" desc:"mark it internal"`
	NoInternal bool   `json:"no_internal,omitempty" flag:"no-internal" desc:"mark it not internal"`
	Values     string `json:"values,omitempty" flag:"values" desc:"the new allowed values, comma separated"`
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
	head := "KEY\tSCOPE\tTYPE\tVALUES\n"
	if o.ids {
		head = "ID\t" + head
	}
	b.WriteString(head)
	for _, k := range o.Data {
		if o.ids {
			b.WriteString(short(mustAny(k.ID)) + "\t")
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", k.Name, k.Scope, k.Type, strings.Join(k.Values, ", "))
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
	return []op.Entry{
		op.Define(op.Spec{Name: OpMetaAdd, Summary: "Add a metadata key to a place's registry", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST,
			Examples: []op.Example{{Title: "a closed set", CLI: "lore meta add release_window --type text --strict --values spring,summer", Want: "mky_"}}}, o.metaAdd),
		op.Define(op.Spec{Name: OpMetaEdit, Summary: "Edit a metadata key (strict, internal, allowed values)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.HasDependents), Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST}, o.metaEdit),
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
		op.Define(op.Spec{Name: OpMetaValueRename, Summary: "Rename an allowed value (a label change; the val_ id stays)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors}, o.metaValueRename),
	}
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
	if !snakeName.MatchString(in.Name) {
		return RowOut{}, errs.New(errs.InvalidInput, "metadata key "+quote(in.Name)+" is not snake_case")
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
	fields := map[string]value.Value{mStrict: value.BoolOf(in.Strict), mInternal: value.BoolOf(internal)}
	scope := scopeOf(target)
	if err := optTexts(fields, map[string]string{mName: in.Name, mScope: scope, mType: in.Type, mDescription: in.Description}); err != nil {
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
	vis := "not internal"
	if internal {
		vis = "internal"
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(target),
		Human: fmt.Sprintf("added %s %s (%s) to %s (%s)\n", short(w.Row.ID), in.Name, strings.Join(extra, ", "), place(target), vis)}, nil
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

func (o ops) metaEdit(ctx context.Context, in MetaEditIn) (RowOut, error) {
	k, err := o.metaKey(ctx, in.Key)
	if err != nil {
		return RowOut{}, err
	}
	patch := map[string]value.Value{}
	if err := triState(patch, mStrict, in.Strict, in.NoStrict); err != nil {
		return RowOut{}, err
	}
	if err := triState(patch, mInternal, in.Internal, in.NoInternal); err != nil {
		return RowOut{}, err
	}
	name := text(k.Row, mName)
	var want []string
	if in.Values != "" {
		want = splitList(in.Values)
		if err := checkMetaValues(want); err != nil {
			return RowOut{}, err
		}
		have, verr := o.metaValues(ctx, k)
		if verr != nil {
			return RowOut{}, verr
		}
		for _, v := range have {
			if !slices.Contains(want, text(v.Row, mName)) {
				if _, err := o.env.Delete(ctx, engine.Ref{Store: v.Src, Table: schema.TableMetaValues, ID: v.Row.ID}); err != nil {
					return RowOut{}, err
				}
			}
		}
		for _, v := range want {
			if !slices.Contains(valueNames(have), v) {
				if _, err := o.addValue(ctx, k, v, ""); err != nil {
					return RowOut{}, err
				}
			}
		}
	}
	if len(patch) == 0 && in.Values == "" {
		return RowOut{}, errs.New(errs.InvalidInput, "meta edit: nothing to change").WithHint("give --strict, --no-strict, --internal, --no-internal or --values")
	}
	if len(patch) == 0 {
		return RowOut{ID: k.Row.ID.String(), Store: string(k.Src), Human: name + ": allowed values " + strings.Join(want, ", ") + "\n"}, nil
	}
	return o.patchRow(ctx, k, patch, "edited "+name)
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
	return o.patchRow(ctx, k, map[string]value.Value{mDescription: v}, text(k.Row, mName)+": description set")
}

func (o ops) metaRename(ctx context.Context, in RenameIn) (RowOut, error) {
	if !snakeName.MatchString(in.New) {
		return RowOut{}, errs.New(errs.InvalidInput, "metadata key "+quote(in.New)+" is not snake_case")
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
	for _, k := range rows {
		name := text(k.Row, mName)
		if len(in.Keys) > 0 && !slices.Contains(in.Keys, name) {
			continue
		}
		vs, verr := o.metaValues(ctx, k)
		if verr != nil {
			return MetaKeysOut{}, verr
		}
		out.Data = append(out.Data, MetaKey{ID: k.Row.ID.String(), Name: name, Scope: text(k.Row, mScope), Store: string(k.Src),
			Type: text(k.Row, mType), Strict: flag(k.Row, mStrict), Internal: flag(k.Row, mInternal), Sensitive: flag(k.Row, mSensitive),
			Description: text(k.Row, mDescription), Values: valueNames(vs)})
	}
	out.Count = len(out.Data)
	return out, nil
}

func (o ops) metaDelete(ctx context.Context, in MetaKeyRef) (RowOut, error) {
	k, err := o.metaKey(ctx, in.Key)
	if err != nil {
		return RowOut{}, err
	}
	// meta_values cascade with their key (schema relation).
	if _, err := o.env.Delete(ctx, engine.Ref{Store: k.Src, Table: schema.TableMetaKeys, ID: k.Row.ID}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: k.Row.ID.String(), Store: string(k.Src),
		Human: fmt.Sprintf("✓ deleted metadata key %s (%s)\n", text(k.Row, mName), short(k.Row.ID))}, nil
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
