// Package gitsetup installs and inspects per-clone git wiring — config keys,
// marked blocks in text files (.gitattributes), and blocks chained into hook
// scripts — idempotently and without clobbering what is already there.
//
// Why it exists: a tool that relies on a merge driver or a hook must
// re-install it on every clone (git never runs code from a cloned repo
// automatically). Doing that safely means: never repoint core.hooksPath
// (that silently disables a husky / lefthook / .githooks setup), never
// overwrite a user's hook script (chain a marked block into it), and make
// every step a no-op when already done. The package holds only that
// mechanism; WHAT to install (driver commands, patterns, hook bodies) is the
// caller's decision.
//
// Requires the `git` program on PATH; every function returns ErrNoGit when
// it is missing so callers can degrade instead of failing.
package gitsetup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitBinary is the program every call shells out to.
const gitBinary = "git"

// hookFileMode makes a hook script executable (git ignores non-executable
// hooks silently).
const hookFileMode = 0o755

// File modes for what gitsetup writes besides hooks: the same defaults git
// itself uses for a work tree (directories traversable by everyone, plain
// files readable by everyone, writable only by the owner).
const (
	dirMode  = 0o755
	fileMode = 0o644
	// execBits are the permission bits of which any one makes a file
	// executable for git's purposes.
	execBits = 0o111
)

// revParseLines is how many lines Open's `git rev-parse --show-toplevel
// --git-path hooks --git-path config` prints: one per requested value.
const revParseLines = 3

// shebang starts every hook script this package creates.
const shebang = "#!/bin/sh"

// Sentinels.
var (
	// ErrNoGit means the git program is not installed.
	ErrNoGit = errors.New("gitsetup: git not found on PATH")
	// ErrNotRepo means the directory is not inside a git work tree.
	ErrNotRepo = errors.New("gitsetup: not inside a git work tree")
)

// run executes git in dir and returns trimmed stdout.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := runRaw(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

// runRaw executes git in dir and returns stdout untouched. Machine-readable
// formats (porcelain) are column-sensitive: trimming would eat the leading
// space of a " M path" entry and shift every column.
func runRaw(ctx context.Context, dir string, args ...string) (string, error) {
	return runInput(ctx, dir, "", args...)
}

// runInput is runRaw with stdin: git's -z NUL framing is only accepted for
// paths read from stdin (--stdin), never for paths given as arguments.
func runInput(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	if _, err := exec.LookPath(gitBinary); err != nil {
		return "", ErrNoGit
	}
	cmd := exec.CommandContext(ctx, gitBinary, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

// Repo describes the work tree containing a directory.
type Repo struct {
	// Toplevel is the absolute root of the work tree.
	Toplevel string
	// HooksDir is the absolute directory git currently runs hooks from:
	// core.hooksPath when set (resolved against Toplevel), else the
	// repository's default hooks dir (worktree-aware).
	HooksDir string
	// HooksPathConfigured is true when core.hooksPath is set (a hook
	// manager owns the directory; blocks are chained into its scripts).
	HooksPathConfigured bool
	// ConfigPath is the repository's local config file (worktree-aware).
	// Callers can stat it to notice config changes without spawning git.
	ConfigPath string
}

// Open inspects the work tree containing dir.
func Open(ctx context.Context, dir string) (Repo, error) {
	out, err := run(ctx, dir, "rev-parse", "--show-toplevel", "--git-path", "hooks", "--git-path", "config")
	if errors.Is(err, ErrNoGit) {
		return Repo{}, err
	}
	if err != nil {
		return Repo{}, ErrNotRepo
	}
	lines := strings.Split(out, "\n")
	if len(lines) < revParseLines {
		return Repo{}, fmt.Errorf("gitsetup: unexpected rev-parse output %q", out)
	}
	r := Repo{Toplevel: lines[0]}
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(dir, p)
	}
	r.HooksDir = abs(lines[1])
	r.ConfigPath = abs(lines[2])
	if hp, err := run(ctx, dir, "config", "--get", "core.hooksPath"); err == nil && hp != "" {
		r.HooksPathConfigured = true
		if !filepath.IsAbs(hp) {
			hp = filepath.Join(r.Toplevel, hp)
		}
		r.HooksDir = hp
	}
	return r, nil
}

// ConfigGet reads a local config key; ok is false when unset.
func ConfigGet(ctx context.Context, dir, key string) (string, bool, error) {
	out, err := run(ctx, dir, "config", "--local", "--get", key)
	if err == nil {
		return out, true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", false, nil // git's "key not set" exit code
	}
	return "", false, err
}

// EnsureConfig sets each local config key whose current value differs.
// Returns the keys it changed. Reads the whole local config in ONE git call
// (this runs on every lore command, so process spawns matter).
func EnsureConfig(ctx context.Context, dir string, kv [][2]string) ([]string, error) {
	cur, err := localConfig(ctx, dir)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, p := range kv {
		if v, ok := cur[strings.ToLower(p[0])]; ok && v == p[1] {
			continue
		}
		if _, err := run(ctx, dir, "config", "--local", p[0], p[1]); err != nil {
			return changed, err
		}
		changed = append(changed, p[0])
	}
	return changed, nil
}

// localConfig returns the repository-local config as lowercased key → value.
// Keys are lowercased because git treats section and variable names
// case-insensitively and prints them lowercased in --list output.
func localConfig(ctx context.Context, dir string) (map[string]string, error) {
	out, err := run(ctx, dir, "config", "--local", "--list", "-z")
	if err != nil {
		return nil, err
	}
	cfg := map[string]string{}
	for entry := range strings.SplitSeq(out, "\x00") {
		if entry == "" {
			continue
		}
		k, v, _ := strings.Cut(entry, "\n")
		cfg[strings.ToLower(k)] = v
	}
	return cfg, nil
}

// blockStart / blockEnd delimit a managed block. The marker names the
// owner so several tools can each keep one block in the same file.
func blockStart(marker string) string { return "# >>> " + marker + " >>>" }
func blockEnd(marker string) string   { return "# <<< " + marker + " <<<" }

// EnsureBlock makes the file at path contain exactly one managed block with
// the given lines, appended at the end on first install and replaced in
// place afterwards. Content outside the block is never touched. Returns
// whether the file changed.
func EnsureBlock(path, marker string, lines []string) (bool, error) {
	cur, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("gitsetup: read %s: %w", path, err)
	}
	block := blockStart(marker) + "\n" + strings.Join(lines, "\n") + "\n" + blockEnd(marker) + "\n"
	next, ok := replaceBlock(string(cur), marker, block)
	if !ok {
		next = string(cur)
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += block
	}
	if next == string(cur) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), fileMode); err != nil {
		return false, fmt.Errorf("gitsetup: write %s: %w", path, err)
	}
	return true, nil
}

// replaceBlock swaps an existing managed block; ok is false when absent.
func replaceBlock(content, marker, block string) (string, bool) {
	start := strings.Index(content, blockStart(marker))
	if start < 0 {
		return content, false
	}
	endMarker := blockEnd(marker)
	rel := strings.Index(content[start:], endMarker)
	if rel < 0 {
		return content, false
	}
	end := start + rel + len(endMarker)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:start] + block + content[end:], true
}

// HasBlock reports whether path contains the marker's block.
func HasBlock(path, marker string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), blockStart(marker))
}

// EnsureHookBlock chains a managed block into a hook script in hooksDir.
//
// The block goes right after the shebang line (not at the end): an existing
// script may `exit 0` early, and a block appended after that would never
// run. A missing script is created with a shebang. The file is made
// executable. Returns whether anything changed.
func EnsureHookBlock(hooksDir, hook, marker string, lines []string) (bool, error) {
	if err := os.MkdirAll(hooksDir, dirMode); err != nil {
		return false, fmt.Errorf("gitsetup: mkdir %s: %w", hooksDir, err)
	}
	path := filepath.Join(hooksDir, hook)
	cur, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("gitsetup: read %s: %w", path, err)
	}
	block := blockStart(marker) + "\n" + strings.Join(lines, "\n") + "\n" + blockEnd(marker) + "\n"
	content := string(cur)
	next, replaced := replaceBlock(content, marker, block)
	if !replaced {
		switch {
		case content == "":
			next = shebang + "\n" + block
		case strings.HasPrefix(content, "#!"):
			nl := strings.IndexByte(content, '\n')
			if nl < 0 {
				next = content + "\n" + block
			} else {
				next = content[:nl+1] + block + content[nl+1:]
			}
		default:
			next = block + content
		}
	}
	changed := next != content
	if changed {
		if err := os.WriteFile(path, []byte(next), hookFileMode); err != nil {
			return false, fmt.Errorf("gitsetup: write %s: %w", path, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return changed, fmt.Errorf("gitsetup: stat %s: %w", path, err)
	}
	if info.Mode().Perm()&execBits == 0 {
		if err := os.Chmod(path, hookFileMode); err != nil {
			return changed, fmt.Errorf("gitsetup: chmod %s: %w", path, err)
		}
		changed = true
	}
	return changed, nil
}

// porcelainStatusLen is the "XY " prefix of a porcelain v1 entry.
const porcelainStatusLen = 3

// Uncommitted lists paths under pathspec (relative to dir) with
// uncommitted changes — staged, unstaged or untracked — as paths RELATIVE
// TO dir, so callers can join them onto dir even when dir is a
// subdirectory of the work tree (git itself reports repo-root-relative
// paths).
//
// Parses `--porcelain -z` without trimming: entries are "XY path\0", and a
// rename/copy entry is followed by its original path as one extra field.
func Uncommitted(ctx context.Context, dir, pathspec string) ([]string, error) {
	out, err := runRaw(ctx, dir, "status", "--porcelain", "-z", "--untracked-files=all", "--", pathspec)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	top, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	base, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("gitsetup: resolve %s: %w", dir, err)
	}
	fields := strings.Split(out, "\x00")
	var paths []string
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) <= porcelainStatusLen {
			continue
		}
		x := f[0]
		rel, err := filepath.Rel(base, filepath.Join(top, filepath.FromSlash(f[porcelainStatusLen:])))
		if err != nil {
			return nil, fmt.Errorf("gitsetup: relativise %s: %w", f, err)
		}
		paths = append(paths, rel)
		if x == 'R' || x == 'C' {
			i++ // skip the original-path field of a rename/copy
		}
	}
	return paths, nil
}

// Add stages pathspec (relative to dir), including deletions.
func Add(ctx context.Context, dir, pathspec string) error {
	_, err := run(ctx, dir, "add", "-A", "--", pathspec)
	return err
}

// IgnoredPaths returns the subset of paths (relative to dir) that git's
// ignore rules exclude, each mapped to the rule responsible, formatted
// "<source>:<line>: <pattern>". Rules are evaluated without consulting the
// index (--no-index): the question is whether a NEW file at that path would
// be picked up by `git add`, and a tracked file is otherwise never reported.
// A path whose last matching rule is a negation ("!pattern") is not ignored.
func IgnoredPaths(ctx context.Context, dir string, paths []string) (map[string]string, error) {
	out := map[string]string{}
	if len(paths) == 0 {
		return out, nil
	}
	raw, err := runInput(ctx, dir, strings.Join(paths, "\x00")+"\x00", "check-ignore", "--no-index", "-v", "-z", "--stdin")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return out, nil // nothing ignored
		}
		return nil, err
	}
	// -v -z emits four NUL-terminated fields per path: source, line, pattern, path.
	fields := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	for i := 0; i+checkIgnoreFields-1 < len(fields); i += checkIgnoreFields {
		source, line, pattern, path := fields[i], fields[i+1], fields[i+2], fields[i+3]
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		out[path] = source + ":" + line + ": " + pattern
	}
	return out, nil
}

// checkIgnoreFields is the number of fields `git check-ignore -v -z` prints
// per path.
const checkIgnoreFields = 4

// IsIgnored reports whether git ignores path (relative to dir).
func IsIgnored(ctx context.Context, dir, path string) (bool, error) {
	_, err := run(ctx, dir, "check-ignore", "-q", "--", path)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
