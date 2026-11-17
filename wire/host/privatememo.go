package host

// privatememo.go — devco D-511: whether the project is private (its .lore
// folder ignored by git, D-500) without a git process on every hook run.
//
// Why: D-500 asked `git check-ignore` on each wiring pass, and every git
// hook of a commit wires the clone, so the hooks of one commit started six
// git processes, over the budget of five (D-420). The answer is now
// remembered under the lore home beside the repo's cache, keyed by the files git decides it
// from; the config read naming the global excludes file is answered
// in-process by gitfast.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/model/fspath"
	"github.com/thesatellite-ai/lore/wire"
)

// privateIgnoreMemo is the suffix of the memo file (under the lore home) holding the last answer.
const privateIgnoreMemo = "lore-dir-ignored"

// gitIgnoreFile is the per-folder ignore file git reads.
const gitIgnoreFile = ".gitignore"

// repoMayHoldLoreFiles is wire.RepoMayHoldLoreFiles for a non-client
// project at root inside this session's clone, with the ignore answer
// remembered (wire.LoreDirIgnoredMemo).
func (s *liveSession) repoMayHoldLoreFiles(ctx context.Context, root string) bool {
	memo := privateMemoPath(root)
	if s.stateDir == "" || memo == "" {
		return wire.RepoMayHoldLoreFiles(ctx, root) // no git folder or lore home known: ask git
	}
	return !wire.LoreDirIgnoredMemo(ctx, root, memo, s.ignoreInputs(ctx, root))
}

// privateMemoPath is where the answer is remembered: beside the repo's
// cache under the lore home (<home>/cache/repo/<name>-<hash>.db +
// "."+privateIgnoreMemo), never in the repo or its .git: a read and a dry
// run write nothing into a repo (TestReadsNeverWireGit,
// TestDryRunsWriteNothing, TestReadCacheLivesUnderHome). "" when no lore
// home resolves.
func privateMemoPath(root string) string {
	h, err := lorehome.Resolve()
	if err != nil || h.Dir == "" {
		return ""
	}
	p := wire.RepoCachePath(h.Dir, root) + "." + privateIgnoreMemo
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return ""
	}
	return p
}

// ignoreInputs lists the files git reads to decide whether root/.lore is
// ignored: the .gitignore of root and of every folder above it up to the
// work tree's top, the clone's info/exclude, and the global excludes file
// (core.excludesFile, else git's default under XDG_CONFIG_HOME or
// ~/.config).
func (s *liveSession) ignoreInputs(ctx context.Context, root string) []string {
	var in []string
	for dir := filepath.Clean(root); ; dir = filepath.Dir(dir) {
		in = append(in, filepath.Join(dir, gitIgnoreFile))
		if _, inside := fspath.Rel(s.top, dir); !inside || dir == filepath.Clean(s.top) || dir == filepath.Dir(dir) {
			break
		}
	}
	in = append(in, filepath.Join(filepath.Dir(s.stateDir), "info", "exclude"))
	if g := globalExcludesFile(ctx, s.repo); g != "" {
		in = append(in, g)
	}
	return in
}

// globalExcludesFile is the excludes file git reads besides the repo's own
// files: core.excludesFile (a leading ~/ is the home folder), else
// $XDG_CONFIG_HOME/git/ignore, else ~/.config/git/ignore; "" when no home
// is known.
func globalExcludesFile(ctx context.Context, repo *gitref.Repo) string {
	home, _ := os.UserHomeDir()
	if out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "core.excludesFile"}}); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			if rest, ok := strings.CutPrefix(p, "~/"); ok && home != "" {
				return filepath.Join(home, rest)
			}
			return p
		}
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "git", "ignore")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "git", "ignore")
}
