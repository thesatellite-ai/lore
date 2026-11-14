package wire

// whoami_guards.go — whoami's "guards on" is said only when the clone's
// commit-msg hook really runs lore's leak check (J-11). A client repo a
// store links by its remote (no .lore/) had no hook at all, and whoami
// still said "client repo  (guards on)" while every commit went through
// unchecked. The repo line now says the guards are off in this clone and
// names the command that installs them.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// guardsOnText is the part of whoami's repo line that claims the guards.
const guardsOnText = "guards on"

// leakCheckHookLine is in every commit-msg block lore writes (the
// project block and the client repo guard alike).
const leakCheckHookLine = "check leaks --staged --commit-msg"

// commitGuardInstalled reports whether the commit-msg hook git runs for
// the clone at dir holds lore's leak check and is executable.
func commitGuardInstalled(ctx context.Context, dir string) bool {
	out, err := gitRun{dir: dir, args: []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}}.output(ctx)
	if err != nil {
		return false
	}
	hooks := filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(out))))
	if hp, herr := (gitRun{dir: dir, args: []string{"config", "--get", "core.hooksPath"}}).output(ctx); herr == nil {
		if p := filepath.FromSlash(strings.TrimSpace(string(hp))); p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(gitTop(ctx, dir), p)
			}
			hooks = p
		}
	}
	p := filepath.Join(hooks, "commit-msg")
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		return false
	}
	b, err := os.ReadFile(filepath.Clean(p))
	return err == nil && strings.Contains(string(b), leakCheckHookLine)
}

// guardsOffRepoLine rewrites whoami's repo line when it claims the guards
// in a git clone whose commit-msg hook does not run lore's check.
func guardsOffRepoLine(ctx context.Context, s *orgcfg.Situation, lines []string) []string {
	if s == nil || s.NotGitRepo || s.Result.Role != orgresolve.RoleClient || commitGuardInstalled(ctx, s.Dir) {
		return lines
	}
	fix := "lore fix"
	if s.LinkedByRemote != "" {
		org := "<org>"
		if s.Result.Org != nil {
			org = s.Result.Org.Name
		}
		fix = "lore org link --org " + org + " --project <folder> --yes"
	}
	for i, l := range lines {
		if strings.Contains(l, guardsOnText) && strings.Contains(strings.TrimSpace(l), "repo") {
			lines[i] = strings.Replace(l, guardsOnText, "guards OFF in this clone: no lore commit-msg hook, commits are not checked for internal notes; "+fix+" here installs it", 1)
		}
	}
	return lines
}
