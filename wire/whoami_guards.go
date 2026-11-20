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
	"github.com/thesatellite-ai/lore/model/fspath"
)

// guardsOnText is the part of whoami's repo line that claims the guards.
const guardsOnText = "guards on"

// leakCheckHookLine is in every commit-msg block lore writes (the
// project block and the client repo guard alike).
const leakCheckHookLine = "check leaks --staged --commit-msg"

// pushCheckHookLine is in every pre-push block lore writes (the ride-along
// block of a project and the client repo guard alike, D-730).
const pushCheckHookLine = "sync hook pre-push"

// commitGuardInstalled reports whether the commit-msg hook git runs for
// the clone at dir holds lore's leak check and is executable.
func commitGuardInstalled(ctx context.Context, dir string) bool {
	return hookHolds(ctx, dir, "commit-msg", leakCheckHookLine)
}

// hookHolds reports whether the hook named hook that git runs for the
// clone at dir is executable and holds line.
func hookHolds(ctx context.Context, dir, hook, line string) bool {
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
	p := filepath.Join(hooks, hook)
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		return false
	}
	b, err := os.ReadFile(filepath.Clean(p))
	return err == nil && strings.Contains(string(b), line)
}

// guardsCommitsOnlyText replaces "guards on" in a client repo with no
// .lore/ whose clone holds lore's commit-msg block but no pre-push block
// (linked before D-730): a --no-verify commit leaves unchecked there.
const guardsCommitsOnlyText = "guards on for commits only: no lore pre-push hook, pushes are not checked for internal notes; `lore doctor --fix` here installs it"

// guardsOffRepoLine rewrites whoami's repo line when it claims the guards
// in a git clone whose commit-msg hook does not run lore's check.
func guardsOffRepoLine(ctx context.Context, s *orgcfg.Situation, lines []string) []string {
	if s == nil || s.NotGitRepo || s.Result.Role != orgresolve.RoleClient {
		return lines
	}
	if commitGuardInstalled(ctx, s.Dir) {
		if loreFolderAt(gitTop(ctx, s.Dir)) || hookHolds(ctx, s.Dir, "pre-push", pushCheckHookLine) {
			return lines
		}
		for i, l := range lines {
			if strings.Contains(l, guardsOnText) && strings.Contains(strings.TrimSpace(l), "repo") {
				lines[i] = strings.Replace(l, guardsOnText, guardsCommitsOnlyText, 1)
			}
		}
		return lines
	}
	fix := "lore fix here installs it"
	switch {
	case trackedHooksDir(ctx, s.Dir) != "":
		// D-732: lore writes nothing into a hooks folder the repo commits,
		// so no lore command installs it there: the step is the lines
		// doctor prints, pasted into the repo's own commit-msg
		fix = trackedHooksGuardFix(trackedHooksDir(ctx, s.Dir))
	case s.LinkedByRemote != "":
		fix = guardLinkFix(s) + " here installs it"
	case !loreFolderAt(gitTop(ctx, s.Dir)):
		// devco:D-587: lore fix refuses in a repo with no lore data
		// ("nothing to fix"), so the step is the link that installs it
		fix = unlinkedGuardFix(s)
	}
	for i, l := range lines {
		if strings.Contains(l, guardsOnText) && strings.Contains(strings.TrimSpace(l), "repo") {
			lines[i] = strings.Replace(l, guardsOnText, "guards OFF in this clone: no lore commit-msg hook, commits are not checked for internal notes; "+fix, 1)
		}
	}
	return lines
}

// trackedHooksDir is the hooks folder git runs for the clone at dir when
// it is one the repo commits (core.hooksPath inside the work tree, outside
// .git, a file of it tracked); "" otherwise.
func trackedHooksDir(ctx context.Context, dir string) string {
	hp, err := gitRun{dir: dir, args: []string{"config", "--get", "core.hooksPath"}}.output(ctx)
	p := filepath.FromSlash(strings.TrimSpace(string(hp)))
	if err != nil || p == "" {
		return ""
	}
	top := gitTop(ctx, dir)
	if !filepath.IsAbs(p) {
		p = filepath.Join(top, p)
	}
	rel, inside := fspath.Rel(top, p)
	if !inside || rel == "." || strings.SplitN(filepath.ToSlash(rel), "/", 2)[0] == ".git" {
		return ""
	}
	out, err := gitRun{dir: top, args: []string{"ls-files", "--", rel}}.output(ctx)
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return ""
	}
	return p
}

// trackedHooksGuardFix is whoami's guard step for a clone whose hooks
// folder is committed (D-732): doctor's paste lines, never a command that
// would write there.
func trackedHooksGuardFix(hooks string) string {
	return "git runs hooks from " + hooks + ", which is committed with the repo, so lore writes nothing there: `lore doctor` prints the lines to paste into its commit-msg"
}

// unlinkedGuardFix is the guard step of a client repo no store links and
// lore holds no data in (devco:D-587): `lore org link` installs the commit
// guard there, and lore fix refuses ("nothing to fix"), so whoami names
// the link and the command that lists the folders it takes.
func unlinkedGuardFix(s *orgcfg.Situation) string {
	org := "<org>"
	if s.Result.Org != nil {
		org = s.Result.Org.Name
	}
	return "`lore org link --org " + org + " --project <folder>` installs it (`lore store folder list --store " + org + "` lists the folders)"
}

// guardLinkFix is the command that installs the commit guard in a client
// repo linked by its remote: `lore org link` with the org and the folder
// of the link that names this repo (devco:N-9: it said `--project
// <folder>` though the link was printed two lines below). A placeholder
// stays only for what lore does not know.
func guardLinkFix(s *orgcfg.Situation) string {
	org, folder := "<org>", "<folder>"
	if s.Result.Org != nil {
		org = s.Result.Org.Name
	}
	for _, l := range s.Result.Links {
		if l.Entry == org || org == "<org>" {
			org, folder = l.Entry, l.Folder
			break
		}
	}
	return "lore org link --org " + org + " --project " + folder + " --yes"
}
