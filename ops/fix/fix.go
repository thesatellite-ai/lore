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
//	fix.run --all    ─▶ Env.FixRepos: every repo the setups know
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
)

// Env is what fix.run needs; *wire.App implements it.
type Env interface {
	doctor.Env
	// RepoTop is the top folder of the repo the command runs in ("" or an
	// error outside one).
	RepoTop(ctx context.Context) (string, error)
	// FixRepos are the repo roots of --all, sorted, each once.
	FixRepos(ctx context.Context) ([]string, error)
	// FixPrepare runs once before the repos of --all: not dry, it fetches
	// every store clone once (jobs at a time) so the children need not.
	FixPrepare(ctx context.Context, dry bool, jobs int) Prep
	// FixIn runs lore fix in root in its own lore process.
	FixIn(ctx context.Context, root string, dry bool, p Prep) RepoResult
	// FixProgress is where --all prints each repo's block as it finishes
	// (nil: the blocks go in the final text instead).
	FixProgress() io.Writer
}

// Prep is what FixPrepare did.
type Prep struct {
	// NoFetch: every store clone was fetched just now, so the children run
	// with fetches off (LORE_NO_FETCH=1).
	NoFetch bool `json:"no_fetch,omitempty"`
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
}

// Block is the repo's text block of --all: the repo, its own output (or
// the exact error of a run that failed), how long it took.
func (r RepoResult) Block() string {
	var b strings.Builder
	fmt.Fprintf(&b, "== %s\n", r.Repo)
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
	// Warnings and Errors are what the re-check still finds.
	Warnings []string `json:"warnings,omitempty"`
	Errors   []string `json:"errors,omitempty"`
	// Repos are --all's blocks, one per repo.
	Repos []RepoResult `json:"repos,omitempty"`
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
		b.WriteString(o.Summary() + "\n")
		return b.String()
	}
	b.WriteString("lore fix: " + o.Repo + "\n")
	b.WriteString(doctor.StepsText(o.Steps))
	if o.DryRun {
		b.WriteString(LineDryRun + "\n")
	} else if !doctor.Changed(o.Steps) {
		b.WriteString(LineCurrent + "\n")
	}
	for _, e := range o.Errors {
		b.WriteString("error: " + e + "\n")
	}
	for _, w := range o.Warnings {
		b.WriteString("warning: " + w + "\n")
	}
	fmt.Fprintf(&b, "Status: %s\n", strings.ToUpper(string(o.Status)))
	return b.String()
}

// Summary is --all's last line: repos, ok and failed counts, the failed
// repos, the wall time.
func (o Out) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "lore fix --all: %d repo(s), %d ok, %d failed", len(o.Repos), o.OK, len(o.Failed))
	if len(o.Failed) > 0 {
		fmt.Fprintf(&b, ": %s", strings.Join(o.Failed, ", "))
	} else {
		b.WriteString(" (none failed)")
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
			Name: OpRun, Summary: "Bring this project up to date: migrate v0.1.17 rows, render, agent hooks, old clones, every safe doctor repair; prints each step and the status after them",
			Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			// a doctor repair may ask first (reversed --supersedes links)
			Confirm: op.ConfirmRule{MayAsk: true},
			Errors:  []errs.Code{errs.NotAStore, errs.NeedsConfirm, errs.Internal},
			Examples: []op.Example{
				{Title: "fix this project", CLI: "lore fix", Want: "Status:"},
				{Title: "see what it would do", CLI: "lore fix --dry-run", Want: ""},
				{Title: "every repo on this machine", CLI: "lore fix --all", Want: ""},
				{Title: "every repo, one at a time", CLI: "lore fix --all --jobs 1", Want: ""},
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
	top, err := env.RepoTop(ctx)
	if err != nil || top == "" {
		return Out{}, errs.New(errs.NotAStore, "lore fix works on one project: not inside a git repo here").
			WithHint("run lore fix inside the repo, or lore fix --all for every repo the setups know")
	}
	r, err := doctor.FixProject(ctx, env, in.DryRun)
	if err != nil {
		return Out{}, err
	}
	out := Out{DryRun: in.DryRun, Repo: top, Steps: r.Steps, Status: r.Status, Warnings: r.Warnings, Errors: r.Errors,
		Failed: doctor.FailedSteps(r.Steps), Exit: r.Exit}
	if len(out.Failed) > 0 && out.Exit == 0 {
		out.Exit = 1 // a step that could not run is not a healthy result
	}
	return out, nil
}

// runAll is fix.run --all.
func runAll(ctx context.Context, env Env, in RunIn) (Out, error) {
	start := time.Now()
	jobs := in.Jobs
	if jobs <= 0 {
		jobs = DefaultJobs
	}
	roots, err := env.FixRepos(ctx)
	if err != nil {
		return Out{}, err
	}
	prep := env.FixPrepare(ctx, in.DryRun, jobs)
	out := Out{DryRun: in.DryRun, Jobs: jobs, Prep: prep}
	var emit func(RepoResult)
	if w := env.FixProgress(); w != nil {
		out.Streamed = true
		emit = func(r RepoResult) { _, _ = io.WriteString(w, r.Block()+"\n") }
	}
	out.Repos = RunAll(ctx, roots, jobs, func(ctx context.Context, root string) RepoResult {
		return env.FixIn(ctx, root, in.DryRun, prep)
	}, emit)
	for _, r := range out.Repos {
		if r.Failed() {
			out.Failed = append(out.Failed, r.Repo)
		} else {
			out.OK++
		}
	}
	if len(out.Failed) > 0 {
		out.Exit = 1
	}
	out.WallMS = max(time.Since(start).Milliseconds(), 1)
	return out, nil
}
