package settings

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
)

// Field options of RES 3.1 (OV32), the kind_fields columns of the same
// names (schema/builtin.go). min/max/default are text so one column covers
// every type, the way meta_keys stores them.
const (
	fDefault      = "default"
	fPattern      = "pattern"
	fMin          = "min"
	fMax          = "max"
	fStrict       = "strict"
	fDeprecated   = "deprecated"
	fReplacedBy   = "replaced_by"
	fRemindBefore = "remind_before"
	fTypeHistory  = "type_history"
	kSecretMgr    = "secret_manager"
)

// FieldRuleFlags are the field options shared by field add and field edit
// (RES 3.1 "Field options"). Exported: the CLI builder skips unexported
// embedded structs (OV27).
type FieldRuleFlags struct {
	Default      string `json:"default,omitempty" flag:"default" desc:"the value written when an add does not set the field"`
	Pattern      string `json:"pattern,omitempty" flag:"pattern" desc:"text only: values must match this RE2 expression"`
	Min          string `json:"min,omitempty" flag:"min" desc:"number, date, datetime: inclusive lower bound"`
	Max          string `json:"max,omitempty" flag:"max" desc:"number, date, datetime: inclusive upper bound"`
	Strict       bool   `json:"strict,omitempty" flag:"strict" desc:"a both-sides merge change goes to a person instead of newer-wins"`
	Deprecated   bool   `json:"deprecated,omitempty" flag:"deprecated" desc:"writes warn; lint lists rows that still use it"`
	ReplacedBy   string `json:"replaced_by,omitempty" flag:"replaced-by" desc:"with --deprecated: the field to use instead"`
	RemindBefore string `json:"remind_before,omitempty" flag:"remind-before" desc:"date, datetime: lint lists rows whose value is within this time (e.g. 30d, 2w, 12h)"`
}

// remindUnits are the day-sized units --remind-before takes beside Go's
// h/m/s (RES 3.1: d = 24 h, w = 7 d; months refused, their length varies).
var remindUnits = map[byte]time.Duration{'d': hoursPerDay * time.Hour, 'w': 7 * hoursPerDay * time.Hour}

// parseRemind reads a --remind-before value (30d, 2w, 12h).
func parseRemind(s string) (time.Duration, error) {
	if s != "" {
		if unit, ok := remindUnits[s[len(s)-1]]; ok {
			n, err := strconv.Atoi(s[:len(s)-1])
			if err == nil && n > 0 {
				return time.Duration(n) * unit, nil
			}
		}
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d, nil
		}
	}
	return 0, errs.New(errs.InvalidInput, "--remind-before "+quote(s)+" is not a duration").WithHint("e.g. 30d, 2w or 12h")
}

// isTextLike are the kinds --pattern applies to (meta.Violation's set).
func isTextLike(k value.Kind) bool {
	return k == value.KindText || k == value.KindEmail || k == value.KindURL || k == value.KindDNS
}

// fieldOptions validates fl for a field of type typ (allowed values vals,
// sibling fields by name for --replaced-by) and writes the set options
// into patch. Flow: pattern/min/max checked by logic/meta (one rule engine
// for metadata and resources, R12) → remind_before by type → replaced_by
// names a sibling → default parses as the type and fits the rules.
func fieldOptions(typ string, vals []string, siblings map[string]located, fl FieldRuleFlags, patch map[string]value.Value) error {
	k, err := value.ParseKind(typ)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "field type "+quote(typ), err)
	}
	if fl.Pattern != "" && !isTextLike(k) {
		return errs.New(errs.InvalidInput, "--pattern applies to text fields, not "+typ)
	}
	rules := meta.Rules{Pattern: fl.Pattern, Min: fl.Min, Max: fl.Max}
	if err = meta.CheckRuleDefs(meta.Type{Kind: k}, rules); err != nil {
		return err
	}
	if fl.RemindBefore != "" {
		if k != value.KindDate && k != value.KindDateTime {
			return errs.New(errs.InvalidInput, "--remind-before applies to date and datetime fields, not "+typ)
		}
		if _, err = parseRemind(fl.RemindBefore); err != nil {
			return err
		}
	}
	if fl.ReplacedBy != "" {
		if !fl.Deprecated {
			return errs.New(errs.InvalidInput, "--replaced-by needs --deprecated")
		}
		rep, ok := siblings[fl.ReplacedBy]
		if !ok {
			return errs.New(errs.NotFound, "--replaced-by "+quote(fl.ReplacedBy)+": the kind has no such field")
		}
		// Stored as the fld_ id so a rename of the replacement keeps the link (OQ20).
		if patch[fReplacedBy], err = txt(rep.Row.ID.String()); err != nil {
			return err
		}
	}
	if fl.Default != "" {
		if err = checkDefault(k, fl.Default, vals, rules); err != nil {
			return err
		}
	}
	if err = optTexts(patch, map[string]string{fDefault: fl.Default, fPattern: fl.Pattern, fMin: fl.Min, fMax: fl.Max, fRemindBefore: fl.RemindBefore}); err != nil {
		return err
	}
	if fl.Strict {
		patch[fStrict] = value.BoolOf(true)
	}
	if fl.Deprecated {
		patch[fDeprecated] = value.BoolOf(true)
	}
	return nil
}

// checkDefault refuses a --default the field itself would refuse. A ref,
// ref_list or secret_ref default is refused: a default ref would name one
// resource for every place, and a default secret name would be a guess.
func checkDefault(k value.Kind, raw string, vals []string, rules meta.Rules) error {
	switch k {
	case value.KindRef, value.KindSecretRef:
		return errs.New(errs.InvalidInput, "--default is not allowed on a "+string(k)+" field")
	default:
	}
	if len(vals) > 0 && !slices.Contains(vals, raw) {
		return errs.New(errs.InvalidInput, "--default "+quote(raw)+" is not one of "+strings.Join(vals, ", "))
	}
	v, err := parseAs(k, raw)
	if err != nil {
		return errs.New(errs.InvalidInput, "--default "+quote(raw)+" is not a "+string(k))
	}
	b, err := v.MarshalJSON()
	if err != nil {
		return errs.Wrap(errs.Internal, "encode default", err)
	}
	why, err := meta.Violation("--default", meta.Type{Kind: k}, b, rules)
	if err != nil {
		return err
	}
	if why != "" {
		return errs.New(errs.InvalidInput, why)
	}
	return nil
}

// rulesOf are the pattern/min/max/deprecation options of field row f.
func rulesOf(f located) meta.Rules {
	return meta.Rules{Pattern: text(f.Row, fPattern), Min: text(f.Row, fMin), Max: text(f.Row, fMax),
		Deprecated: flag(f.Row, fDeprecated), ReplacedBy: text(f.Row, fReplacedBy)}
}

// checkFieldRules refuses a parsed value of field def that breaks its
// pattern or bounds (RES 3.1: "the value must match"; E_RESOURCE_INVALID).
// Unlike metadata, a resource rule always refuses: `strict` on a resource
// field is the merge rule (3.13), not a warn/refuse switch.
func checkFieldRules(d kindDef, name string, def located, k value.Kind, v value.Value) error {
	r := rulesOf(def)
	if r.Pattern == "" && r.Min == "" && r.Max == "" {
		return nil
	}
	b, err := v.MarshalJSON()
	if err != nil {
		return errs.Wrap(errs.Internal, "encode value", err)
	}
	why, err := meta.Violation(text(def.Row, kName), meta.Type{Kind: k}, b, meta.Rules{Pattern: r.Pattern, Min: r.Min, Max: r.Max})
	if err != nil {
		return err
	}
	if why != "" {
		return invalid(d, name, why)
	}
	return nil
}

// fieldNameOf is the name of the field with fld_ id (the id when gone).
func (d kindDef) fieldNameOf(id string) string {
	if f, ok := d.byID[id]; ok {
		return text(f.Row, kName)
	}
	return id
}

// deprecationNotes are the warnings of a write that sets deprecated
// fields (RES 3.1: writes warn, naming the replacement).
func deprecationNotes(d kindDef, set map[string]json.RawMessage) string {
	var b strings.Builder
	for _, n := range d.fieldNames() {
		f := d.byName[n]
		if _, ok := set[f.Row.ID.String()]; !ok || !flag(f.Row, fDeprecated) {
			continue
		}
		r := rulesOf(f)
		r.ReplacedBy = d.fieldNameOf(r.ReplacedBy)
		b.WriteString("warning: " + meta.DeprecatedNote(d.name()+"."+n, r) + "\n")
	}
	return b.String()
}

// applyDefaults fills every unset field that has a default (RES 3.1: "the
// value written when an add does not set the field"), on add only, and
// returns the "default" notes.
func (o ops) applyDefaults(ctx context.Context, d kindDef, name string, vals map[string]json.RawMessage) (string, error) {
	var b strings.Builder
	for _, n := range d.fieldNames() {
		f := d.byName[n]
		dflt := text(f.Row, fDefault)
		if _, set := vals[f.Row.ID.String()]; set || dflt == "" {
			continue
		}
		v, err := o.fieldValue(ctx, d, name, f, dflt)
		if err != nil {
			return "", err
		}
		raw, err := v.MarshalJSON()
		if err != nil {
			return "", errs.Wrap(errs.Internal, "encode default of "+n, err)
		}
		vals[f.Row.ID.String()] = raw
		b.WriteString("  " + n + " = " + display(v) + " (default)\n")
	}
	return b.String(), nil
}

// loosening is the confirmation a field edit asks before it loosens a
// field (RES 3.14: unmark sensitive/internal/strict, change or drop a
// pattern or bound that was set): E_NEEDS_CONFIRM without --yes.
func loosening(ctx context.Context, f located, in FieldEditIn) error {
	var what []string
	for _, x := range []struct {
		col string
		off bool
	}{{fSensitive, in.NoSensitive}, {fInternal, in.NoInternal}, {fStrict, in.NoStrict}} {
		if x.off && flag(f.Row, x.col) {
			what = append(what, "unmark "+x.col)
		}
	}
	for _, x := range []struct{ col, next string }{{fPattern, in.Pattern}, {fMin, in.Min}, {fMax, in.Max}} {
		if x.next != "" && text(f.Row, x.col) != "" && x.next != text(f.Row, x.col) {
			what = append(what, "change "+x.col+" "+text(f.Row, x.col)+" → "+x.next)
		}
	}
	if len(what) == 0 {
		return nil
	}
	return bulk.Ask(ctx, "loosen field "+text(f.Row, kName)+"? "+strings.Join(what, ", "))
}

// remindWindow is field f's --remind-before as a duration (0 when unset or
// unreadable: lint then has nothing to remind of).
func remindWindow(f located) time.Duration {
	d, err := parseRemind(text(f.Row, fRemindBefore))
	if err != nil {
		return 0
	}
	return d
}

// checkSecretManager refuses a --secret-manager that cannot be a
// secret_ref scheme (the value parser decides, so kind and value agree).
func checkSecretManager(m string) error {
	if m == "" {
		return nil
	}
	if _, err := value.SecretRefOf(m + "://probe"); err != nil {
		return errs.New(errs.InvalidInput, "--secret-manager "+quote(m)+" is not a secret manager scheme").WithHint("e.g. gopass, op, vault")
	}
	return nil
}
