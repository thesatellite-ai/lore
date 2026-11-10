//go:build setupe2e

package setupe2e

// flow.go — "Add setup → every step → Summary → Save" through the page's
// HTTP API, taking each step's auto-filled answers exactly as the page
// does (the questions' values, then the step's values, then the saved
// progress answers), and
// the judge of the three promises:
//
//  1. no refusal names a value lore filled in itself;
//  2. Save succeeds, or refuses only for a value the person must supply
//     (a required question with no value), and says which;
//  3. every gh/ssh call made for the new setup acts as that setup's account.
//
// plus config.yaml holding the new setup with that account. invalid_yaml
// expects typed config problems from setup.list instead of a Save.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// Names the page uses (AddSetupDialog.tsx DEFAULT_SOLO_NAME, then a
// second name when it is taken).
const (
	firstSoloName  = "personal"
	secondSoloName = "work"
	kindSolo       = "solo"
	summaryStep    = "summary"
	// supplyText is what the walk types into a required text question
	// that has no value (the page's person would type something).
	supplyText = "e2e"
)

// Outcome is what one walk saw.
type Outcome struct {
	Shape Shape
	Setup string
	// Steps lists "<step>: <status>" in order.
	Steps []string
	// MustSupply lists "<step>.<question>" the walk had to type in.
	MustSupply []string
	// skipGH / skipSSH are the log lines written before the walk began.
	skipGH, skipSSH int
	// Judged counts the gh/ssh log lines whose account was checked.
	Judged int
	// Fixed lists "<step>.<check>" whose offered fix the walk ran.
	Fixed []string
	// NeedsPerson lists refusals of values only the person can supply
	// ("<step>.<question>: <message>"): allowed, the walk stops there.
	NeedsPerson []string
	// Push is the push mode the walk picked on the Stores step (shape push
	// batch or manual on a git store); "" when it left the question empty.
	Push string
	// Failures are broken promises; empty = the shape passed.
	Failures []string
	// Trace is a per-call log for a failure report.
	Trace []string
}

func (o *Outcome) fail(msg string)  { o.Failures = append(o.Failures, msg) }
func (o *Outcome) trace(msg string) { o.Trace = append(o.Trace, msg) }

// Walk runs the flow against a started world and judges it.
func Walk(ctx context.Context, w *World) *Outcome {
	o := &Outcome{Shape: w.Shape}
	// Calls lore made while starting (before the page could ask anything,
	// e.g. fetching the v0.1.17 store of a v0117 home from a lore repo)
	// are not made for the new setup: judgeStartFetch (startfetch.go)
	// judges them, and the log judge of the walk starts after them.
	startGH, _ := ReadLog(w.Info().Logs.GH)   // a read error = judge every line
	startSSH, _ := ReadLog(w.Info().Logs.SSH) // ditto
	o.skipGH, o.skipSSH = len(startGH), len(startSSH)
	judgeStartFetch(o, w, startSSH, w.Output())
	c, err := NewClient(ctx, w)
	if err != nil {
		o.fail(fmt.Sprintf("session: %v", err))
		return o
	}
	list, err := Call[struct{}, opssetup.ListOut](ctx, c, opssetup.OpList, struct{}{})
	if err != nil {
		o.fail(fmt.Sprintf("setup.list: %v", err))
		return o
	}
	if w.Shape.LoreHome == HomeInvalidYAML {
		judgeConfigProblems(o, list)
		return o
	}
	if len(list.ConfigProblems) > 0 {
		o.fail(fmt.Sprintf("setup.list reports config problems on a valid config.yaml: %+v", list.ConfigProblems))
		return o
	}
	name := firstSoloName
	if slices.ContainsFunc(list.Setups, func(s opssetup.SetupSummary) bool { return s.Name == name }) {
		name = secondSoloName
	}
	o.Setup = name
	if !walkSteps(ctx, c, o, name) {
		return o
	}
	judgeConfig(o, w, name)
	judgeLogs(o, w, name)
	// After the log judge: the CLI check of judgePush is the person at a
	// terminal after the setup, not a call made for the setup.
	judgePush(ctx, o, w, name)
	return o
}

// walkSteps saves every step in plan order; false when it had to stop.
func walkSteps(ctx context.Context, c *Client, o *Outcome, name string) bool {
	plan, err := Call[opssetup.PlanIn, opssetup.PlanOut](ctx, c, opssetup.OpPlan, opssetup.PlanIn{Setup: name, Kind: kindSolo})
	if err != nil {
		o.fail(fmt.Sprintf("setup.plan: %v", err))
		return false
	}
	pick := &pagePick{}
	for i := 0; i < len(plan.Steps); i++ {
		step := plan.Steps[i]
		answers := baseline(step, progressOf(plan, step.ID))
		pick.apply(ctx, c, o, step, progressOf(plan, step.ID) != nil, answers)
		pagePrefill(ctx, c, o, step, answers)
		pickPush(o, c.world, step, answers)
		auto := filled(answers)
		for _, q := range step.Questions {
			if !q.Required || filledAnswer(answers[q.ID]) {
				continue
			}
			o.MustSupply = append(o.MustSupply, string(step.ID)+"."+q.ID)
			answers[q.ID] = supplied(q, name)
		}
		in := opssetup.ApplyIn{Setup: name, Step: string(step.ID), Answers: answers}
		if step.ID == summaryStep {
			in.Answers = onboard.Answers{}
		}
		o.trace(fmt.Sprintf("-- log mark before %s: %s", step.ID, logMark(c.world)))
		out, err := Call[opssetup.ApplyIn, opssetup.ApplyOut](ctx, c, opssetup.OpApply, in)
		o.trace(fmt.Sprintf("apply %s answers=%s → status=%s err=%v checks=%s", step.ID, answerText(in.Answers), out.Status, err, checkText(out.Checks)))
		if err != nil && runOfferedFixes(ctx, c, o, name, step, in.Answers) {
			// The person clicked "Run it" on each offered fix, then Save again.
			out, err = Call[opssetup.ApplyIn, opssetup.ApplyOut](ctx, c, opssetup.OpApply, in)
			o.trace(fmt.Sprintf("apply %s again → status=%s err=%v checks=%s", step.ID, out.Status, err, checkText(out.Checks)))
		}
		if err != nil {
			judgeRefusal(ctx, c, o, name, step, in.Answers, auto, err)
			return false
		}
		o.Steps = append(o.Steps, string(step.ID)+": "+string(out.Status))
		if step.ID == summaryStep {
			return true
		}
		// The page reads the plan again after every save (invalidateSetups).
		if plan, err = Call[opssetup.PlanIn, opssetup.PlanOut](ctx, c, opssetup.OpPlan, opssetup.PlanIn{Setup: name, Kind: kindSolo}); err != nil {
			o.fail(fmt.Sprintf("setup.plan after %s: %v", step.ID, err))
			return false
		}
	}
	o.fail(fmt.Sprintf("the plan has no %s step (steps: %v)", summaryStep, o.Steps))
	return false
}

// fixRequest is setup.fix's body as the page sends it after its confirm
// dialog (hooks.ts useSetupFix: {...input, yes: true}).
type fixRequest struct {
	opssetup.FixIn
	Yes bool `json:"yes"`
}

// runOfferedFixes checks the step and runs every failing check's runnable
// fix the page offers as "Run it" (setup.fix with yes, as after the
// page's confirm); true when at least one ran.
func runOfferedFixes(ctx context.Context, c *Client, o *Outcome, name string, step opssetup.PlanStep, answers onboard.Answers) bool {
	chk, err := Call[opssetup.CheckIn, opssetup.CheckOut](ctx, c, opssetup.OpCheck, opssetup.CheckIn{Setup: name, Step: string(step.ID), Answers: answers})
	if err != nil || len(chk.FieldErrors) > 0 {
		return false
	}
	ran := false
	for _, r := range chk.Checks {
		if (r.Status != onboard.StatusFail && r.Status != onboard.StatusWarn) || !r.Fix.Runnable {
			continue
		}
		fx, err := Call[fixRequest, opssetup.FixOut](ctx, c, opssetup.OpFix, fixRequest{FixIn: opssetup.FixIn{Setup: name, Check: string(r.Check), Target: r.Target}, Yes: true})
		o.trace(fmt.Sprintf("fix %s target=%s → ran=%v exit=%d err=%v out=%q", r.Check, r.Target, fx.Argv, fx.Exit, err, fx.OutputTail))
		o.Fixed = append(o.Fixed, string(step.ID)+"."+string(r.Check))
		ran = true
	}
	return ran
}

// judgeRefusal classifies a refused Save: a field error on a value lore
// filled → promise 1 broken; any other refusal → promise 2 broken (the
// walk supplied every required value, so nothing is left for the person).
func judgeRefusal(ctx context.Context, c *Client, o *Outcome, name string, step opssetup.PlanStep, answers onboard.Answers, auto map[string]bool, err error) {
	chk, cerr := Call[opssetup.CheckIn, opssetup.CheckOut](ctx, c, opssetup.OpCheck, opssetup.CheckIn{Setup: name, Step: string(step.ID), Answers: answers})
	if cerr == nil {
		for _, fe := range chk.FieldErrors {
			if auto[fe.Question] {
				o.fail(fmt.Sprintf("step %s: Save refused the auto-filled %s=%s: %s (%s)", step.ID, fe.Question, answerText(onboard.Answers{fe.Question: answers[fe.Question]}), fe.Message, fe.Rule))
			} else if strings.TrimSpace(fe.Message) == "" {
				o.fail(fmt.Sprintf("step %s: Save refused %s without saying why", step.ID, fe.Question))
			} else {
				// A value only the person can give (nothing to prefill it
				// from): an allowed refusal, as long as it names the field.
				o.NeedsPerson = append(o.NeedsPerson, fmt.Sprintf("%s.%s: %s", step.ID, fe.Question, fe.Message))
			}
		}
		if len(chk.FieldErrors) > 0 {
			return
		}
	}
	o.fail(fmt.Sprintf("step %s: Save refused with every required value filled: %v; checks: %s", step.ID, err, checkText(chk.Checks)))
}

func judgeConfigProblems(o *Outcome, list opssetup.ListOut) {
	if len(list.ConfigProblems) == 0 {
		o.fail("invalid config.yaml: setup.list returned no config_problems")
		return
	}
	for _, p := range list.ConfigProblems {
		if p.File == "" || p.Problem == "" || p.Fix == "" || p.Line == 0 {
			o.fail(fmt.Sprintf("invalid config.yaml: a config problem lacks file, line, problem or fix: %+v", p))
		}
	}
	o.Steps = append(o.Steps, fmt.Sprintf("config_problems: %d", len(list.ConfigProblems)))
}

// savedConfig is the part of config.yaml the judge reads.
type savedConfig struct {
	Setups []onboard.SetupEntry `yaml:"setups"`
}

// judgeConfig: the new setup is in config.yaml; with gh logins it has a
// GitHub account, its personal store (a GitHub ssh url) belongs to it.
func judgeConfig(o *Outcome, w *World, name string) {
	raw, err := os.ReadFile(filepath.Join(w.Home, ".lore", "config.yaml"))
	if err != nil {
		o.fail(fmt.Sprintf("config.yaml after Save: %v", err))
		return
	}
	var cfg savedConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		o.fail(fmt.Sprintf("config.yaml after Save does not decode: %v", err))
		return
	}
	i := slices.IndexFunc(cfg.Setups, func(s onboard.SetupEntry) bool { return s.Name == name })
	if i < 0 {
		o.fail(fmt.Sprintf("config.yaml after Save has no setup %q:\n%s", name, raw))
		return
	}
	e := cfg.Setups[i]
	acct := setupAccount(e)
	if len(w.Accounts) > 0 && acct == "" {
		o.fail(fmt.Sprintf("saved setup %s has no GitHub account though gh has %d login(s)", name, len(w.Accounts)))
	}
	if acct != "" && !slices.ContainsFunc(w.Accounts, func(a Account) bool { return a.Login == acct }) {
		o.fail(fmt.Sprintf("saved setup %s names GitHub account %q, which is no gh login of this machine", name, acct))
	}
	if owner := storeOwner(e.PersonalStore); owner != "" && acct != "" && owner != acct {
		o.fail(fmt.Sprintf("saved setup %s: personal store %s belongs to %s, the setup's account is %s", name, e.PersonalStore, owner, acct))
	}
	judgeIdentity(o, w, name, e, acct)
}

// judgeIdentity (polish final3 M1): the saved identity comes from ONE
// account. Its email is one of the setup's GitHub account's emails (the
// world's truth for that login: its GitHub emails and noreply address),
// and its handle, when set, is that login. Before the fix, a new setup
// with octo-work active saved octo-personal's email with handle octo-work.
func judgeIdentity(o *Outcome, w *World, name string, e onboard.SetupEntry, acct string) {
	i := slices.IndexFunc(w.Accounts, func(a Account) bool { return a.Login == acct })
	if i < 0 {
		return // no gh login: nothing names the account's emails
	}
	a := w.Accounts[i]
	email := strings.TrimPrefix(e.Identity.Key, humanKeyPrefix)
	if len(e.Identity.Emails) > 0 && !strings.HasPrefix(e.Identity.Key, humanKeyPrefix) {
		email = e.Identity.Emails[0]
	}
	own := append(slices.Clone(a.Emails), noreplyOf(a))
	if email != "" && !slices.ContainsFunc(own, func(x string) bool { return strings.EqualFold(x, email) }) {
		o.fail(fmt.Sprintf("saved setup %s mixes accounts: identity email %s is not one of %s's emails %v (identity %+v)", name, email, acct, own, e.Identity))
	}
	if e.Identity.Handle != "" && !strings.EqualFold(e.Identity.Handle, acct) {
		o.fail(fmt.Sprintf("saved setup %s mixes accounts: identity handle %s, GitHub account %s", name, e.Identity.Handle, acct))
	}
}

// humanKeyPrefix is a person's identity key prefix (human:<email>).
const humanKeyPrefix = "human:"

// noreplyOf is GitHub's private commit address of a world login.
func noreplyOf(a Account) string { return fmt.Sprintf("%d+%s@users.noreply.github.com", a.id, a.Login) }

// setupAccount is the login every gh call of the setup must use
// (gh_account, else github.user — the product's rule, wire/setupfix.go).
func setupAccount(e onboard.SetupEntry) string {
	if e.GitHub.GHAccount != "" {
		return e.GitHub.GHAccount
	}
	return e.GitHub.User
}

// storeOwner is the owner of a git@<host>:<owner>/<repo>.git url; "" otherwise.
func storeOwner(u string) string {
	_, path, ok := strings.Cut(u, ":")
	if !ok || !strings.HasPrefix(u, "git@") {
		return ""
	}
	owner, _, ok := strings.Cut(path, "/")
	if !ok {
		return ""
	}
	return owner
}

// judgeLogs: a gh call about a repo of a world login (repos/<o>/…, repo
// create <o>/…) acts as <o>; an ssh git call for <o>/… authenticates as
// <o>. Every login owns only its own repos, so any other account on such
// a line is a call made as the wrong account.
func judgeLogs(o *Outcome, w *World, name string) {
	logins := map[string]bool{}
	for _, a := range w.Accounts {
		logins[a.Login] = true
	}
	info := w.Info()
	gh, err := ReadLog(info.Logs.GH)
	if err != nil {
		o.fail(fmt.Sprintf("%v", err))
	}
	gh = gh[min(o.skipGH, len(gh)):]
	for _, l := range gh {
		if own := ghRepoOwner(l.Args); own != "" && logins[own] {
			o.Judged++
		}
		if own := ghRepoOwner(l.Args); own != "" && logins[own] && l.Account != own {
			o.fail(fmt.Sprintf("gh call for %s's repo ran as %s: gh %s", own, l.Account, l.Args))
		}
	}
	ssh, err := ReadLog(info.Logs.SSH)
	if err != nil {
		o.fail(fmt.Sprintf("%v", err))
	}
	ssh = ssh[min(o.skipSSH, len(ssh)):]
	for _, l := range ssh {
		if own := sshRepoOwner(l.Args); own != "" && logins[own] {
			o.Judged++
		}
		if own := sshRepoOwner(l.Args); own != "" && logins[own] && l.Account != own {
			o.fail(fmt.Sprintf("ssh git call for %s's repo authenticated as %s: ssh %s", own, l.Account, l.Args))
		}
	}
	_ = name
}

// ghRepoOwner: the repo owner a gh call is about, "" when none.
func ghRepoOwner(args string) string {
	f := strings.Fields(args)
	for i, a := range f {
		if strings.HasPrefix(a, "repos/") {
			own, _, _ := strings.Cut(strings.TrimPrefix(a, "repos/"), "/")
			return own
		}
		if a == "create" && i > 0 && f[i-1] == "repo" && i+1 < len(f) {
			own, _, _ := strings.Cut(f[i+1], "/")
			return own
		}
	}
	return ""
}

// sshRepoOwner: the owner in "git-upload-pack 'o/r.git'" / receive-pack.
func sshRepoOwner(args string) string {
	for _, prog := range []string{"git-upload-pack ", "git-receive-pack "} {
		if _, rest, ok := strings.Cut(args, prog); ok {
			rest = strings.Trim(strings.TrimSpace(rest), "'\"/")
			own, _, _ := strings.Cut(rest, "/")
			return own
		}
	}
	return ""
}

// Store step answers (logic/onboard QIDPersonalStore, QIDStoreURL,
// QIDPush and the personal store choices).
const (
	personalStoreStep = "personal_store"
	qidPersonalStore  = "personal_store"
	qidStoreURL       = "store_url"
	qidPush           = "push"
	storeInherit      = "inherit"
	fileScheme        = "file://"
)

// pickPush is the person picking the shape's push mode on the Stores
// step, which they do only when the store picked there is a git repo:
// create or existing with a git url, or inherit with a git personal.store.
// A local folder or none has no remote (lore refuses another mode there),
// so the question stays empty, as it does for push now.
func pickPush(o *Outcome, w *World, step opssetup.PlanStep, answers onboard.Answers) {
	mode := w.Shape.PushMode()
	if string(step.ID) != personalStoreStep || mode == PushNow {
		return
	}
	url := ""
	switch answers[qidPersonalStore].Text {
	case "create", "existing":
		url = strings.TrimSpace(answers[qidStoreURL].Text)
	case storeInherit:
		url = sharedPersonalStore(w)
	}
	if url == "" || strings.HasPrefix(url, fileScheme) {
		o.trace(fmt.Sprintf("push %s not picked: the Stores step chose %q (%s), no git repo", mode, answers[qidPersonalStore].Text, url))
		return
	}
	answers[qidPush] = onboard.Answer{Text: mode}
	o.Push = mode
}

// sharedPersonalStore is config.yaml's personal.store ("" when unset or
// v0.1.17's "local").
func sharedPersonalStore(w *World) string {
	var cfg struct {
		Personal struct {
			Store string `yaml:"store"`
		} `yaml:"personal"`
	}
	raw, err := os.ReadFile(w.Info().Config)
	if err != nil || yaml.Unmarshal(raw, &cfg) != nil || cfg.Personal.Store == "local" {
		return ""
	}
	return cfg.Personal.Store
}

// personalPush is config.yaml's personal.push ("" when unset).
func personalPush(path string) (string, error) {
	var cfg struct {
		Personal struct {
			Push string `yaml:"push"`
		} `yaml:"personal"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	return cfg.Personal.Push, nil
}

// judgePush: config.yaml personal.push is the mode the walk picked, and
// unset when it picked none (push now writes nothing). After a batch
// save, `lore store show me` for the new setup says so, with the default
// interval.
func judgePush(ctx context.Context, o *Outcome, w *World, name string) {
	got, err := personalPush(w.Info().Config)
	if err != nil {
		o.fail(fmt.Sprintf("config.yaml after Save: %v", err))
		return
	}
	if got != o.Push {
		o.fail(fmt.Sprintf("config.yaml personal.push = %q after the Stores step was saved with push %q (shape push %s)", got, o.Push, w.Shape.PushMode()))
		return
	}
	if o.Push != PushBatch {
		return
	}
	out, err := w.RunLore(ctx, "--setup", name, "store", "show", "me")
	if err != nil || !strings.Contains(out, "push batch every 5m") {
		o.fail(fmt.Sprintf("lore --setup %s store show me after a batch save: %v\n%s", name, err, out))
		return
	}
	o.Steps = append(o.Steps, "store show me: push batch every 5m")
}

// ---------- answers ----------

// baseline is the page's starting answers for a step (logic.ts baselineAnswers).
func baseline(step opssetup.PlanStep, p *onboard.Progress) onboard.Answers {
	out := onboard.Answers{}
	for _, q := range step.Questions {
		if !opssetup.PlanValue.IsZero(q.Value) {
			out[q.ID] = onboard.Answer(q.Value)
		}
	}
	for k, v := range step.Values {
		out[k] = v
	}
	if p != nil {
		for k, v := range p.Answers {
			out[k] = v
		}
	}
	return out
}

func progressOf(plan opssetup.PlanOut, id onboard.StepID) *onboard.Progress {
	for i := range plan.Progress {
		if plan.Progress[i].Step == id {
			return &plan.Progress[i]
		}
	}
	return nil
}

// logMark is "gh N ssh M" lines so far (the trace places calls between steps).
func logMark(w *World) string {
	gh, _ := ReadLog(w.Info().Logs.GH)   // a read error shows as 0
	ssh, _ := ReadLog(w.Info().Logs.SSH) // ditto
	return fmt.Sprintf("gh %d ssh %d", len(gh), len(ssh))
}

// Question ids a picked account card fills (discovery/logic.ts DISCOVER_QID).
const (
	qidName      = "name"
	qidEmail     = "email"
	qidEmails    = "emails"
	qidHandle    = "handle"
	qidHostAlias = "host_alias"
	qidSSHKey    = "ssh_key"
	qidRoute     = "route"
	kindStep     = "kind" // NO_DISCOVERY_STEPS
)

// pagePick is the account card the page has picked (discovery/pick.ts):
// a new setup's Identity step adopts the account the GitHub step defaults
// to (logic.ts implicitPick: the best username candidate), and from then
// on every step's first visit fills from that card (StepForm.tsx
// useDiscoveryAutofill → pickAnswers, overwriting the plan's prefills),
// except the folders: a pick the page made itself leaves them to the
// Folders step's own prefill (pick.ts pickedByPage).
type pagePick struct {
	acct *opssetup.DiscoveredAccount
}

func (p *pagePick) apply(ctx context.Context, c *Client, o *Outcome, step opssetup.PlanStep, saved bool, answers onboard.Answers) {
	if step.ID == kindStep {
		return
	}
	has := func(id string) bool {
		return slices.ContainsFunc(step.Questions, func(q opssetup.PlanQuestion) bool { return q.ID == id })
	}
	if p.acct == nil && !saved && has(qidEmail) && has(qidHandle) {
		d, err := Call[opssetup.DiscoverIn, opssetup.DiscoverOut](ctx, c, opssetup.OpDiscover, opssetup.DiscoverIn{})
		if err != nil {
			o.trace(fmt.Sprintf("setup.discover: %v", err))
			return // the page shows no cards; the plan's prefills stay
		}
		if len(d.Fields.Username) > 0 {
			for i := range d.Accounts {
				if strings.EqualFold(d.Accounts[i].Login, d.Fields.Username[0].Value) {
					p.acct = &d.Accounts[i]
				}
			}
		}
		if p.acct != nil {
			o.trace("page picks account card " + p.acct.Login)
		}
	}
	if p.acct == nil {
		return
	}
	a := p.acct
	set := func(id string, v onboard.Answer) {
		if has(id) {
			answers[id] = v
		}
	}
	if a.Name != "" {
		set(qidName, onboard.Answer{Text: a.Name})
	}
	if a.Email != "" {
		set(qidEmail, onboard.Answer{Text: a.Email})
	}
	if len(a.Emails) > 0 {
		set(qidEmails, onboard.Answer{List: slices.Clone(a.Emails), IsList: true})
	}
	set(qidHandle, onboard.Answer{Text: a.Login})
	set(githubAccountQID, onboard.Answer{Text: a.Login})
	for _, id := range []string{qidHostAlias, qidSSHKey, qidRoute} {
		set(id, onboard.Answer{Text: ""})
	}
}

// githubAccountQID is the GitHub step's account question (GithubStep.tsx QID.account).
const githubAccountQID = "github_account"

// pagePrefill adds what the page fills by itself on top of the plan's
// values: the GitHub step preselects gh's active login, else the only
// account it offers (github/logic.ts accountOptions + defaultAccount).
func pagePrefill(ctx context.Context, c *Client, o *Outcome, step opssetup.PlanStep, answers onboard.Answers) {
	if !slices.ContainsFunc(step.Questions, func(q opssetup.PlanQuestion) bool { return q.ID == githubAccountQID }) || filledAnswer(answers[githubAccountQID]) {
		return
	}
	al, err := Call[opssetup.AliasesIn, opssetup.AliasesOut](ctx, c, "setup.aliases", opssetup.AliasesIn{})
	if err != nil {
		o.trace(fmt.Sprintf("setup.aliases: %v", err))
		return // the page shows no account either; the walk then types one
	}
	var options []string
	add := func(u string) {
		if u != "" && !slices.ContainsFunc(options, func(o string) bool { return strings.EqualFold(o, u) }) {
			options = append(options, u)
		}
	}
	for _, g := range al.GH {
		if g.Active {
			answers[githubAccountQID] = onboard.Answer{Text: g.User}
			return
		}
		add(g.User)
	}
	for _, a := range al.Aliases {
		add(a.LogsInAs)
	}
	for _, q := range step.Questions {
		if q.ID == githubAccountQID {
			for _, ch := range q.Choices.Static {
				add(ch.Value)
			}
		}
	}
	if len(options) == 1 {
		answers[githubAccountQID] = onboard.Answer{Text: options[0]}
	}
}

// filled marks the questions whose answer the page filled itself.
func filled(a onboard.Answers) map[string]bool {
	out := map[string]bool{}
	for k, v := range a {
		if filledAnswer(v) {
			out[k] = true
		}
	}
	return out
}

// filledAnswer: ” / [] / false are "not answered" (logic.ts isEmptyAnswer).
func filledAnswer(a onboard.Answer) bool {
	switch {
	case a.IsBool:
		return a.Bool
	case a.IsList:
		return len(a.List) > 0
	}
	return a.Text != ""
}

// supplied is what the walk types into a required question with no value.
func supplied(q opssetup.PlanQuestion, name string) onboard.Answer {
	switch q.Type {
	case onboard.QEmail:
		return onboard.Answer{Text: fallbackEmail}
	case onboard.QEmailList:
		return onboard.Answer{List: []string{fallbackEmail}, IsList: true}
	case onboard.QBool:
		return onboard.Answer{Bool: true, IsBool: true}
	}
	if q.ID == "name" {
		return onboard.Answer{Text: name}
	}
	if len(q.Choices.Static) > 0 {
		return onboard.Answer{Text: q.Choices.Static[0].Value}
	}
	return onboard.Answer{Text: supplyText}
}

func answerText(a onboard.Answers) string {
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(" ")
		}
		v, _ := a[k].MarshalJSON() // Answer's marshal cannot fail
		fmt.Fprintf(&b, "%s=%s", k, v)
	}
	b.WriteString("}")
	return b.String()
}

func checkText(cs []onboard.CheckResult) string {
	var parts []string
	for _, c := range cs {
		s := string(c.Check) + "=" + string(c.Status)
		if c.Status != onboard.StatusOK && c.Detail != "" {
			s += "(" + c.Detail + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}
