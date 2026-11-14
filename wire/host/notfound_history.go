package host

// notfound_history.go — the git side of a not-found answer (H-7, H-14):
// a row file git still has, though not on this branch's tip.
//
//	id not found ─► .lore/data/<table>/<id>.json
//	  ├► git log -1 --diff-filter=D HEAD -- <file>
//	  │     a commit on this branch removed it (a git rm): name the commit,
//	  │     its author, and `git checkout <sha>^ -- :/<file>`
//	  └► git log --reflog -1 --diff-filter=AM -- <file>
//	        a commit that had it is on no branch or remote branch (a
//	        force-push or a rebase dropped it, the reflog keeps it): name
//	        it and `git cherry-pick <sha>`
//
// Neither: "" (the older-branch / other-repo hint stands). Nothing is
// fetched; git reads what this clone has.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// histLogFormat prints a commit as "<sha>\x00<author>\x00<subject>".
const histLogFormat = "--format=%H%x00%an%x00%s"

// histShortSHA is how much of a commit id a hint prints.
const histShortSHA = 7

// histCommit is one commit a hint names.
type histCommit struct{ sha, author, subject string }

// short is the commit's abbreviated id.
func (c histCommit) short() string {
	if len(c.sha) > histShortSHA {
		return c.sha[:histShortSHA]
	}
	return c.sha
}

// about is "<sha> (<subject>, by <author>)".
func (c histCommit) about() string {
	return c.short() + " (" + strings.TrimSpace(c.subject) + ", by " + c.author + ")"
}

// lastCommit is the newest commit git log args print with histLogFormat,
// ok=false when there is none or git fails.
func lastCommit(ctx context.Context, repo *gitref.Repo, args ...string) (histCommit, bool) {
	out, err := repo.Run(ctx, gitref.Invocation{Args: append([]string{"log", "-1", histLogFormat}, args...)})
	if err != nil {
		return histCommit{}, false
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\x00", 3)
	if len(parts) != 3 || parts[0] == "" {
		return histCommit{}, false
	}
	return histCommit{sha: parts[0], author: parts[1], subject: parts[2]}, true
}

// onSomeBranch reports whether sha is on a local or remote-tracking
// branch (true when git cannot tell: the hint then says nothing).
func onSomeBranch(ctx context.Context, repo *gitref.Repo, sha string) bool {
	out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"for-each-ref", "--count=1", "--format=%(refname)", "--contains", sha, "refs/heads/", "refs/remotes/"}})
	return err != nil || strings.TrimSpace(string(out)) != ""
}

// rowFileHint explains an id whose row file (rel, from the top of the
// work tree) git still holds off this branch's tip: removed by a commit
// on this branch (H-14), or in a commit a force-push or a rebase dropped
// (H-7). "" when git knows of neither.
func rowFileHint(ctx context.Context, repo *gitref.Repo, id, rel string) string {
	spec := ":/" + rel
	if _, found, err := repo.ReadFile(ctx, "HEAD", rel); err == nil && found {
		return "" // on this branch: the miss is something else (another store, a stale cache)
	}
	if c, ok := lastCommit(ctx, repo, "--diff-filter=D", "HEAD", "--", spec); ok {
		return id + " was removed from the files by commit " + c.about() + ", not deleted with lore: bring it back with `git checkout " + c.short() + "^ -- " + spec + "`, then commit"
	}
	c, ok := lastCommit(ctx, repo, "--reflog", "--diff-filter=AM", "--", spec)
	if !ok || onSomeBranch(ctx, repo, c.sha) {
		return ""
	}
	return id + " is in commit " + c.about() + ", which is on no branch any more (a force-push or a rebase dropped it; git's reflog still has it): bring it back with `git cherry-pick " + c.short() + "` (or only the file: `git checkout " + c.short() + " -- " + spec + "`); the author's clone may still have it too (`git status` there says ahead)"
}
