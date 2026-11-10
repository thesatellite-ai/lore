package setup

// fix.go — fix templates and setup.fix (ONBOARDING_DESIGN 2.2 Fix, 5.3,
// 7.1): a check's fix is a text plus an argv template whose {name}
// placeholders are filled from the setup, the step's answers and the
// probe's Vars. setup.fix re-runs the check, rebuilds the argv from the
// template, re-validates it (E_FIX_REFUSED), asks (E_NEEDS_CONFIRM; a
// Runnable fix never runs without a yes, an Outward one always asks) and
// hands it to the FixRunner.

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// placeholder is one {name} in a fix template.
var placeholder = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)

// fixVars is the variable set of one result: the setup's own fields,
// then the answers (onboard.AnswersVars), then the probe's Vars, later
// winning. None of these ever holds a secret (Prober invariant; 3.2 keeps
// keys and paths only). Catalog.FillFix validates each value it uses.
func fixVars(s stepState, answers onboard.Answers, r ProbeResult) map[string]string {
	v := map[string]string{
		onboard.VarSetup: s.name, onboard.VarOrg: s.entry.Org,
		onboard.VarUser: s.entry.GitHub.User, onboard.VarAlias: s.entry.GitHub.HostAlias,
		onboard.VarSSHKey: s.entry.GitHub.SSHKey,
	}
	if len(s.entry.Identity.Emails) > 0 {
		v[onboard.VarEmail] = s.entry.Identity.Emails[0]
	}
	maps.Copy(v, onboard.AnswersVars(answers))
	maps.Copy(v, r.Vars)
	return v
}

// checkArgv re-validates a filled argv against its template (ONB 5.3: the
// page can send only a check id; the argv is rebuilt here and checked
// again before anything runs). Catalog.FillFix already checks each value
// against its rule; this is the structural second layer: the same number
// of parts, each literal element unchanged;
// each substituted element must be fully resolved, non-empty, free of
// control characters, and must not start with "-" unless the template
// element does (no flag injection through a value). The returned string
// is the 7.2 "{field} {why}".
func checkArgv(tmpl, argv []string) (string, bool) {
	if len(tmpl) == 0 {
		return "command is empty", false
	}
	if len(argv) != len(tmpl) {
		return "command has " + itoa(len(argv)) + " parts, the template " + itoa(len(tmpl)), false
	}
	for i, t := range tmpl {
		a := argv[i]
		if !placeholder.MatchString(t) {
			if a != t {
				return "part " + itoa(i) + " " + quote(a) + " differs from the template " + quote(t), false
			}
			continue
		}
		switch {
		case placeholder.MatchString(a):
			return "part " + itoa(i) + " " + quote(a) + " has no value", false
		case a == "":
			return "part " + itoa(i) + " is empty", false
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(t, "-"):
			return "part " + itoa(i) + " " + quote(a) + " would be read as a flag", false
		case strings.IndexFunc(a, unicode.IsControl) >= 0:
			return "part " + itoa(i) + " holds a control character", false
		}
	}
	return "", true
}

// fixRefused is E_FIX_REFUSED with the 7.2 message.
func fixRefused(check onboard.CheckID, why string) error {
	return errs.New(errs.FixRefused, "fix for "+string(check)+" refused: "+why).
		WithHint("run lore setup check to see the fix text and do it by hand")
}

// runnable rebuilds r's fix argv from the catalog template and
// re-validates it for running: the fix must be Runnable with a command,
// every filled value must pass Catalog.FillFix, and the result must pass
// checkArgv. It returns the fix to run.
func (e engine) runnable(s stepState, it probeItem, r onboard.CheckResult, vars map[string]string, choice string) (onboard.Fix, error) {
	c, ok := e.cat.Check(it.check, it.step.ID, s.kind, vars)
	if !ok {
		return onboard.Fix{}, fixRefused(it.check, "check is not in the catalog")
	}
	tmpl, err := pickCommand(c.ID, c.Fix, r, choice)
	if err != nil {
		return onboard.Fix{}, err
	}
	f, err := e.cat.FillFix(c.ID, c.Fix, vars, it.step)
	if err != nil {
		return onboard.Fix{}, err
	}
	if choice != "" {
		fc, _ := f.Choice(choice) // pickCommand found it in the template
		f.Command, f.Text = fc.Command, fc.Label
	}
	if why, ok := checkArgv(tmpl, f.Command); !ok {
		return onboard.Fix{}, fixRefused(c.ID, why)
	}
	return f, nil
}

// pickCommand is the argv template setup.fix runs for fix: its Command,
// or with choice set the command of that choice. A fix with choices and
// no command needs a choice; a choice the fix does not offer is refused.
func pickCommand(id onboard.CheckID, fix onboard.Fix, r onboard.CheckResult, choice string) ([]string, error) {
	ids := make([]string, 0, len(fix.Choices))
	for _, c := range fix.Choices {
		ids = append(ids, c.ID)
	}
	switch {
	case choice != "":
		c, ok := fix.Choice(choice)
		if !ok {
			if len(ids) == 0 {
				return nil, fixRefused(id, "fix offers no choices; run it without --choice")
			}
			return nil, fixRefused(id, "fix has no choice "+quote(choice)+"; pick one of "+strings.Join(ids, ", "))
		}
		return c.Command, nil
	case len(fix.Command) == 0 && len(ids) > 0:
		return nil, fixRefused(id, "fix offers a choice; rerun with --choice "+strings.Join(ids, " or --choice "))
	case !fix.Runnable || len(fix.Command) == 0:
		return nil, fixRefused(id, "fix is not runnable; do it by hand: "+r.Fix.Text)
	}
	return fix.Command, nil
}

// Fix is setup.fix: re-run the check now, rebuild and re-validate its fix
// argv, ask (always: a fix runs only after an explicit yes, 2.2), run it.
func (e engine) Fix(ctx context.Context, in FixIn) (FixOut, error) {
	name := setupName(ctx, in.Setup)
	if err := requireName(name); err != nil {
		return FixOut{}, err
	}
	id := onboard.CheckID(strings.TrimSpace(in.Check))
	if !slices.Contains(onboard.CheckIDs(), id) {
		return FixOut{}, errs.New(errs.InvalidInput, "check "+quote(in.Check)+" is not a known check id").
			WithHint("lore setup check lists the checks of each step")
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return FixOut{}, err
	}
	s, err := e.load(ctx, st, name, nil)
	if err != nil {
		return FixOut{}, err
	}
	// A name with neither an entry nor saved progress is a typo, not a
	// setup being made (its checks would save progress first): say so
	// instead of "nothing to fix".
	if !s.exists && len(s.progress) == 0 {
		cfg, err := st.Config(ctx)
		if err != nil {
			return FixOut{}, err
		}
		return FixOut{}, notFound(name, cfg)
	}
	items := e.fixSteps(s, id)
	if len(items) == 0 {
		return FixOut{}, errs.New(errs.InvalidInput, "check "+string(id)+" does not apply to a "+string(s.kind)+" setup").
			WithNext("lore", "setup", "plan", "--kind", string(s.kind))
	}
	answersOf := func(st onboard.StepID) onboard.Answers { return s.progress[st].Answers }
	ps, err := e.probeItems(ctx, s, items, answersOf, in.Target)
	if err != nil {
		return FixOut{}, err
	}
	p := fixPick(ps)
	it, raw, r := p.item, p.raw, p.res
	answers := answersOf(it.step.ID)
	if r.Status != onboard.StatusFail && r.Status != onboard.StatusWarn {
		return FixOut{}, fixRefused(id, "check is "+string(r.Status)+" now; nothing to fix")
	}
	choice := strings.TrimSpace(in.Choice)
	f, err := e.runnable(s, it, r, fixVars(s, answers, raw), choice)
	if err != nil {
		return FixOut{}, err
	}
	if !op.Confirmed(ctx) {
		q := "Run " + strings.Join(f.Command, " ") + "?"
		if f.Outward {
			q = "Run " + strings.Join(f.Command, " ") + "? It changes something on a remote service."
		}
		next := []string{"setup", "fix", name, "--check", string(id), "--yes"}
		if in.Target != "" {
			next = append(next, "--target", in.Target)
		}
		if choice != "" {
			next = append(next, "--choice", choice)
		}
		return FixOut{}, errs.New(errs.NeedsConfirm, f.Text+"\n"+q).WithQuestion(q).
			WithHint("rerun with --yes to run it").WithNext("lore", next...)
	}
	fr, err := e.env.SetupFixRunner(ctx)
	if err != nil {
		return FixOut{}, err
	}
	run, err := fr.RunFixIn(WithFixGHAccount(WithFixCheck(ctx, id), GHAccount(s.entry)), TargetRepo(r.Target), f.Command)
	if err != nil || run.Exit != 0 {
		return run, err
	}
	return e.recheck(ctx, s, it, answersOf, r.Target, run)
}

// stillFailingPrefix starts FixRun.StillFailing.
const stillFailingPrefix = "ran OK but the problem is still there: "

// recheck runs the fixed check again on the same target right after its
// fix exited 0 and records the outcome on run: a fix whose command
// succeeds while the check still fails (lore render for a tool missing
// from context.targets) is reported as exactly that, never as "finished".
// A re-check that cannot run leaves run as it is: the fix did run.
func (e engine) recheck(ctx context.Context, s stepState, it probeItem, answersOf func(onboard.StepID) onboard.Answers, target string, run FixRun) (FixRun, error) {
	ps, err := e.probeItems(ctx, s, []probeItem{it}, answersOf, target)
	if err != nil || len(ps) == 0 {
		return run, nil // the fix ran; a re-check that cannot run is not its error
	}
	after := fixPick(ps).res
	run.Recheck = after.Status
	if after.Status == onboard.StatusFail || after.Status == onboard.StatusWarn {
		why := strings.TrimSpace(after.Detail)
		if why == "" {
			why = string(after.Check)
		}
		run.StillFailing = stillFailingPrefix + why
	}
	return run, nil
}

// fixCheckKey carries the check whose fix a FixRunner runs.
type fixCheckKey struct{}

// WithFixCheck tags ctx with the check whose fix runs under it, so the
// FixRunner can label what it runs with that check id: the page's Details
// of a check row and the debug log list a "Run it" fix next to the
// check's own probes (the wire runner records it in the probe trace).
func WithFixCheck(ctx context.Context, id onboard.CheckID) context.Context {
	return context.WithValue(ctx, fixCheckKey{}, id)
}

// FixCheckOf is the check WithFixCheck set ("" = none).
func FixCheckOf(ctx context.Context) onboard.CheckID {
	id, _ := ctx.Value(fixCheckKey{}).(onboard.CheckID)
	return id
}

// fixGHAccountKey carries the gh login a FixRunner runs a gh fix as.
type fixGHAccountKey struct{}

// WithFixGHAccount tags ctx with the gh login a gh fix runs as ("" = gh's
// active account). The FixRunner hands that login's token to the gh child
// only, so `gh repo create you/lore-me` creates the repo as the setup's
// account even when gh has another login active (realmac item 9: it ran
// as the active account, which may not create a repo for another one).
func WithFixGHAccount(ctx context.Context, account string) context.Context {
	return context.WithValue(ctx, fixGHAccountKey{}, strings.TrimSpace(account))
}

// FixGHAccountOf is the login WithFixGHAccount set ("" = none).
func FixGHAccountOf(ctx context.Context) string {
	a, _ := ctx.Value(fixGHAccountKey{}).(string)
	return a
}

// GHAccount is the gh login lore asks GitHub as for setup: its recorded
// gh_account, else its GitHub user, "" when it has neither (then gh's
// active account answers). Every gh call made for a setup (store.private,
// a gh fix) goes as this login, never as whichever one gh has active.
func GHAccount(setup onboard.SetupEntry) string {
	if a := strings.TrimSpace(setup.GitHub.GHAccount); a != "" {
		return a
	}
	return strings.TrimSpace(setup.GitHub.User)
}

// fixSteps is every step of s's plan that runs check id, in plan order,
// else the check's home step (a doctor-only check). One check can belong
// to several steps (store.private: personal_store and team), each probing
// the store its own answers name, so setup.fix must not stop at the first.
func (e engine) fixSteps(s stepState, id onboard.CheckID) []probeItem {
	var out []probeItem
	for _, st := range s.plan {
		if slices.Contains(st.Checks, id) {
			out = append(out, probeItem{check: id, step: st})
		}
	}
	if len(out) == 0 {
		if home, ok := e.cat.HomeStep(id, s.kind); ok {
			out = append(out, probeItem{check: id, step: s.planStep(home)})
		}
	}
	return out
}

// fixPick is the result setup.fix acts on: the first step where the check
// fails, else the first where it warns, else the first (whose status the
// refusal names). ps is never empty.
func fixPick(ps []probed) probed {
	for _, want := range []onboard.Status{onboard.StatusFail, onboard.StatusWarn} {
		if i := slices.IndexFunc(ps, func(p probed) bool { return p.res.Status == want }); i >= 0 {
			return ps[i]
		}
	}
	return ps[0]
}

// quote is strconv.Quote, the house style for names in messages.
func quote(s string) string { return strconv.Quote(s) }

// itoa is strconv.Itoa.
func itoa(n int) string { return strconv.Itoa(n) }
