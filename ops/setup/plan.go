package setup

// plan.go — the guided-setup engine the op bodies share (ONBOARDING_DESIGN
// 2.3) and setup.plan itself (7.1).
//
//	setup.plan  ─▶ Catalog.Plan(kind) + Store.Progress(setup)
//	setup.check ─▶ Catalog.Validate ─▶ Prober.Probe (parallel) ─▶ results ─▶ Store.PutProgress
//	setup.apply ─▶ order (Needs) ─▶ validate ─▶ probe ─▶ gate ─▶ Applier.Apply ─▶ re-probe ─▶ progress
//
// The step catalog is pure data in logic/onboard; the engine reads it
// through the small Catalog seam below so a test can hand it a fixed
// catalog and the ops never import a catalog variable (no hidden state).

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
)

// Catalog is the read side of the step catalog (logic/onboard, ONB 2.2)
// the setup ops need. The default (onboardCatalog) answers from
// logic/onboard; tests pass their own.
type Catalog interface {
	// Plan is the steps of kind, in catalog order.
	Plan(kind onboard.Kind) []onboard.Step
	// Check is id's entry as step runs it for kind, with the level and fix
	// variant that depend on the context applied; vars are the probe's
	// Vars (nil before probing). False for an unknown id.
	Check(id onboard.CheckID, step onboard.StepID, kind onboard.Kind, vars map[string]string) (onboard.Check, bool)
	// Validate checks answers against the step's questions for kind
	// (required, type, Rules); empty means valid (ONB 2.3: nothing is
	// probed while any field error exists).
	Validate(step onboard.Step, kind onboard.Kind, answers onboard.Answers) []onboard.FieldError
	// FillFix resolves fix's placeholders from vars and re-validates every
	// filled value (E_FIX_REFUSED); Command is never nil.
	FillFix(id onboard.CheckID, fix onboard.Fix, vars map[string]string, step onboard.Step) (onboard.Fix, error)
	// KindChecks lists every check that applies to kind, doctor-only ones
	// included (repair 6.5 runs them all).
	KindChecks(kind onboard.Kind) []onboard.CheckID
	// HomeStep is the step a failing check sends the person back to (6.5).
	HomeStep(id onboard.CheckID, kind onboard.Kind) (onboard.StepID, bool)
}

// engine is the shared machinery of the setup ops: the env ports, the
// catalog and the clock (CheckResult.CheckedAt).
type engine struct {
	env Onboard
	cat Catalog
	now func() time.Time
	// jobs is setup.discover's background job (discover_job.go); set only
	// on the engine DiscoverOps binds, nil elsewhere.
	jobs *discoverJobs
}

// newEngine binds env and cat; now is the engine's clock (opclock: the
// env's Now when it offers one, so a test build's LORE_NOW fixes
// removed_at, disabled_at and every check stamp as it fixes row stamps;
// else the system clock), in UTC.
func newEngine(env Onboard, cat Catalog) engine {
	return engine{env: env, cat: cat, now: func() time.Time { return opclock.Now(env).UTC() }}
}

// setupName is the setup an op works on: its own field first, then the
// global --setup / X-Lore-Setup carried in ctx (contracts: never a per-op
// --setup flag).
func setupName(ctx context.Context, field string) string {
	if n := strings.TrimSpace(field); n != "" {
		return n
	}
	return op.SetupOf(ctx)
}

// newSetupName is setupName for check and apply: a setup being made has
// no name yet when its kind step runs (the page sends setup "" until the
// kind step is saved), so the kind step's own name answer names it (4.1).
func newSetupName(ctx context.Context, field, step string, answers onboard.Answers) string {
	if n := setupName(ctx, field); n != "" || step != string(onboard.StepKind) {
		return n
	}
	return strings.TrimSpace(answers[onboard.QIDName].Text)
}

// requireName refuses an empty or malformed setup name (3.2 name rule).
func requireName(name string) error {
	if name == "" {
		return errs.New(errs.InvalidInput, "no setup named; give the setup name").
			WithHint("lore setup list shows the setups on this machine")
	}
	if n := utf8.RuneCountInString(name); n > onboard.SetupNameMaxLen {
		// Name the limit, not the name: echoing a 10k-char paste buries
		// the cause, and "must be snake_case" is wrong for a long valid one.
		return errs.New(errs.InvalidInput, "setup name is "+itoa(n)+" characters; at most "+itoa(onboard.SetupNameMaxLen)+" allowed").
			WithHint("pick a shorter name such as personal or acme-hub")
	}
	if !onboard.ValidSetupName(name) {
		return errs.New(errs.InvalidInput, "setup name "+quote(name)+" must be snake_case or kebab-case (a-z, 0-9, _ or -, starting with a letter)").
			WithHint("pick a name such as personal or acme-hub")
	}
	return nil
}

// notFound is E_SETUP_NOT_FOUND with the 7.2 message. It names the
// setups this machine has, so a typo is fixed without a second command,
// and always carries a hint line (the text form prints only the hint, not
// next): with no setups the hint is the guided setup that makes one.
func notFound(name string, cfg onboard.Config) error {
	if len(cfg.Setups) == 0 {
		return errs.New(errs.SetupNotFound, "no setup named "+quote(name)+"; this machine has no setups").
			WithHint("lore setup makes one").WithNext("lore", "setup")
	}
	names := make([]string, len(cfg.Setups))
	for i, e := range cfg.Setups {
		names[i] = e.Name
	}
	return errs.New(errs.SetupNotFound, "no setup named "+quote(name)+"; the setups on this machine: "+strings.Join(names, ", ")).
		WithHint("lore setup list shows each one with its folders").WithNext("lore", "setup", "list")
}

// stepState is everything one step's ops read about a setup: the config
// entry (zero with Name and Kind while the setup is being made), its plan
// and its progress rows by step.
type stepState struct {
	name   string
	entry  onboard.SetupEntry
	exists bool // the setups[] entry is in config.yaml
	// done: at load, the entry existed with no progress rows (its summary
	// ran, 4.11). Fixed at load so a kind-step apply that creates the
	// entry does not make the half-made setup look finished.
	done bool
	kind onboard.Kind
	plan []onboard.Step
	// unset: no entry, no saved kind step and no kind answer, so kind is
	// solo only by default; owners maps every kind whose plan has a step
	// to that step's kinds, so a step of another kind's plan (join on a
	// fresh machine, devco:L-1) names that plan, not the solo one.
	unset    bool
	owners   map[onboard.StepID][]onboard.Kind
	progress map[onboard.StepID]onboard.Progress
	// prior is a finished setup's kept results as they stood before this
	// apply's write (HealthSource), read before the write changes the
	// entry they were made for; nil when none (keepStepResults).
	prior []onboard.CheckResult
}

// finished reports a setup whose summary ran: the entry exists and it has
// no progress rows left (ONB 4.11 deletes them), so every step counts done.
func (s stepState) finished() bool { return s.done }

// saved reports a step whose answers were saved (or that was skipped):
// every step of a finished setup, else a done or skipped progress row.
func (s stepState) saved(id onboard.StepID) bool {
	if s.finished() {
		return true
	}
	p, ok := s.progress[id]
	return ok && (p.Status == onboard.StepDone || p.Status == onboard.StepSkipped)
}

// stepDone reports whether id counts as done or skipped for Needs.
func (s stepState) stepDone(id onboard.StepID) bool {
	if s.finished() {
		return true
	}
	p, ok := s.progress[id]
	return ok && (p.Status == onboard.StepDone || p.Status == onboard.StepSkipped)
}

// step finds id in the plan (E_INVALID_INPUT naming the plan otherwise).
func (s stepState) step(id string) (onboard.Step, error) {
	for _, st := range s.plan {
		if string(st.ID) == id {
			return st, nil
		}
	}
	if kinds := s.owners[onboard.StepID(id)]; s.unset && len(kinds) > 0 {
		k := string(kinds[0])
		e := errs.New(errs.InvalidInput, "setup "+quote(s.name)+" has no kind yet on this machine, and step "+quote(id)+" is in the "+k+" plan; apply the kind step first: lore setup apply --setup "+s.name+" --step kind --answers '{\"kind\":\""+k+"\"}'")
		if onboard.StepID(id) == onboard.StepJoin {
			return onboard.Step{}, e.WithHint("lore setup join --invite \"<the org's store url>\" prints every step of the member plan, filled in").
				WithNext("lore", "setup", "join", "--invite", "<the org's store url>")
		}
		return onboard.Step{}, e.WithNext("lore", "setup", "apply", "--setup", s.name, "--step", string(onboard.StepKind), "--answers", `{"kind":"`+k+`"}`)
	}
	return onboard.Step{}, errs.New(errs.InvalidInput, "step "+quote(id)+" is not in the "+string(s.kind)+" plan").
		WithNext("lore", "setup", "plan", "--kind", string(s.kind))
}

// kindAnswer is the kind step's question id (ONB 4.1) the engine reads
// while a setup is being made and has no entry yet.
const kindAnswer = "kind"

// load reads the config entry and the progress rows of name and decides
// its kind: the entry's kind, else the answer of the saved kind step, else
// the kind answer being sent (the kind step itself), else solo (ONB 4.1).
func (e engine) load(ctx context.Context, st Store, name string, answers onboard.Answers) (stepState, error) {
	cfg, err := st.Config(ctx)
	if err != nil {
		return stepState{}, err
	}
	rows, err := st.Progress(ctx, name)
	if err != nil {
		return stepState{}, err
	}
	s := stepState{name: name, progress: make(map[onboard.StepID]onboard.Progress, len(rows))}
	for _, r := range rows {
		s.progress[r.Step] = r
	}
	s.entry, s.exists = cfg.Setup(name)
	s.done = s.exists && len(rows) == 0
	kindRow, saved := s.progress[onboard.StepKind]
	switch {
	case saved && kindRow.Status == onboard.StepDone && answerKind(kindRow.Answers).Valid():
		// The saved kind step wins over the entry: a setup whose kind was
		// changed (reopens) follows the new kind's plan, and the entry
		// the join or team step writes carries the kind and default saved
		// here.
		s.kind = answerKind(kindRow.Answers)
		if s.exists {
			s.entry.Kind = s.kind
			if d, ok := kindRow.Answers[onboard.QIDDefault]; ok && d.IsBool {
				s.entry.Default = d.Bool
			}
		}
	case s.exists && s.entry.Kind.Valid():
		s.kind = s.entry.Kind
	case answerKind(s.progress[onboard.StepKind].Answers).Valid():
		s.kind = answerKind(s.progress[onboard.StepKind].Answers)
	case answerKind(answers).Valid():
		s.kind = answerKind(answers)
	default:
		s.kind = onboard.KindSolo
		s.unset = !s.exists
	}
	if !s.exists {
		s.entry = onboard.SetupEntry{Name: name, Kind: s.kind}
	}
	s.plan = e.cat.Plan(s.kind)
	if s.unset {
		s.owners = map[onboard.StepID][]onboard.Kind{}
		for _, k := range onboard.Kinds() {
			for _, st := range e.cat.Plan(k) {
				s.owners[st.ID] = append(s.owners[st.ID], k)
			}
		}
	}
	return s, nil
}

// answerKind is the kind answer in a, "" when absent.
func answerKind(a onboard.Answers) onboard.Kind { return onboard.Kind(a[kindAnswer].Text) }

// probeItem is one check to run and the step it runs in (the step picks
// the fix text, ONB 4.5, and the answers fill it).
type probeItem struct {
	check onboard.CheckID
	step  onboard.Step
}

// probe runs the step's checks with answers; see probeItems. A
// create-only team check that would only find this setup's own org
// (onboard.OwnOrg: a re-check of the saved step, the re-probe right after
// its save, a retry of a save that stopped midway) is not probed and
// reports skip with the reason, so it neither blocks the save nor shows
// as a failure on the setup's own org. Results keep step.Checks order.
func (e engine) probe(ctx context.Context, s stepState, step onboard.Step, answers onboard.Answers) ([]onboard.CheckResult, error) {
	items := make([]probeItem, 0, len(step.Checks))
	own := map[onboard.CheckID]string{}
	noURL := onboard.StoreURLMissing(step.ID, answers)
	noStore := onboard.StoreChecksNotApplicable(step.ID, answers)
	for _, id := range step.Checks {
		if noStore && onboard.IsStoreCheck(id) {
			// no store url belongs to this choice: a result that says so
			// (every planned check has one), not "no store url yet"
			own[id] = onboard.SkipNoStoreNeeded
			continue
		}
		if noURL && onboard.IsStoreCheck(id) {
			own[id] = onboard.SkipNoStoreURL
			continue
		}
		if why, ok := e.ownOrg(ctx, s, id, answers); ok {
			own[id] = why
			continue
		}
		items = append(items, probeItem{check: id, step: step})
	}
	ps, err := e.probeItems(ctx, s, items, func(onboard.StepID) onboard.Answers { return answers }, "")
	if err != nil || len(own) == 0 {
		return resultsOf(ps), err
	}
	got := resultsOf(ps)
	out := make([]onboard.CheckResult, 0, len(step.Checks))
	for _, id := range step.Checks {
		if why, ok := own[id]; ok {
			c, _ := e.cat.Check(id, step.ID, s.kind, nil)
			lvl := c.Level
			if why == onboard.SkipNoStoreNeeded {
				lvl = onboard.LevelInfo // nothing to do: a note, not an error-level skip
			}
			out = append(out, onboard.CheckResult{Check: id, Status: onboard.StatusSkip, Level: lvl, Detail: why, CheckedAt: e.now()})
			continue
		}
		out = append(out, resultsFor(got, id)...)
	}
	return out, nil
}

// resultsFor is every result of check id in rs, in order: a per-tool
// check has one per picked tool.
func resultsFor(rs []onboard.CheckResult, id onboard.CheckID) []onboard.CheckResult {
	var out []onboard.CheckResult
	for _, r := range rs {
		if r.Check == id {
			out = append(out, r)
		}
	}
	return out
}

// perToolChecks run once per picked tool (Target = the tool), so each tool
// is its own row with its own fix instead of one merged line (realmac
// owner test: one wall of text for five tools).
var perToolChecks = map[onboard.CheckID]bool{
	onboard.CheckToolsSkillInstalled: true, onboard.CheckToolsSkillVersion: true, onboard.CheckToolsSkillModified: true,
	onboard.CheckToolsPointerFile: true, onboard.CheckToolsHooks: true,
}

// pickedTools are the tools a per-tool check covers: the Tools step's
// answer when given (an empty pick is a choice), else the saved setup's.
func pickedTools(answers onboard.Answers, entry onboard.SetupEntry) []string {
	list := entry.Tools
	if a, ok := answers[onboard.QIDTools]; ok && a.IsList {
		list = a.List
	}
	out := make([]string, 0, len(list))
	for _, t := range list {
		if t = strings.TrimSpace(t); t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// probeReusing is probe for a save: the results in reuse (onboard.
// FreshChecks: same answers, younger than onboard.CheckFreshTTL) stand in
// for their checks, and only the others run. Results keep step.Checks
// order; a reused result keeps its own CheckedAt, so the page shows
// "checked 12s ago" rather than a time it did not run.
func (e engine) probeReusing(ctx context.Context, s stepState, step onboard.Step, answers onboard.Answers, reuse map[onboard.CheckID]onboard.CheckResult) ([]onboard.CheckResult, error) {
	if len(reuse) == 0 {
		return e.probe(ctx, s, step, answers)
	}
	// A per-tool check has a result per tool; the reuse map holds one per
	// check, so those always run again (local file reads, cheap).
	reuse = maps.Clone(reuse)
	maps.DeleteFunc(reuse, func(id onboard.CheckID, _ onboard.CheckResult) bool { return perToolChecks[id] })
	rest := step
	rest.Checks = slices.DeleteFunc(slices.Clone(step.Checks), func(id onboard.CheckID) bool { _, ok := reuse[id]; return ok })
	ran, err := e.probe(ctx, s, rest, answers)
	if err != nil {
		return nil, err
	}
	out := make([]onboard.CheckResult, 0, len(step.Checks))
	for _, id := range step.Checks {
		if r, ok := reuse[id]; ok {
			out = append(out, r)
			continue
		}
		out = append(out, resultsFor(ran, id)...)
	}
	return out, nil
}

// ownOrg is onboard.OwnOrg for check id of setup s with answers: the
// saved org from s, the orgs[] entry from the Store when it can list them
// (OrgStoreLister), the other setups from config.yaml. Anything that
// cannot be read leaves the check to run (false).
func (e engine) ownOrg(ctx context.Context, s stepState, id onboard.CheckID, answers onboard.Answers) (string, bool) {
	if !onboard.OrgCreateCheck(id) {
		return "", false
	}
	c := onboard.OrgClaim{Org: strings.TrimSpace(answers[onboard.QIDOrg].Text), URL: strings.TrimSpace(answers[onboard.QIDStoreURL].Text)}
	if s.exists {
		c.SavedOrg = s.entry.Org
	}
	if why, ok := onboard.OwnOrg(id, c); ok || c.Org == "" {
		return why, ok
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return "", false
	}
	ol, ok := st.(OrgStoreLister)
	if !ok {
		return "", false
	}
	if c.EntryURLs, c.EntryFound, err = ol.OrgStoreURLs(ctx, c.Org); err != nil || !c.EntryFound {
		return "", false
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return "", false
	}
	c.ClaimedByOther = slices.ContainsFunc(cfg.Setups, func(o onboard.SetupEntry) bool { return o.Name != s.name && o.Org == c.Org }) ||
		// A trashed setup still owns its team (a restore brings it back):
		// the entry is not a midway save of this one, and org.name_free
		// says how to restore or purge it.
		slices.ContainsFunc(cfg.Trash, func(t onboard.TrashedSetup) bool { return t.Setup.Org == c.Org })
	return onboard.OwnOrg(id, c)
}

// probed is one check's outcome: the item, the raw probe result (its
// Vars rebuild a fix argv) and the CheckResult made from it.
type probed struct {
	item probeItem
	raw  ProbeResult
	res  onboard.CheckResult
}

// resultsOf is the CheckResults of ps, never nil.
func resultsOf(ps []probed) []onboard.CheckResult {
	out := make([]onboard.CheckResult, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.res)
	}
	return out
}

// probeItems runs items for setup s in one parallel batch (the Prober
// owns the concurrency and the timeouts, ONB 6.1) and turns each raw
// result into the 2.2 CheckResult; answersOf gives each step's answers;
// target "" = the setup as a whole. An unknown check id is reported as
// StatusError so a catalog slip shows instead of vanishing.
func (e engine) probeItems(ctx context.Context, s stepState, items []probeItem, answersOf func(onboard.StepID) onboard.Answers, target string) ([]probed, error) {
	if len(items) == 0 {
		return []probed{}, nil
	}
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return nil, err
	}
	known := make([]probeItem, 0, len(items))
	reqs := make([]ProbeRequest, 0, len(items))
	out := make([]probed, 0, len(items))
	for _, it := range items {
		c, ok := e.cat.Check(it.check, it.step.ID, s.kind, nil)
		if !ok {
			res := onboard.CheckResult{Check: it.check, Status: onboard.StatusError, Level: onboard.LevelWarn,
				Detail: "check " + string(it.check) + " is not in the catalog", CheckedAt: e.now()}
			out = append(out, probed{item: it, raw: ProbeResult{Check: it.check, Status: res.Status, Detail: res.Detail}, res: res})
			continue
		}
		answers := answersOf(it.step.ID)
		if tools := pickedTools(answers, s.entry); target == "" && perToolChecks[it.check] && len(tools) > 0 {
			for _, t := range tools {
				known = append(known, it)
				reqs = append(reqs, ProbeRequest{Check: c, Setup: s.entry, Answers: answers, Target: t})
			}
			continue
		}
		known = append(known, it)
		reqs = append(reqs, ProbeRequest{Check: c, Setup: s.entry, Answers: answers, Target: target})
	}
	got := pr.Probe(ctx, reqs)
	for i, it := range known {
		r := ProbeResult{Check: it.check, Target: target, Status: onboard.StatusError, Detail: "the probe returned no result"}
		if i < len(got) {
			r = got[i]
		}
		out = append(out, probed{item: it, raw: r, res: e.result(it, r, s, answersOf(it.step.ID))})
	}
	return out, nil
}

// result is one CheckResult: the level for this context, the fix filled
// from the setup, the answers and the probe's Vars (only on warn/fail;
// Command never nil), the check time. A fix whose values do not pass
// re-validation is shown as text only and is not runnable.
func (e engine) result(it probeItem, r ProbeResult, s stepState, answers onboard.Answers) onboard.CheckResult {
	c, _ := e.cat.Check(it.check, it.step.ID, s.kind, r.Vars)
	out := onboard.CheckResult{Check: c.ID, Status: r.Status, Level: c.Level, Target: stepTarget(r, s, answers), Detail: r.Detail,
		CheckedAt: e.now(), TookMS: r.TookMS}
	if r.Status == onboard.StatusWarn && r.Vars[onboard.VarLevel] == string(onboard.LevelInfo) {
		out.Level = onboard.LevelInfo
	}
	if r.Status == onboard.StatusWarn || r.Status == onboard.StatusFail {
		vars := fixVars(s, answers, r)
		f, err := e.cat.FillFix(c.ID, c.Fix, vars, it.step)
		if err != nil {
			// Text only, filled where it can be: never the refusal's own
			// E_FIX_REFUSED text nor a raw {placeholder} (onboard.TextFix).
			f = onboard.TextFix(c.Fix, vars)
		}
		out.Fix = f
	}
	return out
}

// stepTarget is the Target a result row carries. A check probed per item
// has its own target. A store check of a step (personal_store, team,
// join) probes the store the step's answers name and has none; the row
// then carries that store's url, so a "Run it" sends it back to setup.fix
// and the fix re-probes that same store rather than whichever store the
// first step owning the check would resolve (store.private of the team
// step is not the personal store's). Only a url that is exactly the
// answered or saved one is used: Vars hold the sanitized url, and a
// sanitized url must never be probed in place of the real one.
func stepTarget(r ProbeResult, s stepState, answers onboard.Answers) string {
	if r.Target != "" {
		return r.Target
	}
	u := r.Vars[onboard.VarURL]
	if u == "" {
		return ""
	}
	for _, known := range []string{strings.TrimSpace(answers[onboard.QIDStoreURL].Text), s.entry.PersonalStore} {
		if known == u {
			return u
		}
	}
	return ""
}

// blocking lists the error-level failing checks: the apply gate of ONB
// 2.3 ("any error-level fail → failed, nothing written"). Warn-level
// fails, warnings, skips and probes that could not run (StatusError:
// degraded, never broken, 6.4) do not block.
func blocking(results []onboard.CheckResult) []onboard.CheckID {
	var out []onboard.CheckID
	for _, r := range results {
		if r.Status == onboard.StatusFail && r.Level == onboard.LevelError {
			out = append(out, r.Check)
		}
	}
	return out
}

// statusOf is the step status the checks give: failed on any blocking
// check, else ready (ONB 2.3).
func statusOf(results []onboard.CheckResult) onboard.StepStatus {
	if len(blocking(results)) > 0 {
		return onboard.StepFailed
	}
	return onboard.StepReady
}

// ---------------------------------------------------------------------
// setup.plan

// Plan is setup.plan: the steps of the kind and the setup's progress.
// Kind: --kind, else the named setup's kind, else solo. A setup being
// made has no entry yet, so an unknown name is not an error here.
func (e engine) Plan(ctx context.Context, in PlanIn) (PlanOut, error) {
	if in.Kind != "" && !onboard.Kind(in.Kind).Valid() {
		return PlanOut{}, errs.New(errs.InvalidInput, "kind "+quote(in.Kind)+" must be solo, member or admin").
			WithHint("lore setup plan --kind solo")
	}
	out := PlanOut{Steps: []PlanStep{}, Progress: []onboard.Progress{}}
	name := setupName(ctx, in.Setup)
	kind := onboard.Kind(in.Kind)
	var cfg onboard.Config
	entry := onboard.SetupEntry{Name: name}
	exists, finished := false, false
	var state stepState // zero while the setup has no name: no step saved
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return PlanOut{}, err
	}
	if c, cerr := st.Config(ctx); cerr == nil {
		// A config that does not read only loses the kind defaults here;
		// setup.check reports it (setup.config).
		cfg = c
	}
	if name != "" {
		s, err := e.load(ctx, st, name, nil)
		if err != nil {
			return PlanOut{}, err
		}
		if kind == "" {
			kind = s.kind
		}
		out.Progress = currentFixes(progressInOrder(e.cat.Plan(kind), s.progress))
		entry, exists, finished = s.entry, s.exists, s.finished()
		state = s
	}
	if kind == "" {
		kind = onboard.KindSolo
	}
	if entry.Kind == "" {
		entry.Kind = kind
	}
	steps := e.cat.Plan(kind)
	pre := e.prefills(ctx, steps, entry)
	for _, step := range steps {
		out.Steps = append(out.Steps, planStep(step, kind, entry, exists, state.saved(step.ID), cfg, pre))
	}
	out.Exists = exists
	if finished {
		// A finished setup has no progress rows (4.11 deletes them), and
		// every step counts done (2.3: "lore setup again on a finished
		// setup shows every step done"): report that, so a front end's
		// counter reads 9 of 9, with the saved values as the answers.
		// Each row carries the last doctor results of its step's checks
		// (HealthSource, when cached): a front end's marks then say
		// whether the saved answers still pass, the same before and
		// after a reload (realmac #11), instead of ✓ until a re-check.
		var cached []onboard.CheckResult
		if hs, ok := e.env.(HealthSource); ok {
			cached, _ = hs.SetupHealth(ctx, name)
		}
		out.Progress = make([]onboard.Progress, 0, len(out.Steps))
		for _, st := range out.Steps {
			out.Progress = append(out.Progress, onboard.Progress{Setup: name, Step: st.ID, Answers: st.Values,
				Checks: stepResults(cached, st.Checks), Status: onboard.StepDone})
		}
	}
	return out, nil
}

// stepResults is the results whose check id is one of ids, in their order
// (never nil: the wire shape is a list).
func stepResults(results []onboard.CheckResult, ids []onboard.CheckID) []onboard.CheckResult {
	out := []onboard.CheckResult{}
	for _, r := range results {
		if slices.Contains(ids, r.Check) {
			out = append(out, r)
		}
	}
	return out
}

// PlanStep is one catalog step as setup.plan sends it (ONB 2.2, 2.4):
// the catalog Step's fields, with every prefill resolved for the setup
// being made or edited:
//
//   - questions[].choices.static: the catalog's static choices and the
//     detected ones (deduplicated by value); choices.prefill keeps the
//     detection id so a front end can say where they came from.
//   - values: the answers the step starts from, keyed by question id (the
//     same object setup.check and setup.apply take as answers): the
//     setup's own saved value when it exists (editing), else the Prober's
//     detection, else the kind defaults (4.1); a question with nothing
//     known has no key.
//
// A detection that cannot run leaves no value and no extra choices; it is
// never an error (the person types the answer). The fields are spelled
// out (not an embedded onboard.Step) because the op schema reads named
// json fields only.
type PlanStep struct {
	ID        onboard.StepID    `json:"id"`
	Title     string            `json:"title"`
	Help      string            `json:"help,omitempty"`
	Kinds     []onboard.Kind    `json:"kinds"`
	Needs     []onboard.StepID  `json:"needs,omitempty"`
	Questions []PlanQuestion    `json:"questions,omitempty"`
	Checks    []onboard.CheckID `json:"checks,omitempty"`
	Apply     onboard.ApplyID   `json:"apply,omitempty"`
	Optional  bool              `json:"optional,omitempty"`
	Values    onboard.Answers   `json:"values,omitempty"`
}

// PlanQuestion is one catalog Question as setup.plan sends it: its
// fields, choices.static resolved (see PlanStep), and value, the same
// starting answer as the step's values[id] (omitted when nothing is
// known). Spelled out for the op schema, as PlanStep.
type PlanQuestion struct {
	ID       string               `json:"id"`
	Label    string               `json:"label"`
	Help     string               `json:"help,omitempty"`
	Type     onboard.QType        `json:"type"`
	Required bool                 `json:"required,omitempty"`
	Prefill  onboard.PrefillID    `json:"prefill,omitempty"`
	Choices  onboard.ChoiceSource `json:"choices,omitzero"`
	Rules    []onboard.Rule       `json:"rules,omitempty"`
	Kinds    []onboard.Kind       `json:"kinds,omitempty"`
	Value    PlanValue            `json:"value,omitzero"`
}

// PlanValue is a question's starting answer in its natural JSON form (a
// string, a list or a bool, as onboard.Answer marshals). It is text to the
// op schema (an Answer is not a json-tagged struct).
type PlanValue onboard.Answer

// IsZero reports no value (omitzero).
func (v PlanValue) IsZero() bool { return !answerSet(onboard.Answer(v)) }

// MarshalJSON writes the natural form.
func (v PlanValue) MarshalJSON() ([]byte, error) { return onboard.Answer(v).MarshalJSON() }

// UnmarshalJSON reads the natural form.
func (v *PlanValue) UnmarshalJSON(b []byte) error { return (*onboard.Answer)(v).UnmarshalJSON(b) }

// MarshalText is MarshalJSON (op schema: a text field).
func (v PlanValue) MarshalText() ([]byte, error) { return v.MarshalJSON() }

// UnmarshalText is UnmarshalJSON.
func (v *PlanValue) UnmarshalText(b []byte) error { return v.UnmarshalJSON(b) }

// PrefillGitHubUsers is onboard.PrefillGitHubUsers, kept under this name
// for the wire's prober switch and tests (ONB 4.3: the github_account
// choices are GitHub users, not the host aliases ssh.github_keys gives).
const PrefillGitHubUsers = onboard.PrefillGitHubUsers

// defaultSetupName is the solo setup name 1.4 prefills ("personal").
const defaultSetupName = "personal"

// prefillKey is one detection to resolve.
type prefillKey = onboard.PrefillID

// prefills resolves every detection the steps name, once each and in
// parallel (the Prober's limiter bounds the probes, ONB 6.1). A Prober
// that cannot be had, or a detection that fails, resolves to nothing.
func (e engine) prefills(ctx context.Context, steps []onboard.Step, entry onboard.SetupEntry) map[prefillKey]PrefillOut {
	ids := []prefillKey{}
	for _, st := range steps {
		for _, q := range st.Questions {
			for _, id := range []prefillKey{q.Prefill, questionChoicePrefill(q)} {
				if id != onboard.PrefillNone && !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
		}
	}
	out := make(map[prefillKey]PrefillOut, len(ids))
	if len(ids) == 0 {
		return out
	}
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return out
	}
	res := make([]PrefillOut, len(ids))
	ok := make([]bool, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, err := pr.Prefill(ctx, id, entry); err == nil {
				res[i], ok[i] = p, true
			}
		}()
	}
	wg.Wait()
	for i, id := range ids {
		if ok[i] {
			out[id] = res[i]
		}
	}
	return out
}

// questionChoicePrefill is the detection that gives q its choices.
func questionChoicePrefill(q onboard.Question) prefillKey { return q.Choices.Prefill }

// planStep resolves step's choices and values for kind (see PlanStep).
// Questions of another kind give no value (4.2 has an email question per
// kind under one id).
func planStep(step onboard.Step, kind onboard.Kind, entry onboard.SetupEntry, exists, saved bool, cfg onboard.Config, pre map[prefillKey]PrefillOut) PlanStep {
	out := PlanStep{ID: step.ID, Title: step.Title, Help: step.Help, Kinds: step.Kinds, Needs: step.Needs,
		Checks: step.Checks, Apply: step.Apply, Optional: step.Optional,
		Questions: make([]PlanQuestion, 0, len(step.Questions)), Values: onboard.Answers{}}
	for _, q := range step.Questions {
		if src := questionChoicePrefill(q); src != onboard.PrefillNone {
			q.Choices.Static = mergeChoices(q.Choices.Static, pre[src].Choices)
		}
		if step.ID == onboard.StepPersonalStore && q.ID == onboard.QIDPersonalStore {
			q.Choices.Static = localChoiceDetail(q.Choices.Static, cfg, entry.Name)
		}
		var value onboard.Answer
		hasValue := false
		if len(q.Kinds) == 0 || slices.Contains(q.Kinds, kind) {
			value, hasValue = questionValue(step.ID, q, kind, entry, exists, saved, cfg, pre)
		}
		if hasValue && exists && saved {
			// A saved answer this machine did not detect (a secret manager
			// not on PATH) is still offered, so the picker shows it picked
			// instead of hiding it (owner rule 2026-11-09).
			q.Choices.Static = withSavedChoices(q, value)
		}
		pq := PlanQuestion{ID: q.ID, Label: q.Label, Help: q.Help, Type: q.Type, Required: q.Required,
			Prefill: q.Prefill, Choices: q.Choices, Rules: q.Rules, Kinds: q.Kinds}
		if hasValue {
			out.Values[q.ID] = value
			pq.Value = PlanValue(value)
		}
		out.Questions = append(out.Questions, pq)
	}
	return out
}

// localChoiceDetail is the personal store choices with the local one
// saying it is the person's one personal store `me`, shared with the
// setups already on it (onboard.LocalStoreDetail). The catalog's slice is
// copied, never changed.
func localChoiceDetail(choices []onboard.Choice, cfg onboard.Config, setup string) []onboard.Choice {
	out := slices.Clone(choices)
	for i := range out {
		if out[i].Value == onboard.PersonalStoreLocal {
			out[i].Detail = onboard.LocalStoreDetail(cfg, setup)
		}
	}
	return out
}

// questionValue is q's starting answer (see PlanStep values). saved: the
// step was saved (or skipped) before, so an empty saved folder list stays
// empty instead of taking a detected folder nobody picked.
func questionValue(step onboard.StepID, q onboard.Question, kind onboard.Kind, entry onboard.SetupEntry, exists, saved bool, cfg onboard.Config, pre map[prefillKey]PrefillOut) (onboard.Answer, bool) {
	// "Same as my other setups" is offered as the starting answer only
	// when there is a shared personal.store to be the same as (realmac
	// finding 12: it was preselected with none, and the url the checks
	// tested was dropped on save).
	others := cfg.SharedStore != "" && slices.ContainsFunc(cfg.Setups, func(o onboard.SetupEntry) bool { return o.Name != entry.Name })
	// A team setup's identity lives in orgs[] (IdentityOf): show that.
	entry.Identity = cfg.IdentityOf(entry)
	if exists && step == onboard.StepTeam && q.ID == onboard.QIDStoreURL && entry.Org != "" {
		// A saved admin setup's team step shows its team's own store (its
		// orgs[] entry), never a fresh "create" suggestion: saving it
		// unchanged then refused with E_SETUP_ORG_MISMATCH, the entry's
		// store against the suggested one (round-3 finding).
		if u := cfg.OrgStores[entry.Org]; u != "" {
			return textAnswer(u), true
		}
	}
	if v, ok := entryValue(step, q, entry, exists, others); ok {
		return v, true
	}
	if saved && step == onboard.StepFolders && q.ID == onboard.QIDFolders {
		// A saved setup with no folders (realmac NEW-4 world: reopening
		// it showed ~/Downloads/lore1/** as if saved, and Save & next
		// would write it). Prefill only a step never saved.
		return onboard.Answer{}, false
	}
	if v, ok := savedEmpty(q, exists, saved); ok {
		return v, true
	}
	if q.Prefill != onboard.PrefillNone && answerSet(pre[q.Prefill].Value) {
		return pre[q.Prefill].Value, true
	}
	if src := questionChoicePrefill(q); src != onboard.PrefillNone && answerSet(pre[src].Value) {
		return pre[src].Value, true
	}
	if step == onboard.StepPersonalStore && q.ID == onboard.QIDPersonalStore {
		return personalStoreDefault(cfg, pre), true
	}
	return kindDefault(step, q, kind, entry, cfg)
}

// savedEmpty is the answer of a picker the saved setup left empty: no
// tools and no secret manager are choices too, so a saved step of an
// existing setup shows them as saved (no box ticked, "none" picked),
// never the tools or the manager this machine detects (owner rule
// 2026-11-09: editing starts from the saved setup, detection never
// overrides it). ok is false for every other question, or while the
// step was never saved (a new setup's detected defaults).
func savedEmpty(q onboard.Question, exists, saved bool) (onboard.Answer, bool) {
	if !exists || !saved {
		return onboard.Answer{}, false
	}
	switch q.ID {
	case onboard.QIDTools:
		return listAnswer(nil), true
	case onboard.QIDBackend:
		return textAnswer(onboard.SecretsNone), true
	}
	return onboard.Answer{}, false
}

// withSavedChoices is q's choices with every saved value of v that is not
// among them added after them, labelled as saved and not found here, so
// a choice or multi question never hides what the setup holds. Other
// question types keep their choices as they are.
func withSavedChoices(q onboard.Question, v onboard.Answer) []onboard.Choice {
	if q.Type != onboard.QChoice && q.Type != onboard.QMulti {
		return q.Choices.Static
	}
	vals := v.List
	if !v.IsList {
		vals = []string{v.Text}
	}
	out := slices.Clone(q.Choices.Static)
	for _, s := range vals {
		if s == "" || slices.ContainsFunc(out, func(c onboard.Choice) bool { return c.Value == s }) {
			continue
		}
		out = append(out, onboard.Choice{Value: s, Label: s, Detail: "saved; not found on this machine"})
	}
	return out
}

// personalStoreDefault is the personal store choice a setup starts with
// when nothing saved gives one: the shared personal.store when there is
// one; else "use an existing repo" when a repo url is suggested (the url
// field shows it, and store.read offers gh repo create when it is not
// there yet); else a local folder, which needs nothing. A required
// choice is never left empty next to a filled url (realmac NEW-1: Save
// refused the page's own defaults with "Personal store is required").
func personalStoreDefault(cfg onboard.Config, pre map[prefillKey]PrefillOut) onboard.Answer {
	switch {
	case cfg.SharedStore != "":
		return textAnswer(onboard.PersonalStoreInherit)
	case answerSet(pre[onboard.PrefillStoreURLCreate].Value):
		return textAnswer(onboard.PersonalStoreExisting)
	}
	return textAnswer(onboard.PersonalStoreLocal)
}

// answerSet reports an answer worth sending as a value (a non-empty text
// or list, or any bool).
func answerSet(a onboard.Answer) bool {
	return a.IsBool || len(a.List) > 0 || strings.TrimSpace(a.Text) != ""
}

// mergeChoices is static followed by found, without repeating a value.
func mergeChoices(static, found []onboard.Choice) []onboard.Choice {
	out := append([]onboard.Choice{}, static...)
	for _, c := range found {
		if !slices.ContainsFunc(out, func(o onboard.Choice) bool { return o.Value == c.Value }) {
			out = append(out, c)
		}
	}
	// The static "none"-style choices read best last (4.8: the found
	// managers first, then None).
	if len(static) > 0 && len(found) > 0 {
		out = append(out[len(static):], static...)
	}
	return out
}

// textAnswer, listAnswer and boolAnswer are Answers of each form.
func textAnswer(s string) onboard.Answer { return onboard.Answer{Text: s} }
func listAnswer(l []string) onboard.Answer {
	return onboard.Answer{IsList: true, List: append([]string{}, l...)}
}
func boolAnswer(b bool) onboard.Answer                 { return onboard.Answer{IsBool: true, Bool: b} }
func nonEmpty(a onboard.Answer) (onboard.Answer, bool) { return a, answerSet(a) }

// entryValue is q's answer read back from the saved setups[] entry (a
// setup being edited or repaired starts from what it has, not from a
// fresh detection: A13 bug 9, the default box of a default setup).
// others: another setup exists on this machine; without one, an unset
// personal_store has no "same as my other setups" to mean, so it gives no
// value and the person picks (ONB 4.4).
func entryValue(step onboard.StepID, q onboard.Question, e onboard.SetupEntry, exists, others bool) (onboard.Answer, bool) {
	if !exists {
		return onboard.Answer{}, false
	}
	switch q.ID {
	case onboard.QIDKind:
		return nonEmpty(textAnswer(string(e.Kind)))
	case onboard.QIDName:
		if step == onboard.StepKind {
			return nonEmpty(textAnswer(e.Name))
		}
		return nonEmpty(textAnswer(e.Identity.Name))
	case onboard.QIDDefault:
		return boolAnswer(e.Default), true
	case onboard.QIDEmail:
		if _, addr, ok := strings.Cut(e.Identity.Key, ":"); ok {
			return nonEmpty(textAnswer(addr))
		}
	case onboard.QIDEmails:
		return nonEmpty(listAnswer(e.Identity.Emails))
	case onboard.QIDHandle:
		return nonEmpty(textAnswer(e.Identity.Handle))
	case onboard.QIDGitHubAccount:
		return nonEmpty(textAnswer(e.GitHub.User))
	case onboard.QIDHostAlias:
		return nonEmpty(textAnswer(e.GitHub.HostAlias))
	case onboard.QIDSSHKey:
		return nonEmpty(textAnswer(e.GitHub.SSHKey))
	case onboard.QIDRoute:
		return nonEmpty(textAnswer(string(e.GitHub.Route)))
	case onboard.QIDPersonalStore:
		switch ps := e.PersonalStore; ps {
		case "":
			if !others {
				return onboard.Answer{}, false
			}
			return textAnswer(onboard.PersonalStoreInherit), true
		case onboard.PersonalStoreInherit:
			return textAnswer(onboard.PersonalStoreInherit), true
		case onboard.PersonalStoreNone:
			return textAnswer(ps), true
		}
		return textAnswer(onboard.PersonalStoreExisting), true
	case onboard.QIDStoreURL:
		if ps := e.PersonalStore; step == onboard.StepPersonalStore && ps != "" && ps != onboard.PersonalStoreInherit && ps != onboard.PersonalStoreNone {
			return textAnswer(ps), true
		}
	case onboard.QIDOrg:
		return nonEmpty(textAnswer(e.Org))
	case onboard.QIDTools:
		if len(e.Tools) > 0 {
			return listAnswer(e.Tools), true
		}
	case onboard.QIDBackend:
		return nonEmpty(textAnswer(e.Secrets.Backend))
	case onboard.QIDFolders:
		return nonEmpty(listAnswer(e.Folders))
	}
	return onboard.Answer{}, false
}

// kindDefault is the 4.1 starting answer of the kind step when nothing
// else gives one: the plan's kind; a solo setup is named "personal" while
// that name is free; the first solo setup is the default one.
func kindDefault(step onboard.StepID, q onboard.Question, kind onboard.Kind, e onboard.SetupEntry, cfg onboard.Config) (onboard.Answer, bool) {
	if step != onboard.StepKind {
		if step == onboard.StepFolders && q.ID == onboard.QIDDefault {
			return boolAnswer(!hasDefault(cfg)), true
		}
		return onboard.Answer{}, false
	}
	switch q.ID {
	case onboard.QIDKind:
		return textAnswer(string(kind)), true
	case onboard.QIDName:
		if e.Name != "" {
			return textAnswer(e.Name), true
		}
		if _, taken := cfg.Setup(defaultSetupName); kind == onboard.KindSolo && !taken {
			return textAnswer(defaultSetupName), true
		}
	case onboard.QIDDefault:
		return boolAnswer(!hasDefault(cfg)), true
	}
	return onboard.Answer{}, false
}

// hasDefault reports whether some setup is the default one already.
func hasDefault(cfg onboard.Config) bool {
	return slices.ContainsFunc(cfg.Setups, func(s onboard.SetupEntry) bool { return s.Default })
}

// progressInOrder lists the rows in plan order (the Store does not
// promise an order); rows of steps outside the plan come last.
func progressInOrder(plan []onboard.Step, rows map[onboard.StepID]onboard.Progress) []onboard.Progress {
	out := make([]onboard.Progress, 0, len(rows))
	seen := map[onboard.StepID]bool{}
	for _, st := range plan {
		if p, ok := rows[st.ID]; ok {
			out = append(out, p)
			seen[st.ID] = true
		}
	}
	rest := make([]onboard.Progress, 0)
	for id, p := range rows {
		if !seen[id] {
			rest = append(rest, p)
		}
	}
	slices.SortFunc(rest, func(a, b onboard.Progress) int { return strings.Compare(string(a.Step), string(b.Step)) })
	return append(out, rest...)
}

// currentFixes is rows with each saved check's fix checked against the
// catalog this binary carries (issue 13): a saved row keeps the fix text it
// was saved with, so after an upgrade, or once a check's cause maps to
// another fix, it would show a fix this lore no longer gives until the
// person pressed Re-check. Such a fix becomes onboard.StaleFix (text only,
// never runnable); status, detail and checked_at stay as saved. No probe
// runs: opening a step stays as fast as reading the table.
func currentFixes(rows []onboard.Progress) []onboard.Progress {
	for i := range rows {
		rows[i].Checks = onboard.CurrentFixes(rows[i].Checks)
	}
	return rows
}

// planStep is the plan's step id, or a bare Step{ID} when the plan lacks
// it (a doctor-only check's home outside this kind's plan).
func (s stepState) planStep(id onboard.StepID) onboard.Step {
	for _, st := range s.plan {
		if st.ID == id {
			return st
		}
	}
	return onboard.Step{ID: id}
}

// onboardCatalog is the Catalog of logic/onboard (A6's catalog API).
type onboardCatalog struct{}

// inRepoPlan: the plan always lists this_folder; outside a repo its
// checks answer skip (the Prober knows the cwd, the op does not).
const inRepoPlan = true

func (onboardCatalog) Plan(kind onboard.Kind) []onboard.Step {
	return onboard.PlanFor(kind, inRepoPlan)
}

func (onboardCatalog) Check(id onboard.CheckID, step onboard.StepID, kind onboard.Kind, vars map[string]string) (onboard.Check, bool) {
	return onboard.CheckFor(id, step, onboard.LevelCtx{Kind: kind, Store: storeRole(step)}, onboard.FixVariant(vars[onboard.VarFixVariant]))
}

func (onboardCatalog) Validate(step onboard.Step, kind onboard.Kind, a onboard.Answers) []onboard.FieldError {
	return onboard.ValidateAnswers(step, kind, a)
}

func (onboardCatalog) FillFix(id onboard.CheckID, fix onboard.Fix, vars map[string]string, step onboard.Step) (onboard.Fix, error) {
	return onboard.FillFix(id, fix, vars, step)
}

func (onboardCatalog) KindChecks(kind onboard.Kind) []onboard.CheckID {
	out := make([]onboard.CheckID, 0, len(onboard.CheckIDs()))
	for _, id := range onboard.CheckIDs() {
		if slices.Contains(onboard.CheckKinds(id), kind) {
			out = append(out, id)
		}
	}
	return out
}

func (onboardCatalog) HomeStep(id onboard.CheckID, kind onboard.Kind) (onboard.StepID, bool) {
	return onboard.StepOfCheck(id, kind)
}

// storeRole is the store a step's store.* checks are about (ONB 4.4:
// the personal store; 4.5/4.6: the org store), which sets the level of
// store.write and store.private.
func storeRole(step onboard.StepID) onboard.StoreRole {
	switch step {
	case onboard.StepPersonalStore:
		return onboard.StoreRoleMe
	case onboard.StepJoin, onboard.StepTeam:
		return onboard.StoreRoleOrg
	}
	return onboard.StoreRoleNone
}
