package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Policy operations (CLI_REFERENCE 3.11): each rule set at a level is one
// `policies` row (pol_: key, value as JSON, scope). Lower levels may only
// tighten; logic/policy decides what "stricter" means per key.
const (
	OpPolicyShow  = "policy.show"
	OpPolicySet   = "policy.set"
	OpPolicyUnset = "policy.unset"
	// policyREST keeps the REST collection /v1/policy (spec 9.3 example).
	policyREST = "policy"
)

// policies field names (schema.Builtin).
const (
	pKey   = "key"
	pValue = "value"
	pScope = "scope"
)

// sourceDefault is the "source" word of a key no level sets.
const sourceDefault = "default"

// PolicyShowIn is the input of policy.show.
type PolicyShowIn struct {
	Key string `json:"id,omitempty" arg:"0" desc:"a key or a key prefix (default every key)"`
}

// PolicySetIn is the input of policy.set.
type PolicySetIn struct {
	Key   string `json:"key" arg:"0" desc:"the policy key (lore policy show lists them)"`
	Value string `json:"value" arg:"1" desc:"the value (a list key takes a comma list)"`
	To    string `json:"to,omitempty" flag:"to" desc:"the place (repo, <store>, <store>/<folder>, <store>/org; default repo)"`
}

// PolicyUnsetIn is the input of policy.unset.
type PolicyUnsetIn struct {
	Key string `json:"key" arg:"0" desc:"the policy key"`
	To  string `json:"to,omitempty" flag:"to" desc:"the place whose row is removed (default repo)"`
}

// PolicyRule is one key's effective value.
type PolicyRule struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Source is "default", or the level and the pol_ row.
	Source string `json:"source"`
	Level  string `json:"level,omitempty"`
	Row    string `json:"row,omitempty"`
	Store  string `json:"store,omitempty"`
	Doc    string `json:"doc"`
}

// PolicyOut is the output of policy.show.
type PolicyOut struct {
	Count int          `json:"count"`
	Data  []PolicyRule `json:"data"`
}

// Text implements op.Texter (CLI_REFERENCE 3.11 sample).
func (o PolicyOut) Text() string {
	if len(o.Data) == 0 {
		return "(no matching policy keys)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", r.Key, orNone(r.Value), r.Source)
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func (o ops) policyOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpPolicyShow, Summary: "Show the effective value of every policy rule and where it comes from", Permission: op.PermRead,
			Access: op.AccessRead, Errors: append(slices.Clone(readErrors), errs.ConfigInvalid), RESTNoun: policyREST}, o.policyShow),
		op.Define(op.Spec{Name: OpPolicySet, Summary: "Set a policy rule at a level (may only tighten a higher level)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: writeErrors, Confirm: op.ConfirmRule{Always: true}, RESTNoun: policyREST,
			Examples: []op.Example{{Title: "keep the trash a year", CLI: "lore policy set trash.retention_days 365 --yes", Want: "pol_"}}}, o.policySet),
		op.Define(op.Spec{Name: OpPolicyUnset, Summary: "Remove a policy rule's row at a level (the next level up applies again)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: writeErrors, Confirm: op.ConfirmRule{Always: true}, RESTNoun: policyREST}, o.policyUnset),
	}
}

// policyEntries reads every live policies row with its level.
func (o ops) policyEntries(ctx context.Context) ([]policy.Entry, map[ids.AnyID]store.Source, error) {
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TablePolicies)
	if err != nil {
		return nil, nil, err
	}
	where := map[ids.AnyID]store.Source{}
	out := make([]policy.Entry, 0, len(rows))
	for _, l := range rows {
		e, eerr := policy.EntryFromRow(storeLevel(o.env, l.Src), l.Row)
		if eerr != nil {
			return nil, nil, eerr
		}
		out = append(out, e)
		where[l.Row.ID] = l.Src
	}
	return out, where, nil
}

func (o ops) policyShow(ctx context.Context, in PolicyShowIn) (PolicyOut, error) {
	entries, where, err := o.policyEntries(ctx)
	if err != nil {
		return PolicyOut{}, err
	}
	set, err := policy.Resolve(entries)
	if err != nil {
		return PolicyOut{}, err
	}
	out := PolicyOut{Data: []PolicyRule{}}
	for _, d := range policy.Keys() {
		if in.Key != "" && !strings.HasPrefix(string(d.Key), in.Key) {
			continue
		}
		eff, _ := set.Get(d.Key)
		r := PolicyRule{Key: string(d.Key), Value: display(eff.Value), Source: sourceDefault, Doc: d.Doc}
		if !eff.IsDefault {
			r.Level, r.Row, r.Store = string(eff.Level), eff.Source.String(), string(where[eff.Source])
			r.Source = fmt.Sprintf("%s %s (%s)", eff.Level, place(where[eff.Source]), short(eff.Source))
		}
		out.Data = append(out.Data, r)
	}
	out.Count = len(out.Data)
	return out, nil
}

// policyValue parses CLI text for key d.
func policyValue(d policy.KeyDef, raw string) (value.Value, error) {
	if d.Shape == policy.ShapeSet || d.Shape == policy.ShapeAllow {
		return textList(splitList(raw))
	}
	return inferText(raw)
}

// rankOf is a level's distance (0 = nearest).
func rankOf(l policy.Level) int { return slices.Index(policy.Levels(), l) }

func (o ops) policySet(ctx context.Context, in PolicySetIn) (RowOut, error) {
	d, ok := policy.Lookup(policy.Key(in.Key))
	if !ok {
		return RowOut{}, errs.New(errs.InvalidInput, "unknown policy key "+quote(in.Key)).WithHint("lore policy show lists the keys")
	}
	target := targetOf(in.To)
	if string(target) == storeMe {
		return RowOut{}, errs.New(errs.InvalidInput, "--to me: a personal store holds no team rules").WithHint("--to repo, --to <store> or --to <store>/<folder>")
	}
	if d.Key == policy.KeyContextTargets {
		// Q-R2-3: only known tool profiles (built-ins + the user's own).
		if err := checkContextTargets(ctx, o.env, in.Value); err != nil {
			return RowOut{}, err
		}
	}
	v, err := policyValue(d, in.Value)
	if err != nil {
		return RowOut{}, err
	}
	level := storeLevel(o.env, target)
	if _, err = policy.Resolve([]policy.Entry{{Level: level, Key: d.Key, Value: v}}); err != nil {
		return RowOut{}, errs.Wrap(errs.InvalidInput, "policy set "+in.Key+": "+quote(in.Value)+" is not a valid value", err)
	}
	entries, where, err := o.policyEntries(ctx)
	if err != nil {
		return RowOut{}, err
	}
	higher := []policy.Entry{{Level: level, Key: d.Key, Value: v}}
	for _, e := range entries {
		if e.Key == d.Key && rankOf(e.Level) > rankOf(level) {
			higher = append(higher, e)
		}
	}
	set, err := policy.Resolve(higher)
	if err != nil {
		return RowOut{}, err
	}
	if eff, _ := set.Get(d.Key); !eff.Source.IsZero() {
		return RowOut{}, errs.New(errs.PolicyDenied, fmt.Sprintf("%s: %s sets %s, so %s may only set a value at least as strict",
			in.Key, place(where[eff.Source]), display(eff.Value), place(target))).WithHint("nothing written")
	}
	jv, err := asJSON(v)
	if err != nil {
		return RowOut{}, err
	}
	rows, err := liveRows(ctx, o.env, []store.Source{target}, schema.TablePolicies)
	if err != nil {
		return RowOut{}, err
	}
	var w engine.Written
	if hit := where2(rows, in.Key); hit != nil {
		w, err = o.env.Update(ctx, engine.Ref{Store: target, Table: schema.TablePolicies, ID: hit.Row.ID}, map[string]value.Value{pValue: jv})
	} else {
		fields := map[string]value.Value{pValue: jv}
		if err = optTexts(fields, map[string]string{pKey: in.Key, pScope: scopeOf(target)}); err != nil {
			return RowOut{}, err
		}
		w, err = create(ctx, o.env, target, schema.TablePolicies, map[string]string{pKey: in.Key}, fields)
	}
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(target),
		Human: fmt.Sprintf("✓ %s = %s (%s, %s)\n", in.Key, display(v), place(target), short(w.Row.ID))}, nil
}

// where2 is the live row of key among rows (nil when none).
func where2(rows []located, key string) *located {
	if hit := where(rows, map[string]string{pKey: key}); len(hit) > 0 {
		return &hit[0]
	}
	return nil
}

func (o ops) policyUnset(ctx context.Context, in PolicyUnsetIn) (RowOut, error) {
	target := targetOf(in.To)
	rows, err := liveRows(ctx, o.env, []store.Source{target}, schema.TablePolicies)
	if err != nil {
		return RowOut{}, err
	}
	hit := where2(rows, in.Key)
	if hit == nil {
		return RowOut{}, errs.New(errs.NotFound, in.Key+" is not set in "+place(target)).WithHint("lore policy show " + in.Key + " shows where it is set")
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: target, Table: schema.TablePolicies, ID: hit.Row.ID}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: hit.Row.ID.String(), Store: string(target),
		Human: fmt.Sprintf("✓ %s unset in %s (%s); the next level up or the default applies\n", in.Key, place(target), short(hit.Row.ID))}, nil
}
