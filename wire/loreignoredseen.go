package wire

// loreignoredseen.go — devco D-882: one `git check-ignore -- .lore` per
// command.
//
// Why: whether git ignores .lore/ (LoreDirIgnored, devco D-500) is asked by
// render's pointer and stamp, doctor's sync and .gitignore checks
// (D-856), the .gitignore lines and the hook wiring. Each asked git, so a
// plain `lore render` started five git processes and `lore doctor` ten,
// over their budgets of four and eight (D-418). The answer is now kept in
// this process under the size and mtime of every file git decides it from
// (the same key the hooks' memo file uses, D-511): when lore itself adds
// a line to a .gitignore or info/exclude between two calls, the key
// changes and git is asked again.
//
// Flow:
//
//	LoreDirIgnored(root) ─▶ key = root + stat of localIgnoreInputs(root)
//	                     ─▶ seen[key]? ─▶ the remembered answer, no git
//	                     ─▶ else git check-ignore; exit 0 or 1 is kept

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/gitfast"
)

// loreDirIgnoredSeen maps an ignoreInputsKey to LoreDirIgnored's answer
// (bool) for this process.
var loreDirIgnoredSeen sync.Map

// gitDirLink is the line a .git file (a worktree or submodule) starts with.
const gitDirLink = "gitdir:"

// localIgnoreInputs lists the files git reads to decide whether
// root/.lore is ignored, found without running git: the .gitignore of
// root and of every folder above it up to the work tree's top (the first
// folder holding .git), that clone's info/exclude (through a worktree's
// commondir), and the global files: git's config files (where
// core.excludesFile is set) and the default global excludes file. Only a
// core.excludesFile pointing elsewhere is not watched itself; a change of
// the setting is (its config file changes).
func localIgnoreInputs(root string) []string {
	var in []string
	if d := os.Getenv("GIT_DIR"); d != "" {
		in = append(in, filepath.Join(d, "info", "exclude"))
	}
	for dir := filepath.Clean(root); ; dir = filepath.Dir(dir) {
		in = append(in, filepath.Join(dir, ".gitignore"))
		if gd, ok := gitDirOf(dir); ok {
			in = append(in, filepath.Join(gd, "info", "exclude"))
			break
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	home, _ := os.UserHomeDir()
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".config")
	}
	if g := os.Getenv("GIT_CONFIG_GLOBAL"); g != "" {
		in = append(in, g)
	}
	if home != "" {
		in = append(in, filepath.Join(home, ".gitconfig"))
	}
	if xdg != "" {
		in = append(in, filepath.Join(xdg, "git", "config"), filepath.Join(xdg, "git", "ignore"))
	}
	return in
}

// gitDirOf is the git folder whose info/exclude applies to the work tree
// at dir, when dir holds a .git folder or a .git file naming one (a
// worktree's commondir is followed: info/exclude is the main clone's).
func gitDirOf(dir string) (string, bool) {
	dot := filepath.Join(dir, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return dot, true
	}
	b, err := os.ReadFile(filepath.Clean(dot))
	if err != nil {
		return dot, true // unreadable: its missing exclude only means a key that never matches a stale one
	}
	gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), gitDirLink)
	if !ok {
		return dot, true
	}
	gd = strings.TrimSpace(gd)
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
		cd := strings.TrimSpace(string(c))
		if !filepath.IsAbs(cd) {
			cd = filepath.Join(gd, cd)
		}
		return filepath.Clean(cd), true
	}
	return filepath.Clean(gd), true
}

// gitSaidNo reports whether err is git check-ignore's exit 1 ("not
// ignored"), an answer worth keeping; any other failure (not a checkout,
// no git, a cancelled run) is not kept.
func gitSaidNo(err error) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode() == 1
	}
	var fe *gitfast.ExitError
	if errors.As(err, &fe) {
		return fe.Code == 1
	}
	return false
}
