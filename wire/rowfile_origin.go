package wire

// rowfile_origin.go — where an unusable row file came from (devco R-8). A
// file lore leaves out of every read (a link, invalid JSON, a wrong id) is
// often not the reader's doing: a teammate pushed it with plain git and the
// reader's sync brought it in. The old next step ("put the file back: git
// checkout -- <file>", "fix or remove it") then restored the very same file
// or asked the reader to edit someone else's commit with no name attached.
// When the file is committed and unchanged since, the line names the
// commit and its author and gives the step that undoes it; a file changed
// or never committed here keeps its own step (the local edit is the cause).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/store"
)

// rowFileOrigin is the commit that last brought an unusable row file in.
type rowFileOrigin struct {
	sha    string // short commit id
	author string // "Name <email>" of the commit's git author
	mine   bool   // the author's email is the reader's own git user.email
}

// rowFileOriginOf finds the commit of the row file gitPath (relative to
// the top of dir, slash-separated) when the file is tracked and has no
// change in the work tree or index; ok is false otherwise or when git
// cannot say (no repo, no commit): the caller keeps the file's own step.
func rowFileOriginOf(ctx context.Context, dir, gitPath string) (rowFileOrigin, bool) {
	if dir == "" || gitPath == "" {
		return rowFileOrigin{}, false
	}
	st, err := (gitRun{dir: dir, args: []string{"status", "--porcelain", "--ignored", "--", gitPath}}).output(ctx)
	if err != nil || strings.TrimSpace(string(st)) != "" {
		return rowFileOrigin{}, false // changed, untracked or ignored here: the reader's own edit
	}
	out, err := (gitRun{dir: dir, args: []string{"log", "-1", "--format=%h%x00%an%x00%ae", "--", gitPath}}).output(ctx)
	if err != nil {
		return rowFileOrigin{}, false
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\x00")
	if len(parts) != 3 || parts[0] == "" {
		return rowFileOrigin{}, false // not committed
	}
	o := rowFileOrigin{sha: parts[0], author: parts[1] + " <" + parts[2] + ">"}
	if me, merr := (gitRun{dir: dir, args: []string{"config", "user.email"}}).output(ctx); merr == nil {
		o.mine = strings.EqualFold(strings.TrimSpace(string(me)), parts[2])
	}
	return o, true
}

// step is the next step for a committed unusable row file in dir: who
// added it in which commit, and the commit that undoes it.
func (o rowFileOrigin) step(dir string) string {
	revert := "git -C " + dir + " revert " + o.sha
	if o.mine {
		return "you committed it in " + o.sha + ": fix the file and commit, or undo that commit: " + revert + " (then lore sync)"
	}
	return "it came in commit " + o.sha + " by " + o.author + ": ask them to fix it, or undo that commit: " + revert + " (then lore sync)"
}

// rowFileStep is the next step of the unusable row file rel of store src:
// the commit and author when the file came committed (rowFileOriginOf),
// else the file's own step (rowFileNext).
func rowFileStep(ctx context.Context, b store.Backend, src store.Source, folder, rel, own string) string {
	d, ok := concrete(b).(interface{ Dir() string })
	if !ok {
		return own
	}
	if o, found := rowFileOriginOf(ctx, d.Dir(), rowFileRel(b, src, folder, rel)); found {
		return o.step(d.Dir())
	}
	return own
}
