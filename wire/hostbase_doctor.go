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
//	               └▶ project rows (in a project only; v0.1.17's runDoctor
//	                  steps 9-10b): repo addresses (PROV2, Q5), duplicate
//	                  imports (PROV9), the sub-task tree (TASK 6.13),
//	                  quarantined metadata (METADATA 3.13), secret rules
//	                  switched off (D2/D9), CI workflows pinning lore below
//	                  min_lore_version (FORMAT_EVOLUTION 4)
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
// (opsdoctor.Host.Base; fix has nothing to repair here).
func (a *App) CoreDoctorBase(ctx context.Context) opsdoctor.Base {
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
		b.Identity = opsdoctor.Identity{Resolved: r.StableKey, Source: string(r.Step), Stable: r.Stable()}
		if !r.Stable() {
			warn(doctorEphemeralWarning)
		}
	}
	if found := lorePathBinaries(os.Getenv(pathEnv)); len(found) > 1 {
		b.PathConflicts = found
		warn(fmt.Sprintf(doctorPathWarning, strings.Join(found, ", ")))
	}
	if _, err := a.Engine(ctx); err != nil {
		return b // not a lore project: the project rows have nothing to read
	}
	note := func(msg string) { b.Notes = append(b.Notes, msg) }
	b.RepoAddresses = a.doctorRepoAddresses(ctx, warn, note)
	a.doctorDuplicateImports(ctx, warn)
	a.doctorTaskTree(ctx, warn)
	a.doctorMetaQuarantine(ctx, warn)
	a.doctorSecretRulesOff(ctx, note)
	a.doctorCIPins(ctx, warn)
	return b
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
		if seen[p] {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != windowsGOOS && info.Mode().Perm()&execBits == 0) {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
