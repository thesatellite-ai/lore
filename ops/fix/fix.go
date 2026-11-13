// Package fix defines `lore fix` (layer L5, decisions D-ak, D-al): one
// command brings a project up to date. It runs everything doctor can fix
// for the project, in order, printing each step (what, where, result) and
// the status after them: migrate run (v0.1.17 rows), render (stale
// LORE.md, tool files), agent hooks (what lore setup --agent-hooks
// writes), old store clones the doctor proved safe to remove, then the
// doctor checks' own repairs (git info/exclude, store clone identity, …)
// and the re-check. `lore doctor --fix` runs the same function
// (ops/doctor/fixproject.go), so the two never drift.
//
//	fix.run          ─▶ doctor.FixProject(dry) ─▶ Out{Repo, Steps, Status}
//	fix.run --all    ─▶ Env.FixRepos: every git repo the setups know (known
//	                    repos, the setup folders walked for repos with
//	                    .lore/); non-repos and repos a store link names
//	                    that hold no lore data noted, never run in
//	                 ─▶ Env.FixPrepare: (not dry) fetch each store clone once
//	                 ─▶ RunAll: Jobs repos at once (default 8), each in its
//	                    own lore process (Env.FixIn); each repo's block is
//	                    printed whole to Env.FixProgress (stderr) as soon as
//	                    it finishes, never interleaved; a failure does not
//	                    stop the others
//	                 ─▶ summary: N repos, ok / failed (named), wall time;
//	                    exit 1 when one failed
//
// --json (owner bug 14, choice recorded): stdout keeps one JSON document,
// the final result with every repo (repos[], in path order), and the text
// blocks stream on stderr as the repos finish, so a script reads one
// document and a person watching sees progress. In text mode stdout ends
// with the summary line only (the blocks are already on stderr).
//
// --dry-run reaches no remote: the CLI turns LORE_OFFLINE on for the
// process (app/fixoffline.go) before any store opens, and the children
// inherit it.
//
// It is idempotent: a second run changes nothing and says everything is
// current. A dry run changes nothing and lists what each step would do.
package fix

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/doctor"
)

// Operation names (`lore fix`, the bare noun runs fix.run).
const (
	Noun  = "fix"
	OpRun = "fix.run"
	// RunVerb is the verb bare `lore fix` runs.
	RunVerb = "run"
)

// restNoun keeps the REST collection "fix" un-pluralised.
const restNoun = "fix"

// Lines of the text output.
const (
	LineCurrent = "Everything is current."
	LineDryRun  = "Nothing changed (dry run)."
	// LineMigrateAll is the command that migrates every repo --all left on
	// an older format.
	LineMigrateAll = "lore fix --all --migrate"
	// LineStepsFailed starts the line of a run where a step could not run
	// (B1-5: "Everything is current." after a FAILED migrate read as a
	// clean repo); the failed steps follow.
	// LineNoRepos is --all's line when it found no repo: how it chooses
	// them and what to do.
	LineNoRepos     = "lore fix --all found no repo: it fixes the repo you run it in, the known repos (where lore ran before) and the repos with .lore/ in your setups' folders (`lore setup list`) and in the folders you name; name the folders holding your repos (lore fix --all --dirs ~/code --dirs ~/work), run `lore fix` inside each repo, or add the folder to a setup"
	LineStepsFailed = "Not everything is current: these steps failed, see their lines above: "
)

// Env is what fix.run needs; *wire.App implements it.
type Env interface {
	doctor.Env
	// RepoTop is the top folder of the repo the command runs in ("" or an
	// error outside one).
	RepoTop(ctx context.Context) (string, error)
	// FixRepos are the repo roots of --all, sorted, each once, and the
	// folders it passed over (not git repos), each with why.
	FixRepos(ctx context.Context, dirs []string) (Found, error)
	// FixPrepare runs once before the repos of --all: not dry, it fetches
	// every store clone once (jobs at a time) so the children need not.
	FixPrepare(ctx context.Context, dry bool, jobs int) Prep
	// FixIn runs lore fix in root in its own lore process.
	FixIn(ctx context.Context, root string, dry bool, p Prep) RepoResult
	// FixProgress is where --all prints each repo's block as it finishes
	// (nil: the blocks go in the final text instead).
	FixProgress() io.Writer
	// FixCacheSweep integrity-checks every cache file on the machine once,
	// after the repos of --all (each repo's own run checks only its own
	// caches), and returns a line per damaged or unreadable file.
	FixCacheSweep(ctx context.Context) []string
}

// FormatGate is the optional upgrade of Env that tells a project on an
// older lore format (D13): old is true with the one line saying so and the
// command that upgrades it (*wire.App implements it).
type FormatGate interface {
	FixFormatOld(ctx context.Context) (line string, old bool, err error)
}

// LoreDataGate is the optional upgrade of Env that tells a repo with no
// lore data (FP-7): none is true when the repo the command runs in holds
// no .lore folder, so lore fix has no project to repair and sets nothing
// up there (*wire.App implements it).
type LoreDataGate interface {
	FixNoLoreData(ctx context.Context) (none bool)
}

// noLoreDataErr is lore fix's answer in a repo with no lore data.
func noLoreDataErr(top string) error {
	return errs.New(errs.NotAStore, "lore fix: no lore data in "+top+" (no .lore folder), so there is nothing to fix and lore fix creates nothing there").
		WithHint("lore init sets this repo up; lore fix --all lists such repos and leaves them as they are")
}

// Found is what --all found to fix.
type Found struct {
	// Roots are the repo roots, sorted, each once.
	Roots []string
	// Skipped are folders that looked like a project but are no git repo
	// (a setup folder, the home holding ~/.lore), and repos a store link
	// names that hold no lore data (FP-7): a note each, never a failure
	// (owner item 26), never run in.
	Skipped []Skip
	// Note says how the repos were chosen when no setup has folders
	// (B4-8: a machine upgraded from lore 0.1.17), "" otherwise.
	Note string
}

// Skip is one folder --all passed over, and why.
type Skip struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// Line is the skip's printed line.
func (s Skip) Line() string { return "skipped " + s.Path + ": " + s.Why }

// Prep is what FixPrepare did.
type Prep struct {
	// NoFetch: every store clone was fetched just now, so the children run
	// with store clone fetches off (LORE_NO_STORE_FETCH=1); each repo's own
	// remote stays reachable to its child.
	NoFetch bool `json:"no_fetch,omitempty"`
	// Yes: the caller confirmed (lore fix --all --yes), so every child
	// runs with --yes (owner item 24); set by runAll, not by FixPrepare.
	Yes bool `json:"-"`
	// Migrate: lore fix --all --migrate, so every child runs with
	// --migrate (D15); set by runAll.
	Migrate bool `json:"-"`
	// Fetched counts the store clones fetched.
	Fetched int `json:"stores_fetched,omitempty"`
	// Failed names the store clones whose fetch failed (the children then
	// fetch for themselves).
	Failed []string `json:"stores_fetch_failed,omitempty"`
}

// DefaultJobs is how many repos --all runs at once.
const DefaultJobs = 8

// RunIn is the input of fix.run.
type RunIn struct {
	All    bool `json:"all,omitempty" flag:"all" desc:"fix every repo the setups know (their folders and the known repos), one block each; keeps going past a failure and exits 1 naming the failed repos"`
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"show what each step would do; change nothing and reach no remote (no fetch, push or clone)"`
	Jobs   int  `json:"jobs,omitempty" flag:"jobs" desc:"with --all: how many repos run at once (default 8; 1 runs them one after another)"`
	// Migrate (D15): lore fix never raises a project's format by itself;
	// with --migrate it runs `lore migrate run` as its migrate step (a
	// person's choice: teammates then need lore 0.1.18).
	Migrate bool `json:"migrate,omitempty" flag:"migrate" desc:"also migrate a project still on the lore 0.1.17 format (runs lore migrate run; teammates then need lore 0.1.18); without it such a project is refused, or listed by --all"`
	// Dirs (L-6): folders --all also looks through for repos, as it does a
	// setup's folders: a lore 0.1.17 repo outside the home folder that no
	// lore command ran in yet is found no other way.
	Dirs []string `json:"dirs,omitempty" arg:"0" rest:"true" desc:"with --all: folders to look through for repos too, as deep as a setup's folders (lore fix --all --dirs ~/code --dirs ~/work)"`
}

// RepoResult is one repo's block of --all.
type RepoResult struct {
	Repo   string           `json:"repo"`
	Status doctor.Health    `json:"status,omitempty"`
	Exit   int              `json:"exit_status"`
	Steps  []doctor.FixStep `json:"steps,omitempty"`
	// Error is why the run in this repo failed ("" when it ran).
	Error string `json:"error,omitempty"`
	// Text is the repo's own text output (not in JSON).
	Text string `json:"-"`
	// WallMS is how long the repo's run took.
	WallMS int64 `json:"wall_ms"`
	// Result is the repo's whole output (one-repo runs through FixIn).
	Result *Out `json:"-"`
	// FormatOld: the repo's project is on an older lore format and the
	// run was not asked to migrate (D15): Text is the one line and the
	// command that upgrades it.
	FormatOld bool `json:"format_old,omitempty"`
	// Retried: this is the second run, alone, of a repo whose first run
	// left a repair that could not fix beside the others (retryAlone).
	Retried bool `json:"retried,omitempty"`
}

// retriedHeader ends the block header of a second run (owner item 44:
// the old " (run again alone: …)" read as an instruction still to do,
// when lore had done it; this says it ran and that the block replaces
// the first).
const retriedHeader = " (second run, done alone after the other repos: the first run, beside them, left a repair unfinished; this block replaces it)"

// retriedStillHeader ends the header of a second run that still left a
// repair unfinished: running alone did not help, so the other repos were
// not the cause (the not fixed lines say what is).
const retriedStillHeader = " (second run, done alone after the other repos: a repair is still unfinished alone, so the other repos were not the cause; this block replaces the first)"

// lockCodes are the error codes of a write that lost a race with another
// lore process (a store, database or migration lock held, a busy wait
// that timed out): a step or run failing with one is run again alone.
var lockCodes = []errs.Code{errs.StoreLocked, errs.DBLocked, errs.BusyTimeout, errs.MigrateLocked}

// lostRace reports whether text names one of lockCodes.
func lostRace(text string) bool {
	for _, c := range lockCodes {
		if strings.Contains(text, string(c)) {
			return true
		}
	}
	return false
}

// NeedsAloneRun reports whether --all runs the repo again, alone, after
// the others: its doctor fixes step names a repair that could not fix,
// or a step or the whole run failed on a lock another repo's run held.
// A run that did not finish in time is not run again (it would take as
// long once more).
func (r RepoResult) NeedsAloneRun() bool {
	if doctor.RepairFailed(r.Steps) || lostRace(r.Error) {
		return true
	}
	for _, st := range r.Steps {
		if st.Failed && lostRace(st.Result) {
			return true
		}
	}
	return false
}

// Block is the repo's text block of --all: the repo, its own output (or
// the exact error of a run that failed), how long it took.
func (r RepoResult) Block() string {
	var b strings.Builder
	head := r.Repo
	if r.Retried {
		if r.NeedsAloneRun() {
			head += retriedStillHeader
		} else {
			head += retriedHeader
		}
	}
	fmt.Fprintf(&b, "== %s\n", head)
	switch {
	case r.Text != "":
		b.WriteString(r.Text)
		if !strings.HasSuffix(r.Text, "\n") {
			b.WriteString("\n")
		}
	case r.Error != "":
		b.WriteString("  FAILED: " + strings.ReplaceAll(strings.TrimSpace(r.Error), "\n", "\n          ") + "\n")
	default:
		fmt.Fprintf(&b, "  FAILED: lore fix exited %d without a result\n", r.Exit)
	}
	if r.WallMS > 0 {
		fmt.Fprintf(&b, "  (%s)\n", seconds(r.WallMS))
	}
	return b.String()
}

// seconds renders a duration in ms as seconds with one decimal.
func seconds(ms int64) string { return fmt.Sprintf("%.1fs", float64(ms)/1000) }

// Failed reports whether the repo counts as a failure of --all: the run
// failed, a step failed, or the repo is broken after it.
func (r RepoResult) Failed() bool {
	if r.FormatOld {
		return false // listed with its command, never a failure (D15)
	}
	return r.Error != "" || r.Status == doctor.StatusBroken || r.Exit > 1 /* above degraded: broken or a failed run */ || len(doctor.FailedSteps(r.Steps)) > 0
}

// Out is the output of fix.run.
type Out struct {
	DryRun bool `json:"dry_run,omitempty"`
	// Repo is the repo fixed (one-repo run).
	Repo  string           `json:"repo,omitempty"`
	Steps []doctor.FixStep `json:"steps,omitempty"`
	// Status is the doctor status after the steps (re-checked).
	Status doctor.Health `json:"status,omitempty"`
	// Warnings and Errors are what the re-check still finds (one repo);
	// with --all, Errors are the machine-wide cache sweep's lines.
	Warnings []string `json:"warnings,omitempty"`
	Errors   []string `json:"errors,omitempty"`
	// Setup and SetupNotes are the active setup's checks that are not ok
	// (one repo, not a dry run): the problems, and the notes that change
	// nothing in the status, as lore doctor lists them.
	Setup      []string `json:"setup,omitempty"`
	SetupNotes []string `json:"setup_notes,omitempty"`
	// Repos are --all's blocks, one per repo.
	Repos []RepoResult `json:"repos,omitempty"`
	// FormatOld names the repos on an older lore format that --all left
	// as they are (D15: migrated only with --migrate).
	FormatOld []string `json:"format_old,omitempty"`
	// Skipped are the folders --all passed over (not git repos, or no
	// lore data), noted.
	Skipped []Skip `json:"skipped,omitempty"`
	// Note says how --all chose the repos when no setup has folders.
	Note string `json:"note,omitempty"`
	// Failed names the repos (--all) or steps that failed.
	Failed []string `json:"failed,omitempty"`
	// OK counts the repos that did not fail (--all).
	OK int `json:"ok,omitempty"`
	// Jobs is how many repos ran at once (--all).
	Jobs int `json:"jobs,omitempty"`
	// WallMS is the whole run's wall time (--all).
	WallMS int64 `json:"wall_ms,omitempty"`
	// Prep is what ran before the repos (--all).
	Prep Prep `json:"prep,omitzero"`
	// Streamed: the blocks were printed as the repos finished, so the
	// final text is the summary only.
	Streamed bool `json:"-"`
	Exit     int  `json:"exit_status"`
}

// ExitStatus implements op.ExitStatuser: one repo exits as doctor does
// (0 healthy, 1 degraded, 2 broken); --all exits 1 when a repo failed.
func (o Out) ExitStatus() int { return o.Exit }

// Text implements op.Texter.
func (o Out) Text() string {
	var b strings.Builder
	if o.Repos != nil || o.Repo == "" {
		if !o.Streamed {
			for _, r := range o.Repos {
				b.WriteString(r.Block() + "\n")
			}
		}
		for _, sk := range o.Skipped {
			b.WriteString(sk.Line() + "\n")
		}
		for _, e := range o.Errors {
			b.WriteString("error: " + e + "\n")
		}
		b.WriteString(o.Summary() + "\n")
		return b.String()
	}
	b.WriteString("lore fix: " + o.Repo + "\n")
	b.WriteString(doctor.StepsText(o.Steps))
	failed := doctor.FailedSteps(o.Steps)
	switch {
	case len(failed) > 0:
		b.WriteString(LineStepsFailed + strings.Join(failed, ", ") + "\n")
		if o.DryRun {
			b.WriteString(LineDryRun + "\n")
		}
	case o.DryRun:
		b.WriteString(LineDryRun + "\n")
	case !doctor.Changed(o.Steps):
		b.WriteString(LineCurrent + "\n")
	}
	for _, e := range o.Errors {
		b.WriteString("error: " + e + "\n")
	}
	for _, w := range o.Warnings {
		b.WriteString("warning: " + w + "\n")
	}
	for _, w := range o.Setup {
		b.WriteString("warning: " + w + "\n")
	}
	for _, n := range o.SetupNotes {
		b.WriteString("note: " + n + "\n")
	}
	// owner item C2: what the person does next (the migrated files to
	// commit) is one line, last before the status
	for _, n := range doctor.NextLines(o.Steps) {
		b.WriteString(n + "\n")
	}
	if o.DryRun && o.Exit == 0 && o.Status != doctor.StatusHealthy && dryOnlyWouldDo(o) {
		fmt.Fprintf(&b, "Status: %s (each finding is one the real lore fix run fixes)\n", strings.ToUpper(string(o.Status)))
	} else {
		fmt.Fprintf(&b, "Status: %s\n", strings.ToUpper(string(o.Status)))
	}
	return b.String()
}

// Summary is --all's last line: repos, ok and failed counts, the failed
// repos, the wall time.
func (o Out) Summary() string {
	var b strings.Builder
	if o.Note != "" && len(o.Repos) > 0 {
		b.WriteString(o.Note + "\n")
	}
	if len(o.Repos) == 0 {
		// B4-8: say how repos are chosen, never a bare "0 repo(s) … none failed"
		b.WriteString(LineNoRepos + "\n")
	}
	fmt.Fprintf(&b, "lore fix --all: %d repo(s), %d ok, %d failed", len(o.Repos), o.OK, len(o.Failed))
	if len(o.Failed) > 0 {
		fmt.Fprintf(&b, ": %s", strings.Join(o.Failed, ", "))
	} else {
		b.WriteString(" (none failed)")
	}
	if len(o.FormatOld) > 0 {
		// D15: never migrated without --migrate; each block names its command
		fmt.Fprintf(&b, "; %d on the lore 0.1.17 format, not migrated (%s upgrades them all; teammates then need lore 0.1.18)", len(o.FormatOld), LineMigrateAll)
	}
	if len(o.Skipped) > 0 {
		fmt.Fprintf(&b, "; %d folder(s) skipped (see the skipped lines)", len(o.Skipped))
	}
	if len(o.Errors) > 0 {
		fmt.Fprintf(&b, "; %d cache error(s)", len(o.Errors))
	}
	fmt.Fprintf(&b, "; %s wall, %d at a time", seconds(o.WallMS), max(o.Jobs, 1))
	if o.DryRun {
		b.WriteString(", dry run (no remote reached)")
	} else if o.Prep.Fetched > 0 {
		fmt.Fprintf(&b, ", %d store clone(s) fetched once", o.Prep.Fetched)
	}
	return b.String()
}

// RunAll runs run for every root, at most jobs at once (jobs < 1: 1), and
// calls emit with each result as soon as it is in, one call at a time (a
// block is printed whole, never interleaved with another). The results
// come back in roots' order.
func RunAll(ctx context.Context, roots []string, jobs int, run func(ctx context.Context, root string) RepoResult, emit func(RepoResult)) []RepoResult {
	jobs = max(jobs, 1)
	out := make([]RepoResult, len(roots))
	var (
		emitMu sync.Mutex
		wg     sync.WaitGroup
	)
	slots := make(chan struct{}, jobs)
	for i, root := range roots {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			start := time.Now()
			r := run(ctx, root)
			if r.Repo == "" {
				r.Repo = root
			}
			if r.WallMS == 0 {
				r.WallMS = max(time.Since(start).Milliseconds(), 1)
			}
			out[i] = r
			if emit != nil {
				emitMu.Lock()
				emit(r)
				emitMu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// Ops returns the fix operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Bring this project up to date: render, agent hooks, old clones, every safe doctor repair (with --migrate, first migrate a project on the lore 0.1.17 format); prints each step and the status after them",
			Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			// a doctor repair may ask first (reversed --supersedes links)
			Confirm: op.ConfirmRule{MayAsk: true},
			Errors:  []errs.Code{errs.NotAStore, errs.InvalidInput, errs.FormatOld, errs.NeedsConfirm, errs.Internal},
			Examples: []op.Example{
				{Title: "fix this project", CLI: "lore fix", Want: "Status:"},
				{Title: "see what it would do", CLI: "lore fix --dry-run", Want: ""},
				{Title: "every repo on this machine", CLI: "lore fix --all", Want: ""},
				{Title: "every repo, one at a time", CLI: "lore fix --all --jobs 1", Want: ""},
				{Title: "also the repos under these folders", CLI: "lore fix --all --dirs ~/code --dirs ~/work", Want: ""},
				{Title: "also migrate a project on the lore 0.1.17 format", CLI: "lore fix --migrate", Want: ""},
			},
		}, func(ctx context.Context, in RunIn) (Out, error) {
			return Run(ctx, env, in)
		}),
	}
}

// Run is fix.run over env.
func Run(ctx context.Context, env Env, in RunIn) (Out, error) {
	if in.All {
		return runAll(ctx, env, in)
	}
	if len(in.Dirs) > 0 {
		return Out{}, errs.New(errs.InvalidInput, "lore fix takes folders only with --all").
			WithHint("lore fix --all --dirs " + strings.Join(in.Dirs, " "))
	}
	top, err := env.RepoTop(ctx)
	if err != nil || top == "" {
		return Out{}, errs.New(errs.NotAStore, "lore fix works on one project: not inside a git repo here").
			WithHint("run lore fix inside the repo, or lore fix --all for every repo the setups know")
	}
	// FP-7: a repo with no lore data is never set up by lore fix (its
	// open would create .lore/); refused before any step runs
	if g, ok := env.(LoreDataGate); ok && g.FixNoLoreData(ctx) {
		return Out{}, noLoreDataErr(top)
	}
	// D13, D15: a project on an older format is never written, and lore
	// fix migrates only when asked (--migrate)
	if in.Migrate {
		ctx = doctor.WithMigrate(ctx)
	} else if g, ok := env.(FormatGate); ok {
		if line, old, gerr := g.FixFormatOld(ctx); gerr == nil && old {
			return Out{}, errs.New(errs.FormatOld, line)
		}
	}
	r, err := doctor.FixProject(ctx, env, in.DryRun)
	if err != nil {
		return Out{}, err
	}
	out := Out{DryRun: in.DryRun, Repo: top, Steps: r.Steps, Status: r.Status, Warnings: r.Warnings, Errors: r.Errors,
		Failed: doctor.FailedSteps(r.Steps), Exit: r.Exit}
	out.Setup, out.SetupNotes = doctor.SetupLines(r)
	if len(out.Failed) > 0 && out.Exit == 0 {
		out.Exit = 1 // a step that could not run is not a healthy result
	}
	if in.DryRun && dryOnlyWouldDo(out) {
		out.Exit = 0 // B4-14: every finding is one the real run fixes
	}
	return out, nil
}

// dryRealRunChecks are the doctor checks whose findings a dry run leaves
// for the real run's steps (render rewrites stale copies).
var dryRealRunChecks = []string{"rendered_copy: "}

// dryOnlyWouldDo reports a dry run whose re-check found nothing but what
// the real run's steps do: no error, no failed step, and every warning of
// a check in dryRealRunChecks.
func dryOnlyWouldDo(o Out) bool {
	if len(o.Errors) > 0 || len(o.Failed) > 0 || o.Status == doctor.StatusBroken {
		return false
	}
	for _, w := range o.Warnings {
		if !slices.ContainsFunc(dryRealRunChecks, func(p string) bool { return strings.HasPrefix(w, p) }) {
			return false
		}
	}
	return true
}

// runAll is fix.run --all.
func runAll(ctx context.Context, env Env, in RunIn) (Out, error) {
	start := time.Now()
	jobs := in.Jobs
	if jobs <= 0 {
		jobs = DefaultJobs
	}
	found, err := env.FixRepos(ctx, in.Dirs)
	if err != nil {
		return Out{}, err
	}
	roots := found.Roots
	prep := env.FixPrepare(ctx, in.DryRun, jobs)
	prep.Yes = op.Confirmed(ctx) // --yes reaches each repo's own run
	prep.Migrate = in.Migrate    // D15: only --migrate migrates, in each repo
	out := Out{DryRun: in.DryRun, Jobs: jobs, Prep: prep, Skipped: found.Skipped, Note: found.Note}
	var emit func(RepoResult)
	if w := env.FixProgress(); w != nil {
		out.Streamed = true
		emit = func(r RepoResult) { _, _ = io.WriteString(w, r.Block()+"\n") }
	}
	out.Repos = RunAll(ctx, roots, jobs, func(ctx context.Context, root string) RepoResult {
		return env.FixIn(ctx, root, in.DryRun, prep)
	}, emit)
	if !in.DryRun && jobs > 1 {
		retryAlone(ctx, env, out.Repos, prep, emit)
	}
	for _, r := range out.Repos {
		switch {
		case r.FormatOld:
			out.FormatOld = append(out.FormatOld, r.Repo)
		case r.Failed():
			out.Failed = append(out.Failed, r.Repo)
		default:
			out.OK++
		}
	}
	// The machine-wide cache sweep runs once here, not in every child
	// (owner item 35 follow-up): read-only, so a dry run does it too.
	out.Errors = env.FixCacheSweep(ctx)
	if len(out.Failed) > 0 || len(out.Errors) > 0 {
		out.Exit = 1
	}
	out.WallMS = max(time.Since(start).Milliseconds(), 1)
	return out, nil
}

// retryAlone runs lore fix once more, one repo at a time, in every repo
// whose doctor fixes step names a repair that tried and could not fix, or
// whose step or run failed on a lock another repo held (NeedsAloneRun;
// owner items 38, 44: the actor_stable_key merges of repos sharing one org or
// personal store ran at the same time, each writing that store, and the
// ones that lost the store lock left the duplicate as it was). Alone, the
// repair has the stores to itself; the child fetches its store clones
// itself (they changed under the others' writes). The new block replaces
// the first and is printed too, marked as the second run.
func retryAlone(ctx context.Context, env Env, repos []RepoResult, prep Prep, emit func(RepoResult)) {
	again := prep
	again.NoFetch = false
	for i, r := range repos {
		if !r.NeedsAloneRun() {
			continue
		}
		start := time.Now()
		rr := env.FixIn(ctx, r.Repo, false, again)
		if rr.Repo == "" {
			rr.Repo = r.Repo
		}
		if rr.WallMS == 0 {
			rr.WallMS = max(time.Since(start).Milliseconds(), 1)
		}
		rr.Retried = true
		repos[i] = rr
		if emit != nil {
			emit(rr)
		}
	}
}
