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
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/selfupdate"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opslint "github.com/thesatellite-ai/lore/ops/lint"
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
	// The base checks below share one read of each store
	// (engine.WithReadSnapshot, item 41); the fixing one after them gets
	// its own. devco D-474: they are reads under --fix too, as in a plain
	// doctor (D-441): with the fix's write access the stale-link check's
	// project lookup made a store folder's missing project row, a store
	// commit no repair asked for. Only the repair below writes.
	fixCtx := ctx
	// A plain doctor run's checks reuse this snapshot (D-739:
	// EnsureReadSnapshot keeps the one ops/doctor runRepo made for the
	// pass); a fixing run reads afresh, as before.
	snap := engine.WithReadSnapshot(ctx)
	if !fix {
		snap = engine.EnsureReadSnapshot(ctx)
	}
	ctx = engine.WithAccess(snap, op.AccessRead)
	b.Sync = a.doctorSync(ctx, warn, note)             // hostbase_doctor_sync.go (E-GAPS-1 G4)
	waits := a.doctorStoreConflicts(ctx, b.Sync, warn) // doctor_store_conflicts.go (I-1)
	a.doctorPushModes(ctx, warn, note, waits)          // storepushops.go: each store's push mode
	b.RepoAddresses = a.doctorRepoAddresses(ctx, warn, note)
	a.doctorDuplicateImports(ctx, warn)
	a.doctorNearDuplicates(ctx, warn) // devco D-926: an import and its rewrite, both live
	a.doctorCopiedIDs(ctx, warn)      // devco D-929: one row live in two stores
	a.doctorStaleFacts(ctx, warn)     // devco:D-941: a fact whose evidence changed since it was verified
	a.doctorTaskTree(ctx, warn)
	a.doctorMetaQuarantine(ctx, warn)
	a.doctorSecretRulesOff(ctx, note)
	a.doctorTextAllows(ctx, note) // #129
	a.doctorCIPins(ctx, warn)
	a.doctorClaudeLocal(ctx, warn)
	a.doctorAttrOverride(ctx, warn)    // gitattr_override.go: a later .gitattributes line beats lore's block (devco:Q-11)
	a.doctorStaleLinks(ctx, warn)      // hostbase_doctor_links.go: links on rewritten commits (H-8)
	a.doctorLinkedLeftovers(ctx, warn) // hostbase_doctor_leftover.go: notes left in a linked repo (D-729)
	a.doctorSignals(ctx, warn, note)   // hostbase_doctor_signals.go: expired files only report (a note: they do not change the status)
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
	if fix {
		ctx = engine.WithReadSnapshot(fixCtx)
	}
	fixed := func(msg string) {
		b.Fixed = append(b.Fixed, msg)
		note(msg)
	}
	if err := a.doctorSupersedes(ctx, fix, warn, note, fail, fixed); err != nil { // hostbase_doctor_supersede.go
		return opsdoctor.Base{}, err
	}
	a.doctorLinkedRows(ctx, fix, warn, fixed) // hostbase_doctor_linkrows.go: rows a linked repo with no notes left does not need (D-736)
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
		line := "repo address " + n.Key + " is unconfirmed (lore repo set-url --url " + n.Key + ", add --mirror for another address"
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

// doctorNearDuplicates counts the pairs of live rows that repeat each
// other (devco D-926: an import and its rewrite a month later both stayed
// live, so every pinned rule rendered twice) and names the lint command
// that lists each pair with the command to archive one.
func (a *App) doctorNearDuplicates(ctx context.Context, warn func(string)) {
	found, err := opslint.FindNearDuplicates(ctx, a)
	if err != nil {
		warn(string(opslint.NearDuplicate) + ": the rows could not be read: " + err.Error())
		return
	}
	if len(found) == 0 {
		return
	}
	warn(string(opslint.NearDuplicate) + ": " + strconv.Itoa(len(found)) + " pair(s) of live rows say the same thing (an import and its rewrite, a copy in two folders), so both copies are rendered and found; each pair with the command that archives the older one: lore lint --code " + string(opslint.NearDuplicate))
}

// doctorCopiedIDs counts the rows live in two stores (devco D-929, bug
// hunt H-187: `lore store adopt` keeps ids, so an edit reached one copy and
// search kept finding the other's old text) and names the lint command
// that lists each with the command that keeps one copy.
func (a *App) doctorCopiedIDs(ctx context.Context, warn func(string)) {
	// devco:D-934: over the folders lists read too (a store attached
	// whole: lensFolders), where lore store adopt puts the copies
	var found []opslint.Finding
	err := a.withExtra(a.lensFolders(ctx), func() error {
		// open with the folders first: the store list FindCopiedIDs walks
		// (ReadStores) is empty until a read opens the state
		if _, eerr := a.Engine(ctx); eerr != nil {
			return eerr
		}
		var ferr error
		found, ferr = opslint.FindCopiedIDs(ctx, a)
		return ferr
	})
	if err != nil {
		warn(string(opslint.CopiedID) + ": the rows could not be read: " + err.Error())
		return
	}
	if len(found) == 0 {
		return
	}
	warn(string(opslint.CopiedID) + ": " + strconv.Itoa(len(found)) + " row(s) live in two stores (lore store adopt and a copied row file keep ids), so an edit reaches one copy only; list, search and ask show the newest; each with the command that keeps one copy: lore lint --code " + string(opslint.CopiedID))
}

// doctorStaleFacts counts the rows stating a fact about code whose
// evidence files changed in git after the fact was verified (devco:D-941,
// bug hunt H-190) and names the lint command that lists each with the
// changed files and the command that re-verifies it.
func (a *App) doctorStaleFacts(ctx context.Context, warn func(string)) {
	found, err := opslint.FindStaleFacts(ctx, a)
	if err != nil {
		warn(string(opslint.StaleFact) + ": the rows could not be read: " + err.Error())
		return
	}
	unchecked := 0
	stale := found[:0:0]
	for _, f := range found {
		switch {
		case f.Code == opslint.UncheckedFact && f.ID == "":
			unchecked += f.Rows
		case f.Code == opslint.UncheckedFact:
			unchecked++
		default:
			stale = append(stale, f)
		}
	}
	found = stale
	if unchecked > 0 { // bug hunt H-298: never quietly fresh
		warn(string(opslint.UncheckedFact) + ": " + strconv.Itoa(unchecked) + " row(s)' evidence (fact_evidence) could not be checked (run inside the code repo they are about): lore lint --code " + string(opslint.UncheckedFact))
	}
	if len(found) == 0 {
		return
	}
	warn(string(opslint.StaleFact) + ": " + strconv.Itoa(len(found)) + " row(s) state a fact whose evidence (fact_evidence) changed in git after it was verified (fact_verified_at), so it may no longer hold; each with the changed files and the command that re-verifies it: lore lint --code " + string(opslint.StaleFact))
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
		warn("quarantined metadata: " + q.Table + " " + q.ID + ": " + strings.Join(q.Problems, "; ") + " (fix with lore meta set --id " + q.ID + " …, or lore meta unset)")
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
		note(line + "; turn it back on: lore secrets rule on --id " + rule.RuleID + " --to " + rule.Store)
	}
}

// doctorTextAllows lists every text allow (#129): a passage of an internal
// note that may ship in a client repo is never invisible; each line names
// the note, where, the reason, the author and the command that removes it.
func (a *App) doctorTextAllows(ctx context.Context, note func(string)) {
	e, ok := a.Registry().Lookup(opssettings.OpSecretAllowList)
	if !ok {
		return
	}
	res, err := runDirect(ctx, e, op.JSONDecoder{Body: []byte("{}")})
	if err != nil {
		note("text allows could not be read: " + err.Error())
		return
	}
	raw, err := res.JSON()
	if err != nil {
		return
	}
	var out opssettings.AllowsOut
	if err := json.Unmarshal(raw, &out); err != nil {
		note("text allows could not be decoded: " + err.Error())
		return
	}
	for _, al := range out.Data {
		if al.RuleID != leakcheck.TextAllowRule {
			continue
		}
		note(textAllowDoctorLine(al))
	}
}

// textAllowDoctorLine is doctor's line for one text allow.
func textAllowDoctorLine(al opssettings.Allow) string {
	where := "anywhere"
	if al.Path != "" {
		where = "in " + al.Path
	}
	line := "text allow " + al.ID + ": a passage of internal note " + al.RowID + " may ship " + where + " (" + al.Reason
	if al.CreatedBy != "" {
		line += "; by " + al.CreatedBy
	}
	return line + "); remove it: lore secrets allow rm --id " + al.ID
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

// attrBlockMarker is the marker of lore's block in .gitattributes (the
// wire/host block writer's gitBlockMarker).
const attrBlockMarker = "lore-sync"

// doctorAttrOverride warns when a line after lore's block in the repo's
// .gitattributes takes text, eol or merge back for lore's files: git
// applies the later line, so row files check out with the team's line
// ends and lore's merge driver may not run (devco:Q-11). lore fix (or
// lore doctor --fix) moves lore's block to the end; a plain write keeps
// lore's rules in the clone's own .git/info/attributes, which git reads
// first, and changes no tracked file (devco:D-753).
func (a *App) doctorAttrOverride(ctx context.Context, warn func(string)) {
	top, err := a.repoTop(ctx)
	if err != nil || top == "" || !HoldsLoreData(top) {
		return
	}
	b, err := os.ReadFile(filepath.Join(top, ".gitattributes"))
	if err != nil {
		return
	}
	if line, over := AttrBlockOverride(string(b), attrBlockMarker); over {
		warn("gitattributes: `" + line + "` comes after lore's block in .gitattributes and overrides it for lore's files (row files get its line ends, lore's merge driver may not run); `lore fix` moves lore's block to the end, then commit .gitattributes (until then a lore write keeps lore's rules in this clone's .git/info/attributes)")
	}
}

// doctorClaudeLocal warns when the checkout's CLAUDE.local.md is a symlink
// (lore never writes through one), tracked by git (its content would be
// committed) or holds anything but lore's import lines, each once.
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
	if text, rerr := os.ReadFile(filepath.Clean(local)); rerr == nil {
		if msg := claudeLocalProblem(string(text)); msg != "" {
			warn(msg)
		}
	}
}

// claudeLocalProblem is the warning for CLAUDE.local.md's text, "" when it
// holds only import lines (`@<path>`), each once. Render writes one import
// line per private store, separated by blank lines, so several lines are
// normal; a line that is not an import (pasted notes) or the same import
// twice is not.
func claudeLocalProblem(text string) string {
	seen := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "":
			continue
		case !strings.HasPrefix(l, claudeImportPrefix):
			return claudeLocalFile + " holds more than lore's import lines; internal context belongs in " + lorehome.Shown() + "/render"
		case seen[l]:
			return claudeLocalFile + " holds the import line " + l + " more than once (`lore render` writes it once)"
		}
		seen[l] = true
	}
	return ""
}

// claudeImportPrefix starts a Claude Code import line.
const claudeImportPrefix = "@"

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
