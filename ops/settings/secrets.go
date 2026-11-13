package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
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
type RulesIn struct {
	Replaced bool `json:"replaced,omitempty" flag:"replaced" desc:"also list which gitleaks rule ids cover the same ground as each lore rule (3.3)"`
}

// Coverage is one lore pack rule and the gitleaks ids covering the same
// token shapes (`--replaced`); Gitleaks empty = lore only.
type Coverage struct {
	RuleID   string   `json:"rule_id"`
	Gitleaks []string `json:"gitleaks"`
}

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
	Row string `json:"row_id,omitempty" arg:"0" desc:"the row id the finding is on (or --file for a finding in a repo file)"`
	// File is a repo file the finding is in (a schema field named
	// password, a test fixture): the entry is keyed by its repo-relative
	// path, rule and hash, and the commit check honours it for that file.
	File   string `json:"file,omitempty" flag:"file" desc:"a repo file the finding is in, instead of a row id"`
	Rule   string `json:"rule" flag:"rule" desc:"the rule id of the finding"`
	Reason string `json:"reason" flag:"reason" desc:"why it is not a secret"`
	Field  string `json:"field,omitempty" flag:"field" desc:"the field (default the one text field holding --match)"`
	Match  string `json:"match,omitempty" flag:"match" desc:"the matched text (only its hash is stored)"`
	// MatchHash is text, not a number (bug B1-15): a commit refusal prints
	// the finding as its sha256 fingerprint ("sha256:3fa9…"), `lore check
	// leaks --rows` and `lore lint` print the number; both are taken.
	MatchHash MatchHashArg `json:"match_hash,omitempty" flag:"match-hash" desc:"the finding's hash, instead of --match: the sha256:… fingerprint a refused commit prints, or the number lore check leaks --rows prints"`
}

// MatchHashArg is --match-hash as given: text on the command line, and in
// JSON either a string or the number older API clients send.
type MatchHashArg string

// UnmarshalJSON implements json.Unmarshaler: a JSON string or number.
func (m *MatchHashArg) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*m = MatchHashArg(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return errs.Wrap(errs.InvalidInput, "match_hash: want a sha256:… fingerprint or a number", err)
	}
	*m = MatchHashArg(n.String())
	return nil
}

// UnmarshalText implements encoding.TextUnmarshaler (the CLI flag).
func (m *MatchHashArg) UnmarshalText(b []byte) error { *m = MatchHashArg(b); return nil }

// MarshalText implements encoding.TextMarshaler.
func (m MatchHashArg) MarshalText() ([]byte, error) { return []byte(m), nil }

// allowFileHint is the next step of `secrets allow <not a row id>`: a
// finding in an ordinary repo file is allowed by its path.
const allowFileHint = "for a finding in a repo file: lore secrets allow --file <path> --rule <rule> --match-hash <sha256:… fingerprint> --reason \"<why>\""

// fileAllowField is the field a file entry stores. An entry also names the
// project row as row_id: releases before --file read every entry as a row
// entry (row_id required, a malformed entry fails every write), and the
// project row has no field of this name, so to them it allows nothing.
const fileAllowField = "file"

// aPath is the secret_allows field of a file entry.
const aPath = "path"

// FileAllower is an optional Env upgrade (wire.App) for `secrets allow
// --file`: the repo-relative slash path of a file named on the command
// line, and the match hash of the finding of rule in that file (as staged,
// else as on disk) whose fingerprint starts with fingerprint.
type FileAllower interface {
	RepoFilePath(ctx context.Context, p string) (string, error)
	FileFindingHash(ctx context.Context, path, rule, fingerprint string) (uint32, error)
}

// allowFile is `secrets allow --file`: one entry for a finding in an
// ordinary repo file, keyed by path, rule and hash, in the repo store.
func (o ops) allowFile(ctx context.Context, in AllowIn) (RowOut, error) {
	if in.Rule == "" || strings.TrimSpace(in.Reason) == "" {
		return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: --rule and --reason are required")
	}
	if in.Match != "" && strings.TrimSpace(string(in.MatchHash)) != "" {
		return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: give --match or --match-hash, not both")
	}
	hash, fingerprint, err := parseMatchHash(string(in.MatchHash))
	if err != nil {
		return RowOut{}, err
	}
	fa, ok := o.env.(FileAllower)
	if !ok {
		return RowOut{}, errs.New(errs.Unsupported, "secrets allow --file: not available here")
	}
	path, err := fa.RepoFilePath(ctx, in.File)
	if err != nil {
		return RowOut{}, err
	}
	switch {
	case in.Match != "":
		hash = scan.HashMatch(in.Match)
	case fingerprint != "":
		if hash, err = fa.FileFindingHash(ctx, path, in.Rule, fingerprint); err != nil {
			return RowOut{}, err
		}
	case strings.TrimSpace(string(in.MatchHash)) == "":
		return RowOut{}, errs.New(errs.InvalidInput, "secrets allow --file: give --match-hash <sha256:… fingerprint> (the refused commit prints it)")
	}
	projects, err := liveRows(ctx, o.env, []store.Source{store.SourceRepo}, schema.TableProjects)
	if err != nil {
		return RowOut{}, err
	}
	if len(projects) == 0 {
		return RowOut{}, errs.New(errs.NotAStore, "secrets allow --file: this repo has no lore project").WithHint("run lore init here first")
	}
	fields := map[string]value.Value{aHash: value.Int(int64(hash))}
	if err = optTexts(fields, map[string]string{aField: fileAllowField, aPath: path, aRuleID: in.Rule, aReason: in.Reason}); err != nil {
		return RowOut{}, err
	}
	if fields[aRowID], err = ref(projects[0].Row.ID); err != nil {
		return RowOut{}, err
	}
	w, err := o.env.Create(ctx, store.SourceRepo, schema.TableSecretAllows, fields)
	if err != nil {
		return RowOut{}, err
	}
	if err = o.auditSecretEvent(ctx, auditAllowAdded, "secrets allow", projects[0].Row.ID, "allow="+w.Row.ID.String(), "file="+path,
		"rule="+in.Rule, "reason="+strings.TrimSpace(in.Reason)); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(store.SourceRepo),
		Human: fmt.Sprintf("✓ %s: %s marked not a secret (%s); commit the new .lore/data/secret_allows file with it\n", in.Rule, path, short(w.Row.ID))}, nil
}

// FindingHasher is an optional Env upgrade (wire.App): the match hash of
// the finding of rule in a row's field whose fingerprint (scan.Mask) is
// fingerprint, scanned with the rules the commit check uses. Without it a
// fingerprint cannot be resolved and only the number is taken.
type FindingHasher interface {
	FindingMatchHash(ctx context.Context, r row.Row, field, rule, fingerprint string) (uint32, error)
}

// Forms of --match-hash: an fnv number, or a sha256 fingerprint with or
// without its prefix, at least minFingerprintHex hex characters.
const (
	fingerprintPrefix = "sha256:"
	minFingerprintHex = 8
)

// parseMatchHash reads --match-hash: the number (n, "" fingerprint) or a
// fingerprint (0, its lower-case hex). Checked before the row is read, so
// a wrong value is refused at once.
func parseMatchHash(s string) (uint32, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, "", nil
	}
	if n, err := strconv.ParseUint(s, 10, 32); err == nil {
		return uint32(n), "", nil
	}
	hexPart := strings.ToLower(strings.TrimPrefix(strings.ToLower(s), fingerprintPrefix))
	if len(hexPart) >= minFingerprintHex && len(hexPart) <= 64 && strings.Trim(hexPart, "0123456789abcdef") == "" {
		return 0, hexPart, nil
	}
	return 0, "", errs.New(errs.InvalidInput, "secrets allow: --match-hash "+quote(s)+" is neither the sha256:… fingerprint a refusal prints nor a match hash number").
		WithHint("copy the match a refused commit printed (sha256:…), or the --match-hash number `lore check leaks --rows` prints")
}

// AllowRmIn is the input of secrets.allow.rm.
type AllowRmIn struct {
	ID     string `json:"id" arg:"0" desc:"the allow entry (sal_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
}

// AllowListIn is the input of secrets.allow.list.
type AllowListIn struct {
	// --created-by / --updated-by: the shared writer filters of every
	// list (INTEG-AUDIT-1; engine.List applies them to the table that
	// declares this list's noun, schema.Noun).
	metalist.Audit
}

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
	// Replaced is set by --replaced: every lore pack rule, in pack order.
	Replaced []Coverage `json:"replaced,omitempty"`
}

// Text implements op.Texter.
func (o RulesOut) Text() string {
	var b strings.Builder
	if len(o.Replaced) > 0 {
		b.WriteString("LORE RULE\tALSO COVERED BY GITLEAKS\n")
		for _, c := range o.Replaced {
			cov := "(none: lore only)"
			if len(c.Gitleaks) > 0 {
				cov = strings.Join(c.Gitleaks, ", ")
			}
			fmt.Fprintf(&b, "%s\t%s\n", c.RuleID, cov)
		}
		b.WriteString("\n")
	}
	if len(o.Data) == 0 {
		b.WriteString("(no rule rows: gitleaks defaults and lore's own pack apply)\n")
		return b.String()
	}
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
	Path      string `json:"path,omitempty"` // a file entry (secrets allow --file)
	RuleID    string `json:"rule_id"`
	MatchHash string `json:"match_hash"`
	Reason    string `json:"reason,omitempty"`
	Store     string `json:"store"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
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
		row, field := a.RowID, a.Field
		if a.Path != "" {
			row, field = "file "+a.Path, "-"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", a.ID, row, field, a.RuleID, a.Reason)
	}
	return b.String()
}

func (o ops) secretOps() []op.Entry {
	ask := op.ConfirmRule{Always: true}
	return append([]op.Entry{
		op.Define(op.Spec{Name: OpSecretRules, Summary: "List secret rule rows (added, changed or off) with their level", Permission: op.PermRead,
			Access: op.AccessRead, Errors: readErrors, RESTNoun: rulesREST, Examples: []op.Example{{Title: "every secret rule", CLI: "lore secrets rules"}}}, o.rules),
		op.Define(op.Spec{Name: OpSecretRuleAdd, Summary: "Add a lore secret rule on top of the defaults", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: ask,
			Examples: []op.Example{{Title: "an internal token shape", CLI: `lore secrets rule add --id acme-token --regex 'acme_[a-z0-9]{32}' --yes`, Want: "srl_"}}}, o.ruleAdd).Asks(o.ruleAddQuestion),
		op.Define(op.Spec{Name: OpSecretRuleEdit, Summary: "Change a secret rule (lore defaults and gitleaks ids included)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: writeErrors, Confirm: ask, Examples: []op.Example{{Title: "tighten a rule's pattern", CLI: "lore secrets rule edit --id srl_0d4e --entropy 4.5"}}}, o.ruleEdit).Asks(o.ruleEditQuestion),
		op.Define(op.Spec{Name: OpSecretRuleOff, Summary: "Switch a secret rule off at a level, with a reason", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: ask, Examples: []op.Example{{Title: "switch a rule off here", CLI: `lore secrets rule off --id srl_0d4e --reason "test fixtures hold sample tokens" --yes`}}}, o.ruleOff).Asks(o.ruleOffQuestion),
		op.Define(op.Spec{Name: OpSecretRuleOn, Summary: "Switch a secret rule back on at a level", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: ask, Examples: []op.Example{{Title: "switch a rule on again", CLI: "lore secrets rule on --id srl_0d4e"}}}, o.ruleOn).Asks(o.ruleOnQuestion),
		op.Define(op.Spec{Name: OpSecretAllow, Summary: "Record one finding as not a secret for one row, field and rule", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: writeErrors, Examples: []op.Example{{Title: "keep a test fixture key as not a secret", CLI: `lore secrets allow mem_0c3a --rule srl_0d4e --match fixture-token-123 --reason "a test fixture"`}}}, o.allow).Checks(allowCheck),
		op.Define(op.Spec{Name: OpSecretAllowRm, Summary: "Remove an allow entry (the finding is reported again)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: writeErrors, Confirm: ask, Examples: []op.Example{{Title: "stop allowing a finding", CLI: "lore secrets allow rm --id sal_2e19"}}}, o.allowRm).Asks(o.allowRmQuestion),
		op.Define(op.Spec{Name: OpSecretAllowList, Summary: "List the allow entries", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors, Examples: []op.Example{{Title: "every allowed (false positive) finding", CLI: "lore secrets allow list"}}}, o.allowList),
	}, o.secretIndexOps()...)
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

// allowFind is the live allow entry raw names: E_INVALID_ID for a
// malformed id, E_NOT_FOUND (or where a trashed one is) for a missing one.
// allow rm's question calls it too, so a bad id is reported, not asked
// about.
func (o ops) allowFind(ctx context.Context, raw string) (ids.ID[ids.SecretAllow], store.Source, row.Row, error) {
	id, err := ids.Parse[ids.SecretAllow](strings.TrimSpace(raw))
	if err != nil {
		return id, "", row.Row{}, lookup.BadID("secrets allow rm: "+quote(raw)+" is not an allow id (sal_…)", err).
			WithHint("lore secrets allow list prints each sal_ id")
	}
	src, have, err := o.env.Get(ctx, engine.Ref{Table: schema.TableSecretAllows, ID: id.Any()})
	if err != nil {
		if errs.Has(err, errs.NotFound) {
			// DELETION 3.2: a trashed allow entry says where it is.
			if terr := inTrashByKey(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableSecretAllows, func(l located) bool { return l.Row.ID == id.Any() }); terr != nil {
				return id, "", row.Row{}, terr
			}
		}
		return id, "", row.Row{}, err
	}
	return id, src, have, nil
}

// rules lists the secret_rules rows of every attached store.
//
//	rows (secret_rules, all stores) → Rule per row (level from the store)
//	--replaced → every lore pack rule + its gitleaks twins (3.3)
func (o ops) rules(ctx context.Context, in RulesIn) (RulesOut, error) {
	rows, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableSecretRules)
	if err != nil {
		return RulesOut{}, err
	}
	out := RulesOut{Data: []Rule{}}
	for _, l := range rows {
		out.Data = append(out.Data, ruleOf(o.env, l))
	}
	out.Count = len(out.Data)
	if in.Replaced {
		covers := gitleaks.Covers()
		for _, r := range gitleaks.Pack() {
			out.Replaced = append(out.Replaced, Coverage{RuleID: r.ID, Gitleaks: append([]string{}, covers[r.ID]...)})
		}
	}
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
			return nil, errs.Wrap(errs.InvalidInput, "--regex is not an RE2 pattern ("+err.Error()+")", err).
				WithHint("fix the pattern (RE2 syntax: no lookaround or backreferences); nothing written")
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
	// FA-AGENT-4: secret.rule_changed beside the row's own audit entry.
	if err = o.auditSecretEvent(ctx, auditRuleChanged, "secrets rule "+verb, w.Row.ID, "rule="+id, "row="+w.Row.ID.String(),
		"place="+place(target), "state="+text(w.Row, sState), "changed="+changedNames(fields)); err != nil {
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
		return RowOut{}, errs.New(errs.InvalidInput, "rule "+in.RuleID+" already has a row in "+place(target)).WithHint("lore secrets rule edit --id " + in.RuleID)
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
		out, perr := o.patchRow(ctx, *have, map[string]value.Value{sState: v, sReason: value.Null{}}, in.RuleID+" on in "+place(target))
		if perr != nil {
			return out, perr
		}
		return out, o.auditSecretEvent(ctx, auditRuleChanged, "secrets rule on", have.Row.ID, "rule="+in.RuleID, "row="+have.Row.ID.String(),
			"place="+place(target), "state="+schema.SecretRuleOn, "changed=state")
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: target, Table: schema.TableSecretRules, ID: have.Row.ID}); err != nil {
		return RowOut{}, err
	}
	if err := o.auditSecretEvent(ctx, auditRuleChanged, "secrets rule on", have.Row.ID, "rule="+in.RuleID, "row="+have.Row.ID.String(),
		"place="+place(target), "state=default", "changed=removed"); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: have.Row.ID.String(), Store: string(target), Human: fmt.Sprintf("✓ %s on in %s (%s removed)\n", in.RuleID, place(target), short(have.Row.ID))}, nil
}

// allowCheck refuses a secrets allow input that is wrong on its face (a
// row id and --file together, a row id that cannot be one, no rule or
// reason, --match with --match-hash, a --match-hash that is neither form),
// with nothing opened: the Runner calls it before the app opens
// (op.Prechecker, bug L-1: these answered only after ~4 s), and allow runs
// it again for every other surface. The --file form takes no row id.
// Checks that need the row or the file stay in allow / allowFile.
func allowCheck(in AllowIn) error {
	if strings.TrimSpace(in.File) != "" {
		if strings.TrimSpace(in.Row) != "" {
			return errs.New(errs.InvalidInput, "secrets allow: give a row id or --file, not both")
		}
	} else if _, err := ids.ParseAny(strings.TrimSpace(in.Row)); err != nil {
		return lookup.BadID("secrets allow: "+quote(in.Row)+" is not a row id; "+allowFileHint, err)
	}
	if in.Rule == "" || strings.TrimSpace(in.Reason) == "" {
		return errs.New(errs.InvalidInput, "secrets allow: --rule and --reason are required")
	}
	if in.Match != "" && strings.TrimSpace(string(in.MatchHash)) != "" {
		return errs.New(errs.InvalidInput, "secrets allow: give --match or --match-hash, not both")
	}
	_, _, err := parseMatchHash(string(in.MatchHash))
	return err
}

func (o ops) allow(ctx context.Context, in AllowIn) (RowOut, error) {
	if err := allowCheck(in); err != nil {
		return RowOut{}, err
	}
	if strings.TrimSpace(in.File) != "" {
		return o.allowFile(ctx, in)
	}
	rowID, err := ids.ParseAny(strings.TrimSpace(in.Row))
	if err != nil {
		return RowOut{}, lookup.BadID("secrets allow: "+quote(in.Row)+" is not a row id; "+allowFileHint, err)
	}
	hash, fingerprint, err := parseMatchHash(string(in.MatchHash))
	if err != nil {
		return RowOut{}, err
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
	if fingerprint != "" {
		fh, ok := o.env.(FindingHasher)
		if !ok {
			return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: give --match-hash as the number `lore check leaks --rows` prints")
		}
		if hash, err = fh.FindingMatchHash(ctx, r, field, in.Rule, fingerprint); err != nil {
			return RowOut{}, err
		}
	}
	switch {
	case in.Match != "":
		if v, ok := r.Fields[field]; !ok || !strings.Contains(v.String(), in.Match) {
			return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: the "+field+" of "+rowID.String()+" does not contain that text")
		}
		hash = scan.HashMatch(in.Match)
	case hash == 0:
		return RowOut{}, errs.New(errs.InvalidInput, "secrets allow: give --match <text> or --match-hash <sha256:… or number>")
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
	// FA-AGENT-4: names and ids only; the matched text never reaches the log.
	if err = o.auditSecretEvent(ctx, auditAllowAdded, "secrets allow", rowID, "allow="+w.Row.ID.String(), "field="+field,
		"rule="+in.Rule, "reason="+strings.TrimSpace(in.Reason)); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(src),
		Human: fmt.Sprintf("✓ %s: %s.%s marked not a secret (%s)\n", in.Rule, short(rowID), field, short(w.Row.ID))}, nil
}

func (o ops) allowRm(ctx context.Context, in AllowRmIn) (RowOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // INTEG-TRASH-3; E_REASON_REQUIRED under delete.require_reason
	id, src, have, err := o.allowFind(ctx, in.ID)
	if err != nil {
		return RowOut{}, err
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: src, Table: schema.TableSecretAllows, ID: id.Any()}); err != nil {
		return RowOut{}, err
	}
	var target ids.AnyID
	if t, perr := ids.ParseAny(text(have, aRowID)); perr == nil {
		target = t
	}
	if err := o.auditSecretEvent(ctx, auditAllowRemoved, "secrets allow rm", target, "allow="+id.String(), "field="+text(have, aField),
		"rule="+text(have, aRuleID)); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: id.String(), Store: string(src), Human: fmt.Sprintf("✓ removed %s; the finding is reported again on the next scan\n", short(id.Any()))}, nil
}

func (o ops) allowList(ctx context.Context, _ AllowListIn) (AllowsOut, error) {
	rows, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableSecretAllows)
	if err != nil {
		return AllowsOut{}, err
	}
	out := AllowsOut{Data: []Allow{}}
	for _, l := range rows {
		out.Data = append(out.Data, Allow{ID: l.Row.ID.String(), RowID: text(l.Row, aRowID), Field: text(l.Row, aField),
			Path: text(l.Row, aPath), RuleID: text(l.Row, aRuleID), MatchHash: text(l.Row, aHash), Reason: text(l.Row, aReason), Store: string(l.Src),
			CreatedBy: auditrec.Of(l.Row).CreatedBy, UpdatedBy: auditrec.Of(l.Row).UpdatedBy})
	}
	out.Count = len(out.Data)
	return out, nil
}

// changedNames lists the rule fields a write set, sorted (secret.rule_changed
// "what changed").
func changedNames(fields map[string]value.Value) string {
	names := make([]string, 0, len(fields))
	for n := range fields {
		if n != sRuleID {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ",")
}
