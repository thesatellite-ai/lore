package wire

// setuphealth_age.go — stored setup health goes stale when the git config
// its checks read changes (devco D-605, owner use pass 2 weak 2). setup
// list showed a three-day-old identity.git_email warning, with the fix that
// older check printed, after the person had already fixed their git config
// (an includeIf in a file ~/.gitconfig includes); only `setup check` cleared
// it. RecordHealth keeps the size and modification time of each of these
// files beside the results (healthFile.GitConfig), and setup list asks
// HealthInputChanged: a file that changed, appeared or went since makes it
// say "not checked since <date>" with the check that refreshes it instead
// of the old cell. No clock is compared, only the files' own states:
//
//	the global git config   GIT_CONFIG_GLOBAL, else ~/.gitconfig and
//	                        $XDG_CONFIG_HOME/git/config (~/.config/git/config)
//	every file it includes  include.path and includeIf.<cond>.path, relative
//	                        to the including file, ~ expanded, followed
//	                        to a depth of includeDepth

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.HealthAger = (*App)(nil)

// includeDepth bounds how deep includes are followed (git's own limit is
// 10; a loop stops at the seen set before that).
const includeDepth = 10

// HealthInputChanged implements opssetup.HealthAger: the first global git
// config file, or file it includes, whose size or modification time differs
// from what RecordHealth kept with setup's results (or that appeared or
// went since), written with ~ for the home folder; "" when none did, or
// when the results were recorded with no file states (then nothing tells).
func (a *App) HealthInputChanged(ctx context.Context, setup string) string {
	hf, err := a.readHealth()
	if err != nil {
		return ""
	}
	kept, ok := hf.GitConfig[setup]
	if !ok {
		return ""
	}
	home, _ := os.UserHomeDir()
	now := gitConfigStates(ctx)
	files := make([]string, 0, len(now)+len(kept))
	for f := range now {
		files = append(files, f)
	}
	for f := range kept {
		if _, both := now[f]; !both {
			files = append(files, f)
		}
	}
	sort.Strings(files) // the same file named on every run
	for _, f := range files {
		if kept[f] != now[f] {
			return tildeShown(f, home)
		}
	}
	return ""
}

// gitConfigStates is "size@mtime" of every git config file
// gitConfigFilesRead finds, by path.
func gitConfigStates(ctx context.Context) map[string]string {
	home, _ := os.UserHomeDir()
	out := map[string]string{}
	for _, f := range gitConfigFilesRead(ctx, home) {
		if fi, err := os.Stat(f); err == nil {
			out[f] = strconv.FormatInt(fi.Size(), 10) + "@" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
		}
	}
	return out
}

// gitConfigFilesRead are the global git config files that exist and every
// file they include (whatever the include's condition: a gitdir: or
// hasconfig: rule applies somewhere under a setup's folders), each once.
func gitConfigFilesRead(ctx context.Context, home string) []string {
	var tops []string
	if g := os.Getenv("GIT_CONFIG_GLOBAL"); g != "" {
		tops = append(tops, g)
	} else if home != "" {
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		tops = append(tops, filepath.Join(home, ".gitconfig"), filepath.Join(xdg, "git", "config"))
	}
	seen := map[string]bool{}
	var out []string
	var walk func(f string, depth int)
	walk = func(f string, depth int) {
		f = filepath.Clean(f)
		if seen[f] || depth > includeDepth {
			return
		}
		seen[f] = true
		if _, err := os.Stat(f); err != nil {
			return
		}
		out = append(out, f)
		for _, p := range includedPaths(ctx, f, home) {
			walk(p, depth+1)
		}
	}
	for _, f := range tops {
		walk(f, 0)
	}
	return out
}

// includedPaths are the include.path and includeIf.*.path values of the
// config file f, as absolute paths (git resolves a relative one against
// the including file's folder and expands a leading ~/).
func includedPaths(ctx context.Context, f, home string) []string {
	raw, err := gitRun{dir: filepath.Dir(f), args: []string{"config", "--file", f, "--get-regexp", `^include(if\..*)?\.path$`}}.output(ctx)
	if err != nil {
		return nil // no include in the file (git exits 1) or an unreadable one
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		_, p, ok := strings.Cut(line, " ")
		if !ok || p == "" {
			continue
		}
		switch {
		case strings.HasPrefix(p, "~/") && home != "":
			p = filepath.Join(home, p[2:])
		case !filepath.IsAbs(p):
			p = filepath.Join(filepath.Dir(f), p)
		}
		out = append(out, p)
	}
	return out
}

// tildeShown is p with the home folder written as ~.
func tildeShown(p, home string) string {
	if home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}
