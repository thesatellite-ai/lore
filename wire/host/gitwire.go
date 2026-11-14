package host

// gitwire.go — the per-clone git wiring of a binary built from the root
// module (ported from saas/cmd/cli sync_wire.go ensureGitWiring, P3-MODEB-9):
// the merge drivers in the clone's config, the .gitattributes block of the
// project, and lore's hook blocks, so a fresh clone where only cmd/lore runs
// is wired by its first command (F18) and its commits and pushes get the
// F16 checks (commit-msg: `lore check leaks --staged --commit-msg`,
// pre-push: `lore sync hook pre-push`).
//
//	live run in a project ─▶ fingerprint (wiring version + stat of every
//	   wired file) equals .git/lore/git-wiring? ─▶ nothing to do
//	   else ─▶ config, .gitattributes, hook blocks ─▶ new fingerprint
//
// The values are the old binary's, byte for byte (same markers, same hook
// lines), so the two binaries never rewrite each other's blocks. Blocks are
// chained into the hook folder git uses (core.hooksPath when set), never
// repointing it. Best effort: a failure is a status line, never a refused
// command.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/reposync"
	"github.com/thesatellite-ai/lore/wire"
)

// Git wiring values (v0.1.17 sync_wire.go).
const (
	gitBlockMarker      = "lore-sync"
	mergeDriverLore     = "lore"
	mergeDriverKeepOurs = "lore-keep-ours"
	loreMDRel           = ".lore/LORE.md"
	gitAttributesFile   = ".gitattributes"
	gitAttributesMode   = 0o644
	hookPreCommit       = "pre-commit"
	hookCommitMsg       = "commit-msg"
	hookPostCheckout    = "post-checkout"
	hookPostMerge       = "post-merge"
	hookPostRewrite     = "post-rewrite"
	// gitWiringStamp is the fingerprint file in .git/lore;
	// gitWiringReadSuffix names a read's memory (in the lore home, never
	// the clone) of the state it last reported (readWiring).
	gitWiringStamp      = "git-wiring"
	gitWiringReadSuffix = ".read"
	// gitWiringVersion changes whenever what is installed changes.
	gitWiringVersion = "3"
	binaryName       = "lore"
)

// gitAttributeLines are the project's .gitattributes rules: row files LF,
// lore's field merge driver, collapsed in PR diffs; every file render
// writes at a fixed path keeps ours, because lore renders it again from the
// merged rows. LORE.full.md had no rule (devco D-19): two people adding
// rules and pulling got a text conflict in it on every merge.
// ds:def id=sync-gitattributes-symdu732 owner=@khanakia stability=stable desc=".gitattributes rules lore installs"
var gitAttributeLines = func() []string {
	lines := []string{
		".lore/data/**/*.json text eol=lf merge=" + mergeDriverLore + " linguist-generated=true",
		".lore/data/*.json text eol=lf merge=" + mergeDriverLore + " linguist-generated=true",
	}
	for _, p := range wire.RenderedFilePatterns {
		lines = append(lines, p+" text eol=lf merge="+mergeDriverKeepOurs+" linguist-generated=true")
	}
	return lines
}()

// mergeDriverConfig is the per-clone config the attributes refer to; bin is
// mergeDriverBinary (bare `lore` only when that is this very binary, so a
// merge never falls to an older lore on PATH that cannot read `_v: 2`
// rows).
// ds:def id=sync-mergedriver-config-cthptudb owner=@khanakia stability=stable desc="per-clone merge driver config"
func mergeDriverConfig(bin string) [][2]string {
	return [][2]string{
		{"merge." + mergeDriverLore + ".name", "lore field-level row merge"},
		{"merge." + mergeDriverLore + ".driver", bin + " merge-driver %O %A %B %P"},
		{"merge." + mergeDriverKeepOurs + ".name", "lore: keep ours, re-render after merge"},
		{"merge." + mergeDriverKeepOurs + ".driver", bin + " merge-driver --keep-ours %O %A %B %P"},
	}
}

// mergeDriverBinary is the program written into the driver config for this
// executable: the bare name when `lore` on PATH is this file, else its
// absolute path single-quoted for git's shell (reposync.DriverBinary).
func mergeDriverBinary() string { return reposync.RunningDriverBinary() }

// wireMode is how far a command may change merge driver config the clone
// already holds. The .gitattributes and hook blocks are the same in both.
//
// DECISION (mergedriver batch): an ordinary command (a check, a list, a
// write) only fills a missing key and repairs a lore driver whose absolute
// program path no longer exists; it never replaces a value that works, so
// running a scratch or second lore binary in a clone cannot repoint every
// later merge at it. Install steps (lore init, the repo.init op, lore
// doctor --fix) also replace a value that is not a lore driver at all.
// Neither replaces a lore driver that can run.
type wireMode int

const (
	// wireAuto is every writing command's own wiring (the live hook's
	// prepare).
	wireAuto wireMode = iota
	// wireInstall is init and doctor --fix.
	wireInstall
	// wireRead is a read command's wiring: none (owner bug 6,
	// 2026-11-12: `lore member list` wrote hook blocks into a repo's
	// .githooks). A read writes no merge driver config, no hook block, no
	// .gitattributes and no .gitignore line, in the work tree or in .git;
	// it says once what is missing and the command that sets it up
	// (readWiring). Only init, sync, doctor --fix and a write wire.
	wireRead
)

// driverConfigAction is what wiring does with one merge driver key whose
// current value is cur (found false when the key is not set).
func driverConfigAction(mode wireMode, key, want, cur string, found bool) (write bool) {
	cur = strings.TrimSpace(cur)
	switch {
	case !found || cur == "":
		return true
	case cur == want:
		return false
	case !strings.HasSuffix(key, mergeDriverKeySuffix):
		// A .name key is only a label: an install step corrects it, an
		// ordinary command leaves a custom one.
		return mode == wireInstall
	case reposync.DriverMissing(cur):
		return true // already broken: every merge through it fails
	case reposync.DriverRuns(cur):
		return false // a lore driver that runs is never replaced
	default:
		return mode == wireInstall // not a lore driver: only an install step takes the key
	}
}

// mergeDriverKeySuffix ends the config keys holding a driver command line.
const mergeDriverKeySuffix = ".driver"

// hookBlocks are the blocks chained into each hook for the project at rel
// (repo-relative, "" or "." for the top level), with their markers: the
// project blocks carry `cd <project>`; post-commit and pre-push are
// repo-wide (one ride commit covers every project) and use rideHookMarker.
// ds:def id=sync-hooklines-g87bgvsj owner=@khanakia stability=stable desc="hook blocks chained into git hooks"
func hookBlocks(rel string) map[string]struct {
	marker string
	lines  []string
} {
	cd := ""
	marker := gitBlockMarker
	if rel != "" && rel != "." {
		cd = `cd "` + filepath.ToSlash(rel) + `" && `
		marker = gitBlockMarker + ":" + filepath.ToSlash(rel)
	}
	bn := binaryName
	guard := `command -v ` + bn + ` >/dev/null 2>&1 && `
	// The refresh blocks name their hook (and post-rewrite its "$1",
	// "rebase" or "amend"): the refresh then tells the post-rewrite that
	// ends a rebase, which may render, from a checkout or amend inside one,
	// which must not (refreshDeferred).
	refresh := func(hook, args string) []string {
		// stdout is dropped; stderr is kept for the one line saying the
		// committed render changed (B3-32)
		return []string{"( " + cd + guard + bn + " sync hook refresh " + hook + args + " >/dev/null ) || true"}
	}
	probe := bn + " sync hook --help >/dev/null 2>&1"
	tooOld := `echo "` + bn + `: installed ` + bn + ` cannot check .lore/data (no 'sync hook'); upgrade it" >&2`
	coreProbe := bn + " migrate --help >/dev/null 2>&1"
	hasV2 := `grep -rqsE '"_v": *2' .lore/data`
	needsCore := `echo "` + bn + `: .lore/data holds lore 0.1.18 rows but the ` + bn + ` on PATH is older; install lore 0.1.18 or newer to commit" >&2; exit 1`
	absMsg := `m="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"; `
	// No lore that can check, in a clone the last check found to be a
	// client repo (wire/clientmark.go): refuse rather than let the commit
	// through unchecked.
	noChecker := `elif [ "$(git config --get ` + wire.ClientRepoGitKey + `)" = true ]; then echo "` + bn + `: this is a client repo and no ` + bn + ` on PATH can check this commit for internal notes; install ` + bn + `, or if the repo is no longer a client repo: git config --unset ` + wire.ClientRepoGitKey + `" >&2; exit 1; `
	type block = struct {
		marker string
		lines  []string
	}
	// D-16: a commit that changes nothing under .lore/data (a code-only
	// commit) has nothing for the pre-commit check: one git status instead
	// of three lore processes. Any git error still runs the check.
	noRows := `{ d="$(git status --porcelain --untracked-files=all -- .lore/data 2>/dev/null)" && [ -z "$d" ] && exit 0; true; } && `
	// D-16: the trailer and the leak check in one lore process; a lore
	// without --check-leaks (an older build), or any failure, runs the
	// separate calls below exactly as before, so a refusal still stops the
	// commit and is printed once.
	oneCall := "lore_cm_ok=; ( " + absMsg + cd + guard + `o="$(` + bn + ` task hook commit-msg --check-leaks "$m" 2>&1)" && { [ -z "$o" ] || printf '%s\n' "$o" >&2; } ) && lore_cm_ok=1 || true`
	fallback := `[ -n "$lore_cm_ok" ] || `
	return map[string]block{
		hookPreCommit:    {marker, []string{"( " + cd + noRows + "if command -v " + bn + " >/dev/null 2>&1; then if ! " + coreProbe + " && " + hasV2 + "; then " + needsCore + "; elif " + probe + "; then " + bn + " sync hook pre-commit; else " + tooOld + "; fi; fi ) || exit $?"}},
		hookPostCheckout: {marker, refresh(hookPostCheckout, "")},
		hookPostMerge:    {marker, refresh(hookPostMerge, "")},
		hookPostRewrite:  {marker, refresh(hookPostRewrite, ` "$1"`)},
		hookCommitMsg: {marker, []string{
			oneCall,
			fallback + "( " + absMsg + cd + guard + bn + ` task hook commit-msg "$m" >/dev/null 2>&1 ) || true`,
			fallback + "( " + absMsg + cd + "if command -v " + bn + " >/dev/null 2>&1 && " + bn + " check leaks --help >/dev/null 2>&1; then " + bn + ` check leaks --staged --commit-msg "$m"; ` + noChecker + `fi ) || exit $?`,
		}},
		hookPostCommit: {rideHookMarker, rideHookLines[hookPostCommit]},
		hookPrePush:    {rideHookMarker, rideHookLines[hookPrePush]},
	}
}

// hookBlocksFor is hookBlocks(rel), each block guarded by sharedHookLines
// when the hooks folder is shared by every repo (hooksFolder).
func hookBlocksFor(rel string, shared bool) map[string]struct {
	marker string
	lines  []string
} {
	blocks := hookBlocks(rel)
	if !shared {
		return blocks
	}
	for n, b := range blocks {
		blockRel := rel
		if b.marker == rideHookMarker {
			blockRel = "" // post-commit and pre-push are repo-wide
		}
		b.lines = sharedHookLines(blockRel, b.lines)
		blocks[n] = b
	}
	return blocks
}

// hooksDir is the folder git runs hooks from (core.hooksPath when set),
// in this system's own spelling: git prints C:/Users/... on Windows, and
// the folder is shown to the person and joined with file names.
func (s *liveSession) hooksDir(ctx context.Context) (string, error) {
	dir, _, err := s.hooksFolder(ctx)
	return dir, err
}

// hooksFolder is hooksDir plus whether the folder is shared by every repo
// of the machine: core.hooksPath set in the global or system git config
// (a hook manager's folder), not in this clone's own config.
func (s *liveSession) hooksFolder(ctx context.Context) (dir string, shared bool, err error) {
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}})
	if err != nil {
		return "", false, err
	}
	dir = filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(out))))
	hp, herr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--show-scope", "--get", "core.hooksPath"}})
	if herr != nil {
		return dir, false, nil
	}
	scope, val, _ := strings.Cut(strings.TrimSpace(string(hp)), "\t")
	if p := filepath.FromSlash(strings.TrimSpace(val)); p != "" {
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.top, p)
		}
		dir = p
		shared = scope == gitScopeGlobal || scope == gitScopeSystem
	}
	return dir, shared, nil
}

// git config scopes (`git config --show-scope`) whose core.hooksPath names
// a folder every repo of the machine runs its hooks from.
const (
	gitScopeGlobal = "global"
	gitScopeSystem = "system"
)

// sharedHookLines guards lines for a hook folder every repo shares (D-21):
// git runs them in repos lore was never set up in too, so each line runs
// lore only where this clone's own hooks folder would have held it: a
// project block at rel only when rel holds .lore/, a top-level or
// repo-wide block only when the top holds .lore/ or lore wired this clone
// (its .git/lore/git-wiring stamp). Anywhere else the line is a no-op: lore
// is not started, so a repo with no lore data gets no config key, no
// fetched ref and no file from a commit.
func sharedHookLines(rel string, lines []string) []string {
	guard := `[ -d .lore ] || [ -f "$(git rev-parse --git-common-dir 2>/dev/null)/` + liveStateDirName + `/` + gitWiringStamp + `" ]`
	if rel != "" && rel != "." {
		guard = `[ -d "` + filepath.ToSlash(rel) + `/.lore" ]`
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "if " + guard + "; then " + l + "; fi"
	}
	return out
}

// ensureGitWiring wires the clone for the project at root (see the file
// comment).
func (s *liveSession) ensureGitWiring(ctx context.Context, root string, mode wireMode) {
	if os.Getenv(EnvSyncGit) == envOff {
		return
	}
	dir, shared, err := s.hooksFolder(ctx)
	if err != nil {
		s.warn("git wiring: " + err.Error())
		return
	}
	cfgPath, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "config"}})
	if err != nil {
		s.warn("git wiring: " + err.Error())
		return
	}
	rel, rerr := filepath.Rel(s.top, root)
	if rerr != nil || strings.HasPrefix(rel, "..") {
		rel = ""
	}
	bin := mergeDriverBinary()
	blocks := hookBlocksFor(rel, shared)
	paths := []string{strings.TrimSpace(string(cfgPath)), filepath.Join(root, gitAttributesFile), filepath.Join(root, gitignoreName)}
	names := make([]string, 0, len(blocks))
	for n := range blocks {
		names = append(names, n)
		paths = append(paths, filepath.Join(dir, n))
	}
	sort.Strings(names)
	fp := wiringFingerprint(s.h.cfg.Version, bin, rel, paths)
	stamp := filepath.Join(s.stateDir, gitWiringStamp)
	if b, rerr := os.ReadFile(filepath.Clean(stamp)); mode != wireInstall && rerr == nil && strings.TrimSpace(string(b)) == fp {
		return
	}
	if mode == wireRead {
		// A read wires nothing (owner bug 6): it looks, and says once
		// what is missing and which command sets it up.
		s.readWiring(ctx, root, dir, shared, strings.TrimSpace(string(cfgPath)), bin, rel, stamp+gitWiringReadSuffix, fp)
		return
	}
	// The config, attributes and hook files (not .gitignore, which has its
	// own notice): any byte changed means this command wired the clone.
	wired := make([]string, 0, len(paths)-1)
	wired = append(wired, paths[:2]...)
	wired = append(wired, paths[3:]...)
	before := wiredFiles(wired)
	for _, kv := range mergeDriverConfig(bin) {
		cur, gerr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--local", "--get", kv[0]}})
		if !driverConfigAction(mode, kv[0], kv[1], string(cur), gerr == nil) {
			continue
		}
		if _, serr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--local", kv[0], kv[1]}}); serr != nil {
			s.warn("git wiring: " + serr.Error())
			return
		}
	}
	attrs := filepath.Join(root, gitAttributesFile)
	// fmt1: a project on the 0.1.17 format gets no tracked file written
	// before lore migrate run (.gitattributes, .gitignore); the clone's
	// own .git/config and hooks are not tracked and are still set up
	formatOld := wire.ProjectFormatOld(root)
	switch {
	case !wire.HoldsLoreData(root):
		// no row files here (a client repo): the rules would only change
		// a tracked file of theirs for nothing (plain files sweep 3.8)
	case formatOld:
	default:
		if err := ensureFileBlock(attrs, gitBlockMarker, gitAttributeLines); err != nil {
			s.warn("git wiring: " + err.Error())
			return
		}
	}
	for _, n := range names {
		b := blocks[n]
		if err := ensureHookBlock(dir, n, b.marker, b.lines); err != nil {
			s.warn("git wiring: " + err.Error())
			return
		}
	}
	if changed := changedWiring(wired, before, dir); len(changed) > 0 {
		// no hidden magic: each file lore changed in the clone, named the
		// first time (later runs find it wired and change nothing)
		note := ""
		if shared {
			note = "; " + dir + " is the hooks folder of every repo (core.hooksPath in your global git config): lore's blocks there run lore only in repos lore is set up in"
		}
		s.cmdNotice(ctx, "set up git for lore's row files in this clone: "+strings.Join(changed, "; ")+note+" (`lore where` lists everything lore keeps here)")
	}
	// Row files git would not commit (OWNERQ-9): re-include them, once
	// per state of .gitignore; `lore sync` refuses while some stay hidden.
	// A read only reports them (readWiring): the rewrite is a write's, or
	// `lore doctor --fix`'s, never a side effect of looking.
	if !wire.HoldsLoreData(root) {
		// no row files to re-include, and no tracked file of a client
		// repo is changed (plain files sweep 3.8)
		writeWiringStamp(s.stateDir, stamp, s.h.cfg.Version, bin, rel, paths)
		return
	}
	if formatOld {
		return // no stamp: the first command after lore migrate run adds the tracked rules
	}
	switch changed, still, err := EnsureDataNotIgnored(ctx, root); {
	case err != nil:
		s.warn("git wiring: " + err.Error())
		return
	case changed:
		s.cmdNotice(ctx, "a .gitignore rule hid lore files; re-included "+dataRel+" in .gitignore — commit it")
	case len(still) > 0:
		s.warn("git ignores lore data, so teammates will not receive it (`lore sync` refuses until it is fixed):\n  " + strings.Join(still, "\n  "))
	}
	writeWiringStamp(s.stateDir, stamp, s.h.cfg.Version, bin, rel, paths)
}

// readWiring is a read's look at a clone whose wiring stamp is stale: it
// writes nothing into the work tree or .git (owner bug 6) and prints at
// most one note naming what is missing and the command that sets it up,
// plus the warning when git ignores row files. What it said is
// remembered in the lore home per state of the wired files (readKey,
// fp), so later reads of the same state are quiet and spawn no git.
func (s *liveSession) readWiring(ctx context.Context, root, hooks string, shared bool, cfgPath, bin, rel, readKey, fp string) {
	if wire.ReadNoticeSaid(s.h.cfg.Home, readKey, fp) {
		return
	}
	var missing []string
	for _, kv := range mergeDriverConfig(bin) {
		cur, gerr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--local", "--get", kv[0]}})
		if driverConfigAction(wireAuto, kv[0], kv[1], string(cur), gerr == nil) {
			missing = append(missing, "merge drivers "+mergeDriverLore+" and "+mergeDriverKeepOurs+" in "+cfgPath)
			break
		}
	}
	data := wire.HoldsLoreData(root)
	if attrs := filepath.Join(root, gitAttributesFile); data && dataDirExists(root) {
		// the rules only matter for row files: a repo whose rows live in a
		// store (a converted Mode B repo) is not told
		if stale, err := fileBlockStale(attrs, gitBlockMarker, gitAttributeLines); err == nil && stale {
			missing = append(missing, "lore's merge rules in "+attrs+" (then commit "+gitAttributesFile+")")
		}
	}
	var stale []string
	blocks := hookBlocksFor(rel, shared)
	for n, b := range blocks {
		if hookBlockStale(filepath.Join(hooks, n), b.marker, b.lines) {
			stale = append(stale, n)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		missing = append(missing, "hook blocks in "+hooks+" ("+strings.Join(stale, ", ")+")")
	}
	if len(missing) > 0 && !wire.ProjectFormatOld(root) { // fmt1: lore sync and writes set nothing up there before lore migrate run
		s.cmdNotice(ctx, "git is not set up for lore's row files in this clone: `lore sync` (or the next lore write here) sets up "+strings.Join(missing, "; "))
	}
	if data {
		switch still, err := stillIgnoredData(ctx, root, false); {
		case err != nil:
			s.warn("git wiring: " + err.Error())
		case len(still) > 0:
			shown, more := still, ""
			if len(still) > maxIgnoredShown {
				shown, more = still[:maxIgnoredShown], "\n  and "+strconv.Itoa(len(still)-maxIgnoredShown)+" more"
			}
			s.cmdNotice(ctx, "git ignores lore data, so teammates will not receive it (`lore sync` refuses until it is fixed); a read leaves "+gitignoreName+" as it is: `lore doctor --fix` re-includes "+dataRel+" (then commit "+gitignoreName+"):\n  "+strings.Join(shown, "\n  ")+more)
		}
	}
	wire.MarkReadNotice(s.h.cfg.Home, readKey, fp)
}

// hookBlockStale reports whether ensureHookBlock would change the hook
// file at p (its block missing or different, or the file not executable),
// reading only.
func hookBlockStale(p, marker string, lines []string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return true
	}
	cur, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return true
	}
	return hookBlockNext(string(cur), marker, lines) != string(cur) || info.Mode().Perm()&rideExecBits == 0
}

// cmdNotice prints one notice prefixed with the running command ("lore
// member list: ", wire.CommandPrefix) unless LORE_SYNC_QUIET=1: what a
// command set up, or what a read found missing, names the command that
// printed it, never `lore sync` when another command ran.
func (s *liveSession) cmdNotice(ctx context.Context, line string) {
	if syncQuiet() {
		return
	}
	s.h.line(wire.CommandPrefix(ctx) + line)
}

// dataDirExists reports whether the project at root has a .lore/data
// folder (row files the .gitattributes rules apply to).
func dataDirExists(root string) bool {
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(dataRel)))
	return err == nil && fi.IsDir()
}

// writeWiringStamp records the fingerprint of what is wired now in the
// clone's git folder (.git/lore, never the work tree).
func writeWiringStamp(stateDir, stamp, version, bin, rel string, paths []string) {
	if err := os.MkdirAll(stateDir, liveStateMode); err == nil {
		_ = os.WriteFile(stamp, []byte(wiringFingerprint(version, bin, rel, paths)+"\n"), liveNoteMode) // a lost stamp only means the next command checks again
	}
}

// decimal is the base the fingerprint prints sizes and times in.
const decimal = 10

// wiringFingerprint hashes what is installed, the running lore's version
// and the size and mtime of every wired file: equal fingerprints mean
// nobody touched them since this lore last wired, so no git process is
// spawned. The version is in it (E-FATES-2, v0.1.17 wiringVersion): a
// binary replaced at the same path re-wires the clone once rather than
// keep hooks and driver config an older lore wrote (re-wiring is
// idempotent, so a no-op upgrade costs one check).
func wiringFingerprint(loreVersion, bin, rel string, paths []string) string {
	var b strings.Builder // a Builder never fails a write; the hash is taken once
	b.WriteString(gitWiringVersion + "\n" + loreVersion + "\n" + bin + "\n" + rel + "\n")
	for _, l := range gitAttributeLines {
		b.WriteString(l + "\n")
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for _, p := range sorted {
		info, err := os.Stat(p)
		if err != nil {
			b.WriteString(p + ":missing\n")
			continue
		}
		b.WriteString(p + ":" + strconv.FormatInt(info.Size(), decimal) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), decimal) + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ensureFileBlock makes path hold exactly one block under marker: replaced
// in place, else appended. Content outside the block is never touched.
func ensureFileBlock(path, marker string, lines []string) error {
	// Through a root at the file's folder, so the write stays in it.
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // read-and-write errors are returned below; a close error adds nothing
	name := filepath.Base(path)
	cur, err := root.ReadFile(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	content := string(cur)
	next := withFileBlock(content, marker, lines)
	if next == content {
		return nil
	}
	return root.WriteFile(name, []byte(next), gitAttributesMode)
}

// fileBlockStale reports whether ensureFileBlock would change path (the
// file or lore's block in it is missing or differs), reading only.
func fileBlockStale(path, marker string, lines []string) (bool, error) {
	cur, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return withFileBlock(string(cur), marker, lines) != string(cur), nil
}

// withFileBlock is content with lore's block for marker holding exactly
// lines: replaced in place, or appended when there is none.
func withFileBlock(content, marker string, lines []string) string {
	start, end := "# >>> "+marker+" >>>", "# <<< "+marker+" <<<"
	block := start + "\n" + strings.Join(lines, "\n") + "\n" + end + "\n"
	next := content
	if i := strings.Index(content, start); i >= 0 {
		if j := strings.Index(content[i:], end); j >= 0 {
			k := i + j + len(end)
			if k < len(content) && content[k] == '\n' {
				k++
			}
			next = content[:i] + block + content[k:]
		}
	}
	if !strings.Contains(content, start) {
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += block
	}
	return next
}
