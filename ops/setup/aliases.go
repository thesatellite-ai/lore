package setup

// aliases.go — setup.aliases (ONB 4.3 GitHub step): every ~/.ssh/config
// Host alias of github.com (plus github.com itself) with its key file, the
// key fingerprint and the GitHub user its ssh -T greets, and gh's logins.
// With keys, it also tests every private key file alone (-i KEY,
// IdentitiesOnly=yes, IdentityAgent=none): "which account does this key
// log in as". Read-only; the probes run in parallel through the shared
// limiter, each with a 3 s limit (wire aliasProbeTimeout), and the answer is cached for
// opsdoctor.CacheTTL by the Prober (fresh skips the cache). setup.plan
// stays network-free; the page calls this op when the GitHub step opens.
//
//	page / CLI ─▶ setup.aliases ─▶ Prober (GitHubLister) ─▶ ssh config, key fingerprints, ssh -T per alias (‖), gh hosts.yml
//
// The engine also uses the table to decide the github step's route when
// the person picked only the account (deriveRoute: an ssh route that logs
// in as the account, else https with its gh login, else a fix message),
// and to refuse a forced alias that logs in as someone else (validateStep).
// With In.User, the answer carries that decision (Route) for the page's
// read-only "lore will connect as …" line.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// OpAliases is the op name (on the page allowlist, ONB 5.3).
const OpAliases = "setup.aliases"

// AliasesIn is setup.aliases' input.
type AliasesIn struct {
	Fresh bool `json:"fresh,omitempty" flag:"fresh" desc:"probe again instead of the answer kept for a minute"`
	Keys  bool `json:"keys,omitempty" flag:"keys" desc:"also test every private key alone: which GitHub account each logs in as"`
	// User asks for the route lore would use for this GitHub account.
	User string `json:"user,omitempty" flag:"user" desc:"also say how lore would connect as this GitHub account (ssh route or https via gh)"`
}

// AliasesOut is setup.aliases' output.
type AliasesOut struct {
	Aliases []GitHubAlias `json:"aliases"`
	// GH is gh's logins for github.com (hosts.yml; empty without gh).
	GH []GHLogin `json:"gh"`
	// Keys is set only when In.Keys: one row per private key file.
	Keys []GitHubKey `json:"keys,omitempty"`
	// Route is how lore would reach GitHub as In.User (zero, Line "",
	// without User).
	Route onboard.RouteChoice `json:"route"`
}

// GitHubAlias is one Host alias of github.com.
type GitHubAlias struct {
	Alias string `json:"alias"`
	// IdentityFile is the alias's first IdentityFile as written in the
	// config ("" when it sets none: ssh then tries its default keys).
	IdentityFile string `json:"identity_file,omitempty"`
	// KeyMissing: IdentityFile is set but no file is there (~ expanded
	// against this home). KeyNearby is the same file name in ~/.ssh when
	// that exists (the path to write in the config instead).
	KeyMissing bool   `json:"key_missing,omitempty"`
	KeyNearby  string `json:"key_nearby,omitempty"`
	// PubKey is IdentityFile + .pub when that file exists; NoPub is true
	// when it does not. Fingerprint is the key's either way (ssh-keygen
	// -lf on the .pub, else on the key file; a .pub is never required).
	PubKey      string `json:"pub_key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	NoPub       bool   `json:"no_pub,omitempty"`
	// IdentitiesOnly is the alias's "IdentitiesOnly yes".
	IdentitiesOnly bool `json:"identities_only"`
	// LogsInAs is the user ssh -T greeted; Status is the probe class
	// (ok, auth_denied, timeout, unreachable, host_key, tool_missing,
	// failed) and Detail one sanitised line, never raw output.
	LogsInAs string `json:"logs_in_as,omitempty"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

// GitHubKey is one private key file tested alone.
type GitHubKey struct {
	Path        string `json:"path"`
	PubKey      string `json:"pub_key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	LogsInAs    string `json:"logs_in_as,omitempty"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
}

// GHLogin is one gh login for github.com.
type GHLogin struct {
	User   string `json:"user"`
	Active bool   `json:"active"`
}

// GitHubLister is the optional Prober upgrade setup.aliases needs (wire's
// Prober has it; a Prober without it makes the op E_UNSUPPORTED and the
// github messages fall back to the plain rule text).
type GitHubLister interface {
	GitHubAliases(ctx context.Context, in AliasesIn) (AliasesOut, error)
}

// Aliases is setup.aliases.
func (e engine) Aliases(ctx context.Context, in AliasesIn) (AliasesOut, error) {
	l, err := e.lister(ctx)
	if err != nil {
		return AliasesOut{}, err
	}
	if l == nil {
		return AliasesOut{}, errs.New(errs.Unsupported, "this lore cannot list ssh aliases").
			WithHint("lore setup check --step github runs the same ssh checks")
	}
	out, err := l.GitHubAliases(ctx, in)
	if err != nil || strings.TrimSpace(in.User) == "" {
		return out, err
	}
	out.Route = onboard.ChooseRoute(in.User, routeAliases(out), ghUsers(out))
	return out, nil
}

// routeAliases is the table as onboard.RouteAlias rows.
func routeAliases(t AliasesOut) []onboard.RouteAlias {
	out := make([]onboard.RouteAlias, 0, len(t.Aliases))
	for _, a := range t.Aliases {
		out = append(out, onboard.RouteAlias{Alias: a.Alias, KeyFile: a.IdentityFile, KeyMissing: a.KeyMissing,
			LogsInAs: a.LogsInAs, Pending: a.Status == StatusChecking})
	}
	return out
}

// ghUsers is gh's logins of the table.
func ghUsers(t AliasesOut) []string {
	out := make([]string, 0, len(t.GH))
	for _, g := range t.GH {
		out = append(out, g.User)
	}
	return out
}

// lister is the Prober's GitHubLister, nil when it has none.
func (e engine) lister(ctx context.Context) (GitHubLister, error) {
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return nil, err
	}
	l, _ := pr.(GitHubLister)
	return l, nil
}

// StatusChecking is a GitHubAlias.Status for an alias whose ssh -T has
// not answered yet (KnownAliasLister): unknown, never a verdict.
const StatusChecking = "checking"

// AliasQuery asks a KnownAliasLister for the table as known now.
type AliasQuery struct {
	// Wait bounds the wait for ssh answers (0: answers already in; the
	// probes still start, so a later call knows more).
	Wait time.Duration
	// User ends the wait early once an alias logs in as User ("" = none).
	User string
}

// KnownAliasLister is the optional upgrade the github step's validation
// prefers over GitHubLister: answers known so far plus the aliases still
// pending, so a cold setup.check does not wait for every alias (a hung
// one held it 3 s; 2026-11-06). pending rows have Status StatusChecking.
type KnownAliasLister interface {
	GitHubAliasesKnown(ctx context.Context, q AliasQuery) (AliasesOut, []string, error)
}

// githubTable is the alias table for the github step's messages, as known
// after at most q.Wait (a full table from a GitHubLister without the
// known upgrade); pending are the aliases with no answer yet. ok is false
// when it cannot be read (no lister, or the probe failed).
func (e engine) githubTable(ctx context.Context, q AliasQuery) (t AliasesOut, pending []string, ok bool) {
	l, err := e.lister(ctx)
	if err != nil || l == nil {
		return AliasesOut{}, nil, false
	}
	if k, isKnown := l.(KnownAliasLister); isKnown {
		t, pending, err = k.GitHubAliasesKnown(ctx, q)
		return t, pending, err == nil
	}
	t, err = l.GitHubAliases(ctx, AliasesIn{})
	return t, nil, err == nil
}

// loginsOf is the table as onboard.AliasLogin rows.
func loginsOf(t AliasesOut) []onboard.AliasLogin {
	out := make([]onboard.AliasLogin, 0, len(t.Aliases))
	for _, a := range t.Aliases {
		out = append(out, onboard.AliasLogin{Alias: a.Alias, LogsInAs: a.LogsInAs})
	}
	return out
}

// validateStep is Catalog.Validate, plus, on the github step, a forced
// alias (answered, not derived) that logs in as another user than the
// account refused here rather than by a later network check.
func (e engine) validateStep(ctx context.Context, step onboard.Step, kind onboard.Kind, answers onboard.Answers) []onboard.FieldError {
	fe := e.cat.Validate(step, kind, answers)
	if step.ID == onboard.StepPersonalStore {
		return withoutUnusedStoreURL(fe, answers)
	}
	if step.ID == onboard.StepTools {
		return append(fe, e.toolChoiceErrors(ctx, answers)...)
	}
	if step.ID != onboard.StepGitHub {
		return fe
	}
	user := strings.TrimSpace(answers[onboard.QIDGitHubAccount].Text)
	alias := strings.TrimSpace(answers[onboard.QIDHostAlias].Text)
	if alias == "" || user == "" || len(fe) > 0 || onboard.GitRoute(answers[onboard.QIDRoute].Text) == onboard.RouteHTTPS {
		return fe
	}
	// Judged on answers already in (no wait): a pending alias is no error,
	// and the step's ssh_user / key_matches checks, which run next and
	// share the same probes, judge it.
	t, _, _ := e.githubTable(ctx, AliasQuery{})
	logins := loginsOf(t)
	for _, a := range t.Aliases {
		if a.Alias == alias && a.LogsInAs != "" && !strings.EqualFold(a.LogsInAs, user) {
			return append(fe, onboard.FieldError{Question: onboard.QIDHostAlias, Rule: ruleAliasUser,
				Message: onboard.AliasMismatchMessage(user, alias, a.LogsInAs, logins)})
		}
	}
	return fe
}

// withoutUnusedStoreURL drops the store_url errors of a personal_store
// answer whose choice does not use the url: only create and existing
// write it, while the page fills the field for every choice (inherit
// shows personal.store, which may be the v0.1.17 value "local"). Save
// refused that value the page filled itself (B1-27, setupe2e pagee2e).
func withoutUnusedStoreURL(fe []onboard.FieldError, answers onboard.Answers) []onboard.FieldError {
	switch strings.TrimSpace(answers[onboard.QIDPersonalStore].Text) {
	case onboard.PersonalStoreCreate, onboard.PersonalStoreExisting:
		return fe
	}
	out := fe[:0:0]
	for _, f := range fe {
		if f.Question != onboard.QIDStoreURL {
			out = append(out, f)
		}
	}
	return out
}

// ruleAliasUser is the FieldError.Rule of a picked alias that logs in as
// another user than the chosen account.
const ruleAliasUser = "alias_user"

// routeWait bounds how long deriveRoute waits for the aliases' ssh -T
// answers: one probe's full limit plus a slot wait, so a hung alias has
// timed out (a verdict) rather than being "still checking", while a found
// route ends the wait at once.
const routeWait = 4 * time.Second

// ruleRoute is the FieldError.Rule when no route reaches GitHub as the
// account (the message is the fix).
const ruleRoute = "route"

// deriveRoute fills the github step's route when the person picked only
// the account (owner 2026-11-07: the step asks for nothing else). An
// answered route or alias is kept (forced); with an alias and no key, the
// key is the alias's .pub when one exists (a .pub is never required: the
// fingerprint is recorded at apply). Otherwise onboard.ChooseRoute decides
// from the alias table and gh's logins: host_alias + route ssh, or route
// https; with neither, a field error on the account carries the fix.
// Other steps pass unchanged; the answers map is copied, never changed in
// place. A Prober that cannot list aliases leaves the answers as they are
// (ssh through github.com, judged by the step's checks).
func (e engine) deriveRoute(ctx context.Context, step onboard.Step, answers onboard.Answers) (onboard.Answers, []onboard.FieldError) {
	if step.ID != onboard.StepGitHub || answers == nil {
		return answers, nil
	}
	user := strings.TrimSpace(answers[onboard.QIDGitHubAccount].Text)
	alias := strings.TrimSpace(answers[onboard.QIDHostAlias].Text)
	route := onboard.GitRoute(strings.TrimSpace(answers[onboard.QIDRoute].Text))
	key := strings.TrimSpace(answers[onboard.QIDSSHKey].Text)
	if user == "" || route == onboard.RouteHTTPS {
		return answers, nil
	}
	out := make(onboard.Answers, len(answers)+3)
	for k, v := range answers {
		out[k] = v
	}
	if alias != "" {
		out[onboard.QIDRoute] = onboard.Answer{Text: string(onboard.RouteSSH)}
		if key == "" {
			if t, _, ok := e.githubTable(ctx, AliasQuery{}); ok { // file facts only: no wait for ssh
				if pub := aliasPub(t, alias); pub != "" {
					out[onboard.QIDSSHKey] = onboard.Answer{Text: pub}
				}
			}
		}
		return out, nil
	}
	if key != "" {
		// A key alone (ssh -i, no alias): the route is ssh through it.
		out[onboard.QIDRoute] = onboard.Answer{Text: string(onboard.RouteSSH)}
		return out, nil
	}
	t, pending, ok := e.githubTable(ctx, AliasQuery{Wait: routeWait, User: user})
	if !ok {
		return answers, nil
	}
	rows := routeAliases(t)
	for i := range rows {
		rows[i].Pending = rows[i].Pending || slices.Contains(pending, rows[i].Alias)
	}
	rc := onboard.ChooseRoute(user, rows, ghUsers(t))
	switch rc.Route {
	case onboard.RouteSSH:
		out[onboard.QIDHostAlias] = onboard.Answer{Text: rc.Alias}
		out[onboard.QIDRoute] = onboard.Answer{Text: string(onboard.RouteSSH)}
		if pub := aliasPub(t, rc.Alias); pub != "" {
			out[onboard.QIDSSHKey] = onboard.Answer{Text: pub}
		}
		return out, nil
	case onboard.RouteHTTPS:
		out[onboard.QIDRoute] = onboard.Answer{Text: string(onboard.RouteHTTPS)}
		return out, nil
	}
	return answers, []onboard.FieldError{{Question: onboard.QIDGitHubAccount, Rule: ruleRoute, Message: rc.Fix}}
}

// aliasPub is the table's .pub of alias ("" when it has none).
func aliasPub(t AliasesOut, alias string) string {
	for _, a := range t.Aliases {
		if a.Alias == alias {
			return a.PubKey
		}
	}
	return ""
}

// toolChoiceErrors refuses a tools answer outside the tools the step
// offers (the Prober's detected-tools choices): the catalog cannot check
// a prefill-backed list, and before (devco:J-1) an unknown tool such as
// "claude" reached the config writer and came back as a broken
// ~/.lore/config.yaml (E_CONFIG_INVALID) that lore config check called
// valid. A Prober that cannot be had or answers no choices checks
// nothing here; the config validation still refuses the write.
func (e engine) toolChoiceErrors(ctx context.Context, answers onboard.Answers) []onboard.FieldError {
	ans, ok := answers[onboard.QIDTools]
	if !ok || len(ans.List) == 0 {
		return nil
	}
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return nil
	}
	pre, err := pr.Prefill(ctx, onboard.PrefillToolsDetected, onboard.SetupEntry{})
	if err != nil || len(pre.Choices) == 0 {
		return nil
	}
	allowed := make([]string, 0, len(pre.Choices))
	for _, c := range pre.Choices {
		allowed = append(allowed, c.Value)
	}
	for _, v := range ans.List {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(allowed, v) {
			return []onboard.FieldError{{Question: onboard.QIDTools, Rule: onboard.FieldRuleChoice,
				Message: strconv.Quote(v) + " is not one of " + strings.Join(allowed, ", ")}}
		}
	}
	return nil
}
