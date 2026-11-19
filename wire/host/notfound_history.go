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
//	        it and `git cherry-pick <sha>`; one committed here on a
//	        detached HEAD (no branch reflog ever held it) is named as
//	        that, with `git branch` to keep it (devco:Q-3)
//
// Neither: "" (the older-branch / other-repo hint stands). Nothing is
// fetched; git reads what this clone has.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
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
	back := "`git cherry-pick " + c.short() + "` (or only the file: `git checkout " + c.short() + " -- " + spec + "`)"
	switch commitOrigin(ctx, repo, c.sha) {
	case originDetached:
		return id + " is in commit " + c.about() + ", made on a detached HEAD in this clone, so it is on no branch (nothing dropped it): keep it with `git branch detached-" + c.short() + " " + c.short() + "`, or bring it to this branch with " + back
	case originDroppedHere:
		return id + " is in commit " + c.about() + ", made in this clone and on no branch any more (a rebase or a reset here dropped it; git's reflog still has it): bring it back with " + back
	}
	return id + " is in commit " + c.about() + ", which is on no branch any more (a force-push or a rebase dropped it; git's reflog still has it): bring it back with " + back + "; the author's clone may still have it too (`git status` there says ahead)"
}

// commitWhere is where a commit on no branch came from, as this clone's
// reflogs tell it (devco:Q-3).
type commitWhere int

const (
	// originElsewhere: not committed in this clone (fetched, then a
	// force-push upstream dropped it), or the reflogs cannot tell.
	originElsewhere commitWhere = iota
	// originDetached: committed here while HEAD was detached; no branch
	// ever held it.
	originDetached
	// originDroppedHere: committed here on a branch that no longer holds
	// it (a rebase or a reset in this clone).
	originDroppedHere
)

// commitOrigin reads this clone's reflogs for sha: HEAD's (of this
// worktree) says whether it was committed here, the branches' (shared by
// every worktree) whether a branch ever held it. Read from the files, no
// git process per branch; any read failure answers originElsewhere (the
// hint then says what it said before).
func commitOrigin(ctx context.Context, repo *gitref.Repo, sha string) commitWhere {
	out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--absolute-git-dir", "--path-format=absolute", "--git-common-dir"}})
	if err != nil {
		return originElsewhere
	}
	dirs := strings.Fields(string(out))
	if len(dirs) != 2 {
		return originElsewhere
	}
	head, err := os.ReadFile(filepath.Join(dirs[0], "logs", "HEAD"))
	if err != nil || !reflogHas(head, sha, "commit") {
		return originElsewhere
	}
	held := false
	_ = filepath.WalkDir(filepath.Join(dirs[1], "logs", "refs", "heads"), func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || held {
			return nil // a missing folder: no branch reflogs
		}
		if b, rerr := os.ReadFile(p); rerr == nil && reflogHas(b, sha, "") {
			held = true
			return filepath.SkipAll
		}
		return nil
	})
	if held {
		return originDroppedHere
	}
	return originDetached
}

// reflogHas reports whether a reflog file's text has an entry moving the
// ref to sha whose message starts with msgPrefix ("" any message). A line
// is "<old> <new> <who> <time> <tz>\t<message>".
func reflogHas(text []byte, sha, msgPrefix string) bool {
	for line := range strings.SplitSeq(string(text), "\n") {
		f := strings.SplitN(line, " ", 3)
		if len(f) < 3 || f[1] != sha {
			continue
		}
		_, msg, _ := strings.Cut(line, "\t")
		if strings.HasPrefix(msg, msgPrefix) {
			return true
		}
	}
	return false
}
