package setup

// guided_test.go — the step flow of the guided-setup ops (ONB 2.3, 6.5,
// 7.1) over a fake Onboard env and a small fixed catalog, plus a smoke
// run over the real logic/onboard catalog.

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// ---------------------------------------------------------------------
// fakes

type fakeStore struct {
	cfg      onboard.Config
	progress map[string]map[onboard.StepID]onboard.Progress
	pinned   []string
	active   ActiveSetup
}

func newFakeStore(entries ...onboard.SetupEntry) *fakeStore {
	return &fakeStore{cfg: onboard.Config{Setups: entries}, progress: map[string]map[onboard.StepID]onboard.Progress{}}
}

func (f *fakeStore) Config(context.Context) (onboard.Config, error) { return f.cfg, nil }
func (f *fakeStore) PutSetup(_ context.Context, e onboard.SetupEntry) (bool, error) {
	if e.Default { // the Store contract: a default entry clears the others
		for i := range f.cfg.Setups {
			f.cfg.Setups[i].Default = false
		}
	}
	for i, s := range f.cfg.Setups {
		if s.Name == e.Name {
			f.cfg.Setups[i] = e
			return false, nil
		}
	}
	f.cfg.Setups = append(f.cfg.Setups, e)
	return true, nil
}
func (f *fakeStore) RemoveSetup(_ context.Context, name string) (bool, error) {
	for i, s := range f.cfg.Setups {
		if s.Name == name {
			f.cfg.Setups = slices.Delete(f.cfg.Setups, i, i+1)
			delete(f.progress, name)
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeStore) PinRepo(_ context.Context, name string) (string, error) {
	f.pinned = append(f.pinned, name)
	return "rep_1", nil
}
func (f *fakeStore) Active(context.Context, string) (ActiveSetup, error) { return f.active, nil }
func (f *fakeStore) Progress(_ context.Context, setup string) ([]onboard.Progress, error) {
	out := []onboard.Progress{}
	for _, p := range f.progress[setup] {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeStore) PutProgress(_ context.Context, p onboard.Progress) error {
	if f.progress[p.Setup] == nil {
		f.progress[p.Setup] = map[onboard.StepID]onboard.Progress{}
	}
	f.progress[p.Setup][p.Step] = p
	return nil
}
func (f *fakeStore) DeleteProgress(_ context.Context, setup string) error {
	delete(f.progress, setup)
	return nil
}
func (f *fakeStore) KnownRepos(context.Context) ([]onboard.KnownRepo, error) { return nil, nil }
func (f *fakeStore) TouchRepo(context.Context, onboard.KnownRepo) error      { return nil }

// fakeProber answers each check from results (ok when absent).
type fakeProber struct {
	results map[onboard.CheckID]ProbeResult
	calls   int
}

func (p *fakeProber) Probe(_ context.Context, reqs []ProbeRequest) []ProbeResult {
	p.calls++
	out := make([]ProbeResult, 0, len(reqs))
	for _, r := range reqs {
		res, ok := p.results[r.Check.ID]
		if !ok {
			res = ProbeResult{Status: onboard.StatusOK}
		}
		res.Check, res.Target = r.Check.ID, r.Target
		out = append(out, res)
	}
	return out
}
func (p *fakeProber) Prefill(context.Context, onboard.PrefillID, onboard.SetupEntry) (PrefillOut, error) {
	return PrefillOut{}, nil
}

// fakeApplier creates the entry on the kind step and reports Changed only
// when the answers differ from the last apply of that step.
type fakeApplier struct {
	store *fakeStore
	last  map[onboard.StepID]string
	calls []onboard.StepID
}

func (a *fakeApplier) Apply(ctx context.Context, r ApplyRequest) (ApplyResult, error) {
	a.calls = append(a.calls, r.Step)
	b, _ := r.Answers.MarshalText()
	if a.last[r.Step] == string(b) {
		return ApplyResult{Changed: false, Wrote: []string{}, Setup: r.Setup}, nil
	}
	a.last[r.Step] = string(b)
	if r.Step == onboard.StepKind {
		// As wire's ApplySetupEntry: the answers' name, kind and default;
		// a team kind without its org is written by the join / team step.
		e := r.Setup
		if n := r.Answers["name"].Text; n != "" {
			e.Name = n
		}
		if k := onboard.Kind(r.Answers["kind"].Text); k.Valid() {
			e.Kind = k
		}
		if d, ok := r.Answers["default"]; ok && d.IsBool {
			e.Default = d.Bool
		}
		if e.Kind.HasOrg() && e.Org == "" {
			return ApplyResult{Changed: false, Wrote: []string{}, Setup: e}, nil
		}
		if _, err := a.store.PutSetup(ctx, e); err != nil {
			return ApplyResult{}, err
		}
		r.Setup = e
	}
	return ApplyResult{Changed: true, Wrote: []string{"setups[" + r.Setup.Name + "]"}, Setup: r.Setup}, nil
}

type fakeRunner struct {
	ran  [][]string
	dirs []string
	// accounts: the gh login each fix ran as (FixGHAccountOf).
	accounts []string
}

func (r *fakeRunner) RunFix(ctx context.Context, argv []string) (FixRun, error) {
	return r.RunFixIn(ctx, "", argv)
}

func (r *fakeRunner) RunFixIn(ctx context.Context, dir string, argv []string) (FixRun, error) {
	r.accounts = append(r.accounts, FixGHAccountOf(ctx))
	r.ran = append(r.ran, argv)
	r.dirs = append(r.dirs, dir)
	return FixRun{Argv: argv}, nil
}

type fakeEnv struct {
	store   *fakeStore
	prober  *fakeProber
	applier *fakeApplier
	runner  *fakeRunner
}

func (e fakeEnv) SetupStore(context.Context) (Store, error)         { return e.store, nil }
func (e fakeEnv) SetupProber(context.Context) (Prober, error)       { return e.prober, nil }
func (e fakeEnv) SetupApplier(context.Context) (Applier, error)     { return e.applier, nil }
func (e fakeEnv) SetupFixRunner(context.Context) (FixRunner, error) { return e.runner, nil }

func newFakeEnv(entries ...onboard.SetupEntry) fakeEnv {
	st := newFakeStore(entries...)
	return fakeEnv{store: st, prober: &fakeProber{results: map[onboard.CheckID]ProbeResult{}},
		applier: &fakeApplier{store: st, last: map[onboard.StepID]string{}}, runner: &fakeRunner{}}
}

// fakeCatalog: kind → identity → (tools, optional) → github → summary.
type fakeCatalog struct{}

var fakeChecks = map[onboard.CheckID]onboard.Check{
	onboard.CheckSetupNameFree:       {ID: onboard.CheckSetupNameFree, Level: onboard.LevelError, Fix: onboard.Fix{Text: "pick another name"}},
	onboard.CheckIdentityEmailFormat: {ID: onboard.CheckIdentityEmailFormat, Level: onboard.LevelError, Fix: onboard.Fix{Text: "type a real address"}},
	onboard.CheckIdentityGitEmail:    {ID: onboard.CheckIdentityGitEmail, Level: onboard.LevelWarn, Fix: onboard.Fix{Text: "set git email"}},
	onboard.CheckToolsPointerFile: {ID: onboard.CheckToolsPointerFile, Level: onboard.LevelWarn,
		Fix: onboard.Fix{Text: "render {setup}", Command: []string{"lore", "render"}, Runnable: true}},
	onboard.CheckGitHubSSHUser: {ID: onboard.CheckGitHubSSHUser, Level: onboard.LevelError,
		Fix: onboard.Fix{Text: "grant {user}", Command: []string{"gh", "api", "{user}"}, Runnable: true, Outward: true}},
	onboard.CheckGitHubAliasConfig: {ID: onboard.CheckGitHubAliasConfig, Level: onboard.LevelWarn,
		Fix: onboard.Fix{Text: "edit {setup}", Command: []string{"lore", "setup", "edit", "{setup}", "--host-alias", "{alias}"}, Runnable: true}},
}

func (fakeCatalog) Plan(onboard.Kind) []onboard.Step {
	return []onboard.Step{
		{ID: onboard.StepKind, Checks: []onboard.CheckID{onboard.CheckSetupNameFree}, Apply: onboard.ApplySetupEntry},
		{ID: onboard.StepIdentity, Needs: []onboard.StepID{onboard.StepKind},
			Questions: []onboard.Question{{ID: "email", Type: onboard.QEmail, Required: true}},
			Checks:    []onboard.CheckID{onboard.CheckIdentityEmailFormat, onboard.CheckIdentityGitEmail}, Apply: onboard.ApplyIdentity},
		{ID: onboard.StepTools, Needs: []onboard.StepID{onboard.StepKind}, Optional: true,
			Questions: []onboard.Question{{ID: onboard.QIDTools, Type: onboard.QMulti}},
			Checks:    []onboard.CheckID{onboard.CheckToolsPointerFile}, Apply: onboard.ApplyTools},
		// this_folder (4.10): optional with no questions, so saving it
		// checks and marks it done (only the page's Skip skips it).
		{ID: onboard.StepThisFolder, Needs: []onboard.StepID{onboard.StepKind}, Optional: true},
		{ID: onboard.StepGitHub, Needs: []onboard.StepID{onboard.StepIdentity},
			Checks: []onboard.CheckID{onboard.CheckGitHubSSHUser, onboard.CheckGitHubAliasConfig}, Apply: onboard.ApplyGitHub},
		{ID: onboard.StepSummary, Needs: []onboard.StepID{onboard.StepIdentity}},
	}
}
func (fakeCatalog) Check(id onboard.CheckID, _ onboard.StepID, _ onboard.Kind, _ map[string]string) (onboard.Check, bool) {
	c, ok := fakeChecks[id]
	return c, ok
}
func (fakeCatalog) Validate(step onboard.Step, _ onboard.Kind, a onboard.Answers) []onboard.FieldError {
	var out []onboard.FieldError
	for _, q := range step.Questions {
		if _, ok := a[q.ID]; q.Required && !ok {
			out = append(out, onboard.FieldError{Question: q.ID, Rule: "required", Message: "is required"})
		}
	}
	return out
}
func (fakeCatalog) FillFix(id onboard.CheckID, f onboard.Fix, vars map[string]string, _ onboard.Step) (onboard.Fix, error) {
	fill := func(s string) string {
		for k, v := range vars {
			s = strings.ReplaceAll(s, "{"+k+"}", v)
		}
		return s
	}
	out := onboard.Fix{Text: fill(f.Text), Command: []string{}, Runnable: f.Runnable, Outward: f.Outward}
	for _, a := range f.Command {
		v := fill(a)
		if strings.HasPrefix(v, "-") && !strings.HasPrefix(a, "-") {
			return onboard.Fix{}, fixRefused(id, "leading dash")
		}
		out.Command = append(out.Command, v)
	}
	return out, nil
}
func (c fakeCatalog) KindChecks(k onboard.Kind) []onboard.CheckID {
	var out []onboard.CheckID
	for _, st := range c.Plan(k) {
		out = append(out, st.Checks...)
	}
	return out
}
func (c fakeCatalog) HomeStep(id onboard.CheckID, k onboard.Kind) (onboard.StepID, bool) {
	for _, st := range c.Plan(k) {
		if slices.Contains(st.Checks, id) {
			return st.ID, true
		}
	}
	return "", false
}

func newTestEngine(env fakeEnv) engine {
	e := newEngine(env, fakeCatalog{})
	e.now = func() time.Time { return time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC) }
	return e
}

func text(s string) onboard.Answer { return onboard.Answer{Text: s} }

var (
	kindAnswers  = onboard.Answers{"kind": text("solo")}
	emailAnswers = onboard.Answers{"email": text("mira@agency.example")}
	bg           = context.Background()
	yesCtx       = op.WithConfirmed(context.Background(), true)
)

// started is a setup "personal" whose kind step is done.
func started(t *testing.T) (fakeEnv, engine) {
	t.Helper()
	env := newFakeEnv()
	e := newTestEngine(env)
	if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "kind", Answers: kindAnswers}); err != nil {
		t.Fatalf("apply kind: %v", err)
	}
	return env, e
}

// ---------------------------------------------------------------------
// tests

// 2.3 / 7.2: an error-level fail refuses the write with E_SETUP_STEP_FAILED,
// the Applier is never called, and the progress row records "failed".
func TestApplyRefusedOnErrorLevelFail(t *testing.T) {
	env, e := started(t)
	env.prober.results[onboard.CheckIdentityEmailFormat] = ProbeResult{Status: onboard.StatusFail, Detail: "no @"}
	_, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: emailAnswers})
	if !errs.Has(err, errs.SetupStepFailed) {
		t.Fatalf("err = %v, want E_SETUP_STEP_FAILED", err)
	}
	if !strings.Contains(err.Error(), string(onboard.CheckIdentityEmailFormat)) {
		t.Errorf("message %q does not name the failing check", err)
	}
	// and says why (the check's detail), not only its id (round-3: a trashed
	// team's namesake was refused with just "org.name_free")
	if !strings.Contains(err.Error(), string(onboard.CheckIdentityEmailFormat)+" (no @)") {
		t.Errorf("message %q does not carry the failing check's detail", err)
	}
	if slices.Contains(env.applier.calls, onboard.StepIdentity) {
		t.Error("Applier ran for a step with a failing error-level check")
	}
	if got := env.store.progress["personal"][onboard.StepIdentity].Status; got != onboard.StepFailed {
		t.Errorf("progress status = %q, want failed", got)
	}
}

// Warn-level fails, warnings and probes that could not run never block.
func TestApplyNotBlockedByWarnOrProbeError(t *testing.T) {
	env, e := started(t)
	env.prober.results[onboard.CheckIdentityGitEmail] = ProbeResult{Status: onboard.StatusFail}
	env.prober.results[onboard.CheckIdentityEmailFormat] = ProbeResult{Status: onboard.StatusError, Detail: "timeout"}
	out, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: emailAnswers})
	if err != nil || out.Status != onboard.StepDone || !out.Changed {
		t.Fatalf("apply = %+v, %v; want done, changed", out, err)
	}
}

// Needs not done → E_SETUP_STEP_ORDER, nothing probed or written.
func TestApplyOrderRefused(t *testing.T) {
	env, e := started(t)
	probes := env.prober.calls
	_, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "github", Answers: onboard.Answers{}})
	if !errs.Has(err, errs.SetupStepOrder) || !strings.Contains(err.Error(), "needs identity") {
		t.Fatalf("err = %v, want E_SETUP_STEP_ORDER naming identity", err)
	}
	if env.prober.calls != probes || slices.Contains(env.applier.calls, onboard.StepGitHub) {
		t.Error("an out-of-order apply probed or wrote")
	}
}

// Field errors: check reports them and probes nothing; apply refuses.
func TestFieldErrorsProbeNothing(t *testing.T) {
	env, e := started(t)
	probes := env.prober.calls
	out, err := e.Check(bg, CheckIn{Setup: "personal", Step: "identity", Answers: onboard.Answers{}})
	if err != nil || len(out.FieldErrors) != 1 || out.Status != onboard.StepTodo || env.prober.calls != probes {
		t.Fatalf("check = %+v, %v; want one field error, todo, no probe", out, err)
	}
	if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: onboard.Answers{}}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("apply err = %v, want E_INVALID_INPUT", err)
	}
}

// Idempotent re-run: the second apply of the same answers is unchanged
// and still done.
func TestApplyRerunUnchanged(t *testing.T) {
	_, e := started(t)
	for i, want := range []bool{true, false} {
		out, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: emailAnswers})
		if err != nil || out.Changed != want || out.Status != onboard.StepDone {
			t.Fatalf("run %d: %+v, %v; want changed=%v done", i, out, err, want)
		}
	}
}

// Progress is kept per step (answers reused when apply sends none) and
// deleted by the summary once every step is done or skipped (4.11).
func TestProgressPersistedAndCleared(t *testing.T) {
	env, e := started(t)
	if _, err := e.Check(bg, CheckIn{Setup: "personal", Step: "identity", Answers: emailAnswers}); err != nil {
		t.Fatal(err)
	}
	p := env.store.progress["personal"][onboard.StepIdentity]
	if p.Status != onboard.StepReady || p.Answers["email"].Text != "mira@agency.example" || len(p.Checks) != 2 {
		t.Fatalf("progress after check = %+v", p)
	}
	// apply with no answers takes the saved ones
	if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity"}); err != nil {
		t.Fatal(err)
	}
	plan, err := e.Plan(bg, PlanIn{Setup: "personal"})
	if err != nil || len(plan.Progress) != 2 || plan.Progress[0].Step != onboard.StepKind {
		t.Fatalf("plan progress = %+v, %v", plan.Progress, err)
	}
	// summary before tools/github: saved, progress kept
	if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "summary"}); err != nil {
		t.Fatal(err)
	}
	if len(env.store.progress["personal"]) == 0 {
		t.Fatal("progress deleted while steps are still todo")
	}
	out, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "tools"}) // optional, no answers: skipped
	if err != nil || out.Status != onboard.StepSkipped {
		t.Fatalf("tools = %+v, %v; want skipped", out, err)
	}
	if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "github", Answers: onboard.Answers{"x": text("y")}}); err != nil {
		t.Fatal(err)
	}
	out, err = e.Apply(bg, ApplyIn{Setup: "personal", Step: string(onboard.StepThisFolder)}) // optional, no questions: done
	if err != nil || out.Status != onboard.StepDone {
		t.Fatalf("this_folder = %+v, %v; want done", out, err)
	}
	if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "summary"}); err != nil {
		t.Fatal(err)
	}
	if rows := env.store.progress["personal"]; len(rows) != 0 {
		t.Fatalf("progress after a complete summary = %v, want none", rows)
	}
	// a finished setup: re-applying a step is unchanged and adds no rows
	out, err = e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: emailAnswers})
	if err != nil || out.Changed || len(env.store.progress["personal"]) != 0 {
		t.Fatalf("re-apply on finished = %+v, %v, rows %d", out, err, len(env.store.progress["personal"]))
	}
}

// 6.5: repair maps failing checks to their steps; --yes runs only the
// runnable, non-outward fixes; --dry-run runs none and opens no page.
func TestRepairMapsAndRunsSafeFixes(t *testing.T) {
	ent := onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindSolo, GitHub: onboard.SetupGitHub{User: "mira", HostAlias: "github-acme"}}
	env := newFakeEnv(ent)
	e := newTestEngine(env)
	env.prober.results[onboard.CheckToolsPointerFile] = ProbeResult{Status: onboard.StatusFail}
	env.prober.results[onboard.CheckGitHubSSHUser] = ProbeResult{Status: onboard.StatusFail}
	env.prober.results[onboard.CheckGitHubAliasConfig] = ProbeResult{Status: onboard.StatusFail}

	dry, err := e.Repair(bg, RepairIn{Name: "acme-hub", Yes: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.runner.ran) != 0 || dry.Web != (WebStart{}) {
		t.Fatalf("dry run ran %v / web %+v", env.runner.ran, dry.Web)
	}
	steps := dry.Setups[0].Steps
	if len(steps) != 2 || steps[0].Step != onboard.StepTools || steps[1].Step != onboard.StepGitHub || len(steps[1].Checks) != 2 {
		t.Fatalf("steps = %+v; want tools then github (2 checks)", steps)
	}
	if dry.ExitStatus() != 1 {
		t.Errorf("exit = %d, want 1", dry.ExitStatus())
	}

	out, err := e.Repair(bg, RepairIn{Name: "acme-hub", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"lore", "render"}, {"lore", "setup", "edit", "acme-hub", "--host-alias", "github-acme"}}
	if len(env.runner.ran) != len(want) {
		t.Fatalf("ran %v, want %v (the outward gh fix must not run)", env.runner.ran, want)
	}
	for i := range want {
		if !slices.Equal(env.runner.ran[i], want[i]) {
			t.Fatalf("ran[%d] = %v, want %v", i, env.runner.ran[i], want[i])
		}
	}
	if out.Web.Setup != "acme-hub" {
		t.Errorf("web = %+v, want the page on acme-hub", out.Web)
	}

	// without --yes nothing runs
	env.runner.ran = nil
	if _, err := e.Repair(bg, RepairIn{Name: "acme-hub"}); err != nil || len(env.runner.ran) != 0 {
		t.Fatalf("repair without yes ran %v (%v)", env.runner.ran, err)
	}
	// --all lists only setups with a failure
	env.prober.results = map[onboard.CheckID]ProbeResult{}
	all, err := e.Repair(bg, RepairIn{All: true})
	if err != nil || len(all.Setups) != 0 || all.ExitStatus() != 0 {
		t.Fatalf("all healthy = %+v, %v", all, err)
	}
}

// setup.fix: confirm first (outward or not), a non-runnable or passing
// check is refused, a value that would be read as a flag is refused.
func TestFix(t *testing.T) {
	ent := onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindSolo, GitHub: onboard.SetupGitHub{User: "mira", HostAlias: "-oProxyCommand=x"}}
	env := newFakeEnv(ent)
	e := newTestEngine(env)
	in := FixIn{Setup: "acme-hub", Check: string(onboard.CheckToolsPointerFile)}
	if _, err := e.Fix(bg, in); !errs.Has(err, errs.FixRefused) {
		t.Fatalf("passing check: %v, want E_FIX_REFUSED", err)
	}
	env.prober.results[onboard.CheckToolsPointerFile] = ProbeResult{Status: onboard.StatusFail}
	if _, err := e.Fix(bg, in); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("no yes: %v, want E_NEEDS_CONFIRM", err)
	}
	if out, err := e.Fix(yesCtx, in); err != nil || !slices.Equal(out.Argv, []string{"lore", "render"}) {
		t.Fatalf("fix = %+v, %v", out, err)
	}
	env.prober.results[onboard.CheckGitHubSSHUser] = ProbeResult{Status: onboard.StatusFail}
	if _, err := e.Fix(bg, FixIn{Setup: "acme-hub", Check: string(onboard.CheckGitHubSSHUser)}); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("outward without yes: %v", err)
	}
	env.prober.results[onboard.CheckIdentityGitEmail] = ProbeResult{Status: onboard.StatusFail}
	if _, err := e.Fix(yesCtx, FixIn{Setup: "acme-hub", Check: string(onboard.CheckIdentityGitEmail)}); !errs.Has(err, errs.FixRefused) {
		t.Fatalf("not runnable: %v, want E_FIX_REFUSED", err)
	}
	env.prober.results[onboard.CheckGitHubAliasConfig] = ProbeResult{Status: onboard.StatusFail}
	if _, err := e.Fix(yesCtx, FixIn{Setup: "acme-hub", Check: string(onboard.CheckGitHubAliasConfig)}); !errs.Has(err, errs.FixRefused) {
		t.Fatalf("flag-shaped value: %v, want E_FIX_REFUSED", err)
	}
	if n := len(env.runner.ran); n != 1 {
		t.Fatalf("runner ran %d fixes, want 1", n)
	}
}

// checkArgv is the structural second layer under FillFix.
func TestCheckArgv(t *testing.T) {
	tmpl := []string{"lore", "setup", "edit", "{setup}", "--host-alias", "{alias}"}
	for _, c := range []struct {
		argv []string
		ok   bool
	}{
		{[]string{"lore", "setup", "edit", "acme-hub", "--host-alias", "github-acme"}, true},
		{[]string{"lore", "setup", "remove", "acme-hub", "--host-alias", "github-acme"}, false},
		{[]string{"lore", "setup", "edit", "acme-hub", "--host-alias", "{alias}"}, false},
		{[]string{"lore", "setup", "edit", "acme-hub", "--host-alias", "--upload-pack=x"}, false},
		{[]string{"lore", "setup", "edit", "acme-hub", "--host-alias", "a\nb"}, false},
		{[]string{"lore", "setup", "edit", "acme-hub"}, false},
	} {
		if _, ok := checkArgv(tmpl, c.argv); ok != c.ok {
			t.Errorf("checkArgv(%q) = %v, want %v", c.argv, ok, c.ok)
		}
	}
}

// use / edit / show / list.
func TestUseEditShowList(t *testing.T) {
	env := newFakeEnv(
		onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true},
		onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindSolo},
	)
	env.store.active = ActiveSetup{Name: "personal", Why: WhyDefault}
	e := newTestEngine(env)
	if out, err := e.Use(bg, UseIn{Name: "acme-hub"}); err != nil || out.Scope != UseScopeShell || out.Line != "export LORE_SETUP=acme-hub" {
		t.Fatalf("use shell = %+v, %v", out, err)
	}
	if out, err := e.Use(bg, UseIn{Name: "acme-hub", Here: true}); err != nil || out.Scope != UseScopeRepo || env.store.pinned[0] != "acme-hub" {
		t.Fatalf("use here = %+v, %v", out, err)
	}
	if _, err := e.Use(bg, UseIn{Clear: true}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("--clear without --here: %v", err)
	}
	if _, err := e.Use(bg, UseIn{Name: "nope"}); !errs.Has(err, errs.SetupNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	out, err := e.Edit(bg, EditIn{Name: "acme-hub", Default: true, HostAlias: "github-acme"})
	if err != nil || !out.Changed {
		t.Fatalf("edit = %+v, %v", out, err)
	}
	p, _ := env.store.cfg.Setup("personal")
	a, _ := env.store.cfg.Setup("acme-hub")
	if p.Default || !a.Default || a.GitHub.HostAlias != "github-acme" {
		t.Fatalf("after edit: personal %+v, acme-hub %+v", p, a)
	}
	if out, err := e.Edit(bg, EditIn{Name: "acme-hub", HostAlias: "github-acme"}); err != nil || out.Changed {
		t.Fatalf("same edit = %+v, %v; want unchanged", out, err)
	}
	if _, err := e.Edit(bg, EditIn{Name: "acme-hub", PersonalStore: "https://tok:x@github.example/mira/me.git"}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("credential url: %v", err)
	}
	if sh, err := e.Show(bg, ShowIn{Name: "acme-hub"}); err != nil || sh.Setup.Name != "acme-hub" {
		t.Fatalf("show = %+v, %v", sh, err)
	}
	if l, err := e.List(bg, ListIn{}); err != nil || len(l.Setups) != 2 || l.Active.Name != "personal" {
		t.Fatalf("list = %+v, %v", l, err)
	}
}

// The ops register cleanly (spec rules: Local + MayAsk only, codes known).
func TestGuidedOpsRegister(t *testing.T) {
	if _, err := op.NewRegistry(GuidedOps(newFakeEnv())...); err != nil {
		t.Fatal(err)
	}
}

// Smoke over the real catalog: the solo plan, and join parsing an invite.
func TestRealCatalogPlanAndJoin(t *testing.T) {
	env := newFakeEnv()
	e := newEngine(env, onboardCatalog{})
	plan, err := e.Plan(bg, PlanIn{Kind: "solo"})
	if err != nil || len(plan.Steps) == 0 || plan.Steps[0].ID != onboard.StepKind {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if _, err := e.Plan(bg, PlanIn{Kind: "boss"}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad kind: %v", err)
	}
	line, err := onboard.EncodeInvite(onboard.Invite{ID: "inv_01890a5d8a2b7000a000000000000101", Org: "acme-hub", Store: "git@github.com:acme/lore-team.git",
		OrgFolder: "org", Email: "tomas@acme.example", By: "mira@agency.example", Expires: "2026-11-20"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.Join(bg, JoinIn{Invite: "lore setup join --invite " + line, NoOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	if j.Invite.Org != "acme-hub" || j.Web.Setup != "acme-hub" || j.Web.Invite != line || !j.Web.NoOpen || len(j.Plan.Steps) == 0 {
		t.Fatalf("join = %+v", j)
	}
	if _, err := e.Join(bg, JoinIn{Invite: InvitePrefix + "not-base64"}); !errs.Has(err, errs.InviteInvalid) {
		t.Fatalf("bad invite: %v", err)
	}
	// a member an admin added joins by the org's store url (or the name
	// of an org entry here): no line; a name no entry has is refused as
	// an unknown org (a fresh machine), not as a bad invite (B2-1)
	if _, err := e.Join(bg, JoinIn{Invite: "acme-hub"}); !errs.Has(err, errs.JoinOrgUnknown) || !strings.Contains(err.Error(), "store url") || !strings.Contains(err.Error(), "on this machine") {
		t.Fatalf("join by an unknown org name: %v", err)
	}
	// text that is neither a name, a url nor an invite stays E_INVITE_INVALID
	if _, err := e.Join(bg, JoinIn{Invite: "not an org!"}); !errs.Has(err, errs.InviteInvalid) {
		t.Fatalf("join by garbage: %v", err)
	}
	if u, err := e.Join(bg, JoinIn{Invite: "git@github.com:acme/notes.git"}); err != nil || u.Invite.Store != "git@github.com:acme/notes.git" {
		t.Fatalf("join by store url = %+v, %v", u, err)
	}
}

// A repo check's fix runs in its target repo (lore render has no -C).
func TestFixRunsInTargetRepo(t *testing.T) {
	ent := onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindSolo}
	env := newFakeEnv(ent)
	e := newTestEngine(env)
	env.prober.results[onboard.CheckToolsPointerFile] = ProbeResult{Status: onboard.StatusFail}
	ctx := op.WithConfirmed(bg, true)
	if _, err := e.Fix(ctx, FixIn{Setup: "acme-hub", Check: string(onboard.CheckToolsPointerFile), Target: "claude_code " + absHub()}); err != nil {
		t.Fatal(err)
	}
	if len(env.runner.dirs) != 1 || env.runner.dirs[0] != absHub() {
		t.Fatalf("fix ran in %v, want %s (ran %v)", env.runner.dirs, absHub(), env.runner.ran)
	}
}

// ONB 4.1 / 7.2: the kind step refuses another setup's name; onto the
// setup being made (progress rows exist) it is a resume and is applied.
// The page's new-setup call (setup "") takes the name from the kind
// answer. A finished setup's own name is an edit (rename_test.go).
func TestApplyKindNameTaken(t *testing.T) {
	admin := onboard.SetupEntry{Name: "agency", Kind: onboard.KindAdmin, Org: "agency"}
	solo := onboard.Answers{"kind": text("solo"), "name": text("agency")}

	t.Run("another setup's name refused", func(t *testing.T) {
		env := newFakeEnv(admin)
		e := newTestEngine(env)
		_, err := e.Apply(bg, ApplyIn{Setup: "home", Step: "kind", Answers: solo})
		if !errs.Has(err, errs.SetupNameTaken) {
			t.Fatalf("err = %v, want E_SETUP_NAME_TAKEN", err)
		}
	})
	t.Run("resume allowed", func(t *testing.T) {
		env, e := started(t)
		if len(env.store.progress["personal"]) == 0 {
			t.Fatal("started setup has no progress")
		}
		again := onboard.Answers{"kind": text("solo"), "name": text("personal")}
		if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "kind", Answers: again}); err != nil {
			t.Fatalf("resumed kind step: %v", err)
		}
	})
	t.Run("new setup named by the answer", func(t *testing.T) {
		env := newFakeEnv(admin)
		e := newTestEngine(env)
		ans := onboard.Answers{"kind": text("solo"), "name": text("home")}
		if _, err := e.Apply(bg, ApplyIn{Step: "kind", Answers: ans}); err != nil {
			t.Fatalf("apply kind with setup \"\": %v", err)
		}
		if _, ok := env.store.cfg.Setup("home"); !ok {
			t.Error("setup home not written")
		}
	})
}

// 6.5 / A11 bug 6: repair of a saved setup never lists a create-only check
// (setup.name_free, org.name_free, org.free) — they would fail on the
// setup's own name or org entry. While the kind step is not saved yet
// the check still runs.
func TestRepairSkipsCreateOnlyChecksOfSavedSteps(t *testing.T) {
	env := newFakeEnv(onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindSolo})
	e := newTestEngine(env)
	env.prober.results[onboard.CheckSetupNameFree] = ProbeResult{Status: onboard.StatusFail, Detail: "setup acme-hub already exists"}
	out, err := e.Repair(bg, RepairIn{Name: "acme-hub", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Setups) != 1 || len(out.Setups[0].Steps) != 0 {
		t.Fatalf("repair of a finished setup listed %+v; want no step (create-only check)", out.Setups)
	}
	// a setup being made whose kind step is not done: the check still runs
	env.store.progress["acme-hub"] = map[onboard.StepID]onboard.Progress{
		onboard.StepIdentity: {Setup: "acme-hub", Step: onboard.StepIdentity, Status: onboard.StepFailed},
	}
	out, err = e.Repair(bg, RepairIn{Name: "acme-hub", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Setups) != 1 || len(out.Setups[0].Steps) == 0 || out.Setups[0].Steps[0].Step != onboard.StepKind {
		t.Fatalf("unsaved kind step: %+v; want the kind step listed", out.Setups)
	}
}

// A11 bug 7: setup repair prints readable text, never JSON in a table.
func TestRepairOutText(t *testing.T) {
	out := RepairOut{Setups: []RepairSetup{
		{Name: "agency", Steps: []RepairStep{{Step: onboard.StepGitHub, Checks: []onboard.CheckResult{
			{Check: onboard.CheckGitHubSSHUser, Status: onboard.StatusFail, Level: onboard.LevelError, Detail: "key answers as tomas-dev",
				Fix: onboard.Fix{Text: "use the key of mira-dev", Command: []string{"lore", "setup", "edit", "agency", "--host-alias", "github-agency"}}},
			{Check: onboard.CheckGitHubAliasConfig, Status: onboard.StatusFail, Level: onboard.LevelWarn},
		}}}},
		{Name: "personal", Steps: []RepairStep{}},
	}}
	want := "agency: 1 step to redo\n" +
		"  github\n" +
		"    ✗ github.ssh_user  key answers as tomas-dev\n" +
		"      fix: use the key of mira-dev\n" +
		"      run: lore setup edit agency --host-alias github-agency\n" +
		"    ⚠ github.alias_config\n" +
		"personal: nothing to repair\n"
	if got := out.Text(); got != want {
		t.Errorf("text =\n%s\nwant\n%s", got, want)
	}
	if got := (RepairOut{}).Text(); got != "nothing to repair\n" {
		t.Errorf("empty text = %q", got)
	}
}
