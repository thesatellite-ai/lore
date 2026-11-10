package wire

// hostbase_doctor.go — the base section of `lore doctor` for a binary
// without lore.db (cmd/lore): the [identity] section, the PATH check and the project rows of
// v0.1.17's runDoctor (saas/cmd/cli/doctor.go steps 3, 4, 9-10b), which the
// old binary's doctorHost supplies. cmd/lore hands it to the app as
// Options.Context.Doctor (the live hook wraps it, live_doctor.go), so the
// report no longer shows an empty identity:
//
//	CoreDoctorBase ─▶ identity: the chain every write uses; ephemeral or
//	               │  unresolvable ─▶ warning, degraded
//	               ├▶ PATH: every "lore" executable on $PATH; more than one
//	               │  ─▶ warning, degraded
//	               ├▶ [sync] (in a project): row files and git state of
//	               │  .lore/data (hostbase_doctor_sync.go, E-GAPS-1 G4)
//	               └▶ project rows (in a project only; v0.1.17's runDoctor
//	                  steps 9-10b): repo addresses (PROV2, Q5), duplicate
//	                  imports (PROV9), the sub-task tree (TASK 6.13),
//	                  quarantined metadata (METADATA 3.13), secret rules
//	                  switched off (D2/D9), CI workflows pinning lore below
//	                  min_lore_version (FORMAT_EVOLUTION 4), the
//	                  CLAUDE.local.md checks (§6.6), the project pin below
//	                  the floor (an error, FORMAT_EVOLUTION 8.8) or not this
//	                  binary (a note)
//
// There is no lore.db to check, so DBOK stays true and the WAL size 0.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/selfupdate"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// doctor base words (v0.1.17's).
const (
	doctorEphemeralWarning = "identity is ephemeral; future sessions won't link"
	doctorPathWarning      = "multiple lore binaries on PATH: %s"
	doctorIdentityWarning  = "identity: cannot resolve: %v"
	// pathEnv is the search path variable.
	pathEnv = "PATH"
	// windowsGOOS and windowsExe: on Windows the binary is lore.exe.
	windowsGOOS = "windows"
	windowsExe  = ".exe"
	// execBits are the permission bits of an executable file.
	execBits fs.FileMode = 0o111
)

// CoreDoctorBase is the doctor base of a binary without lore.db
// (opsdoctor.Host.Base); fix repairs reversed --supersedes links after the
// caller's confirmation (E_NEEDS_CONFIRM without it).
func (a *App) CoreDoctorBase(ctx context.Context, fix bool) (opsdoctor.Base, error) {
	b := opsdoctor.Base{Status: opsdoctor.StatusHealthy, DBOK: true}
	warn := func(msg string) {
		b.Warnings = append(b.Warnings, msg)
		if b.Status == opsdoctor.StatusHealthy {
			b.Status = opsdoctor.StatusDegraded
		}
	}
	if r, err := resolveIdentity(ctx, a.opts); err != nil {
		warn(fmt.Sprintf(doctorIdentityWarning, err))
	} else {
		b.Identity = opsdoctor.Identity{Resolved: r.StableKey, Source: r.Source(), Stable: r.Stable()}
		if !r.Stable() {
			warn(doctorEphemeralWarning)
		}
	}
	if found := lorePathBinaries(os.Getenv(pathEnv)); len(found) > 1 {
		b.PathConflicts = found
		warn(fmt.Sprintf(doctorPathWarning, strings.Join(found, ", ")) + pathShadowDetail(ctx, found))
	}
	a.doctorUnpushedStores(ctx, warn) // hostbase_doctor_stores.go (machine-wide, so outside a project too)
	if _, err := a.Engine(ctx); err != nil {
		return b, nil // not a lore project: the project rows have nothing to read
	}
	note := func(msg string) { b.Notes = append(b.Notes, msg) }
	b.Sync = a.doctorSync(ctx, warn, note) // hostbase_doctor_sync.go (E-GAPS-1 G4)
	a.doctorPushModes(ctx, warn, note)     // storepushops.go: each store's push mode
	b.RepoAddresses = a.doctorRepoAddresses(ctx, warn, note)
	a.doctorDuplicateImports(ctx, warn)
	a.doctorTaskTree(ctx, warn)
	a.doctorMetaQuarantine(ctx, warn)
	a.doctorSecretRulesOff(ctx, note)
	a.doctorCIPins(ctx, warn)
	a.doctorClaudeLocal(ctx, warn)
	a.doctorSignals(ctx, warn, note) // hostbase_doctor_signals.go: expired files only report (a note: they do not change the status)
	if pin, floorErr := a.doctorVersionPin(ctx); floorErr != "" {
		b.Errors = append(b.Errors, floorErr)
		b.Status = opsdoctor.StatusBroken
	} else if pin != "" {
		note(pin)
	}
	fail := func(msg string) {
		b.Errors = append(b.Errors, msg)
		if b.Status == opsdoctor.StatusHealthy {
			b.Status = opsdoctor.StatusDegraded
		}
	}
	if err := a.doctorSupersedes(ctx, fix, warn, note, fail); err != nil { // hostbase_doctor_supersede.go
		return opsdoctor.Base{}, err
	}
	return b, nil
}

// doctorRepoAddresses is the "Repo addresses" check (PROV2, Q5): an
// unconfirmed address is a note, a credential in a remote or this clone
// naming another repo's address a warning. It returns the report as JSON
// (nil: no repo id yet, or it cannot be read).
func (a *App) doctorRepoAddresses(ctx context.Context, warn, note func(string)) []byte {
	rep, err := a.RepoAddressStatus(ctx)
	if err != nil || rep.RepoID == "" {
		return nil
	}
	for _, n := range rep.Notices {
		line := "repo address " + n.Key + " is unconfirmed (lore repo set-url " + n.Key + ", add --mirror for another address"
		if !n.CaseOnly {
			line += ", or lore repo fork in a fork"
		}
		note(line + ")")
	}
	for _, w := range rep.Warnings {
		warn("a credential is in git remote " + w.Remote + "; remove it: git remote set-url " + w.Remote + " " + w.Stored)
	}
	if rep.Mismatch != "" {
		warn(rep.Mismatch + "; if this clone is the fork, restore the fork's .lore/data/_meta.json (git checkout <fork commit> -- .lore/data/_meta.json) or run `lore repo fork`")
	}
	data, err := json.Marshal(rep)
	if err != nil {
		return nil
	}
	return data
}

// doctorDuplicateImports is PROV9's duplicate_import: two live tasks
// imported under one external id; reported with the keep-the-oldest fix,
// never merged here.
func (a *App) doctorDuplicateImports(ctx context.Context, warn func(string)) {
	dups, err := opstask.FindDuplicateImports(ctx, a)
	if err != nil {
		warn("duplicate_import: the tasks could not be read: " + err.Error())
		return
	}
	for _, d := range dups {
		warn("duplicate_import " + d.Source + " " + d.ExtID + ": " + strings.Join(d.IDs, ", ") +
			"; keep the oldest: " + strings.Join(d.FixCommand(), "; "))
	}
}

// doctorTaskTree is the sub-task tree check (TASK 6.13): cycles, orphans,
// open children of closed parents, depth over tasks.max_depth; a shared
// sibling rank needs no repair and is skipped.
func (a *App) doctorTaskTree(ctx context.Context, warn func(string)) {
	found, err := opstask.CheckTree(ctx, a)
	if err != nil {
		warn("task_tree: the tasks could not be read: " + err.Error())
		return
	}
	for _, f := range found {
		if f.Kind == opstask.FindSharedRank {
			continue
		}
		line := "task_tree " + string(f.Kind) + ": " + f.Message
		if f.Fix != "" {
			line += "; fix: " + f.Fix
		}
		warn(line)
	}
}

// doctorMetaQuarantine counts quarantined metadata (METADATA 3.13): one
// warning per row, naming keys and rules, never values.
func (a *App) doctorMetaQuarantine(ctx context.Context, warn func(string)) {
	qs, err := a.MetaQuarantined(ctx)
	if err != nil {
		return // the cache problem is the cache check's to report
	}
	for _, q := range qs {
		warn("quarantined metadata: " + q.Table + " " + q.ID + ": " + strings.Join(q.Problems, "; ") + " (fix with lore meta set " + q.ID + " …, or lore meta unset)")
	}
}

// doctorSecretRulesOff lists every secret rule switched off at some level
// (D2/D9: what is NOT scanned must be visible), with the command that
// turns it back on. The LORE_SYNC_ALLOW_SECRETS warning is the core
// check DoctorRemovedEnv.
func (a *App) doctorSecretRulesOff(ctx context.Context, note func(string)) {
	e, ok := a.Registry().Lookup(opssettings.OpSecretRules)
	if !ok {
		return
	}
	res, err := runDirect(ctx, e, op.JSONDecoder{Body: []byte("{}")})
	if err != nil {
		note("secret rules could not be read: " + err.Error())
		return
	}
	raw, err := res.JSON()
	if err != nil {
		return
	}
	var out opssettings.RulesOut
	if err := json.Unmarshal(raw, &out); err != nil {
		note("secret rules could not be decoded: " + err.Error())
		return
	}
	for _, rule := range out.Data {
		if rule.State != schema.SecretRuleOff {
			continue
		}
		line := "secret rule " + rule.RuleID + " is off at " + rule.Level
		if rule.Reason != "" {
			line += " (" + rule.Reason + ")"
		}
		note(line + "; turn it back on: lore secrets rule on " + rule.RuleID + " --to " + rule.Store)
	}
}

// workflowsDir is where GitHub keeps CI workflows (FORMAT_EVOLUTION 4).
const workflowsDir = ".github/workflows"

// workflowGlobs are the workflow file patterns.
var workflowGlobs = []string{"*.yml", "*.yaml"}

// doctorCIPins warns for every lore version a CI workflow pins below the
// project's min_lore_version: that CI runs a read-only lore.
func (a *App) doctorCIPins(ctx context.Context, warn func(string)) {
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return
	}
	d, ok, err := rowfs.ReadRepoFormat(top)
	if err != nil || !ok || d.MinLoreVersion == "" {
		return
	}
	wf := os.DirFS(filepath.Join(top, filepath.FromSlash(workflowsDir))) // reads stay inside the workflows folder
	for _, pat := range workflowGlobs {
		files, _ := fs.Glob(wf, pat) // only ErrBadPattern, and the patterns are fixed
		for _, f := range files {
			text, rerr := fs.ReadFile(wf, f)
			if rerr != nil {
				continue
			}
			for _, p := range formatgate.CIPinsBelow(formatgate.CIPins(string(text)), d.MinLoreVersion) {
				warn(fmt.Sprintf("%s:%d pins lore %s, below this project's min_lore_version %s: that CI is read-only here; pin %s or later (`%s upgrade --version` in the workflow)",
					path.Join(workflowsDir, f), p.Line, p.Version, d.MinLoreVersion, d.MinLoreVersion, doctorBinaryName))
			}
		}
	}
}

// pathShadowDetail says which of found (PATH order, more than one) a bare
// `lore` runs, its version and what it shadows, and whether that is this
// binary: git hooks and agents run the first one, so a person who runs this
// binary by its path otherwise cannot tell why a hook or an agent behaves
// like another release (two lores on PATH, 2026-11-08).
func pathShadowDetail(ctx context.Context, found []string) string {
	first := found[0]
	ver := "version unknown"
	if v, ok := binaryLoreVersion(ctx, first); ok {
		ver = "lore " + v
	}
	self, _ := os.Executable()
	var shadowed []string
	for _, p := range found[1:] {
		if self != "" && sameFile(self, p) {
			p += " (this binary)"
		}
		shadowed = append(shadowed, p)
	}
	d := fmt.Sprintf("; `%s` runs %s (%s), which shadows %s; git hooks and agents run %s", doctorBinaryName, first, ver, strings.Join(shadowed, ", "), first)
	if self != "" && sameFile(self, first) {
		d += " (this binary)"
	} else {
		d += ", not this binary: put the one you want first on PATH or remove the other"
	}
	return d
}

// lorePathBinaries lists every lore executable in the folders of path, in
// PATH order, each once (v0.1.17 ran `which -a lore`; this reads the
// folders itself, so it needs no external program).
func lorePathBinaries(path string) []string {
	name := doctorBinaryName
	if runtime.GOOS == windowsGOOS {
		name += windowsExe
	}
	seen := map[string]bool{}
	var out []string
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		p := filepath.Clean(filepath.Join(dir, name))
		// One program reached through two PATH folders (merged /usr's
		// /bin → /usr/bin, a symlink to the installed binary) is one
		// binary: dedupe on the resolved file, print the first path.
		key := p
		if real, err := filepath.EvalSymlinks(p); err == nil {
			key = real
		}
		if seen[key] {
			continue
		}
		info, err := fs.Stat(os.DirFS(dir), name) // name is the fixed binary name: the read stays in dir
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != windowsGOOS && info.Mode().Perm()&execBits == 0) {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

// claudeLocalFile is the per-checkout import file of Claude Code (render
// writes one import line into it; internal context lives in ~/.lore/render).
const claudeLocalFile = "CLAUDE.local.md"

// doctorClaudeLocal warns when the checkout's CLAUDE.local.md is a symlink
// (lore never writes through one), tracked by git (its content would be
// committed) or holds more than the one import line.
func (a *App) doctorClaudeLocal(ctx context.Context, warn func(string)) {
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return
	}
	local := filepath.Join(top, claudeLocalFile)
	fi, err := os.Lstat(local)
	if err != nil {
		return
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		warn(claudeLocalFile + " is a symlink; lore will not write through it (delete it and run `lore render`)")
		return
	}
	if out, gerr := (gitRun{dir: top, args: []string{"ls-files", "--", claudeLocalFile}}).output(ctx); gerr == nil && strings.TrimSpace(string(out)) != "" {
		warn(claudeLocalFile + " is tracked by git: its content would be committed (`git rm --cached " + claudeLocalFile + "`)")
	}
	if text, rerr := os.ReadFile(filepath.Clean(local)); rerr == nil && strings.Count(strings.TrimSpace(string(text)), "\n") > 0 {
		warn(claudeLocalFile + " holds more than the one import line; internal context belongs in ~/.lore/render")
	}
}

// envVersionPin is the CI pin; it overrides the team pin (FORMAT_EVOLUTION 8.4).
const envVersionPin = "LORE_VERSION_PIN"

// pinMeta is the one _meta.json key the pin check reads.
type pinMeta struct {
	Pin string `json:"lore_version_pin"`
}

// doctorVersionPin checks the project pin (LORE_VERSION_PIN, else
// lore_version_pin in .lore/data/_meta.json) against min_lore_version and
// this binary: floorErr when the pin is below the floor (everyone on it is
// read-only), else note when this binary is not the pinned one.
func (a *App) doctorVersionPin(ctx context.Context) (note, floorErr string) {
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return "", ""
	}
	metaRel := rowfs.LoreDir + "/" + rowfs.DataDir + "/" + rowfs.MetaFileName
	pin, source := strings.TrimSpace(os.Getenv(envVersionPin)), envVersionPin
	if pin == "" {
		raw, rerr := fs.ReadFile(os.DirFS(top), metaRel) // a fixed path inside the checkout
		var m pinMeta
		if rerr != nil || json.Unmarshal(raw, &m) != nil || strings.TrimSpace(m.Pin) == "" {
			return "", ""
		}
		pin, source = strings.TrimSpace(m.Pin), "lore_version_pin in "+metaRel
	}
	norm := func(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }
	if d, ok, ferr := rowfs.ReadRepoFormat(top); ferr == nil && ok && d.MinLoreVersion != "" && formatgate.VersionBelow(pin, d.MinLoreVersion) {
		return "", fmt.Sprintf("the pinned lore %s (%s) is below this project's min_lore_version %s: everyone on the pin is read-only; raise the pin",
			norm(pin), source, norm(d.MinLoreVersion))
	}
	if v := a.opts.Version; norm(pin) != norm(v) && !selfupdate.IsDevBuild(v) {
		return fmt.Sprintf("this lore is %s; the project pins %s (%s): run `%s upgrade`", norm(v), norm(pin), source, doctorBinaryName), ""
	}
	return "", ""
}
