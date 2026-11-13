// render.go — `lore render` (S2.4)
//
// # Pulls scoped knowledge from the DB and writes .lore/LORE.md atomically
//
// Determinism (R16 #5, R34.4): ORDER BY id, created_at — same DB state
// produces byte-identical output
//
// Atomic write (R14 #1, R27 #19): tmp + fsync + rename + fsync-parent
// Symlink-aware: if target is a symlink, write to target, preserve symlink
// Skip rename if content sha unchanged (R29 #53 — avoid file-watcher loops)
//
// Token budget (R21 #21): 10/30/30/20/10 hierarchy. v0.1 emits all
// pinned content + memories; v0.2 will add extractive compression
//
// render_history table (R22 #14) is populated for `lore why-context`
//
// TOOL NEUTRAL: the generated file is always .lore/LORE.md; each AI tool the
// repo uses (claude_code → CLAUDE.md, codex → AGENTS.md, cursor →
// .cursor/rules/lore.mdc, …) gets only a pointer block in its own file. The
// tools come from one registry (logic/toolprofile) plus the user's profiles
// in ~/.lore/config.yaml; no tool is the default.

package renderhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/fsreason"
	"github.com/thesatellite-ai/lore/adapters/osrename"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"

	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

type renderFlags struct {
	outPath string
	// targets are --target values: profile names or pointer files. Empty
	// means the repo's context targets (its context.targets row, else
	// toolprofile.Resolve).
	targets []string
	// addTargets are --add-target values: profiles added to the repo's
	// context.targets row before the targets are resolved.
	addTargets []string
	noPointer  bool
	dryRun     bool
	// report, check and asJSON: CONTEXT_BUDGET 6.11 (write nothing).
	report bool
	// scopes adds the per-file, per-tool table and chain sums (6.2.6).
	scopes bool
	check  bool
	asJSON bool
	// trend: 6.11 bytes per render, growth per day (render_trend.go).
	trend bool
	// w collects the command's lines: the render operation's host gives
	// its buffer (P3-MISC-7); nil means out() makes one, which a CLI caller
	// prints afterwards (rerenderLoreMD).
	w *strings.Builder
	// quiet suppresses the per-file stdout lines. Not a CLI flag: set by the
	// post-command sync step that re-renders LORE.md after importing
	// teammates' changes, where stdout belongs to the user's command (and
	// may be --json).
	quiet bool
}

// out is where the command's lines go.
func (f *renderFlags) out() *strings.Builder {
	if f.w == nil {
		f.w = &strings.Builder{}
	}
	return f.w
}

// The `lore render` command is the operation render.run (ops/render,
// render_host.go); runRender below is its machinery (P3-MISC-7).

// renderResult tracks counts for the render_history scope_summary field
type renderResult struct {
	Rules    int `json:"rules"`
	Hotfixes int `json:"hotfixes"`
	Memories int `json:"memories"`
}

func (h *Host) runRender(ctx context.Context, f *renderFlags) (err error) {
	// render has no --repo since 0.1.18: the master scope (repo id "").
	repoID := ""
	projectID, err := h.projectID(ctx)
	if err != nil {
		return err
	}
	if f.scopes && !f.report {
		return errs.New(errs.InvalidInput, "--scopes needs --report").WithHint("run `lore render --report --scopes`")
	}
	if f.trend { // 6.11: read-only, from render_history
		return h.renderTrend(ctx, projectID, f)
	}

	root, err := os.Getwd()
	if err != nil {
		return errs.Wrap(errs.Internal, "getwd", err)
	}
	p, err := h.planCommitted(ctx, projectID, root, f.targets, func() error {
		if len(f.addTargets) == 0 {
			return nil
		}
		return h.addContextTargets(ctx, f.out(), root, f.addTargets)
	})
	if err != nil {
		return err
	}
	pick, in, body, report, full := p.pick, p.in, p.body, p.report, p.full
	rules, hotfixes, skips, dirs, dirBodies, dirReps, pathGroups := p.rules, p.hotfixes, p.skips, p.dirs, p.dirBodies, p.dirReps, p.pathGroups
	report.File = f.outPath

	if f.report && f.scopes {
		// 6.2.6: the per-file table; writes nothing.
		sr := h.buildScopeReport(ctx, report, dirs, dirBodies, dirReps, pick.targets)
		if f.asJSON {
			data, jerr := sr.jsonBody()
			if jerr != nil {
				return errs.Wrap(errs.Internal, "encode report", jerr)
			}
			fmt.Fprintln(f.out(), string(data))
			return nil
		}
		fmt.Fprint(f.out(), sr.text())
		return nil
	}
	if f.report {
		// 6.11: --report writes nothing.
		if f.asJSON {
			data, jerr := report.reportJSON()
			if jerr != nil {
				return errs.Wrap(errs.Internal, "encode report", jerr)
			}
			fmt.Fprintln(f.out(), string(data))
			return nil
		}
		fmt.Fprint(f.out(), report.reportText())
		return nil
	}
	if f.check {
		// 6.11: CI dry run; fails on budget_exceeded only. Over the warn
		// level is a warning on stdout with exit 0 (C-8: a LORE.md that
		// fits its budget failed a CI check), as lore doctor counts it.
		// DECISION OV18-1: the failure is E_REFUSED (no new code).
		if report.Over {
			return errs.New(errs.Refused, fmt.Sprintf("%s would be %d bytes: over the budget (%d, profile %s)",
				f.outPath, report.After, report.Budget, report.Profile)).WithHint("run `lore render --report` to see what is in the core, and `lore lint --context` for clean-up")
		}
		// SOURCE_OF_TRUTH rows 10, 11: a rendered file whose stamp no
		// longer matches its rows fails the check too.
		// A repo that never rendered has no copies yet: only the budget is
		// checked, as before.
		rendered := fileSize(filepath.Join(root, filepath.FromSlash(f.outPath))) >= 0
		if filepath.IsAbs(f.outPath) {
			rendered = fileSize(f.outPath) >= 0
		}
		// fmt1: in a project on the 0.1.17 format the rendered files are
		// lore 0.1.17's (its own layout) and lore render skips: only the
		// budget is checked, as in a repo that never rendered
		formatLine, formatOld := wire.FormatOldAt(ctx, root)
		if rendered && !formatOld {
			stale, serr := h.copiesOf(ctx, root, f.outPath, p)
			if serr != nil {
				return serr
			}
			if err := staleError(stale); err != nil {
				return err
			}
		}
		fmt.Fprintf(f.out(), "%s %s would be %d of %d bytes (%d%%; warn at %d%%)\n", h.decor.check(), f.outPath,
			report.After, report.Budget, report.After*percent/max(report.Budget, 1), report.WarnPct)
		if report.WarnExceeded {
			fmt.Fprintf(f.out(), "%s %s is over the warn level (%d%% of %d, profile %s): `lore render --report` shows what is in the core, `lore lint --context` what to clean up\n",
				h.decor.warn("!"), f.outPath, report.WarnPct, report.Budget, report.Profile)
		}
		switch {
		case formatOld:
			fmt.Fprintf(f.out(), "%s rendered files not compared: %s\n", h.decor.muted("·"), formatLine)
		case rendered:
			fmt.Fprintf(f.out(), "%s rendered files match their rows\n", h.decor.check())
		default:
			fmt.Fprintf(f.out(), "%s %s is not rendered yet (lore render)\n", h.decor.muted("·"), f.outPath)
		}
		return nil
	}

	// INTEG-RENDER-2: the private step below (renderPrivate →
	// planPrivateLocals) reads the repo's own targets even when --target
	// chose this run's tool files. Resolve them now, before any file is
	// written, so a context.targets row naming a profile this machine
	// lacks (a teammate's own) refuses the whole render instead of
	// failing after LORE.md and the pointer files were written.
	if len(f.targets) > 0 && !f.quiet && h.app != nil {
		if _, err = h.resolveTargets(ctx, root, nil, h.coreContextTargets); err != nil {
			return err
		}
	}

	if f.dryRun {
		fmt.Fprint(f.out(), body)
		// The private per-store files (spec 7.3, CFG6), shown, not written.
		return h.renderPrivate(ctx, f.out(), root, true, f.quiet)
	}

	// S-2: a repo with no lore data (one a store only links) gets no
	// LORE.md and no pointer block. The re-render after a command's sync
	// (quiet) says nothing there; `lore render` says why it wrote nothing.
	if top := wire.UntouchedRepo(ctx, root); top != "" {
		if f.quiet {
			return nil
		}
		return wire.UntouchedError(ctx, top)
	}

	// fmt1: a project on the 0.1.17 format gets no tracked file written
	// (LORE.md, LORE.full.md, pointer blocks, folder files, render history)
	// until lore migrate run: a 0.1.17 teammate rewrites LORE.md in its own
	// layout, so both versions would churn it in every commit. render
	// --check, --report and --dry-run above write nothing and still run.
	if line, old := wire.FormatOldAt(ctx, root); old {
		if !f.quiet {
			fmt.Fprintf(f.out(), "%s lore render: skipped: %s\n", h.decor.muted("·"), line)
		}
		return nil
	}

	// Generated content goes to the out file (default .lore/LORE.md), never
	// into a tool's own file — so hand-written content there is never
	// clobbered. Each tool file only receives an idempotent pointer block
	// (stitchPointer below)

	// Resolve symlinks (R27 #19): if the out file is a symlink (or a chain
	// of them), write to the chain's end, unless the link leads out of the
	// repository (wire.WriteThroughLink).
	resolvedOut, err := wire.WriteThroughLink(root, f.outPath)
	if err != nil {
		return err
	}
	if op.Remote(ctx) {
		if err = refuseRemoteOut(root, f.outPath); err != nil {
			return err
		}
	}
	// LORE.full.md beside the out file's end; a link of that name is
	// followed the same way, never replaced by a regular file.
	fullPath, err := wire.WriteThroughLink(root, filepath.Join(filepath.Dir(resolvedOut), fullFileName))
	if err != nil {
		return err
	}
	// The companion file sits next to --out, which may be any folder of the
	// person's: never replace a LORE.full.md lore did not write. Checked
	// before anything is written, so a refusal leaves every file as it was.
	if in.Settings.FullFile {
		if err = refuseForeignFull(fullPath); err != nil {
			return err
		}
	}

	// Skip rename if content unchanged (R29 #53) — but still re-stitch the
	// pointer: a tool file may have lost it even when the body is identical
	unchanged := false
	if existing, rerr := os.ReadFile(filepath.Clean(resolvedOut)); rerr == nil {
		unchanged = sha256Hex(existing) == sha256Hex([]byte(body))
	}

	if unchanged {
		if !f.quiet {
			fmt.Fprintf(f.out(), "%s %s (unchanged)\n", h.decor.muted("·"), resolvedOut)
		}
	} else {
		if err = atomicWriteFile(resolvedOut, []byte(body), 0o644); err != nil {
			return wire.WriteError("write", resolvedOut, err, renderWriteHint)
		}
		if !f.quiet {
			fmt.Fprintf(f.out(), "%s %s (%d bytes, budget %d %s; %d full + %d line rules, %d + %d hotfixes, %s)\n",
				h.decor.check(), resolvedOut, len(body), report.Budget, report.Profile,
				report.MustFull, report.MustLine, report.Critical, report.HighLine, plural.Count(report.Topics, "topic"))
		}
	}
	if report.Over {
		fmt.Fprintln(os.Stderr, h.decor.warn(fmt.Sprintf("WARN: budget_exceeded: %s is %d bytes, over the %d-byte budget of %s; every must rule and critical/high hotfix is still written (lore render --report)",
			resolvedOut, report.After, report.Budget, report.Profile)))
	}
	// CQ2: the full fallback file, next to LORE.md, never imported.
	if in.Settings.FullFile {
		if err = h.writeFullFile(f.out(), fullPath, full, f.quiet); err != nil {
			return err
		}
	}

	if !f.noPointer {
		if !f.quiet {
			h.printTargetPick(f.out(), pick)
		}
		for _, t := range pick.targets {
			if err = h.stitchPointer(f.out(), root, t, resolvedOut); err != nil {
				return err
			}
		}
	}
	// 6.7: path-scoped rule files per target; stale lore-path-* files go.
	reg, err := h.loadToolRegistry(ctx)
	if err != nil {
		return err
	}
	if err := h.writePathRules(f.out(), root, pathGroups, pick.targets, reg.Profiles(), h.loadPathFileBudget(ctx), f.quiet); err != nil {
		return errs.Wrap(errs.Internal, "path rule files", err)
	}
	// 6.2.5: folder files and each target's folder file; stale ones go.
	if err := h.writeDirs(f.out(), root, dirs, dirBodies, pick.targets, len(body), f.quiet); err != nil {
		return errs.Wrap(errs.Internal, "folder files", err)
	}
	// B3-5: a folder scope whose folder moved or went renders into the root
	// file; say so once per scope instead of letting its rules quietly
	// leave the folder.
	if !f.quiet {
		for _, l := range h.ScopeGoneLines(ctx, root) {
			fmt.Fprintln(os.Stderr, h.decor.warn("WARN: "+l))
		}
	}

	if err := h.persistRenderHistory(ctx, projectID, repoID, resolvedOut, body, renderResult{
		Rules: len(rules), Hotfixes: len(hotfixes),
	}, newCutSummary(report, skips)); err != nil {
		// Non-fatal: file written, history failed. Log and continue
		fmt.Fprintln(os.Stderr, h.decor.warn("WARN: render_history write failed: "+err.Error()))
	}

	// Second target (F20, spec 7.3, CFG6): each internal store's rows go to
	// a private file ~/.lore/render/<repo_id>/<store_id>.md, outside the
	// checkout, with only import lines in CLAUDE.local.md (render_private.go).
	// The automatic re-render after a sync (quiet) leaves it to the next
	// explicit `lore render`, so a plain command never reaches a store for it.
	if !f.quiet {
		if err := h.renderPrivate(ctx, f.out(), root, false, f.quiet); err != nil {
			return err
		}
	}

	return nil
}

// committedPlan is everything the committed render would write, built
// without writing anything: runRender writes it, copiesOf compares it with
// the files on disk (SOURCE_OF_TRUTH rows 10, 12).
type committedPlan struct {
	pick       targetPick
	in         renderInput
	rules      []pinnedRule // the core's rules (folder and path rules moved out)
	hotfixes   []pinnedHotfix
	skips      pinSkips
	dirs       dirPlan
	dirBodies  dirBodies
	dirReps    []dirReport
	pathGroups []pathGroup
	body       string // .lore/LORE.md, its stamp in the canary line
	full       string // LORE.full.md
	report     renderReport
}

// planCommitted loads the pinned rows of projectID, resolves the targets
// (values, else the repo's own) and builds LORE.md and LORE.full.md.
// beforeTargets runs after the rows are loaded and expanded and before the
// targets are resolved (render's --add-target: a refused expansion writes
// no setting).
func (h *Host) planCommitted(ctx context.Context, projectID, root string, values []string, beforeTargets func() error) (*committedPlan, error) {
	// ds:def id=render-pins-buj9dujv owner=@khanakia stability=stable desc="what render pins into the context file (must rules, critical/high hotfixes, directive)"
	// Hybrid: pin only severity=must rules + critical/high hotfixes +
	// directive into LORE.md. Everything else (should/may rules,
	// memories, decisions, patterns, …) is fetched via `lore search`
	// on demand per the directive's pre-response checklist
	var skips pinSkips
	rules, err := h.loadPinnedRules(ctx, projectID, &skips)
	if err != nil {
		return nil, err
	}
	hotfixes, err := h.loadPinnedHotfixes(ctx, projectID, &skips)
	if err != nil {
		return nil, err
	}
	// SECRETS 12 / 13.2: references filled for the committed render; a
	// value that brings a secret in refuses it (D23).
	if rules, hotfixes, err = h.expandPinned(ctx, rules, hotfixes); err != nil {
		return nil, err
	}
	if beforeTargets != nil {
		if err = beforeTargets(); err != nil {
			return nil, err
		}
	}
	// CQ6: the budget is the smallest of the targets' budgets; the targets
	// are resolved silently here and announced when stitched.
	pick, err := h.resolveTargets(ctx, root, values, h.coreContextTargets)
	if err != nil {
		return nil, err
	}
	profiles := make([]toolprofile.Profile, 0, len(pick.targets))
	for _, t := range pick.targets {
		profiles = append(profiles, t.profile)
	}
	topics, err := h.loadTopicIndex(ctx)
	if err != nil {
		return nil, err
	}
	// 6.2: folder-scoped rows go to <dir>/LORE.md (render_scopes.go); the
	// full file still holds every scope (CQ2).
	allRules, allHotfixes := rules, hotfixes
	rules, hotfixes, dirs := h.planDirs(ctx, root, rules, hotfixes)
	// 6.7 claude_rules / cursor_rules: glob rules go to the tools' own path
	// files when every target loads them (render_pathrules.go, FA-CTX-2).
	rules, pathGroups := planPathRules(rules, profiles)
	skips.IDs = append(skips.IDs, pathRuleIDs(pathGroups)...)
	skips.PathScoped = len(pathRuleIDs(pathGroups))
	in := renderInput{Settings: h.loadContextSettings(ctx, profiles), Rules: rules, Hotfixes: hotfixes, Topics: topics, Skips: skips, Dirs: dirs.Groups}
	// 6.13 (RES-OQ3b) and META 3.14 (FA-META-10): the resource index and
	// the metadata keys block of the committed sources, after the core and
	// only when they fit (FA-CTX-7).
	if in.Tails, err = h.renderTails(ctx); err != nil {
		return nil, err
	}
	dirBodies, dirReps := buildDirFiles(dirs, in.Settings.EntryMax)

	// The canary is the file's stamp (render_stamp.go): a hash of the
	// rows' origins (store, id, HLC), the settings that shape the text and
	// the body BEFORE the canary line is added, so two renders of the SAME
	// state produce byte-identical output (SC-12) and a changed row
	// changes it. A fresh per-session id wouldn't satisfy the "same DB →
	// same render" invariant. Prompt-injection detection still works: the
	// canary is unique-per-content, and the renderhistory row stores it
	// for lookup.
	contentForCanary, _ := buildBudgetedFile("PLACEHOLDER", in)
	canary := renderCanary(inputDigest(allRules, allHotfixes, in.Settings), contentForCanary)
	body, report := buildBudgetedFile(canary, in)
	full := buildFullFile(allRules, allHotfixes)
	report.Before = len(full)
	report.Dirs, report.DirsOff = dirReps, !dirs.Settings.On && len(dirs.All) > 0
	return &committedPlan{pick: pick, in: in, rules: rules, hotfixes: hotfixes, skips: skips, dirs: dirs, dirBodies: dirBodies,
		dirReps: dirReps, pathGroups: pathGroups, body: body, full: full, report: report}, nil
}

// contextTarget is one tool file render wires: its profile and the file's
// path (relative to the repo root, or absolute when --target gave one).
type contextTarget struct {
	profile toolprofile.Profile
	file    string
	// known is true when profile is a registry profile (its name can go in
	// the context.targets row); false for any other file given to --target.
	known bool
}

// loadToolRegistry is the built-in tool profiles plus the user's own from
// ~/.lore/config.yaml (context.profiles). TOOL NEUTRAL + no-hardwire: a
// tool lore does not ship is a config entry, resolved by the same code.
func (h *Host) loadToolRegistry(ctx context.Context) (*toolprofile.Registry, error) {
	profiles, err := h.profiles(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := toolprofile.New(profiles)
	if err != nil {
		return nil, errs.Wrap(errs.ConfigInvalid, "context.profiles in ~/.lore/config.yaml", err).WithHint("run `lore config check` for every problem with its location")
	}
	return reg, nil
}

// rootProbe reads pointer files under a repo root for toolprofile.Resolve.
type rootProbe struct{ root string }

// Matches implements toolprofile.Finder (Profile.Detect, e.g. a legacy
// .cursorrules).
func (p rootProbe) Matches(pattern string) []string {
	return toolprofile.GlobFiles(os.DirFS(p.root), pattern)
}

// State implements toolprofile.Probe.
func (p rootProbe) State(rel string) toolprofile.FileState {
	data, err := os.ReadFile(filepath.Join(p.root, filepath.FromSlash(rel)))
	switch {
	case err != nil:
		return toolprofile.FileMissing
	case toolprofile.HasPointer(string(data)):
		return toolprofile.FileWired
	default:
		return toolprofile.FilePresent
	}
}

// targetSetting reads a repo's context.targets settings row: the profile
// names and whether a row sets them (ok false = no row, fall back to the
// repo's tool files).
type targetSetting func(ctx context.Context) (names []string, ok bool, err error)

// coreContextTargets is the targetSetting of the repo this process runs in
// (the core App mounted at the working directory). Q-R2-3: the
// context.targets row is read first; a broken policy row fails closed
// (E_CONFIG_INVALID, as for every write), while a core that cannot open at
// all (no repo store yet) falls back to the tool files with a warning, so
// render keeps working where it did before the row existed.
func (h *Host) coreContextTargets(ctx context.Context) ([]string, bool, error) {
	if h.app == nil {
		return nil, false, nil
	}
	return h.contextTargetsOf(ctx, h.app)
}

// contextTargetsOf reads the effective context.targets of app's repo.
func (h *Host) contextTargetsOf(ctx context.Context, app *wire.App) ([]string, bool, error) {
	eng, err := app.Engine(ctx)
	if err != nil {
		if errs.Has(err, errs.ConfigInvalid) {
			return nil, false, err
		}
		if wire.NoCreate(ctx) && errs.Has(err, errs.NotAStore) {
			return nil, false, nil // doctor in a repo with no lore: its report says so once (wire/doctor_nocreate.go)
		}
		fmt.Fprintln(os.Stderr, h.decor.warn("WARN: could not read the context.targets setting ("+err.Error()+"); using the repo's tool files"))
		return nil, false, nil
	}
	set, err := eng.Policy(ctx, store.SourceRepo)
	if err != nil {
		return nil, false, err
	}
	names, ok := set.ContextTargets()
	return names, ok, nil
}

// targetPick is what resolveTargets chose: the files, and how (Selection;
// Reason is empty when --target values chose them).
type targetPick struct {
	targets []contextTarget
	sel     toolprofile.Selection
	// setup names the setup whose tools chose the targets (ReasonSetup).
	setup string
}

// setupTools is the tools of the guided setup that applies in this repo
// and that setup's name (wire.App.SetupInitTools): none without an app,
// a setup, or a chosen tool with a pointer file.
func (h *Host) setupTools(ctx context.Context) ([]string, string) {
	if h.app == nil {
		return nil, ""
	}
	st := h.app.SetupInitTools(ctx)
	return st.Profiles, st.Setup
}

// printTargetPick prints how the targets were chosen (silent for an
// already-wired repo and for the settings row: nothing new to say) and
// names each tool file found but not wired.
func (h *Host) printTargetPick(w *strings.Builder, pick targetPick) {
	switch pick.sel.Reason {
	case toolprofile.ReasonDetected, toolprofile.ReasonFallback:
		fmt.Fprintf(w, "%s context targets: %s (%s)\n", h.decor.muted("·"), describeTargets(pick.targets), describeReason(pick.sel.Reason))
	case toolprofile.ReasonSetup:
		fmt.Fprintf(w, "%s context targets: %s (%s)\n", h.decor.muted("·"), describeTargets(pick.targets), setupReason(pick.setup))
	case toolprofile.ReasonWired, toolprofile.ReasonSetting:
	}
	// TOOL NEUTRAL, no surprises: a tool file added after lore was wired
	// (say CLAUDE.md in an AGENTS.md repo) is not wired behind the user's
	// back; render names it and the command that adds it.
	for _, p := range pick.sel.Unwired {
		hint := "lore render --target " + p.Name
		if pick.sel.Reason == toolprofile.ReasonSetting {
			hint = "lore policy set --key " + string(policy.KeyContextTargets) + " --value \"<profiles incl. " + p.Name + ">\" --yes"
		}
		fmt.Fprintf(w, "%s %s found but not wired to lore; add it with: %s\n", h.decor.muted("·"), p.PointerFile, hint)
	}
	// r3-targets (DECISION P2-OLDCLI-2): a file that carries lore's pointer
	// but is not in context.targets is left as it is; say so and how to
	// list it, instead of skipping it silently.
	for _, p := range pick.sel.Unlisted {
		fmt.Fprintf(w, "%s %s carries lore's pointer but %s does not name it, so render leaves it as is; list it with: lore policy set --key %s --value \"<profiles incl. %s>\" --yes\n",
			h.decor.muted("·"), p.PointerFile, policy.KeyContextTargets, policy.KeyContextTargets, p.Name)
	}
}

// resolveTargets turns --target values into tool files, or, with none,
// resolves the repo's own targets: its context.targets settings row
// (read through setting, Q-R2-3), else the pointer-file detection
// (toolprofile.Resolve). A value is a profile name, a known profile's
// pointer file, or any other file, wired with an @import pointer as
// v0.1.17's --target was. TOOL NEUTRAL: no tool file is assumed.
func (h *Host) resolveTargets(ctx context.Context, root string, values []string, setting targetSetting) (targetPick, error) {
	reg, err := h.loadToolRegistry(ctx)
	if err != nil {
		return targetPick{}, err
	}
	var pick targetPick
	if len(values) == 0 {
		names, ok, serr := setting(ctx)
		if serr != nil {
			return targetPick{}, serr
		}
		if ok {
			// Q-R2-3: the row names the targets; an unknown profile in it
			// (say a teammate's own profile this machine lacks) is refused,
			// never silently skipped.
			sel, selErr := reg.Select(names, rootProbe{root: root})
			if selErr != nil {
				return targetPick{}, errs.Wrap(errs.InvalidInput, "the repo's "+string(policy.KeyContextTargets)+" setting: "+strings.TrimPrefix(errs.EnvelopeOf(selErr).Error.Message, string(policy.KeyContextTargets)+": "), selErr).
					WithHint("change it with: lore policy set --key " + string(policy.KeyContextTargets) + " --value <profiles> --yes, or add the profile under context.profiles in ~/.lore/config.yaml")
			}
			pick.sel = sel
		} else {
			pick.sel = reg.Resolve(rootProbe{root: root})
			// A-9: no row, so the guided setup that applies here names the
			// tools (the same ones `lore init` wires), before detection.
			if names, setup := h.setupTools(ctx); len(names) > 0 {
				pick.sel, pick.setup = reg.FromSetup(names, pick.sel, rootProbe{root: root}), setup
			}
		}
		for _, p := range pick.sel.Profiles {
			pick.targets = append(pick.targets, contextTarget{profile: p, file: p.PointerFile, known: true})
		}
		return pick, nil
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if p, ok := reg.Lookup(v); ok {
			pick.targets = append(pick.targets, contextTarget{profile: p, file: p.PointerFile, known: true})
			continue
		}
		// The target files are the repo's tool files: a path that leads
		// out of the repository (../x.md, /etc/x) is refused before
		// anything is written.
		if err := refuseOutsideTarget(root, v); err != nil {
			return targetPick{}, err
		}
		rel := v
		if filepath.IsAbs(v) {
			if r, rerr := filepath.Rel(root, v); rerr == nil && !strings.HasPrefix(r, "..") {
				rel = r
			}
		}
		if p, ok := reg.ByPointerFile(filepath.ToSlash(rel)); ok {
			pick.targets = append(pick.targets, contextTarget{profile: p, file: p.PointerFile, known: true})
			continue
		}
		// INTEG-RENDER-1: a bare profile-shaped token that is no profile is
		// a typo (claud_code), refused before anything is written; a file
		// of that name is wired when written as a path (./claud_code).
		if err := reg.RefuseTypo(v); err != nil {
			return targetPick{}, err
		}
		pick.targets = append(pick.targets, contextTarget{
			profile: toolprofile.Profile{Name: v, PointerFile: filepath.ToSlash(v), Import: toolprofile.ImportAt},
			file:    v,
		})
	}
	return pick, nil
}

// describeTargets is "codex (AGENTS.md), generic" for one output line.
func describeTargets(ts []contextTarget) string {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		if t.profile.HasPointerFile() {
			parts = append(parts, t.profile.Name+" ("+t.file+")")
		} else {
			parts = append(parts, t.profile.Name)
		}
	}
	return strings.Join(parts, ", ")
}

// describeReason says in words why these targets were chosen.
func describeReason(r toolprofile.Reason) string {
	if r == toolprofile.ReasonSetup {
		return setupReason("")
	}
	switch r {
	case toolprofile.ReasonDetected:
		return "detected in this repo; change with --target <profile>"
	case toolprofile.ReasonFallback:
		return "no AI tool file found; AGENTS.md is the cross-tool file, change with --target <profile>"
	case toolprofile.ReasonSetting:
		return "the repo's " + string(policy.KeyContextTargets) + " setting"
	case toolprofile.ReasonWired:
	}
	return "already wired"
}

// setupReason says the targets are the tools a guided setup chose.
func setupReason(setup string) string {
	if setup == "" {
		return "the tools of the setup that applies here; change with --target <profile>"
	}
	return "the tools of setup " + setup + "; change with --target <profile>"
}

// stitchPointer ensures t's tool file carries the lore pointer block,
// bounded by sentinel markers. Idempotent: an existing block is replaced in
// place; a missing one is placed at the top (after a Cursor rule's
// frontmatter); if nothing changes the file is left untouched. An @import
// path is computed RELATIVE to the tool file's directory so the tool
// resolves it regardless of cwd. A profile without a pointer file
// (generic) has nothing to stitch.
func (h *Host) stitchPointer(w *strings.Builder, root string, t contextTarget, outFile string) error {
	if !t.profile.HasPointerFile() {
		return nil
	}
	agentFile := t.file
	if !filepath.IsAbs(agentFile) {
		agentFile = filepath.Join(root, filepath.FromSlash(agentFile))
	}
	absAgent, err := filepath.Abs(agentFile)
	if err != nil {
		return errs.Wrap(errs.BadPath, "resolve "+agentFile, err)
	}
	absOut, err := filepath.Abs(outFile)
	if err != nil {
		return errs.Wrap(errs.BadPath, "resolve "+outFile, err)
	}
	link := toolprofile.Link{FromPointer: relSlash(filepath.Dir(absAgent), absOut, outFile), FromRoot: relSlash(root, absOut, outFile)}

	// A pointer file that is a link (or a chain of them) is read and
	// written at the chain's end, so the link stays. One that cannot be
	// written through (dangling out of the repo, a folder, unreadable, a
	// file of another kind outside the repo: wire.PointerLinkEnd) is left
	// as it is with a note naming it and why; the render goes on.
	writeAt, isLink, skip := wire.PointerLinkEnd(root, absAgent)
	if skip != "" {
		h.pointerSkipped(w, absAgent, skip)
		return nil
	}
	existing := ""
	if data, err := os.ReadFile(filepath.Clean(writeAt)); err == nil {
		existing = string(data)
	} else if isLink && !os.IsNotExist(err) {
		h.pointerSkipped(w, absAgent, "it is a symbolic link to "+writeAt+", which cannot be read ("+fsreason.Of(writeAt, err)+")")
		return nil
	} else if !os.IsNotExist(err) {
		// The pointer file is the person's --target (or a tool's file):
		// a folder, a name the file system refuses or a file they may not
		// read is their input to fix, so name the file, the reason and the
		// way out instead of an internal error.
		return errs.Wrap(errs.InvalidInput, "render: cannot read the pointer file "+absAgent+": "+fsreason.Of(writeAt, err), err).
			WithHint("give --target a tool profile name or the path of a file (not a folder) you can read and write")
	}

	out := t.profile.StitchPointer(existing, link)
	if note := toolprofile.StrayNote(absAgent, out, toolprofile.PointerStartMarker, toolprofile.PointerEndMarker); note != "" {
		fmt.Fprintf(w, "%s %s\n", h.decor.warn("⚠"), note)
	}
	if out == existing {
		fmt.Fprintf(w, "%s %s (pointer present)\n", h.decor.muted("="), absAgent)
		return nil
	}
	if banner := toolprofile.GeneratedBanner(existing); banner != "" {
		// D-2: another tool generates this file; a stitched pointer is lost
		// on its next run and the two tools fight over it in every diff.
		h.pointerSkipped(w, absAgent, generatedSkipReason(banner, t.profile.Name, link.FromRoot))
		return nil
	}
	if isLink {
		// Through a link: an atomic write at the end's real path (the
		// links of the chain stay links), keeping the end's mode; a write
		// that fails there is a skip with the reason, not a failed render.
		if err := atomicWriteFile(writeAt, []byte(out), wire.PointerFileMode(writeAt, fileMode)); err != nil {
			h.pointerSkipped(w, absAgent, "it is a symbolic link to "+writeAt+", which cannot be written ("+err.Error()+")")
			return nil
		}
		fmt.Fprintf(w, "%s %s (pointer → %s, written through the link to %s)\n", h.decor.check(), absAgent, link.FromPointer, writeAt)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(writeAt), dirMode); err != nil {
		return wire.WriteError("create the folder of", absAgent, err, renderWriteHint)
	}
	if err := writeFileInDir(writeAt, []byte(out), fileMode); err != nil {
		return wire.WriteError("write", absAgent, err, renderWriteHint)
	}
	fmt.Fprintf(w, "%s %s (pointer → %s)\n", h.decor.check(), absAgent, link.FromPointer)
	return nil
}

// generatedSkipReason is the skip note of a pointer file another tool
// generates (banner: its banner line): where the pointer goes instead.
func generatedSkipReason(banner, profile, lorePath string) string {
	other := "lore render --add-target " + toolprofile.NameCodex + " (AGENTS.md)"
	if profile == toolprofile.NameCodex {
		other = "lore render --add-target <profile> for another tool file"
	}
	return "its banner says another tool generates it (" + strconv.Quote(banner) + "), which would drop the pointer on its next run; add a line pointing at " + lorePath + " to that tool's source, or wire another file: " + other
}

// pointerSkipped prints the note for a pointer file left as it is.
func (h *Host) pointerSkipped(w *strings.Builder, file, why string) {
	fmt.Fprintf(w, "%s %s%s%s\n", h.decor.warn("⚠"), file, wire.PointerSkippedMark, why)
}

// renderWriteHint is the hint of a render write that failed for a reason
// other than a permission (wire.WriteError names the chmod for those).
const renderWriteHint = "check that the file and its folder can be written, then run lore render again"

// relSlash is target relative to base with slashes; fallback (the path as
// given) when the two cannot be related (different volumes).
func relSlash(base, target, fallback string) string {
	r, err := filepath.Rel(base, target)
	if err != nil {
		r = fallback
	}
	return filepath.ToSlash(r)
}

// fullFileName is the CQ2 fallback file, next to the generated LORE.md.
const fullFileName = "LORE.full.md"

// writeFullFile writes LORE.full.md when its bytes changed.
func (h *Host) writeFullFile(w *strings.Builder, path, body string, quiet bool) error {
	if existing, err := os.ReadFile(filepath.Clean(path)); err == nil && sha256Hex(existing) == sha256Hex([]byte(body)) {
		return nil
	}
	if err := atomicWriteFile(path, []byte(body), 0o644); err != nil {
		return wire.WriteError("write", path, err, renderWriteHint)
	}
	if !quiet {
		// A relative path with slashes, as the LORE.md line above names
		// its file (Windows printed ".lore\LORE.full.md" under
		// ".lore/LORE.md").
		shown := path
		if !filepath.IsAbs(path) {
			shown = filepath.ToSlash(path)
		}
		fmt.Fprintf(w, "%s %s (%d bytes, every pinned entry in full; not imported)\n", h.decor.check(), shown, len(body))
	}
	return nil
}

// atomicWriteFile writes data to path via tmp+fsync+rename+fsync-parent
//
// Steps:
//  1. Write to <path>.tmp.<rnd> in same directory
//  2. fsync the temp file
//  3. rename(temp, path) — atomic on same filesystem
//  4. fsync the parent directory (durability)
//
// Returns the first error from any step
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".lore.render.*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()

	cleanup := func() { _ = os.Remove(tmpName) }

	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write: %w", writeErr)
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fsync temp: %w", syncErr)
	}
	if chmodErr := tmp.Chmod(perm); chmodErr != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod: %w", chmodErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", closeErr)
	}

	if renameErr := osrename.Rename(tmpName, path); renameErr != nil {
		cleanup()
		return fmt.Errorf("rename: %w", renameErr)
	}

	// fsync parent directory for durability
	dirf, err := os.Open(filepath.Clean(dir))
	if err == nil {
		_ = dirf.Sync()
		_ = dirf.Close()
	}
	return nil
}

// resolveSymlinkTarget is the file a write to path lands on: the end of
// its chain of links (wire.LinkEnd), or path itself when it is not a link
// or the chain cannot be read (it never fails; render reports that error).
func (h *Host) resolveSymlinkTarget(path string) string {
	end, _, err := wire.LinkEnd(path)
	if err != nil {
		return path
	}
	return end
}

// persistRenderHistory records the render for `lore why-context` and
// `render --trend`, with what the render left out of the core (cut, 6.11),
// in the machine store (P3-LOCAL-5). Its time is the core's wall clock,
// which is cliClock (a test build's LORE_NOW fixes it).
func (h *Host) persistRenderHistory(ctx context.Context, projectID, repoID, target, body string, summary renderResult, cut cutSummary) error {
	scope := renderScopeSummary{Included: summary, Cut: &cut}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	return h.recordRender(ctx, wire.RenderRecord{ProjectID: projectID, RepoID: repoID, TargetPath: target, Text: body,
		SHA256: sha256Hex([]byte(body)), TotalBytes: len(body), ScopeSummary: string(scopeJSON)})
}

// sha256Hex returns the hex-encoded sha256 of data
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
