package wire

// fixproject.go — the steps of `lore fix` (and `lore doctor --fix`,
// decisions D-ak, D-al) that are not doctor checks, the --all fan-out, the
// fix `lore upgrade` runs after the binary step, and the v0.1.17 gate of
// every other command:
//
//	FixSteps(dry) ─▶ migrate  (v0.1.17 rows, an interrupted migration)
//	              ─▶ render   (a stale rendered copy, or rows just migrated)
//	              ─▶ agent hooks (lore setup --agent-hooks; not in a client
//	              │   repo, whose tracked files lore never changes)
//	              ─▶ old clones (only the ones doctor old_clones proved
//	                  everything is in the new clone)
//	              (then ops/doctor runs the checks' repairs and re-checks)
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
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
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
	steps := []opsdoctor.FixStep{mig, a.fixRender(ctx, top, dry, mig.Changed && !mig.Failed), a.fixAgentHooks(ctx, top, dry), a.fixOldClones(ctx, dry)}
	return steps
}

// fixMigrate is the migrate step: run when the repo has v1 rows, an
// interrupted migration or a v0.1.17 shared-database pointer.
func (a *App) fixMigrate(ctx context.Context, top string, dry, teamLock bool) opsdoctor.FixStep {
	st := opsdoctor.FixStep{Step: opsdoctor.StepMigrate, Where: filepath.Join(top, rowfs.LoreDir, rowfs.DataDir), Result: opsdoctor.ResultCurrent}
	status, err := a.Status(ctx)
	if err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	_, modeB := FindModeBPointer(top)
	if len(status.Pending) == 0 && !modeB && !strings.HasPrefix(status.Journal, "interrupted") {
		return st
	}
	if teamLock {
		// lore fix takes the team lock on the signals ref, as `lore migrate
		// run` does; the first write's safe steps run without it, as any
		// write does (the lock needs a coordination identity a plain write
		// does not ask for)
		ctx = opsmigrate.WithTeamLock(ctx, a.MigrateTeamLock())
	}
	out, err := opsmigrate.Run(ctx, a, opsmigrate.RunIn{DryRun: dry})
	if err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	lines := strings.Split(strings.TrimRight(out.Text(), "\n"), "\n")
	st.Changed, st.Result = true, strings.TrimSpace(lines[0])
	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); l != "" {
			st.Detail = append(st.Detail, l)
		}
	}
	return st
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
	for _, f := range stale {
		st.Detail = append(st.Detail, f.Line())
	}
	if dry {
		st.Result = "would render"
		return st
	}
	ans, err := h.Render(ctx, opsrender.RunIn{})
	if err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	st.Result = "rendered"
	if first := strings.TrimSpace(strings.SplitN(ans.Text, "\n", 2)[0]); first != "" {
		st.Result += ": " + first
	}
	return st
}

// fixAgentHooks is the agent hooks step: what lore setup --agent-hooks
// writes, for the tools set up here.
func (a *App) fixAgentHooks(ctx context.Context, top string, dry bool) opsdoctor.FixStep {
	st := opsdoctor.FixStep{Step: opsdoctor.StepAgentHooks, Where: top, Result: opsdoctor.ResultCurrent}
	if client, _ := a.ClientRepo(ctx); client {
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
	var change []opssetup.HookPlan
	for _, p := range plans {
		if p.Change {
			change = append(change, p)
			st.Detail = append(st.Detail, p.Tool+": "+p.File)
		}
	}
	if len(change) == 0 {
		return st
	}
	st.Changed = true
	if dry {
		st.Result = fmt.Sprintf("would write %d settings file(s)", len(change))
		return st
	}
	if err := h.WriteAgentHooks(ctx, change, false); err != nil {
		st.Failed, st.Result = true, err.Error()
		return st
	}
	st.Result = fmt.Sprintf("wrote %d settings file(s)", len(change))
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
		st.Detail = append(st.Detail, o.Base)
		if dry {
			continue
		}
		for _, p := range []string{o.Base, o.Bare} {
			if err := os.RemoveAll(p); err != nil {
				st.Failed, st.Result = true, "remove "+p+": "+err.Error()
				return st
			}
		}
	}
	if n == 0 {
		return st
	}
	st.Changed = true
	st.Result = fmt.Sprintf("removed %d old clone(s), everything in them is in the new clones", n)
	if dry {
		st.Result = fmt.Sprintf("would remove %d old clone(s)", n)
	}
	return st
}

// FixRepos are the repo roots of --all (opsfix.Env).
func (a *App) FixRepos(ctx context.Context) ([]string, error) { return a.fixRepos(ctx) }

// FixProgress is where --all prints each repo's block as it finishes:
// stderr, so stdout stays the one result (opsfix.Env).
func (a *App) FixProgress() io.Writer { return fixProgress }

// fixProgress is FixProgress's writer (a test swaps it).
var fixProgress io.Writer = os.Stderr

// FixPrepare fetches every whole store clone under ~/.lore/stores once,
// jobs at a time, before --all's repos run (opsfix.Env, owner bug 14):
// forty repos sharing one store then fetch it once, not forty times. When
// every fetch answered, the children run with LORE_NO_FETCH=1; when one
// failed, they fetch for themselves as before. A dry run fetches nothing
// (LORE_OFFLINE is on for the whole process tree).
func (a *App) FixPrepare(ctx context.Context, dry bool, jobs int) opsfix.Prep {
	var p opsfix.Prep
	if dry || gitenv.NoFetch(os.Environ()) {
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
	return runFixIn(ctx, root, dry, p.NoFetch)
}

// fixScanDepth is how deep --all looks for lore repos in a setup folder.
const fixScanDepth = 3

// fixRepos are the repo roots of --all, sorted, each once, existing only.
func (a *App) fixRepos(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	var roots []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		if fi, err := os.Stat(filepath.Join(p, rowfs.LoreDir)); err != nil || !fi.IsDir() {
			return
		}
		seen[p] = true
		roots = append(roots, p)
	}
	known, err := a.KnownRepos(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range known {
		add(k.Root)
	}
	if cfg, cerr := ReadSetupsConfig(a.opts.Home); cerr == nil {
		var bases []string
		for _, e := range cfg.Enabled() {
			for _, g := range e.Folders {
				bases = append(bases, GlobBase(onboard.ExpandHome(g, userHome(SetupQuery{}))))
			}
		}
		found, serr := ScanLoreRepos(ctx, bases, fixScanDepth)
		if serr != nil {
			return nil, serr
		}
		for _, f := range found {
			add(f.Root)
		}
	}
	slices.Sort(roots)
	return roots, nil
}

// runFixIn runs `lore fix --json` (this binary) in root and reads its
// result; a run that cannot start or answer is the repo's Error.
func runFixIn(ctx context.Context, root string, dry, noFetch bool) opsfix.RepoResult {
	res := opsfix.RepoResult{Repo: root}
	start := time.Now()
	defer func() { res.WallMS = max(time.Since(start).Milliseconds(), 1) }()
	ctx, cancel := context.WithTimeout(ctx, fixRepoTimeout)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		res.Error, res.Exit = "cannot find this lore binary: "+err.Error(), 2
		return res
	}
	args := []string{opsfix.Noun, "--json"}
	if dry {
		args = append(args, "--dry-run")
	}
	cmd := exec.CommandContext(ctx, exe, args...) // #nosec G204 -- this binary, fixed arguments
	cmd.Dir, cmd.Env = root, gitenv.Clean(os.Environ())
	if dry {
		cmd.Env = append(cmd.Env, gitenv.EnvOffline+"="+gitenv.On) // (already on under the CLI; a REST run too)
	} else if noFetch {
		cmd.Env = append(cmd.Env, gitenv.EnvNoFetch+"="+gitenv.On)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, runErr := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.Error, res.Exit = fmt.Sprintf("lore fix did not finish in %s and was stopped%s", fixRepoTimeout, stderrTail(stderr.String())), 2
		return res
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

// stderrTailLines is how much of a failed child's stderr its block keeps.
const stderrTailLines = 20

// stderrTail is ": " and the last lines of a child's stderr ("" when it
// printed none).
func stderrTail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > stderrTailLines {
		lines = append([]string{"…"}, lines[len(lines)-stderrTailLines:]...)
	}
	return ": " + strings.Join(lines, "\n")
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
	r := runFixIn(ctx, top, false, false)
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
		return nil
	}
	if e.Spec().Access != op.AccessWrite {
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
	}
	return nil
}
