package wire

// fixproject.go — the steps of `lore fix` (and `lore doctor --fix`,
// decisions D-ak, D-al) that are not doctor checks, the --all fan-out, the
// fix `lore upgrade` runs after the binary step, and the v0.1.17 gate of
// every other command:
//
//	FixSteps(dry) ─▶ migrate  (v0.1.17 rows, an interrupted migration)
//	              ─▶ folder scopes (lore scope move for each folder scope
//	              │   whose folder git saw renamed: scopegone.go)
//	              ─▶ render   (a stale rendered copy, or rows just migrated)
//	              ─▶ agent hooks (lore setup --agent-hooks; not in a client
//	              │   repo, whose tracked files lore never changes)
//	              ─▶ old clones (only the ones doctor old_clones proved
//	                  everything is in the new clone)
//	              (then ops/doctor runs the checks' repairs and re-checks;
//	              when a repair changed rows after render, FixRenderAgain
//	              renders once more and the check runs again: owner item 43)
//
//	command in a project with v0.1.17 rows (oldRowsGate):
//	  read  ─▶ one stderr line "… run lore fix"; nothing changes
//	  write ─▶ migrate + render (the steps that are safe without asking),
//	           one stderr line saying so, then the write
//
// --all runs `lore fix --json` in each repo with this same binary, one
// process per repo: each repo opens its own stores and tool hosts exactly
// as a command typed there would, and one repo's failure cannot leave
// state behind for the next.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	clibuild "github.com/thesatellite-ai/lore/surface/cli"
)

// Lines of the v0.1.17 gate (owner item 11: exactly "run lore fix").
const (
	// OldRowsReadLine is what a read prints in a project with v0.1.17 rows.
	OldRowsReadLine = "lore: this project has rows written by lore 0.1.17; run lore fix"
	// oldRowsWriteLine is what the first write prints after fixing it.
	oldRowsWriteLine = "lore: this project had rows written by lore 0.1.17, so this write first ran lore fix's safe steps (%s); lore fix does the rest"
	// oldRowsWriteHint is the hint when that fix failed.
	oldRowsWriteHint = "run lore fix and read what it says"
)

// migrateNounPrefix starts every migrate operation's name.
var migrateNounPrefix = opsmigrate.OpRun[:strings.Index(opsmigrate.OpRun, ".")+1]

// fixSafeSteps marks a ctx running the safe steps of the first write (so
// the gate does not run again inside them).
type fixSafeStepsKey struct{}

// FixSteps runs (dry: plans) the steps of lore fix that are not doctor
// checks (opsdoctor.FixStepsEnv).
func (a *App) FixSteps(ctx context.Context, dry bool) []opsdoctor.FixStep {
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return []opsdoctor.FixStep{{Step: opsdoctor.StepMigrate, Result: "skipped: not inside a git repo"}}
	}
	mig := a.fixMigrate(ctx, top, dry, true)
	scopes := a.fixScopes(ctx, top, dry, mig.Failed)
	if !mig.Changed && !mig.Failed && !hasLoreProject(top) {
		// B1-7, B1-8: a repo --all reached by a store link, or one whose
		// .lore/ holds no project (only a LORE.md): nothing to render, and
		// lore never adds its hooks to the tool settings of a repo whose
		// team does not use lore (a teammate who pulls them gets a failing
		// hook in every session)
		rend := opsdoctor.FixStep{Step: opsdoctor.StepRender, Where: filepath.Join(top, filepath.FromSlash(opsrender.DefaultOut)), Result: skippedNoLoreProject}
		hooks := opsdoctor.FixStep{Step: opsdoctor.StepAgentHooks, Where: top, Result: skippedNoLoreProject}
		return []opsdoctor.FixStep{mig, scopes, rend, hooks, a.fixOldClones(ctx, dry)}
	}
	rend := a.fixRender(ctx, top, dry, mig.Changed && !mig.Failed)
	if mig.Failed {
		rend = renderAfterFailedMigrate(top)
	}
	steps := []opsdoctor.FixStep{mig, scopes, rend, a.fixAgentHooks(ctx, top, dry), a.fixOldClones(ctx, dry)}
	if !mig.Failed && !(dry && mig.Changed) {
		// B4-7: once migrate is current (a dry run that would migrate
		// cannot tell yet), lore 0.1.17's database and backups go
		steps = append(steps, a.fixOldFiles(ctx, top, dry))
	}
	return steps
}

// skippedNoLoreProject is the render and agent hooks steps' result in a
// repo with no lore project of its own.
const skippedNoLoreProject = "skipped: no lore project here (no .lore/data or " + attach.FileName + "), so lore leaves this repo's files as they are"

// hasLoreProject reports whether the repo at top has a lore project of its
// own: rows in .lore/data, or stores attached in .lore/stores.yaml.
func hasLoreProject(top string) bool {
	for _, p := range []string{filepath.Join(top, rowfs.LoreDir, rowfs.DataDir), filepath.Join(top, filepath.FromSlash(attach.FileName))} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// FixRenderAgain renders once more after the doctor fixes, when their
// re-check found a stale rendered copy (opsdoctor.FixRenderAgainEnv, owner
// item 43): a repair that changed rows (an actor merge, a --supersedes
// repair, a hand edit picked up) ran after the render step.
func (a *App) FixRenderAgain(ctx context.Context) opsdoctor.FixStep {
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return opsdoctor.FixStep{Step: opsdoctor.StepRender, Result: "skipped: not inside a git repo"}
	}
	st := a.fixRender(ctx, top, false, false)
	if st.Changed && !st.Failed {
		st.Result = renderAgainPrefix + st.Result
	}
	return st
}

// renderAgainPrefix starts the result of the render step run again after
// the doctor fixes.
const renderAgainPrefix = "again, a rendered copy was stale after the doctor fixes: "

// renderAfterFailedMigrate is the render step when the migrate step
// failed (owner item 22): the rows are still in the format lore 0.1.17
// wrote, which this lore does not read, so render would find no project
// (E_NOT_FOUND "this repository has no lore project") or render part of
// it. It is skipped, naming that cause; the next lore fix renders once
// migrate ran.
func renderAfterFailedMigrate(top string) opsdoctor.FixStep {
	return opsdoctor.FixStep{Step: opsdoctor.StepRender, Where: filepath.Join(top, filepath.FromSlash(opsrender.DefaultOut)),
		Result: renderSkippedMigrateFailed}
}

// renderSkippedMigrateFailed is the render step's result after a failed
// migrate step.
const renderSkippedMigrateFailed = "skipped: the migrate step failed, so this project's rows are still in the lore 0.1.17 format render cannot read; run lore fix again once migrate succeeds"

// fixMigrate is the migrate step: run when the repo has v1 rows, an
// interrupted migration or a v0.1.17 shared-database pointer.
func (a *App) fixMigrate(ctx context.Context, top string, dry, teamLock bool) opsdoctor.FixStep {
	st := opsdoctor.FixStep{Step: opsdoctor.StepMigrate, Where: filepath.Join(top, rowfs.LoreDir, rowfs.DataDir), Result: opsdoctor.ResultCurrent}
	// owner item 46: a migration lock a running (or another machine's)
	// lore holds fails the step now, not after Status opened and synced
	// the stores and read every row (minutes on a big repo); a lock whose
	// holder is gone is taken over by the run below
	if err := migrateLockBlocked(top); err != nil {
		if dryNeedsStores(dry, err) {
			st.Result = dryStoresLine(err)
			return st
		}
		st.Failed, st.Result = true, withHint(err)
		return st
	}
	status, err := a.Status(ctx)
	if err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	_, modeB := FindModeBPointer(top)
	// personal rows left in a 0.1.17 folder of the local personal store
	// after its flatten are a migrate step too (personal_store_flatten)
	orphans := false
	if mc, cerr := readMachineConfig(a.opts.Home); cerr == nil {
		orphans = mc.flattenedLocalPersonal(ctx, a.opts.Home, a.opts.rowfsOptions()...) != ""
	}
	if len(status.Pending) == 0 && !modeB && !orphans && !strings.HasPrefix(status.Journal, "interrupted") && len(a.v017Stores(ctx)) == 0 {
		return st
	}
	via := migrateViaFirstSave
	if teamLock {
		via = migrateViaFix
	}
	ctx = withMigrateVia(ctx, via)
	if teamLock {
		// lore fix takes the team lock on the signals ref, as `lore migrate
		// run` does; the first write's safe steps run without it, as any
		// write does (the lock needs a coordination identity a plain write
		// does not ask for)
		ctx = opsmigrate.WithTeamLock(ctx, a.fixTeamLock())
	}
	localBefore := ""
	if mc, cerr := readMachineConfig(a.opts.Home); cerr == nil {
		localBefore = mc.localPersonalV017(a.opts.Home)
	}
	out, err := opsmigrate.Run(ctx, a, opsmigrate.RunIn{DryRun: dry})
	if localBefore != "" && !dry {
		// personal_store_flatten switched personal.store to the folder's
		// root: the open state still has `me` at the old folder, so the
		// write after the first write's safe steps would land there (rows
		// nothing reads any more) or fail on the tombstones; open again
		if rerr := a.reopenState(); rerr != nil && err == nil {
			err = rerr
		}
	}
	if modeB && !dry {
		// owner item 36: the conversion attached the "shared" store in
		// stores.yaml, which this App's open state does not have; the steps
		// after it (render, the doctor) must see the project there
		if rerr := a.reopenState(); rerr != nil && err == nil {
			err = rerr
		}
	}
	if err != nil && dry && errs.Has(err, errs.LinkUnknown) {
		// B4-14: a dry run reaches no remote, so a store this lore never
		// fetched (the first command after upgrading from 0.1.17) cannot
		// answer whether this repo is a client repo; the real run fetches
		// it first. Not a failure: say what the real run would do, from
		// the rows on disk.
		st.Changed, st.Result = true, "would migrate (the real run fetches the stores first; this dry run reached no remote, so it could not check this repo's store links)"
		st.Detail = append(st.Detail, status.Pending...)
		return st
	}
	if err != nil {
		if dryNeedsStores(dry, err) {
			st.Result = dryStoresLine(err)
			return st
		}
		st.Failed, st.Result = true, err.Error()
		if errs.Has(err, errs.MigrateLocked) {
			st.Result = withHint(err) // what it waits for, or how to clear it
		}
		return st
	}
	// owner item C2: the commit line is the step's Next (lore fix prints it
	// last), the full row list its Rows (--json); the text names a count
	// and a few ids per table
	if out.Uncommitted > 0 && !dry {
		st.Next = opsmigrate.CommitLine(out.Uncommitted)
	}
	st.Rows = out.Rows
	text := out
	text.Uncommitted = 0
	lines := strings.Split(strings.TrimRight(text.Text(), "\n"), "\n")
	st.Changed, st.Result = true, strings.TrimSpace(lines[0])
	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); l != "" {
			st.Detail = append(st.Detail, l)
		}
	}
	return st
}

// withHint is err's text and, for a typed error with one, "; " and its
// hint (a fix step's result is one line a person reads, with no separate
// hint line).
func withHint(err error) string {
	var e *errs.Error
	if errors.As(err, &e) && e.Hint != "" {
		return err.Error() + "; " + e.Hint
	}
	return err.Error()
}

// passLockout prints the 0.1.17 lockout block a repo's run printed
// (SayOldLoreLockout) on this process's stderr: a child's stderr is kept
// only for its error, and the block must be seen wherever a store's
// layout commit is made, `lore fix --all` included.
func passLockout(stderr string) {
	first, second := OldLoreLockoutLines[0], OldLoreLockoutLines[1]
	head := first[:strings.Index(first, "%s")]
	tail := first[strings.Index(first, "%s")+2:]
	for line := range strings.SplitSeq(stderr, "\n") {
		if (strings.HasPrefix(line, head) && strings.Contains(line, tail)) || line == second {
			_, _ = fmt.Fprintln(os.Stderr, line)
		}
	}
}

// v017Stores names the stores this repo uses (stores.yaml, the personal
// store, the org's stores) whose marker is still v0.1.17's (lore_store: 1)
// and that this person may change. A repo whose only v0.1.17 data sits in
// such a store has no v1 row of its own, so without this lore fix said
// "current" and the store was never converted nor moved to the new
// layout. A store that cannot be opened here is left to the migration,
// which names it.
func (a *App) v017Stores(ctx context.Context) []string {
	var out []string
	seen := map[string]bool{}
	check := func(name string, s attach.Store) {
		dir, err := a.storeDir(ctx, s)
		if err != nil || seen[dir] {
			return
		}
		seen[dir] = true
		if m, merr := rowfs.ReadMarker(dir); merr == nil && m.Version < rowfs.MarkerFirstStores {
			out = append(out, name)
		}
	}
	if list, err := a.ListStores(ctx); err == nil {
		for _, e := range list {
			if !e.ReadOnly {
				check(e.Name, attach.Store{URL: e.URL, Branch: e.Branch})
			}
		}
	}
	for _, s := range a.orgStoresHere(ctx) {
		check(string(s.Name), s)
	}
	return out
}

// renderSkippedNoProject starts the render step's result when the repo
// has no project to render (owner item 36): skipped with the reason, never
// a failed repo.
const renderSkippedNoProject = "skipped: nothing to render: "

// noProjectToRender reports whether err is Project's "no lore project".
func noProjectToRender(err error) bool {
	return errs.Has(err, errs.NotFound) && strings.Contains(err.Error(), noLoreProject)
}

// dryNeedsStores reports a dry run's migrate step stopped only because a
// store is not on this machine yet: a dry run clones and moves nothing
// (B4-14), so the stores the real run would read are missing, which is
// not a failure of the migration.
func dryNeedsStores(dry bool, err error) bool {
	return dry && (errs.Has(err, errs.StoreUnreachable) || errs.Has(err, errs.LinkUnknown))
}

// dryStoresLine is the dry run's migrate line when the stores are missing.
func dryStoresLine(err error) string {
	return "would migrate once the stores are on this machine (a dry run clones and moves none; the next command that is not a dry run does): " + withHint(err)
}

// reopenState closes the App's open state so the next call opens it
// again from what is on disk now (a store a step attached in stores.yaml).
func (a *App) reopenState() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var cerr error
	if a.st != nil {
		cerr = a.st.close()
	}
	a.st, a.opened, a.err = nil, false, nil
	return cerr
}

// fixRender is the render step: run when a rendered copy is stale, or the
// migrate step just rewrote rows (the copies follow the rows).
func (a *App) fixRender(ctx context.Context, top string, dry, migrated bool) opsdoctor.FixStep {
	st := opsdoctor.FixStep{Step: opsdoctor.StepRender, Where: filepath.Join(top, filepath.FromSlash(opsrender.DefaultOut)), Result: opsdoctor.ResultCurrent}
	h := a.opts.Context.Render
	if h == nil {
		st.Result = "skipped: this build does not render"
		return st
	}
	stale, err := a.DoctorRenderedCopies(ctx)
	if noProjectToRender(err) {
		st.Result = renderSkippedNoProject + err.Error()
		return st
	}
	if err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	_, statErr := os.Stat(st.Where)
	rendered := statErr == nil
	if len(stale) == 0 && !(migrated && rendered) {
		return st
	}
	st.Changed = true
	if dry {
		st.Result = "would render"
		for _, f := range stale {
			st.Detail = append(st.Detail, f.Line())
		}
		return st
	}
	ans, err := h.Render(ctx, opsrender.RunIn{})
	if noProjectToRender(err) {
		st.Changed, st.Result = false, renderSkippedNoProject+err.Error()
		return st
	}
	if err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	// B1-9, B4-6: the step reports what render wrote, never the findings
	// from before it ran (they read as still wrong right after "rendered")
	st.Result = "rendered"
	lines := strings.Split(strings.TrimRight(ans.Text, "\n"), "\n")
	if first := strings.TrimSpace(lines[0]); first != "" {
		st.Result += ": " + first
	}
	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, PointerSkippedMark) {
			st.Detail = append(st.Detail, l)
		}
	}
	// A pointer file render left as it is (a symbolic link it does not
	// write through) is named in the step: "skipped: <file>: <why>".
	for _, l := range strings.Split(ans.Text, "\n") {
		if i := strings.Index(l, PointerSkippedMark); i >= 0 {
			file := strings.TrimSpace(strings.TrimLeft(l[:i], "⚠ "))
			st.Detail = append(st.Detail, "skipped: "+file+": "+l[i+len(PointerSkippedMark):])
		}
	}
	return st
}

// fixAgentHooks is the agent hooks step: what lore setup --agent-hooks
// writes, for the tools set up here.
func (a *App) fixAgentHooks(ctx context.Context, top string, dry bool) opsdoctor.FixStep {
	st := opsdoctor.FixStep{Step: opsdoctor.StepAgentHooks, Where: top, Result: opsdoctor.ResultCurrent}
	client, _ := a.ClientRepo(ctx)
	if note, _ := a.RepoClassNote(ctx); client && dry && strings.Contains(note, string(errs.LinkUnknown)) {
		// B4-14: see fixMigrate; the real run fetches the stores and then
		// knows whether this is a client repo
		st.Result = "would check once the stores are fetched (this dry run reached no remote): an own repo gets the hooks, a client repo none"
		return st
	}
	if client {
		st.Result = "skipped: a client repo (lore never changes its tracked files)"
		return st
	}
	h, err := a.SetupHost(ctx)
	if err != nil {
		st.Result = "skipped: this build has no tool setup"
		return st
	}
	plans, err := h.PlanAgentHooks(ctx, false, false)
	if err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	// A plan is one hook; a settings file holds several (B1-6: two files
	// were "4 settings file(s)", each listed twice): one detail line per
	// file, naming the hook events it gets.
	var change []opssetup.HookPlan
	var files []string
	events := map[string][]string{}
	for _, p := range plans {
		if !p.Change {
			continue
		}
		change = append(change, p)
		key := p.Tool + ": " + p.File
		if _, seen := events[key]; !seen {
			files = append(files, key)
		}
		if ev := p.Hook.Event; ev != "" && !slices.Contains(events[key], ev) {
			events[key] = append(events[key], ev)
		}
	}
	if len(change) == 0 {
		return st
	}
	for _, f := range files {
		line := f
		if len(events[f]) > 0 {
			line += " (" + strings.Join(events[f], ", ") + ")"
		}
		st.Detail = append(st.Detail, line)
	}
	st.Changed = true
	if dry {
		st.Result = fmt.Sprintf("would write %d settings file(s)", len(files))
		return st
	}
	if err := h.WriteAgentHooks(ctx, change, false); err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	st.Result = fmt.Sprintf("wrote %d settings file(s)", len(files))
	return st
}

// fixOldClones is the old clones step: remove the old store clones whose
// every commit and change the doctor proved is in the new clone. Half
// clones and old org link clones stay (the doctor names them).
func (a *App) fixOldClones(ctx context.Context, dry bool) opsdoctor.FixStep {
	st := opsdoctor.FixStep{Step: opsdoctor.StepOldClones, Where: filepath.Join(a.opts.Home, "clones"), Result: opsdoctor.ResultCurrent}
	found, err := git.ScanOldClones(a.opts.Home)
	if err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	n := 0
	for _, o := range found {
		if !o.Whole {
			continue
		}
		if _, ok, _ := git.MovedSafely(ctx, a.opts.Home, o); !ok {
			continue
		}
		n++
		st.Detail = append(st.Detail, o.Paths()...) // B4-20: every folder it removes (the bare .git too)
		if dry {
			continue
		}
		for _, p := range o.Paths() {
			if err := os.RemoveAll(p); err != nil {
				st.Failed, st.Result = true, "remove "+p+": "+err.Error()
				return st
			}
		}
	}
	// B4-20: the <host>/<owner>/ folders the clones sat in, and clones/
	// itself, stay behind empty; they go too (only empty folders)
	empty := 0
	if !dry {
		empty = removeEmptyDirs(st.Where)
	}
	if n == 0 {
		if empty > 0 {
			st.Changed, st.Result = true, fmt.Sprintf("removed %d empty folder(s) old clones left", empty)
		}
		return st
	}
	st.Changed = true
	st.Result = fmt.Sprintf("removed %d old clone(s), everything in them is in the new clones", n)
	if empty > 0 {
		st.Result += fmt.Sprintf(", and %d empty folder(s) they left", empty)
	}
	if dry {
		st.Result = fmt.Sprintf("would remove %d old clone(s)", n)
	}
	return st
}

// FixProgress is where --all prints each repo's block as it finishes:
// stderr, so stdout stays the one result (opsfix.Env).
func (a *App) FixProgress() io.Writer { return fixProgress }

// fixProgress is FixProgress's writer (a test swaps it).
var fixProgress io.Writer = os.Stderr

// FixPrepare fetches every whole store clone under ~/.lore/stores once,
// jobs at a time, before --all's repos run (opsfix.Env, owner bug 14):
// forty repos sharing one store then fetch it once, not forty times. When
// every fetch answered, the children run with LORE_NO_STORE_FETCH=1 (their
// own repo's remote stays reachable); when one
// failed, they fetch for themselves as before. A dry run fetches nothing
// (LORE_OFFLINE is on for the whole process tree).
func (a *App) FixPrepare(ctx context.Context, dry bool, jobs int) opsfix.Prep {
	var p opsfix.Prep
	if dry || gitenv.NoStoreFetch(os.Environ()) {
		return p
	}
	found, err := git.ScanClones(a.opts.Home)
	if err != nil {
		return p
	}
	var dirs []string
	for _, f := range found {
		if f.State == git.CloneWhole && f.Origin != "" {
			dirs = append(dirs, f.Paths.Dir)
		}
	}
	var mu sync.Mutex
	opsfix.RunAll(ctx, dirs, jobs, func(ctx context.Context, dir string) opsfix.RepoResult {
		err := git.RefreshClone(ctx, dir, git.WithRoute(func(ctx context.Context, remote string) ([]string, []string) {
			return cloneRoute(ctx, a.opts.Home, a.projectDir(), remote)
		}))
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			p.Failed = append(p.Failed, dir)
		} else {
			p.Fetched++
		}
		return opsfix.RepoResult{Repo: dir}
	}, nil)
	slices.Sort(p.Failed)
	p.NoFetch = len(p.Failed) == 0
	return p
}

// FixIn runs lore fix in root in its own process (opsfix.Env).
func (a *App) FixIn(ctx context.Context, root string, dry bool, p opsfix.Prep) opsfix.RepoResult {
	return runFixIn(ctx, root, fixChild{dry: dry, noStoreFetch: p.NoFetch, yes: p.Yes})
}

// fixChild is how one repo's lore fix process of --all runs.
type fixChild struct {
	// dry: --dry-run, and LORE_OFFLINE for the whole child.
	dry bool
	// noStoreFetch: the parent fetched every store clone, so the child
	// skips store clone fetches (LORE_NO_STORE_FETCH); the repo's own
	// remote stays reachable (owner item 21: the migrate team lock
	// fetches the repo's signals ref).
	noStoreFetch bool
	// yes: the caller confirmed (lore fix --all --yes), so the child's
	// repairs that ask first run (owner item 24).
	yes bool
	// exe, timeout and grace replace this binary, fixRepoTimeout and
	// fixChildGrace (tests: a fake child, a short limit).
	exe            string
	timeout, grace time.Duration
}

// args are the child's lore arguments.
func (c fixChild) args() []string {
	args := []string{opsfix.Noun, "--json"}
	if c.dry {
		args = append(args, "--dry-run")
	}
	if c.yes {
		args = append(args, "--"+clibuild.FlagYes)
	}
	return args
}

// env is the child's environment over the parent's environ.
func (c fixChild) env(environ []string) []string {
	env := gitenv.Clean(environ)
	switch {
	case c.dry:
		env = append(env, gitenv.EnvOffline+"="+gitenv.On) // (already on under the CLI; a REST run too)
	case c.noStoreFetch:
		env = append(env, gitenv.EnvNoStoreFetch+"="+gitenv.On)
	}
	return env
}

// runFixIn runs `lore fix --json` (this binary) in root and reads its
// result; a run that cannot start or answer is the repo's Error.
//
// A child that does not finish in time (or whose run is cancelled) is
// stopped gracefully, its whole process group: SIGTERM (lore cancels its
// run and releases its locks), fixChildGrace, then SIGKILL. A migration
// lock still naming the child's pid after that is removed, so the next
// run finds it free (owner item 46: a child killed at the limit left
// .lore/state/migrate.lock behind and every later lore fix failed).
func runFixIn(ctx context.Context, root string, c fixChild) opsfix.RepoResult {
	res := opsfix.RepoResult{Repo: root}
	start := time.Now()
	defer func() { res.WallMS = max(time.Since(start).Milliseconds(), 1) }()
	limit := cmp.Or(c.timeout, fixRepoTimeout)
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	exe := c.exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			res.Error, res.Exit = "cannot find this lore binary: "+err.Error(), 2
			return res
		}
	}
	cmd := exec.CommandContext(ctx, exe, c.args()...) // #nosec G204 -- this binary, fixed arguments
	cmd.Dir, cmd.Env = root, c.env(os.Environ())
	fixChildGroup(cmd)
	cmd.Cancel = func() error { return fixChildTerm(cmd) }
	cmd.WaitDelay = cmp.Or(c.grace, fixChildGrace) // then Go kills the child and closes its pipes
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, runErr := cmd.Output()
	passLockout(stderr.String())
	var exitErr *exec.ExitError
	if ctx.Err() != nil && cmd.Process != nil {
		fixChildKill(cmd) // whatever of its group outlived the grace (git, ssh)
		released := ""
		if releaseChildMigrateLock(root, cmd.Process.Pid) {
			released = "; removed the " + MigrateLockFile + " it held"
		}
		if ctx.Err() == context.DeadlineExceeded {
			res.Error, res.Exit = fmt.Sprintf("lore fix did not finish in %s and was stopped%s%s", limit, released, stderrTail(stderr.String())), 2
			return res
		}
	}
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		res.Exit = exitErr.ExitCode()
	default:
		res.Error, res.Exit = runErr.Error(), 2
		return res
	}
	var out opsfix.Out
	if jerr := json.Unmarshal(raw, &out); jerr != nil || out.Repo == "" {
		// a child that crashed, was killed or answered with an error
		// envelope: the repo's block says exactly what it printed
		res.Error = fmt.Sprintf("lore fix exited %d without a result%s", res.Exit, stderrTail(stderr.String()))
		if msg := envelopeMessage(raw); msg != "" {
			res.Error = fmt.Sprintf("lore fix exited %d: %s", res.Exit, msg)
		}
		if res.Exit == 0 {
			res.Exit = 2
		}
		return res
	}
	res.Status, res.Steps = out.Status, out.Steps
	res.Text = out.Text()
	res.Result = &out
	return res
}

// fixRepoTimeout bounds one repo's lore fix under --all, so a child that
// hangs is named with this error and the others still run.
const fixRepoTimeout = 10 * time.Minute

// fixChildGrace is how long a stopped child gets after SIGTERM to release
// its locks and exit before SIGKILL.
const fixChildGrace = 5 * time.Second

// stderrTailLines is how much of a failed child's stderr its block keeps.
const stderrTailLines = 20

// stderrTail is ": " and the last lines of a child's stderr ("" when it
// printed none).
func stderrTail(s string) string {
	s = strings.TrimSpace(crashDumpCut(s))
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > stderrTailLines {
		lines = append([]string{"…"}, lines[len(lines)-stderrTailLines:]...)
	}
	return ": " + strings.Join(lines, "\n")
}

// crashDumpStarts begin the goroutine and register dump the Go runtime
// prints when a process is killed with SIGQUIT or crashes.
var crashDumpStarts = []string{"SIGQUIT:", "SIGABRT:", "SIGSEGV:", "goroutine ", "panic: "}

// crashDumpCut is a child's stderr up to the Go runtime's dump (B4-4: a
// stopped child's register dump reached the terminal as its error); the
// lines lore itself printed before it stay.
func crashDumpCut(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		for _, p := range crashDumpStarts {
			if strings.HasPrefix(l, p) {
				head := strings.Join(lines[:i], "\n")
				if strings.HasPrefix(l, "panic: ") {
					head += "\n" + l // the panic message itself says what went wrong
				}
				return head + "\n(the Go runtime's stack dump left out)"
			}
		}
	}
	return s
}

// envelopeMessage is the message of a JSON error envelope a child printed
// ("" when raw is not one).
func envelopeMessage(raw []byte) string {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Error.Message == "" {
		return ""
	}
	msg := env.Error.Message
	if env.Error.Code != "" {
		msg = env.Error.Code + ": " + msg
	}
	if env.Error.Hint != "" {
		msg += " (hint: " + env.Error.Hint + ")"
	}
	return msg
}

// FixAfterUpgrade runs lore fix for the project the command runs in,
// with the binary lore upgrade just put in place (selfupgrade.ProjectFixer).
func (a *App) FixAfterUpgrade(ctx context.Context) ([]string, error) {
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return []string{"lore fix: skipped, not inside a project"}, nil
	}
	if fi, err := os.Stat(filepath.Join(top, rowfs.LoreDir)); err != nil || !fi.IsDir() {
		return []string{"lore fix: skipped, " + top + " is not a lore project"}, nil
	}
	r := runFixIn(ctx, top, fixChild{})
	if r.Error != "" {
		return nil, errs.New(errs.Internal, r.Error)
	}
	return strings.Split(strings.TrimRight(r.Text, "\n"), "\n"), nil
}

// hasV017Rows reports whether the repo here still has rows lore 0.1.17
// wrote (_v: 1) and was never migrated: its _meta.json declares no
// min_lore_version (every write of 0.1.18 or later and every migration
// raise it), and a row file under .lore/data/<table>/ of a table migrate
// rewrites is at _v: 1. A migrated repo
// is answered from _meta.json alone, so the check costs one small read
// there; an unmigrated one stops at the first v1 file.
func hasV017Rows(top string) bool {
	if top == "" {
		return false
	}
	if d, ok, err := rowfs.ReadRepoFormat(top); err != nil || (ok && d.MinLoreVersion != "") {
		return false
	}
	data := filepath.Join(top, rowfs.LoreDir, rowfs.DataDir)
	migrated := map[string]bool{} // the tables migrate rewrites (status pending counts them)
	for _, t := range CoreTables() {
		migrated[t.String()] = true
	}
	found := false
	_ = filepath.WalkDir(data, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || found {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if p != data && filepath.Dir(p) == data && !migrated[d.Name()] {
				return filepath.SkipDir // a table migrate leaves as it is
			}
			return nil
		}
		if filepath.Ext(p) != ".json" || filepath.Dir(filepath.Dir(p)) != data {
			return nil
		}
		if isV1RowFile(p) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// v1HeadLen is how much of a row file isV1RowFile reads: the envelope
// keys start with "_", which sorts before every field name in lore's
// canonical (sorted) JSON, so "_v" is in the first few dozen bytes.
const v1HeadLen = 256

// v1Markers are the spellings of `"_v": 1` as the next key or the end.
var v1Markers = [][]byte{[]byte(`"_v": 1,`), []byte(`"_v":1,`), []byte(`"_v": 1}`), []byte(`"_v":1}`)}

// isV1RowFile reports whether the row file at p is at _v: 1, from its
// head only (a migrated repo with many rows must not be read whole on
// every command).
func isV1RowFile(p string) bool {
	f, err := os.Open(filepath.Clean(p))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }() // read only
	head := make([]byte, v1HeadLen)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	return slices.ContainsFunc(v1Markers, func(m []byte) bool { return bytes.Contains(head, m) })
}

// hasPendingV017Live reports whether the open state found lore 0.1.17
// live rows its stores do not hold yet (state.pendingV017Live): a linked
// client repo has no .lore/data of its own, so hasV017Rows never sees
// them, and the write must still bring them in first (B4-5). Opening the
// state costs a write nothing: it opens it anyway.
func (a *App) hasPendingV017Live(ctx context.Context) bool {
	if a.opts.Notice == nil {
		return false // liveNote (which counts them) only runs with a notice writer
	}
	st, err := a.state(ctx)
	return err == nil && st.pendingV017Live > 0
}

// oldRowsGate is the v0.1.17 gate of a command (owner item 11): a read in
// a project with v0.1.17 rows prints OldRowsReadLine and changes nothing;
// the first write runs the safe steps of lore fix (migrate, render), says
// so in one line, and then the write runs. The migrate operations, a
// command running inside those steps, and a binary without a notice
// writer for reads are left alone.
func (a *App) oldRowsGate(ctx context.Context, e op.Entry) error {
	name := e.Spec().Name
	if strings.HasPrefix(name, migrateNounPrefix) || ctx.Value(fixSafeStepsKey{}) != nil {
		return nil
	}
	top := gitTopLevel(ctx, a.projectDir())
	if !hasV017Rows(top) {
		if e.Spec().Access != op.AccessWrite || !a.hasPendingV017Live(ctx) {
			return nil
		}
	} else if e.Spec().Access != op.AccessWrite {
		if a.opts.Notice != nil {
			a.opts.Notice(OldRowsReadLine)
		}
		return nil
	}
	ctx = context.WithValue(ctx, fixSafeStepsKey{}, true)
	mig := a.fixMigrate(ctx, top, false, false)
	if mig.Failed {
		return errs.New(errs.MigrationIncomplete, "this project has rows written by lore 0.1.17 and lore could not migrate them before this write: "+mig.Result).
			WithHint(oldRowsWriteHint)
	}
	rend := a.fixRender(ctx, top, false, mig.Changed)
	did := []string{"migrate: " + mig.Result}
	if rend.Changed {
		did = append(did, "render: "+rend.Result)
	}
	if a.opts.Notice != nil {
		a.opts.Notice(fmt.Sprintf(oldRowsWriteLine, strings.Join(did, "; ")))
		// the layout commits the migration made (FlattenAfterMigrate) are
		// shared-store commits: named one per line, as lore fix names them
		for _, l := range mig.Detail {
			if strings.Contains(l, "layout commit") || strings.HasPrefix(l, "pushed store ") {
				a.opts.Notice("lore: " + l)
			}
		}
	}
	return nil
}
