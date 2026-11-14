package host

// rewrite_rows.go — lore rows a history rewrite drops (H-7). A row file
// lives in a commit; a force-push that rewinds the remote branch, or a
// rebase that leaves a commit out, takes the row away from everyone with
// no delete anywhere, and lore's reads then only say "not found".
//
//	pre-push (live_ride.go prePush), per updated ref:
//	  remote tip known here and not an ancestor of what is pushed (a
//	  forced, non-fast-forward push)
//	    ─► row files on the remote tip, absent from the pushed tip, that
//	       no pushed commit deletes ─► refused (E_NEEDS_CONFIRM) naming
//	       them; EnvAllowRowDrop=1 pushes anyway
//	post-rewrite rebase (SyncHookCommand refresh):
//	    row files in ORIG_HEAD (the tip before the rebase), absent from
//	    HEAD, that no commit of ORIG_HEAD..HEAD deletes ─► one notice per
//	    row naming the commit that still has it and `git cherry-pick`
//
// A git error says nothing (the push goes on): this is a warning about a
// rewrite, not a gate on data lore wrote.

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
)

// EnvAllowRowDrop = "1" lets a forced push drop lore rows from the remote
// branch (the pre-push refusal names it).
const EnvAllowRowDrop = "LORE_ALLOW_ROW_DROP"

// envAllowOn is EnvAllowRowDrop's "yes".
const envAllowOn = "1"

// rowFileSpec matches every row file of every project folder:
// [<folder>/].lore/data/<table>/<id>.json.
const rowFileSpec = ":(glob)**/.lore/data/*/*.json"

// droppedRow is one row file a rewrite takes away.
type droppedRow struct {
	// path is the file from the top of the work tree.
	path string
	// commit is the newest commit (short id) that still has it, and
	// subject that commit's subject ("" when unknown).
	commit, subject string
}

// id is the row id (the file name without .json).
func (d droppedRow) id() string { return strings.TrimSuffix(path.Base(d.path), rowFileExt) }

// table is the row's table (its folder name).
func (d droppedRow) table() string { return path.Base(path.Dir(d.path)) }

// gitLines runs git in repo and returns its non-empty output lines (nil on
// any error).
func gitLines(ctx context.Context, repo *gitref.Repo, args ...string) []string {
	out, err := repo.Run(ctx, gitref.Invocation{Args: args})
	if err != nil {
		return nil
	}
	var lines []string
	for l := range strings.SplitSeq(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// lostRowFiles are the row files present in from and absent from to that
// no commit of from..to deletes (a deletion on purpose is not a loss).
func lostRowFiles(ctx context.Context, repo *gitref.Repo, from, to string) []string {
	gone := gitLines(ctx, repo, "diff", "--name-only", "--no-renames", "--diff-filter=D", from, to, "--", rowFileSpec)
	if len(gone) == 0 {
		return nil
	}
	deleted := gitLines(ctx, repo, "log", "--format=", "--name-only", "--no-renames", "--diff-filter=D", from+".."+to, "--", rowFileSpec)
	var lost []string
	for _, p := range gone {
		if isRowFile(p) && !slices.Contains(deleted, p) {
			lost = append(lost, p)
		}
	}
	return lost
}

// isRowFile: <…>/.lore/data/<table>/<id>.json with a row-id-shaped name
// (not _meta.json, _purged.json).
func isRowFile(p string) bool {
	return strings.Contains("/"+p, "/.lore/data/") && looksLikeRowID(strings.TrimSuffix(path.Base(p), rowFileExt))
}

// withCommits names, for each lost file, the newest commit of rev that
// has it.
func withCommits(ctx context.Context, repo *gitref.Repo, rev string, files []string) []droppedRow {
	out := make([]droppedRow, 0, len(files))
	for _, f := range files {
		d := droppedRow{path: f}
		if c, ok := lastCommit(ctx, repo, "--diff-filter=AM", rev, "--", ":/"+f); ok {
			d.commit, d.subject = c.short(), c.subject
		}
		out = append(out, d)
	}
	return out
}

// pushDroppedRows are the rows a forced push takes off the remote: per
// updated ref whose remote tip this clone has and the push does not build
// on, the lost row files between the two tips.
func pushDroppedRows(ctx context.Context, repo *gitref.Repo, updates []pushUpdate) []droppedRow {
	var all []droppedRow
	for _, u := range updates {
		if isZeroOID(u.localSHA) || isZeroOID(u.remoteSHA) || u.localSHA == u.remoteSHA {
			continue // a new branch, a deleted one, nothing moved
		}
		if _, err := repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "-e", u.remoteSHA + "^{commit}"}}); err != nil {
			continue // the remote tip is not here (not fetched): nothing to compare
		}
		if ff, err := repo.IsAncestor(ctx, u.remoteSHA, u.localSHA); err != nil || ff {
			continue // a fast-forward drops nothing
		}
		all = append(all, withCommits(ctx, repo, u.remoteSHA, lostRowFiles(ctx, repo, u.remoteSHA, u.localSHA))...)
	}
	return all
}

// rowList is "dec_… (decisions, in 3aabf17 "Record …")" lines, one per row.
func rowList(rows []droppedRow) string {
	var b strings.Builder
	for _, d := range rows {
		b.WriteString("\n  " + d.id() + " (" + d.table())
		if d.commit != "" {
			b.WriteString(", in " + d.commit + " " + quoteSubject(d.subject))
		}
		b.WriteString(")")
	}
	return b.String()
}

// quoteSubject is a commit subject in double quotes.
func quoteSubject(s string) string { return `"` + strings.TrimSpace(s) + `"` }

// prePushDropCheck refuses a forced push that would take lore rows off
// the remote branch, unless EnvAllowRowDrop is set (H-7).
func prePushDropCheck(ctx context.Context, repo *gitref.Repo, updates []pushUpdate) error {
	if os.Getenv(EnvAllowRowDrop) == envAllowOn {
		return nil
	}
	rows := pushDroppedRows(ctx, repo, updates)
	if len(rows) == 0 {
		return nil
	}
	return errs.New(errs.NeedsConfirm, "this push rewrites the remote branch and takes "+countRows(len(rows))+" off it, with no delete (everyone who pulls loses them)"+rowList(rows)).
		WithHint("keep them: `git pull --rebase` (or put the commits back with `git cherry-pick <commit>`) and push again; drop them on purpose: `" + EnvAllowRowDrop + "=1 git push --force-with-lease`")
}

// countRows is "1 lore row" / "N lore rows".
func countRows(n int) string {
	if n == 1 {
		return "1 lore row"
	}
	return strconv.Itoa(n) + " lore rows"
}

// origHead is the tip before a rebase (git sets ORIG_HEAD).
const origHead = "ORIG_HEAD"

// rebaseDroppedNotice is the post-rewrite notice of a rebase that left out
// row files no commit deleted (a `git pull --rebase` after someone's
// force-push drops the author's own commit as "already upstream"), ""
// when it left out none.
func rebaseDroppedNotice(ctx context.Context, repo *gitref.Repo) string {
	lost := lostRowFiles(ctx, repo, origHead, "HEAD")
	if len(lost) == 0 {
		return ""
	}
	rows := withCommits(ctx, repo, origHead, lost)
	var shas []string
	for _, d := range rows {
		if d.commit != "" && !slices.Contains(shas, d.commit) {
			shas = append(shas, d.commit)
		}
	}
	msg := "lore: this rebase removed " + countRows(len(rows)) + " that no commit deleted" + rowList(rows)
	if len(shas) > 0 {
		msg += "\n  bring back: git cherry-pick " + strings.Join(shas, " ")
	}
	return msg
}

// RewriteRemap moves commit links from rewritten commits to their new
// shas; pairs are "<old>=<new>" (ops/link link.remap's --pair). cmd/lore
// runs link.remap through its runner (H-8).
type RewriteRemap func(ctx context.Context, pairs []string)

// OnRewrite sets what the post-rewrite hook calls with git's old → new
// map (nil: links stay on the old shas).
func (l *Live) OnRewrite(f RewriteRemap) { l.h.remap = f }

// afterRewrite is the post-rewrite part of `lore sync hook refresh`
// (args: git's hook name and its "$1"): a rebase that left rows out says
// so (H-7), and commit links follow the rewritten shas git lists on stdin
// (H-8). Never fails the hook.
func (h *liveHost) afterRewrite(ctx context.Context, stdin io.Reader, args []string) {
	if len(args) > 0 && args[0] == hookPostCheckout {
		h.rebaseStartNotice(ctx) // a rebase that drops every commit runs no post-rewrite
		return
	}
	if len(args) == 0 || args[0] != hookPostRewrite {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	top, _ := livePlace(cwd)
	repo, err := gitref.Open(ctx, top, h.git())
	if err != nil || repo.Bare() {
		return
	}
	if len(args) >= 2 && args[1] == rewriteByRebase {
		if msg := rebaseDroppedNotice(ctx, repo); msg != "" {
			h.line(msg)
		}
	}
	if h.remap == nil || stdin == nil {
		return
	}
	in, err := io.ReadAll(stdin)
	if err != nil {
		return
	}
	if pairs := rewritePairs(in); len(pairs) > 0 {
		h.remap(ctx, pairs)
	}
}

// rewritePairs reads post-rewrite's stdin ("<old> <new> [extra]" lines)
// as "<old>=<new>" pairs.
func rewritePairs(in []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(in))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] != f[1] {
			out = append(out, f[0]+"="+f[1])
		}
	}
	return out
}

// rebaseTodo is the rebase's list of picks still to do (merge backend).
const rebaseTodo = "rebase-merge/git-rebase-todo"

// rebaseStartNotice is the post-checkout part of afterRewrite: git's
// rebase checks out its base first (post-checkout), and when it has no
// pick left to do (`git pull --rebase` after a force-push upstream drops
// every local commit as already upstream), no post-rewrite follows, so the
// dropped-rows notice is printed here.
func (h *liveHost) rebaseStartNotice(ctx context.Context) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	top, _ := livePlace(cwd)
	repo, err := gitref.Open(ctx, top, h.git())
	if err != nil || repo.Bare() {
		return
	}
	gitDir, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return
	}
	todo, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(gitDir)), filepath.FromSlash(rebaseTodo)))
	if err != nil || picksLeft(todo) {
		return // no rebase, or picks follow: post-rewrite says it at the end
	}
	if msg := rebaseDroppedNotice(ctx, repo); msg != "" {
		h.line(msg)
	}
}

// picksLeft reports whether a rebase todo list has a command left (any
// line that is not blank or a comment).
func picksLeft(todo []byte) bool {
	for l := range strings.SplitSeq(string(todo), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			return true
		}
	}
	return false
}
