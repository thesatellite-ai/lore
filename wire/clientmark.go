package wire

// clientmark.go — the clone's "client repo" mark, so the commit-msg hook
// fails closed when lore is gone (LORE_TEAM_SPEC §6.3, F16).
//
// The hook runs `lore check leaks` only when lore is on PATH. Without lore
// (uninstalled, a PATH that lost it, a lore too old to check) nothing
// checks the commit, and a client repo would take pasted internal notes.
// So every leak check run by the hook records in the clone's own git
// config (never committed, never pushed) whether the repo resolved as a
// client repo:
//
//	check leaks (hook) ─▶ Situation ─▶ client: git config --local lore.clientrepo true
//	                                ─▶ own / no org: the key unset
//	                                ─▶ cannot tell: left as it is
//
// and the hook, finding no lore that can check, refuses the commit while
// the key is true (wire/host/gitwire.go hookBlocks).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// ClientRepoGitKey is the clone-local git config key holding the mark.
const ClientRepoGitKey = "lore.clientrepo"

// clientRepoMarkOn is the key's value while the repo is a client repo.
const clientRepoMarkOn = "true"

// markClientRepo records, in the git config of the clone at root, whether
// res resolved it as a client repo. Best effort: a failure leaves the mark
// as it was (the check itself still runs and refuses on its own).
func markClientRepo(ctx context.Context, root string, res *OrgResolution) {
	top := gitTop(ctx, root)
	if top == "" {
		return
	}
	if !loreFolderAt(top) && !loreFolderAt(root) {
		// S-2: a repo with no lore data (one a store only links) has no
		// lore hook that reads the mark, and its git config is not lore's
		// to change (`lore check leaks` run by hand there)
		return
	}
	client := false
	if res != nil {
		s, err := res.Situation(ctx)
		if err != nil || s == nil || s.ResolveErr != nil || s.Result.LinkUnknown {
			return // cannot tell: keep what the last check knew
		}
		client = s.Result.Role == orgresolve.RoleClient
	}
	cur, _ := gitRun{dir: top, args: []string{"config", "--local", "--get", ClientRepoGitKey}}.output(ctx)
	on := strings.TrimSpace(string(cur)) == clientRepoMarkOn
	switch {
	case client && !on:
		if _, err := (gitRun{dir: top, args: []string{"config", "--local", ClientRepoGitKey, clientRepoMarkOn}}).output(ctx); err == nil {
			// no hidden magic: a key lore sets in a person's git config is
			// named the first time (plain files sweep 3.2)
			_, _ = fmt.Fprintln(os.Stderr, "lore: set "+ClientRepoGitKey+"="+clientRepoMarkOn+" in "+localGitConfigFile(ctx, top)+": this is a client repo, so its commit hook refuses a commit no lore can check for internal notes (`lore where` lists it; `git config --unset "+ClientRepoGitKey+"` removes it)")
		}
	case !client && len(strings.TrimSpace(string(cur))) > 0:
		if _, err := (gitRun{dir: top, args: []string{"config", "--local", "--unset-all", ClientRepoGitKey}}).output(ctx); err == nil {
			_, _ = fmt.Fprintln(os.Stderr, "lore: removed "+ClientRepoGitKey+" from "+localGitConfigFile(ctx, top)+": this repo is no longer a client repo")
		}
	}
}

// localGitConfigFile is the file `git config --local` writes in the clone
// at top: <top>/.git/config in a plain clone, the main clone's config in a
// linked worktree (whose .git is a one-line gitdir file, so a message
// naming <worktree>/.git/config would point at nothing).
func localGitConfigFile(ctx context.Context, top string) string {
	out, err := gitRun{dir: top, args: []string{"rev-parse", "--path-format=absolute", "--git-path", "config"}}.output(ctx)
	if p := strings.TrimSpace(string(out)); err == nil && p != "" {
		return p
	}
	return filepath.Join(top, ".git", "config")
}
