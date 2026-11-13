package git

// handgit.go — plain git is safe in a store clone (plain files round,
// brief 2.4, D-g). A person may `git pull`, `git commit` and `git push` in
// the clone by hand; lore sets the clone up so that behaves:
//
//	.git/config            merge.lore.name / merge.lore.driver: lore's field
//	                       merge of row files (`lore merge-driver %O %A %B %P`)
//	.git/info/attributes   `*.json merge=lore` (without a driver configured:
//	                       `*.json -merge`, so git never text-merges a row)
//	.git/hooks/pre-commit  a block between lore's markers that runs lore's
//	                       check of the staged row files and secrets; a
//	                       person's own hook lines around it are kept
//	core.hooksPath         only when a global or system core.hooksPath would
//	                       make git skip .git/hooks: then the clone's own
//	                       config points it back at .git/hooks
//
// When (owner bug 6, 2026-11-12): never on a read. An open sets the clone
// up only when its context says the run is an explicit setup or sync
// (WithGitSetUp: `lore sync`, `lore doctor --fix`, store init/add/use);
// any other open leaves it for the clone's first write, which sets it up
// and says what and where (HandGit.Say) before it commits. Lore's own
// three-way merge does not need the attributes in the clone: it passes
// its own attributes file (mergeAttributes in sync.go).
//
// Lore's own git calls never run that hook and never use that driver: each
// passes -c core.hooksPath=<a folder that does not exist> and -c
// merge.lore.driver=false (run.go), so lore's merges hand every row both
// sides changed to its Merger exactly as `-merge` did, and lore's commits
// were validated before they were written.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/model/errs"
)

// MergeDriverName is the merge driver row files name in the attributes.
const MergeDriverName = "lore"

// Attribute lines lore keeps in a clone's .git/info/attributes.
const (
	// rowAttributesNoDriver: no driver configured, git keeps ours and marks
	// the row conflicted (lore's sync hands it to the Merger).
	rowAttributesNoDriver = "*.json -merge"
	// rowAttributesDriver: git merges a row file with lore's driver.
	rowAttributesDriver = "*.json merge=" + MergeDriverName
)

// Markers of lore's block in a clone's pre-commit hook.
const (
	hookBlockBegin = "# >>> lore store check (lore writes this block; lines outside it are yours)"
	hookBlockEnd   = "# <<< lore store check"
	hookShebang    = "#!/bin/sh"
	hookFileMode   = 0o755
	hooksDirName   = "hooks"
	preCommitName  = "pre-commit"
	// ownDriverOff is the per-call setting of lore's own git runs.
	ownDriverOff = "merge." + MergeDriverName + ".driver=false"
)

// HandGit is what a store clone gets so plain git behaves (WithHandGit).
type HandGit struct {
	// Driver is the merge driver command line (merge.lore.driver).
	Driver string
	// Hook is the pre-commit block's lines (without lore's markers).
	Hook []string
	// Say, when set, is told what a write set up in the clone at dir (one
	// line per file or setting), before the write commits; an open that
	// set the clone up reports it through HandGitSetUp instead.
	Say func(ctx context.Context, dir string, set []string)
}

// gitSetUpKey marks a run that may set store clones up for plain git.
type gitSetUpKey struct{}

// WithGitSetUp returns ctx marking an explicit setup or sync run: a store
// clone it opens is set up for plain git right away (see the file
// comment). Every other run's open leaves that to the first write.
func WithGitSetUp(ctx context.Context) context.Context {
	return context.WithValue(ctx, gitSetUpKey{}, true)
}

// GitSetUp reports whether ctx is an explicit setup or sync run
// (WithGitSetUp).
func GitSetUp(ctx context.Context) bool {
	v, _ := ctx.Value(gitSetUpKey{}).(bool)
	return v
}

// setUpHandGitOnce does the set-up an open left pending (handPending), once
// per handle, and says what it wrote (HandGit.Say). Best effort: a
// failure leaves the clone as it was and the write goes on (lore's own
// commits never use the hook or driver).
func (b *Backend) setUpHandGitOnce(ctx context.Context) {
	if remote := b.identityRemote; remote != "" {
		// the clone's git identity waited for this write or sync too
		// (cloneidentity.go); best effort like the rest
		b.identityRemote = ""
		before := b.identitySet
		if err := b.ensureCloneIdentity(ctx, remote); err == nil && b.identitySet != before && b.cfg.identitySay != nil {
			b.cfg.identitySay(ctx, b.work, b.identitySet)
		}
	}
	if !b.handPending {
		return
	}
	b.handPending = false
	before := len(b.handSetUp)
	if err := b.ensureHandGit(ctx); err != nil {
		return
	}
	if set := b.handSetUp[before:]; len(set) > 0 && b.cfg.handGit.Say != nil {
		b.cfg.handGit.Say(ctx, b.work, slices.Clone(set))
	}
}

// WithHandGit sets the merge driver and the pre-commit hook every store
// clone this opener opens is set up with. Without it a clone keeps
// `*.json -merge` and gets no hook (tests that open clones directly).
func WithHandGit(h HandGit) Option { return func(c *config) { c.handGit = h } }

// HandGitSetUp is what this open set up in the clone for plain git, one
// line per file or setting it wrote (empty when everything was in place);
// wire says each once.
func (b *Backend) HandGitSetUp() []string { return slices.Clone(b.handSetUp) }

// ensureHandGit sets the clone up for plain git (see the file comment).
func (b *Backend) ensureHandGit(ctx context.Context) error {
	h := b.cfg.handGit
	if h.Driver == "" {
		return b.ensureAttributeLine(rowAttributesNoDriver, rowAttributesDriver)
	}
	for _, kv := range [][2]string{
		{"merge." + MergeDriverName + ".name", "lore field-level row merge"},
		{"merge." + MergeDriverName + ".driver", h.Driver},
	} {
		cur, err := b.git.run(ctx, call{dir: b.work, args: []string{"config", "--local", "--get", kv[0]}})
		if err == nil && trimmed(cur) == kv[1] {
			continue
		}
		if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"config", "--local", kv[0], kv[1]}}); err != nil {
			return errs.Wrap(errs.Internal, "git: set "+kv[0]+" in "+b.work+"/.git/config", err)
		}
		b.handSetUp = appendNewLine(b.handSetUp, ".git/config: "+kv[0])
	}
	if err := b.ensureAttributeLine(rowAttributesDriver, rowAttributesNoDriver); err != nil {
		return err
	}
	if len(h.Hook) > 0 {
		if err := b.ensurePreCommit(h.Hook); err != nil {
			return err
		}
	}
	return b.ensureHooksPath(ctx)
}

// appendNewLine appends s to list unless it is there.
func appendNewLine(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// ensureAttributeLine makes sure .git/info/attributes holds want and not
// drop (lore's other row line); every other line is kept.
func (b *Backend) ensureAttributeLine(want, drop string) error {
	info := filepath.Join(b.work, gitDirName, "info")
	p := filepath.Join(info, "attributes")
	cur, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errs.FileIO("read", p, err)
	}
	lines := strings.Split(strings.TrimRight(string(cur), "\n"), "\n")
	if len(cur) == 0 {
		lines = nil
	}
	has := slices.Contains(lines, want)
	kept := slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return strings.TrimSpace(l) == drop })
	if has && len(kept) == len(lines) {
		return nil
	}
	if !has {
		kept = append(kept, want)
	}
	if err := os.MkdirAll(info, cloneDirMode); err != nil {
		return errs.FileIO("create .git/info", info, err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(kept, "\n")+"\n"), 0o600); err != nil {
		return errs.Wrap(errs.Internal, "git: write .git/info/attributes", err)
	}
	if want == rowAttributesDriver {
		b.handSetUp = appendNewLine(b.handSetUp, ".git/info/attributes: "+want)
	}
	return nil
}

// ensurePreCommit writes lore's block into .git/hooks/pre-commit: a new
// hook file when there is none, the block replaced when it changed, or
// appended to a person's own hook (their lines kept).
func (b *Backend) ensurePreCommit(lines []string) error {
	dir := filepath.Join(b.work, gitDirName, hooksDirName)
	p := filepath.Join(dir, preCommitName)
	block := hookBlockBegin + "\n" + strings.Join(lines, "\n") + "\n" + hookBlockEnd + "\n"
	cur, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errs.FileIO("read", p, err)
	}
	s := string(cur)
	var next string
	switch i, j := strings.Index(s, hookBlockBegin), strings.Index(s, hookBlockEnd); {
	case len(cur) == 0:
		next = hookShebang + "\n" + block
	case i >= 0 && j > i:
		end := j + len(hookBlockEnd)
		if end < len(s) && s[end] == '\n' {
			end++
		}
		next = s[:i] + block + s[end:]
	default:
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		next = s + block
	}
	if next == s {
		return b.ensureExecutable(p)
	}
	if err := os.MkdirAll(dir, cloneDirMode); err != nil {
		return errs.FileIO("create the git hooks folder", dir, err)
	}
	if err := os.WriteFile(p, []byte(next), hookFileMode); err != nil {
		return errs.FileIO("write", p, err)
	}
	b.handSetUp = appendNewLine(b.handSetUp, ".git/hooks/"+preCommitName)
	return b.ensureExecutable(p)
}

// ensureExecutable gives the hook file its executable bits (a hook git
// cannot run is skipped silently).
func (b *Backend) ensureExecutable(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return errs.FileIO("read", p, err)
	}
	if st.Mode().Perm()&0o100 != 0 {
		return nil
	}
	if err := os.Chmod(p, st.Mode().Perm()|0o111); err != nil {
		return errs.FileIO("chmod", p, err)
	}
	return nil
}

// ensureHooksPath points the clone's own core.hooksPath at .git/hooks
// when a global or system core.hooksPath would make git skip it (D-g);
// otherwise lore sets none.
func (b *Backend) ensureHooksPath(ctx context.Context) error {
	local, lerr := b.git.run(ctx, call{dir: b.work, args: []string{"config", "--local", "--get", "core.hooksPath"}})
	if lerr == nil && trimmed(local) != "" {
		return nil // the clone names its own hooks folder: the person's choice
	}
	var outside string
	for _, scope := range []string{"--global", "--system"} {
		if v, err := b.git.run(ctx, call{dir: b.work, args: []string{"config", scope, "--get", "core.hooksPath"}}); err == nil && trimmed(v) != "" {
			outside = trimmed(v)
			break
		}
	}
	if outside == "" {
		return nil
	}
	own := filepath.Join(b.work, gitDirName, hooksDirName)
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"config", "--local", "core.hooksPath", filepath.ToSlash(own)}}); err != nil {
		return errs.Wrap(errs.Internal, "git: set core.hooksPath in "+b.work+"/.git/config", err)
	}
	b.handSetUp = appendNewLine(b.handSetUp, ".git/config: core.hooksPath="+filepath.ToSlash(own)+" (your global core.hooksPath "+outside+" would skip the clone's hooks)")
	return nil
}

// noCloneKey marks a run that must not make a store clone.
type noCloneKey struct{}

// WithNoClone returns ctx for a run that writes nothing at all (`lore fix
// --dry-run`, B4-14): OpenClone of a store with no clone on this machine
// answers E_STORE_UNREACHABLE instead of cloning it, so the run reads what
// is here and says what a real run would fetch.
func WithNoClone(ctx context.Context) context.Context {
	return context.WithValue(ctx, noCloneKey{}, true)
}

// NoClone reports whether ctx forbids making a store clone (WithNoClone),
// or the whole process does (ForbidClones).
func NoClone(ctx context.Context) bool {
	v, _ := ctx.Value(noCloneKey{}).(bool)
	return v || clonesForbidden.Load()
}

// clonesForbidden is ForbidClones' process-wide switch.
var clonesForbidden atomic.Bool

// ForbidClones makes every OpenClone of this process refuse to make a clone
// (the CLI entry point of `lore fix --dry-run`, one command per process):
// the org link lookups and doctor checks open stores on contexts of their
// own, which WithNoClone cannot reach.
// It returns the func that lifts the switch again.
func ForbidClones() func() {
	clonesForbidden.Store(true)
	return func() { clonesForbidden.Store(false) }
}
