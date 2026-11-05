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
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

type renderFlags struct {
	commonFlags
	outPath string
	// targets are --target values: profile names or pointer files. Empty
	// means the repo's context targets (its context.targets row, else
	// toolprofile.Resolve).
	targets   []string
	noPointer bool
	dryRun    bool
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

func runRender(ctx context.Context, f *renderFlags) (err error) {
	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer joinClose(&err, client, "the project DB")

	projectID, err := resolveProjectID(ctx, rctx.ProjectID)
	if err != nil {
		return err
	}
	repoID, err := resolveRepoID(ctx, projectID, rctx.RepoMount)
	if err != nil {
		return err
	}
	if f.scopes && !f.report {
		return errcodes.New(errcodes.InvalidInput, "--scopes needs --report").WithHint("run `lore render --report --scopes`")
	}
	if f.trend { // 6.11: read-only, from render_history
		return renderTrend(ctx, projectID, f)
	}

	// ds:def id=render-pins-ez0g6hen owner=@khanakia stability=stable desc="what render pins into the context file (must rules, critical/high hotfixes, directive)"
	// Hybrid: pin only severity=must rules + critical/high hotfixes +
	// directive into LORE.md. Everything else (should/may rules,
	// memories, decisions, patterns, …) is fetched via `lore search`
	// on demand per the directive's pre-response checklist
	var skips pinSkips
	rules, err := loadPinnedRules(ctx, projectID, &skips)
	if err != nil {
		return err
	}
	hotfixes, err := loadPinnedHotfixes(ctx, projectID, &skips)
	if err != nil {
		return err
	}
	// SECRETS 12 / 13.2: references filled for the committed render; a
	// value that brings a secret in refuses it (D23).
	if rules, hotfixes, err = expandPinned(ctx, rules, hotfixes); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	// CQ6: the budget is the smallest of the targets' budgets; the targets
	// are resolved silently here and announced when stitched below.
	pick, err := resolveTargets(ctx, root, f.targets, coreContextTargets)
	if err != nil {
		return err
	}
	profiles := make([]toolprofile.Profile, 0, len(pick.targets))
	for _, t := range pick.targets {
		profiles = append(profiles, t.profile)
	}
	topics, err := loadTopicIndex(ctx)
	if err != nil {
		return err
	}
	// 6.2: folder-scoped rows go to <dir>/LORE.md (render_scopes.go); the
	// full file still holds every scope (CQ2).
	allRules, allHotfixes := rules, hotfixes
	rules, hotfixes, dirs := planDirs(ctx, root, rules, hotfixes)
	// 6.7 claude_rules / cursor_rules: glob rules go to the tools' own path
	// files when every target loads them (render_pathrules.go, FA-CTX-2).
	rules, pathGroups := planPathRules(rules, profiles)
	skips.IDs = append(skips.IDs, pathRuleIDs(pathGroups)...)
	skips.PathScoped = len(pathRuleIDs(pathGroups))
	in := renderInput{Settings: loadContextSettings(ctx, profiles), Rules: rules, Hotfixes: hotfixes, Topics: topics, Skips: skips, Dirs: dirs.Groups}
	// 6.13 (RES-OQ3b) and META 3.14 (FA-META-10): the resource index and
	// the metadata keys block of the committed sources, after the core and
	// only when they fit (FA-CTX-7).
	if in.Tails, err = renderTails(ctx); err != nil {
		return err
	}
	dirBodies, dirReps := buildDirFiles(dirs, in.Settings.EntryMax)

	// Build the rendered text. The canary is content-derived (sha256 of the
	// body BEFORE the canary line is added) so that two renders of the
	// SAME db state produce byte-identical output — required by SC-12
	// A fresh per-session id wouldn't satisfy the "same DB → same render"
	// invariant. Prompt-injection detection still works: the canary is
	// unique-per-content, and the renderhistory row stores it for lookup
	contentForCanary, _ := buildBudgetedFile("PLACEHOLDER", in)
	canary := ids.PrefixRenderHistory + "_" + sha256Hex16(contentForCanary)
	body, report := buildBudgetedFile(canary, in)
	full := buildFullFile(allRules, allHotfixes)
	report.File, report.Before = f.outPath, len(full)
	report.Dirs, report.DirsOff = dirReps, !dirs.Settings.On && len(dirs.All) > 0

	if f.report && f.scopes {
		// 6.2.6: the per-file table; writes nothing.
		sr := buildScopeReport(ctx, report, dirs, dirBodies, dirReps, pick.targets)
		if f.asJSON {
			data, jerr := sr.jsonBody()
			if jerr != nil {
				return errcodes.New(errcodes.Internal, "encode report").WithCause(jerr)
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
				return errcodes.New(errcodes.Internal, "encode report").WithCause(jerr)
			}
			fmt.Fprintln(f.out(), string(data))
			return nil
		}
		fmt.Fprint(f.out(), report.reportText())
		return nil
	}
	if f.check {
		// 6.11: CI dry run; fails above warn_at or on budget_exceeded.
		// DECISION OV18-1: the failure is E_REFUSED (no new code).
		if report.Over || report.WarnExceeded {
			return errcodes.New(errcodes.Refused, fmt.Sprintf("%s would be %d bytes: over the warn level (%d%% of %d, profile %s)",
				f.outPath, report.After, report.WarnPct, report.Budget, report.Profile)).
				WithHint("run `lore render --report` to see what is in the core, and `lore lint --context` for clean-up")
		}
		fmt.Fprintf(f.out(), "%s %s would be %d of %d bytes (%d%%; warn at %d%%)\n", style.Success("✓"), f.outPath,
			report.After, report.Budget, report.After*percent/max(report.Budget, 1), report.WarnPct)
		return nil
	}

	if f.dryRun {
		fmt.Fprint(f.out(), body)
		// The private per-store files (spec 7.3, CFG6), shown, not written.
		return renderPrivate(ctx, f.out(), root, true, f.quiet)
	}

	// Generated content goes to the out file (default .lore/LORE.md), never
	// into a tool's own file — so hand-written content there is never
	// clobbered. Each tool file only receives an idempotent pointer block
	// (stitchPointer below)

	// Resolve symlinks (R27 #19): if the out file is a symlink, write to target
	resolvedOut := resolveSymlinkTarget(f.outPath)

	// Skip rename if content unchanged (R29 #53) — but still re-stitch the
	// pointer: a tool file may have lost it even when the body is identical
	unchanged := false
	if existing, rerr := os.ReadFile(filepath.Clean(resolvedOut)); rerr == nil {
		unchanged = sha256Hex(existing) == sha256Hex([]byte(body))
	}

	if unchanged {
		if !f.quiet {
			fmt.Fprintf(f.out(), "%s %s (unchanged)\n", style.Muted("·"), resolvedOut)
		}
	} else {
		if err = atomicWriteFile(resolvedOut, []byte(body), 0o644); err != nil {
			return errcodes.New(errcodes.Internal, "atomic write").WithCause(err)
		}
		if !f.quiet {
			fmt.Fprintf(f.out(), "%s %s (%d bytes, budget %d %s; %d full + %d line rules, %d + %d hotfixes, %d topics)\n",
				style.Success("✓"), resolvedOut, len(body), report.Budget, report.Profile,
				report.MustFull, report.MustLine, report.Critical, report.HighLine, report.Topics)
		}
	}
	if report.Over {
		fmt.Fprintln(os.Stderr, style.Warn(fmt.Sprintf("WARN: budget_exceeded: %s is %d bytes, over the %d-byte budget of %s; every must rule and critical/high hotfix is still written (lore render --report)",
			resolvedOut, report.After, report.Budget, report.Profile)))
	}
	// CQ2: the full fallback file, next to LORE.md, never imported.
	if in.Settings.FullFile {
		if err = writeFullFile(f.out(), filepath.Join(filepath.Dir(resolvedOut), fullFileName), full, f.quiet); err != nil {
			return err
		}
	}

	if !f.noPointer {
		if !f.quiet {
			printTargetPick(f.out(), pick)
		}
		for _, t := range pick.targets {
			if err = stitchPointer(f.out(), root, t, resolvedOut); err != nil {
				return err
			}
		}
	}
	// 6.7: path-scoped rule files per target; stale lore-path-* files go.
	reg, err := loadToolRegistry(ctx)
	if err != nil {
		return err
	}
	if err := writePathRules(f.out(), root, pathGroups, pick.targets, reg.Profiles(), loadPathFileBudget(ctx), f.quiet); err != nil {
		return errcodes.New(errcodes.Internal, "path rule files").WithCause(err)
	}
	// 6.2.5: folder files and each target's folder file; stale ones go.
	if err := writeDirs(f.out(), root, dirs, dirBodies, pick.targets, len(body), f.quiet); err != nil {
		return errcodes.New(errcodes.Internal, "folder files").WithCause(err)
	}

	if err := persistRenderHistory(ctx, projectID, repoID, resolvedOut, body, renderResult{
		Rules: len(rules), Hotfixes: len(hotfixes),
	}, newCutSummary(report, skips)); err != nil {
		// Non-fatal: file written, history failed. Log and continue
		fmt.Fprintln(os.Stderr, style.Warn("WARN: render_history write failed: "+err.Error()))
	}

	// Second target (F20, spec 7.3, CFG6): each internal store's rows go to
	// a private file ~/.lore/render/<repo_id>/<store_id>.md, outside the
	// checkout, with only import lines in CLAUDE.local.md (render_private.go).
	// The automatic re-render after a sync (quiet) leaves it to the next
	// explicit `lore render`, so a plain command never reaches a store for it.
	if !f.quiet {
		if err := renderPrivate(ctx, f.out(), root, false, f.quiet); err != nil {
			return err
		}
	}

	return nil
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
func loadToolRegistry(ctx context.Context) (*toolprofile.Registry, error) {
	cfg, err := loadTeamConfig(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := toolprofile.New(cfg.Context.Profiles)
	if err != nil {
		return nil, errcodes.New(errcodes.ConfigInvalid, "context.profiles in ~/.lore/config.yaml").
			WithCause(err).WithHint("run `lore config check` for every problem with its location")
	}
	return reg, nil
}

// rootProbe reads pointer files under a repo root for toolprofile.Resolve.
type rootProbe struct{ root string }

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
func coreContextTargets(ctx context.Context) ([]string, bool, error) {
	if coreApp == nil {
		return nil, false, nil
	}
	return contextTargetsOf(ctx, coreApp)
}

// contextTargetsOf reads the effective context.targets of app's repo.
func contextTargetsOf(ctx context.Context, app *wire.App) ([]string, bool, error) {
	eng, err := app.Engine(ctx)
	if err != nil {
		if errs.Has(err, errs.ConfigInvalid) {
			return nil, false, err
		}
		fmt.Fprintln(os.Stderr, style.Warn("WARN: could not read the context.targets setting ("+err.Error()+"); using the repo's tool files"))
		return nil, false, nil
	}
	set, err := eng.Policy(store.SourceRepo)
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
}

// printTargetPick prints how the targets were chosen (silent for an
// already-wired repo and for the settings row: nothing new to say) and
// names each tool file found but not wired.
func printTargetPick(w *strings.Builder, pick targetPick) {
	switch pick.sel.Reason {
	case toolprofile.ReasonDetected, toolprofile.ReasonFallback:
		fmt.Fprintf(w, "%s context targets: %s (%s)\n", style.Muted("·"), describeTargets(pick.targets), describeReason(pick.sel.Reason))
	case toolprofile.ReasonWired, toolprofile.ReasonSetting:
	}
	// TOOL NEUTRAL, no surprises: a tool file added after lore was wired
	// (say CLAUDE.md in an AGENTS.md repo) is not wired behind the user's
	// back; render names it and the command that adds it.
	for _, p := range pick.sel.Unwired {
		hint := "lore render --target " + p.Name
		if pick.sel.Reason == toolprofile.ReasonSetting {
			hint = "lore policy set " + string(policy.KeyContextTargets) + " <profiles incl. " + p.Name + "> --yes"
		}
		fmt.Fprintf(w, "%s %s found but not wired to lore; add it with: %s\n", style.Muted("·"), p.PointerFile, hint)
	}
	// r3-targets (DECISION P2-OLDCLI-2): a file that carries lore's pointer
	// but is not in context.targets is left as it is; say so and how to
	// list it, instead of skipping it silently.
	for _, p := range pick.sel.Unlisted {
		fmt.Fprintf(w, "%s %s carries lore's pointer but %s does not name it, so render leaves it as is; list it with: lore policy set %s <profiles incl. %s> --yes\n",
			style.Muted("·"), p.PointerFile, policy.KeyContextTargets, policy.KeyContextTargets, p.Name)
	}
}

// resolveTargets turns --target values into tool files, or, with none,
// resolves the repo's own targets: its context.targets settings row
// (read through setting, Q-R2-3), else the pointer-file detection
// (toolprofile.Resolve). A value is a profile name, a known profile's
// pointer file, or any other file, wired with an @import pointer as
// v0.1.17's --target was. TOOL NEUTRAL: no tool file is assumed.
func resolveTargets(ctx context.Context, root string, values []string, setting targetSetting) (targetPick, error) {
	reg, err := loadToolRegistry(ctx)
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
				return targetPick{}, errs.Wrap(errs.InvalidInput, "the repo's "+string(policy.KeyContextTargets)+" setting", selErr).
					WithHint("change it with: lore policy set " + string(policy.KeyContextTargets) + " <profiles> --yes, or add the profile under context.profiles in ~/.lore/config.yaml")
			}
			pick.sel = sel
		} else {
			pick.sel = reg.Resolve(rootProbe{root: root})
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

// stitchPointer ensures t's tool file carries the lore pointer block,
// bounded by sentinel markers. Idempotent: an existing block is replaced in
// place; a missing one is placed at the top (after a Cursor rule's
// frontmatter); if nothing changes the file is left untouched. An @import
// path is computed RELATIVE to the tool file's directory so the tool
// resolves it regardless of cwd. A profile without a pointer file
// (generic) has nothing to stitch.
func stitchPointer(w *strings.Builder, root string, t contextTarget, outFile string) error {
	if !t.profile.HasPointerFile() {
		return nil
	}
	agentFile := t.file
	if !filepath.IsAbs(agentFile) {
		agentFile = filepath.Join(root, filepath.FromSlash(agentFile))
	}
	absAgent, err := filepath.Abs(agentFile)
	if err != nil {
		return errcodes.New(errcodes.BadPath, "resolve "+agentFile).WithCause(err)
	}
	absOut, err := filepath.Abs(outFile)
	if err != nil {
		return errcodes.New(errcodes.BadPath, "resolve "+outFile).WithCause(err)
	}
	link := toolprofile.Link{FromPointer: relSlash(filepath.Dir(absAgent), absOut, outFile), FromRoot: relSlash(root, absOut, outFile)}

	existing := ""
	if data, err := os.ReadFile(filepath.Clean(absAgent)); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return errcodes.New(errcodes.Internal, "read "+absAgent).WithCause(err)
	}

	out := t.profile.StitchPointer(existing, link)
	if out == existing {
		fmt.Fprintf(w, "%s %s (pointer present)\n", style.Muted("="), absAgent)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(absAgent), cliDirMode); err != nil {
		return errcodes.New(errcodes.Internal, "mkdir parent").WithCause(err)
	}
	if err := writeFileInDir(absAgent, []byte(out), cliFileMode); err != nil {
		return errcodes.New(errcodes.Internal, "write "+absAgent).WithCause(err)
	}
	fmt.Fprintf(w, "%s %s (pointer → %s)\n", style.Success("✓"), absAgent, link.FromPointer)
	return nil
}

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
func writeFullFile(w *strings.Builder, path, body string, quiet bool) error {
	if existing, err := os.ReadFile(filepath.Clean(path)); err == nil && sha256Hex(existing) == sha256Hex([]byte(body)) {
		return nil
	}
	if err := atomicWriteFile(path, []byte(body), 0o644); err != nil {
		return errcodes.New(errcodes.Internal, "write "+path).WithCause(err)
	}
	if !quiet {
		fmt.Fprintf(w, "%s %s (%d bytes, every pinned entry in full; not imported)\n", style.Success("✓"), path, len(body))
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
	if err := os.MkdirAll(dir, cliDirMode); err != nil {
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

	if renameErr := os.Rename(tmpName, path); renameErr != nil {
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

// resolveSymlinkTarget returns the target if path is a symlink; path otherwise.
// Any lookup failure falls back to path itself, so it never fails.
// (R27 #19 — atomic-write should preserve user's symlink layout.)
func resolveSymlinkTarget(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		// File doesn't exist yet — return path as-is
		return path
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	target, err := os.Readlink(abs)
	if err != nil {
		return path
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(abs), target)
	}
	return target
}

// persistRenderHistory records the render for `lore why-context` and
// `render --trend`, with what the render left out of the core (cut, 6.11),
// in the machine store (P3-LOCAL-5). Its time is the core's wall clock,
// which is cliClock (a test build's LORE_NOW fixes it).
func persistRenderHistory(ctx context.Context, projectID, repoID, target, body string, summary renderResult, cut cutSummary) error {
	scope := renderScopeSummary{Included: summary, Cut: &cut}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	return coreRecordRender(ctx, wire.RenderRecord{ProjectID: projectID, RepoID: repoID, TargetPath: target, Text: body,
		SHA256: sha256Hex([]byte(body)), TotalBytes: len(body), ScopeSummary: string(scopeJSON)})
}

// sha256Hex returns the hex-encoded sha256 of data
// sha256Hex16 returns the first 16 hex chars (64 bits) of sha256(s)
// Used for the content-derived render canary
func sha256Hex16(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:8])
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
