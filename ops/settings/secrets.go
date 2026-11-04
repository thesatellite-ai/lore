package settings

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Secret rule and allow operations (CLI_REFERENCE 3.13): a `secret_rules`
// row (srl_) per added, changed or switched-off rule, a `secret_allows`
// row (sal_) per "not a secret" decision, holding a 32-bit hash of the
// matched text, never the text.
const (
	OpSecretRules     = "secrets.rules"
	OpSecretRuleAdd   = "secrets.rule.add"
	OpSecretRuleEdit  = "secrets.rule.edit"
	OpSecretRuleOff   = "secrets.rule.off"
	OpSecretRuleOn    = "secrets.rule.on"
	OpSecretAllow     = "secrets.allow"
	OpSecretAllowRm   = "secrets.allow.rm"
	OpSecretAllowList = "secrets.allow.list"
	// rulesREST keeps `lore secrets rules` off POST /v1/secrets/rules,
	// which secrets.rule.add owns: it is GET-like, served at
	// /v1/scanner/rules.
	rulesREST = "scanner"
)

// secret_rules / secret_allows field names (schema.Builtin).
const (
	sRuleID      = "rule_id"
	sRegex       = "regex"
	sKeywords    = "keywords"
	sEntropy     = "entropy"
	sState       = "state"
	sDescription = "description"
	sReason      = "reason"
	aRowID       = "row_id"
	aField       = "field"
	aRuleID      = "rule_id"
	aHash        = "match_hash"
	aReason      = "reason"
)

// ruleIDPattern is what a rule id may hold (gitleaks ids: lower-case,
// digits and dashes).
var ruleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// RulesIn is the input of secrets.rules.
type RulesIn struct{}

// RuleAddIn is the input of secrets.rule.add and secrets.rule.edit.
type RuleAddIn struct {
	RuleID      string `json:"id" arg:"0" desc:"the rule id"`
	Regex       string `json:"regex,omitempty" flag:"regex" desc:"the RE2 pattern"`
	Keywords    string `json:"keywords,omitempty" flag:"keywords" desc:"keywords that must appear, comma separated"`
	Entropy     string `json:"entropy,omitempty" flag:"entropy" desc:"minimum Shannon entropy of a match"`
	Description string `json:"description,omitempty" flag:"description" desc:"what the rule catches"`
	To          string `json:"to,omitempty" flag:"to" desc:"the place whose rule row is written (repo, me, <store>, <store>/<folder>; default repo)"`
}

// RuleOffIn is the input of secrets.rule.off.
type RuleOffIn struct {
	RuleID string `json:"id" arg:"0" desc:"the rule id (a lore or gitleaks rule)"`
	Reason string `json:"reason" flag:"reason" desc:"why it is off (lore doctor lists it)"`
	To     string `json:"to,omitempty" flag:"to" desc:"the place (default repo; --to me: your own captures only)"`
}

// RuleOnIn is the input of secrets.rule.on.
type RuleOnIn struct {
	RuleID string `json:"id" arg:"0" desc:"the rule id"`
	To     string `json:"to,omitempty" flag:"to" desc:"the place (default repo)"`
}

// AllowIn is the input of secrets.allow.
type AllowIn struct {
	Row       string `json:"row_id" arg:"0" desc:"the row id the finding is on"`
	Rule      string `json:"rule" flag:"rule" desc:"the rule id of the finding"`
	Reason    string `json:"reason" flag:"reason" desc:"why it is not a secret"`
	Field     string `json:"field,omitempty" flag:"field" desc:"the field (default the one text field holding --match)"`
	Match     string `json:"match,omitempty" flag:"match" desc:"the matched text (only its hash is stored)"`
	MatchHash uint32 `json:"match_hash,omitempty" flag:"match-hash" desc:"the finding's match hash, instead of --match"`
}

// AllowRmIn is the input of secrets.allow.rm.
type AllowRmIn struct {
	ID string `json:"id" arg:"0" desc:"the allow entry (sal_…)"`
}

// AllowListIn is the input of secrets.allow.list.
type AllowListIn struct{}

// Rule is one secret_rules row as shown.
type Rule struct {
	ID          string   `json:"id"`
	RuleID      string   `json:"rule_id"`
	State       string   `json:"state"`
	Level       string   `json:"level"`
	Store       string   `json:"store"`
	Regex       string   `json:"regex,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Entropy     string   `json:"entropy,omitempty"`
	Description string   `json:"description,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// RulesOut is the output of secrets.rules.
type RulesOut struct {
	Count int    `json:"count"`
	Data  []Rule `json:"data"`
}

// Text implements op.Texter.
func (o RulesOut) Text() string {
	if len(o.Data) == 0 {
		return "(no rule rows: gitleaks defaults and lore's own pack apply)\n"
	}
	var b strings.Builder
	b.WriteString("RULE\tSTATE\tLEVEL\tWHERE\tNOTE\n")
	for _, r := range o.Data {
		note := r.Description
		if r.Reason != "" {
			note = r.Reason
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", r.RuleID, r.State, r.Level, place(store.Source(r.Store)), note)
	}
	return b.String()
}

// Allow is one secret_allows row as shown.
type Allow struct {
	ID        string `json:"id"`
	RowID     string `json:"row_id"`
	Field     string `json:"field"`
	RuleID    string `json:"rule_id"`
	MatchHash string `json:"match_hash"`
	Reason    string `json:"reason,omitempty"`
	Store     string `json:"store"`
}

// AllowsOut is the output of secrets.allow.list.
type AllowsOut struct {
	Count int     `json:"count"`
	Data  []Allow `json:"data"`
}

// Text implements op.Texter.
func (o AllowsOut) Text() string {
	if len(o.Data) == 0 {
		return "(no allow entries)\n"
	}
	var b strings.Builder
	b.WriteString("ID\tROW\tFIELD\tRULE\tREASON\n")
	for _, a := range o.Data {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", a.ID, a.RowID, a.Field, a.RuleID, a.Reason)
	}
	return b.String()
}

func (o ops) secretOps() []op.Entry {
	ask := op.ConfirmRule{Always: true}
	return []op.Entry{
		op.Define(op.Spec{Name: OpSecretRules, Summary: "List secret rule rows (added, changed or off) with their level", Permission: op.PermRead,
			Access: op.AccessRead, Errors: readErrors, RESTNoun: rulesREST}, o.rules),
		op.Define(op.Spec{Name: OpSecretRuleAdd, Summary: "Add a lore secret rule on top of the defaults", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: ask,
			Examples: []op.Example{{Title: "an internal token shape", CLI: `lore secrets rule add acme-token --regex 'acme_[a-z0-9]{32}' --yes`, Want: "srl_"}}}, o.ruleAdd),
		op.Define(op.Spec{Name: OpSecretRuleEdit, Summary: "Change a secret rule (lore defaults and gitleaks ids included)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: writeErrors, Confirm: ask}, o.ruleEdit),
		op.Define(op.Spec{Name: OpSecretRuleOff, Summary: "Switch a secret rule off at a level, with a reason", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: ask}, o.ruleOff),
		op.Define(op.Spec{Name: OpSecretRuleOn, Summary: "Switch a secret rule back on at a level", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: ask}, o.ruleOn),
		op.Define(op.Spec{Name: OpSecretAllow, Summary: "Record one finding as not a secret for one row, field and rule", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: writeErrors}, o.allow),
		op.Define(op.Spec{Name: OpSecretAllowRm, Summary: "Remove an allow entry (the finding is reported again)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: writeErrors, Confirm: ask}, o.allowRm),
		op.Define(op.Spec{Name: OpSecretAllowList, Summary: "List the allow entries", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.allowList),
	}
}

func ruleOf(env Env, l located) Rule {
	r := Rule{ID: l.Row.ID.String(), RuleID: text(l.Row, sRuleID), State: text(l.Row, sState), Level: string(storeLevel(env, l.Src)),
		Store: string(l.Src), Regex: text(l.Row, sRegex), Keywords: texts(l.Row, sKeywords), Description: text(l.Row, sDescription),
		Reason: text(l.Row, sReason)}
	if v, ok := l.Row.Fields[sEntropy]; ok {
		r.Entropy = v.String()
	}
	return r
}

func (o ops) rules(ctx context.Context, _ RulesIn) (RulesOut, error) {
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableSecretRules)
	if err != nil {
		return RulesOut{}, err
	}
	out := RulesOut{Data: []Rule{}}
	for _, l := range rows {
		out.Data = append(out.Data, ruleOf(o.env, l))
	}
	out.Count = len(out.Data)
	return out, nil
}

// ruleAt is the live rule row of id in target (nil when none).
func (o ops) ruleAt(ctx context.Context, target store.Source, id string) (*located, error) {
	rows, err := liveRows(ctx, o.env, []store.Source{target}, schema.TableSecretRules)
	if err != nil {
		return nil, err
	}
	if hit := where(rows, map[string]string{sRuleID: id}); len(hit) > 0 {
		return &hit[0], nil
	}
	return nil, nil
}

// ruleFields reads the pattern flags of rule add/edit.
func ruleFields(in RuleAddIn) (map[string]value.Value, error) {
	if !ruleIDPattern.MatchString(in.RuleID) {
		return nil, errs.New(errs.InvalidInput, "rule id "+quote(in.RuleID)+": use lower-case letters, digits, '.', '_' or '-'")
	}
	if in.Regex != "" {
		if _, err := regexp.Compile(in.Regex); err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "--regex is not an RE2 pattern", err)
		}
	}
	fields := map[string]value.Value{}
	if err := optTexts(fields, map[string]string{sRegex: in.Regex, sDescription: in.Description}); err != nil {
		return nil, err
	}
	if kw := splitList(in.Keywords); len(kw) > 0 {
		l, err := textList(kw)
		if err != nil {
			return nil, err
		}
		fields[sKeywords] = l
	}
	if in.Entropy != "" {
		if f, err := strconv.ParseFloat(in.Entropy, 64); err != nil || f < 0 {
			return nil, errs.New(errs.InvalidInput, "--entropy "+quote(in.Entropy)+" is not a non-negative number")
		}
		n, err := value.NumberOf(in.Entropy)
		if err != nil {
			return nil, err
		}
		fields[sEntropy] = n
	}
	return fields, nil
}

// putRule creates or patches the rule row of id in target.
func (o ops) putRule(ctx context.Context, target store.Source, id string, fields map[string]value.Value, verb string) (RowOut, error) {
	have, err := o.ruleAt(ctx, target, id)
	if err != nil {
		return RowOut{}, err
	}
	var w engine.Written
	if have != nil {
		w, err = o.env.Update(ctx, engine.Ref{Store: target, Table: schema.TableSecretRules, ID: have.Row.ID}, fields)
	} else {
		if fields[sRuleID], err = txt(id); err != nil {
			return RowOut{}, err
		}
		w, err = create(ctx, o.env, target, schema.TableSecretRules, map[string]string{sRuleID: id}, fields)
	}
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(target),
		Human: fmt.Sprintf("✓ %s %s in %s (%s)\n", id, verb, place(target), short(w.Row.ID))}, nil
}

func state(s string) (value.Value, error) { return txt(s) }

func (o ops) ruleAdd(ctx context.Context, in RuleAddIn) (RowOut, error) {
	if in.Regex == "" {
		return RowOut{}, errs.New(errs.InvalidInput, "secrets rule add: --regex is required")
	}
	fields, err := ruleFields(in)
	if err != nil {
		return RowOut{}, err
	}
	target := targetOf(in.To)
	if have, herr := o.ruleAt(ctx, target, in.RuleID); herr != nil {
		return RowOut{}, herr
	} else if have != nil {
		return RowOut{}, errs.New(errs.InvalidInput, "rule "+in.RuleID+" already has a row in "+place(target)).WithHint("lore secrets rule edit " + in.RuleID)
	}
	if fields[sState], err = state(schema.SecretRuleOn); err != nil {
		return RowOut{}, err
	}
	return o.putRule(ctx, target, in.RuleID, fields, "added")
}

func (o ops) ruleEdit(ctx context.Context, in RuleAddIn) (RowOut, error) {
	fields, err := ruleFields(in)
	if err != nil {
		return RowOut{}, err
	}
	if len(fields) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "secrets rule edit: nothing to change").WithHint("give --regex, --keywords, --entropy or --description")
	}
	target := targetOf(in.To)
	have, err := o.ruleAt(ctx, target, in.RuleID)
	if err != nil {
		return RowOut{}, err
	}
	// A rule lore itself added here stays "on"; any other rule edited at
	// this level (a default or gitleaks id) is "changed".
	st := schema.SecretRuleChanged
	if have != nil && text(have.Row, sState) == schema.SecretRuleOn {
		st = schema.SecretRuleOn
	}
	if fields[sState], err = state(st); err != nil {
		return RowOut{}, err
	}
	return o.putRule(ctx, target, in.RuleID, fields, "changed")
}

func (o ops) ruleOff(ctx context.Context, in RuleOffIn) (RowOut, error) {
	if !ruleIDPattern.MatchString(in.RuleID) {
		return RowOut{}, errs.New(errs.InvalidInput, "rule id "+quote(in.RuleID)+" is not a rule id")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return RowOut{}, errs.New(errs.InvalidInput, "secrets rule off: --reason is required")
	}
	fields := map[string]value.Value{}
	if err := optTexts(fields, map[string]string{sState: schema.SecretRuleOff, sReason: in.Reason}); err != nil {
		return RowOut{}, err
	}
	return o.putRule(ctx, targetOf(in.To), in.RuleID, fields, "off")
}

func (o ops) ruleOn(ctx context.Context, in RuleOnIn) (RowOut, error) {
	target := targetOf(in.To)
	have, err := o.ruleAt(ctx, target, in.RuleID)
	if err != nil {
		return RowOut{}, err
	}
	if have == nil || text(have.Row, sState) != schema.SecretRuleOff {
		return RowOut{}, errs.New(errs.NotFound, in.RuleID+" is not off in "+place(target)).WithHint("lore secrets rules lists the rules that are off")
	}
	// A rule this level added keeps its row (state on); a default that was
	// only switched off here loses its row, so the default applies again.
	if text(have.Row, sRegex) != "" {
		v, serr := state(schema.SecretRuleOn)
		if serr != nil {
			return RowOut{}, serr
		}
		return o.patchRow(ctx, *have, map[string]value.Value{sState: v, sReason: value.Null{}}, in.RuleID+" on in "+place(target))
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: target, Table: schema.TableSecretRules, ID: have.Row.ID}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: have.Row.ID.String(), Store: string(target), Human: fmt.Sprintf("✓ %s on in %s (%s removed)\n", in.RuleID, place(target), short(have.Row.ID))}, nil
}

func (o ops) allow(ctx context.Context, in AllowIn) (RowOut, error) {
	rowID, err := ids.ParseAny(strings.TrimSpace(in.Row))
	if err != nil {
		return RowOut{}, errs.Wrap(errs.InvalidID, "secrets allow: "+quote(in.Row)+" is not a row id", err)
	}
	if in.Rule == "" || strings.TrimSpace(in.Reason) == "" {
		return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: --rule and --reason are required")
	}
	src, r, err := o.env.Get(ctx, engine.Ref{ID: rowID})
	if err != nil {
		return RowOut{}, err
	}
	if r.Deleted {
		return RowOut{}, errs.New(errs.NotFound, rowID.String()+" is deleted")
	}
	field := in.Field
	if field == "" {
		// Default: the one text field (or, with --match, the one field
		// whose text holds the match).
		var textFields []string
		for name, v := range r.Fields {
			switch v.(type) {
			case value.Text, value.JSON:
				if in.Match == "" || strings.Contains(v.String(), in.Match) {
					textFields = append(textFields, name)
				}
			}
		}
		if len(textFields) != 1 {
			slices.Sort(textFields)
			return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: name the field with --field (text fields: "+strings.Join(textFields, ", ")+")")
		}
		field = textFields[0]
	}
	hash := in.MatchHash
	switch {
	case in.Match != "" && hash != 0:
		return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: give --match or --match-hash, not both")
	case in.Match != "":
		if v, ok := r.Fields[field]; !ok || !strings.Contains(v.String(), in.Match) {
			return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: the "+field+" of "+rowID.String()+" does not contain that text")
		}
		hash = scan.HashMatch(in.Match)
	case hash == 0:
		return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: give --match <text> or --match-hash <n>")
	}
	fields := map[string]value.Value{aHash: value.Int(int64(hash))}
	if err = optTexts(fields, map[string]string{aField: field, aRuleID: in.Rule, aReason: in.Reason}); err != nil {
		return RowOut{}, err
	}
	if fields[aRowID], err = ref(rowID); err != nil {
		return RowOut{}, err
	}
	w, err := o.env.Create(ctx, src, schema.TableSecretAllows, fields)
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(src),
		Human: fmt.Sprintf("✓ %s: %s.%s marked not a secret (%s)\n", in.Rule, short(rowID), field, short(w.Row.ID))}, nil
}

func (o ops) allowRm(ctx context.Context, in AllowRmIn) (RowOut, error) {
	id, err := ids.Parse[ids.SecretAllow](strings.TrimSpace(in.ID))
	if err != nil {
		return RowOut{}, errs.Wrap(errs.InvalidID, "secrets allow rm: "+quote(in.ID)+" is not an allow id (sal_…)", err)
	}
	src, _, err := o.env.Get(ctx, engine.Ref{Table: schema.TableSecretAllows, ID: id.Any()})
	if err != nil {
		return RowOut{}, err
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: src, Table: schema.TableSecretAllows, ID: id.Any()}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: id.String(), Store: string(src), Human: fmt.Sprintf("✓ removed %s; the finding is reported again on the next scan\n", short(id.Any()))}, nil
}

func (o ops) allowList(ctx context.Context, _ AllowListIn) (AllowsOut, error) {
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableSecretAllows)
	if err != nil {
		return AllowsOut{}, err
	}
	out := AllowsOut{Data: []Allow{}}
	for _, l := range rows {
		out.Data = append(out.Data, Allow{ID: l.Row.ID.String(), RowID: text(l.Row, aRowID), Field: text(l.Row, aField),
			RuleID: text(l.Row, aRuleID), MatchHash: text(l.Row, aHash), Reason: text(l.Row, aReason), Store: string(l.Src)})
	}
	out.Count = len(out.Data)
	return out, nil
}
