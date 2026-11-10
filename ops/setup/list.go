package setup

// list.go — setup.list and setup.show (ONBOARDING_DESIGN 7.1): read-only
// views of setups[] and the active setup (3.3).

import (
	"context"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// List is setup.list: every setup and which one is active here. A
// resolution error (E_SETUP_AMBIGUOUS, E_SETUP_ORG_MISMATCH) does not hide
// the list: Active is "none" with the error text as its detail, so
// `lore setup list` still shows what to fix.
//
// An invalid config.yaml (realmac #23) does not fail the list either:
// ConfigProblems names each problem with its line and fix, and when the
// file does not even decode the list is empty rather than an error, so
// Home shows the problem panel instead of a bare E_CONFIG_INVALID.
func (e engine) List(ctx context.Context, _ ListIn) (ListOut, error) {
	var problems []ConfigProblem
	if cc, ok := e.env.(ConfigChecker); ok {
		problems = cc.ConfigProblems(ctx)
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return ListOut{}, err
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		if len(problems) > 0 && errs.CodeOf(err) == errs.ConfigInvalid {
			return ListOut{Setups: []SetupSummary{}, Leftovers: []onboard.Leftover{}, Active: ActiveSetup{Why: WhyNone, Detail: err.Error()}, ConfigProblems: problems}, nil
		}
		return ListOut{}, err
	}
	out := ListOut{Setups: make([]SetupSummary, 0, len(cfg.Setups)), ConfigProblems: problems}
	hs, _ := e.env.(HealthSource)
	for _, s := range cfg.Setups {
		folders := s.Folders
		if folders == nil {
			folders = []string{}
		}
		out.Setups = append(out.Setups, SetupSummary{Name: s.Name, Kind: s.Kind, Org: s.Org, Default: s.Default, Folders: folders,
			Health: e.health(ctx, st, hs, s.Name), Disabled: s.Disabled, DisabledAt: s.DisabledAt, Note: s.Note,
			Unfinished: unfinishedSteps(s)})
	}
	out.Trashed = len(cfg.Trash)
	out.Leftovers = onboard.Leftovers(cfg)
	out.ConfigStyle = e.configStyle(ctx)
	if ps, perr := st.Progress(ctx, ""); perr == nil {
		// Context like Here: a progress read that fails hides no setup.
		out.Unsaved = unsavedSetups(cfg, ps)
	}
	act, err := st.Active(ctx, op.SetupOf(ctx))
	if err != nil {
		act = ActiveSetup{Why: WhyNone, Detail: err.Error()}
	}
	out.Active = act
	if src, ok := e.env.(HereSource); ok {
		// The folder line is context, not the list: a failure to read it
		// never hides the setups.
		if h, herr := src.SetupHere(ctx); herr == nil {
			out.Here = h
		}
	}
	return out, nil
}

// HereSource is an optional upgrade of Onboard: the current folder's
// context (onboard.ResolveHere over what wire reads about the folder).
// setup.list prints its one line; the setup page's session carries it.
type HereSource interface {
	SetupHere(ctx context.Context) (onboard.Here, error)
}

// Text implements op.Texter: the setups table, the active setup and the
// one line about the current folder (onboard.HereLine).
func (o ListOut) Text() string {
	var b strings.Builder
	for _, p := range o.ConfigProblems {
		at := p.File
		if p.Line > 0 {
			at += ":" + itoa(p.Line)
		}
		b.WriteString("config problem: " + at + ": " + p.Problem + "\n  fix: " + p.Fix + "\n")
	}
	b.WriteString("setups:\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = tw.Write([]byte("  NAME\tKIND\tORG\tDEFAULT\tFOLDERS\tHEALTH\n")) // a strings.Builder never fails
	for _, s := range o.Setups {
		def := "false"
		if s.Default {
			def = "true"
		}
		name := s.Name
		if s.Disabled {
			name += " (disabled)"
		}
		health := s.Health
		if health == "" {
			health = healthNotChecked
		}
		_, _ = tw.Write([]byte("  " + name + "\t" + string(s.Kind) + "\t" + s.Org + "\t" + def + "\t" + strings.Join(s.Folders, ", ") + "\t" + health + "\n"))
	}
	_ = tw.Flush()
	if len(o.Leftovers) > 0 {
		b.WriteString("leftovers in ~/.lore/config.yaml (no setup uses them; lore setup leftovers clear <kind>):\n")
		for _, l := range o.Leftovers {
			b.WriteString("  " + string(l.Kind) + "  " + l.Key + " = " + l.Value + "\n")
		}
	}
	for _, u := range o.Unsaved {
		b.WriteString("not saved yet: " + u.Name + " (" + string(u.Kind) + ", " + itoa(len(u.Done)) + " step(s) done")
		if u.Next != "" {
			b.WriteString(", next: " + string(u.Next))
		}
		b.WriteString("): lore setup --setup " + u.Name + " goes on where you left it\n")
	}
	if o.Trashed > 0 {
		b.WriteString("trash: " + itoa(o.Trashed) + " (lore setup trash list)\n")
	}
	b.WriteString("active: ")
	if o.Active.Name == "" {
		b.WriteString(WhyNone)
	} else {
		b.WriteString(o.Active.Name + " (" + o.Active.Why)
		if o.Active.Detail != "" {
			b.WriteString(": " + o.Active.Detail)
		}
		b.WriteString(")")
	}
	if o.Active.Name == "" && o.Active.Detail != "" {
		b.WriteString(" (" + o.Active.Detail + ")")
	}
	b.WriteString("\n")
	if o.Here.State != "" {
		b.WriteString(onboard.HereLine(o.Here) + "\n")
	}
	return b.String()
}

// Show is setup.show: the 3.2 entry (keys and paths only; setups[] never
// holds a secret).
func (e engine) Show(ctx context.Context, in ShowIn) (ShowOut, error) {
	name := setupName(ctx, in.Name)
	if err := requireName(name); err != nil {
		return ShowOut{}, err
	}
	ent, err := e.entry(ctx, name)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{Setup: ent}, nil
}

// entry is the named setups[] entry, E_SETUP_NOT_FOUND when absent.
func (e engine) entry(ctx context.Context, name string) (onboard.SetupEntry, error) {
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return onboard.SetupEntry{}, err
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return onboard.SetupEntry{}, err
	}
	ent, ok := cfg.Setup(name)
	if !ok {
		return onboard.SetupEntry{}, notFound(name, cfg)
	}
	return ent, nil
}

// HealthSource is an optional upgrade of Onboard: the check results a
// recent doctor run cached for a setup (ONB 6.1, the 60 s cache), so
// setup.list shows health without probing (setup.list stays instant: the
// Home screen calls it on every load). ok=false: nothing cached.
type HealthSource interface {
	SetupHealth(ctx context.Context, setup string) (results []onboard.CheckResult, ok bool)
}

// HealthRecorder is an optional upgrade of Onboard: where a setup's check
// results are kept once it is finished (wire: the health file doctor
// writes, read back by HealthSource). Finishing a setup deletes its
// progress rows (4.11), and with them the results every step's checks
// just produced; without this, the setup opened right after its Save
// showed "not checked" on every Summary row and Home said "not checked
// yet", though every check had run seconds before (owner report
// 2026-11-08). A step check of a finished setup's saved answers records
// through it too (check.go recordCheckHealth); without it that check only
// reports.
type HealthRecorder interface {
	RecordHealth(ctx context.Context, setup string, results []onboard.CheckResult) error
}

// Health cell symbols (ONB 5.4 Home: "✓ 14", "⚠ 1", "✗ 1"; 2.2 status
// marks; "?" a probe that could not run).
const (
	healthOK    = "✓"
	healthWarn  = "⚠"
	healthFail  = "✗"
	healthError = "?"
	// healthNotChecked is the text table's cell for a setup no check has
	// run for yet (a new one, or one restored or renamed through the
	// trash): said, never a blank cell that reads like "nothing wrong".
	// The JSON field stays empty (omitted) and the page shows its own
	// "not checked" cell; any check run (doctor, setup check, the page's
	// check) fills it.
	healthNotChecked = "not checked yet (lore doctor --all)"
)

// health is setup name's Health cell from the newer of two sources: the
// last doctor results the env recorded (HealthSource) and the last check
// results of its unfinished steps (setup_progress). The newer one wins, so
// a fresh doctor run and a later step re-check each show on Home (a
// finished setup has no progress rows: doctor only). "" when neither has
// results (never checked; `lore doctor --all` fills it). Nothing is probed.
func (e engine) health(ctx context.Context, st Store, hs HealthSource, name string) string {
	var doc []onboard.CheckResult
	if hs != nil {
		if rs, ok := hs.SetupHealth(ctx, name); ok {
			doc = rs
		}
	}
	var prog []onboard.CheckResult
	if rows, err := st.Progress(ctx, name); err == nil {
		for _, p := range rows {
			prog = append(prog, p.Checks...)
		}
	}
	if len(doc) > 0 && (len(prog) == 0 || !lastChecked(prog).After(lastChecked(doc))) {
		return HealthCell(doc)
	}
	return HealthCell(prog)
}

// lastChecked is the newest CheckedAt of rs (zero when empty).
func lastChecked(rs []onboard.CheckResult) time.Time {
	var t time.Time
	for _, r := range rs {
		if r.CheckedAt.After(t) {
			t = r.CheckedAt
		}
	}
	return t
}

// HealthCell is the 5.4 Home cell of results: the worst outcome and how
// many results have it — "✗ n" for failing error-level checks, else
// "⚠ n" for warnings and warn-level fails, else "? n" for probes that
// could not run, else "✓ n" passing checks; "" when there are none.
// Skipped checks are not counted.
func HealthCell(rs []onboard.CheckResult) string {
	var ok, warn, fail, errd int
	for _, r := range rs {
		switch {
		case r.Note(): // info: nothing to fix (as the page and the doctor count it)
			ok++
		case r.Status == onboard.StatusFail && r.Level == onboard.LevelError:
			fail++
		case r.Status == onboard.StatusFail, r.Status == onboard.StatusWarn:
			warn++
		case r.Status == onboard.StatusError:
			errd++
		case r.Status == onboard.StatusOK:
			ok++
		}
	}
	switch {
	case fail > 0:
		return healthFail + " " + itoa(fail)
	case warn > 0:
		return healthWarn + " " + itoa(warn)
	case errd > 0:
		return healthError + " " + itoa(errd)
	case ok > 0:
		return healthOK + " " + itoa(ok)
	}
	return ""
}

// ConfigProblem is one problem of config.yaml as Home shows it (polish item
// 4, realmac #23): the file (written with ~), the 1-based line (0 when the
// problem has no single line), the key path it is about ("identity.key";
// "" when unknown), what is wrong, and the fix in the person's words.
type ConfigProblem struct {
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Key     string `json:"key,omitempty"`
	Problem string `json:"problem"`
	Fix     string `json:"fix"`
}

// ConfigChecker is an optional upgrade of Onboard: config.yaml's problems
// (the ones `lore config check` reports), each located. setup.list returns
// them so Home names the file, line and fix once instead of every setup
// failing to open. nil: the file is valid (or absent).
type ConfigChecker interface {
	ConfigProblems(ctx context.Context) []ConfigProblem
}

// unfinishedSteps are the steps whose required value e lacks: GitHub when
// the entry has no account (an imported setup carries only identity and
// store, realmac #22). Order follows the steps.
func unfinishedSteps(e onboard.SetupEntry) []onboard.StepID {
	var out []onboard.StepID
	if strings.TrimSpace(e.GitHub.User) == "" {
		out = append(out, onboard.StepGitHub)
	}
	return out
}

// unsavedSetups are the setups with saved progress that config.yaml holds
// neither as a setup nor in the trash (a trashed setup keeps its rows for
// a restore): a member or admin setup left before its join or team step
// (putEntry writes a team entry only once its org exists). Sorted by name.
// Kind is the kind step's answer; Next is the first step of that kind's
// plan not saved yet (this_folder counted, as the page shows it in a repo).
func unsavedSetups(cfg onboard.Config, ps []onboard.Progress) []UnsavedSetup {
	known := map[string]bool{}
	for _, s := range cfg.Setups {
		known[s.Name] = true
	}
	for _, t := range cfg.Trash {
		known[t.Setup.Name] = true
	}
	by := map[string]*UnsavedSetup{}
	var names []string
	for _, p := range ps {
		if known[p.Setup] || p.Setup == "" {
			continue
		}
		u := by[p.Setup]
		if u == nil {
			u = &UnsavedSetup{Name: p.Setup, Done: []onboard.StepID{}}
			by[p.Setup] = u
			names = append(names, p.Setup)
		}
		if p.Step == onboard.StepKind {
			u.Kind = onboard.Kind(strings.TrimSpace(p.Answers[onboard.QIDKind].Text))
		}
		if p.Status == onboard.StepDone || p.Status == onboard.StepSkipped {
			u.Done = append(u.Done, p.Step)
		}
	}
	slices.Sort(names)
	out := make([]UnsavedSetup, 0, len(names))
	for _, n := range names {
		u := by[n]
		for _, st := range onboard.PlanFor(u.Kind, true) {
			if !slices.Contains(u.Done, st.ID) {
				u.Next = st.ID
				break
			}
		}
		out = append(out, *u)
	}
	return out
}
