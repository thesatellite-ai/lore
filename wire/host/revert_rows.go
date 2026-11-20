package host

// revert_rows.go — lore rows a `git revert` deletes (devco M-6). The
// pre-commit hook stages lore's uncommitted row files into whatever commit
// the person makes next (precommit.go: rows travel with your commits), so a
// note written before a code change rides in that code commit. Reverting the
// code commit later deletes the note's file too: the person reverted their
// code, never the note, and a revert deletes no row through lore (no trash
// entry), so everyone who pulls simply loses it.
//
// Only a revert of a commit that also changed code counts: reverting a
// commit of lore's files alone (a note commit, a lore migration) deletes
// them on purpose.
//
//	post-commit (SyncHookCommand), HEAD a single revert:
//	    row files HEAD deletes, absent from the work tree ─► written back
//	    into the work tree from HEAD^ (untracked: the next commit carries
//	    them) and one notice naming each row and the commit that keeps it
//	pre-push (live_ride.go prePush), per updated ref:
//	    pushed revert commits that delete row files still absent from the
//	    pushed tip ─► refused (E_NEEDS_CONFIRM) naming each row, the
//	    command that commits it back, and EnvAllowRowDrop for a drop on
//	    purpose
//
// A revert of several commits (git revert A..B) runs post-commit while its
// sequencer is busy, so only the pre-push refusal catches it. A git error
// says nothing: this guards a git command, it never breaks one.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
)

// revertTrailer matches the line git writes into a revert's message
// ("This reverts commit <sha>."), the one mark a revert commit carries.
var revertTrailer = regexp.MustCompile(`(?m)^This reverts commit ([0-9a-f]{7,64})`)

// revertCommitMsg is the commit message the hint suggests for the rows a
// revert removed.
const revertCommitMsg = "lore: keep rows a revert removed"

// revertedOf is the sha a commit message says it reverts ("" when the
// message is not a revert's).
func revertedOf(msg string) string {
	m := revertTrailer.FindStringSubmatch(msg)
	if m == nil {
		return ""
	}
	return m[1]
}

// revertDeletedRows are the row files the revert commit rev deletes against
// its first parent, as paths from the top of the work tree ("" rev or not
// a revert: none).
func revertDeletedRows(ctx context.Context, repo *gitref.Repo, rev string) []string {
	msg, err := repo.Run(ctx, gitref.Invocation{Args: []string{"log", "-1", "--format=%B", rev}})
	if err != nil || revertedOf(string(msg)) == "" {
		return nil
	}
	if !changesCode(gitLines(ctx, repo, "diff", "--name-only", "--no-renames", rev+"^", rev)) {
		return nil // a revert of lore's files only (a lore commit, a note commit): deleting them was the point
	}
	var rows []string
	for _, p := range gitLines(ctx, repo, "diff", "--name-only", "--no-renames", "--diff-filter=D", rev+"^", rev, "--", rowFileSpec) {
		if isRowFile(p) {
			rows = append(rows, p)
		}
	}
	return rows
}

// changesCode reports whether one of paths (from the top of the work tree)
// is outside every .lore folder: the reverted commit was a code commit the
// rows only rode in, not a commit of lore's own files.
func changesCode(paths []string) bool {
	for _, p := range paths {
		if p != rowfs.LoreDir && !strings.HasPrefix(p, rowfs.LoreDir+"/") && !strings.Contains(p, "/"+rowfs.LoreDir+"/") {
			return true
		}
	}
	return false
}

// afterRevert is the post-commit part that keeps the rows a revert
// removed: when HEAD is a revert whose deleted row files are not in the
// work tree, it writes them back from HEAD^ (work tree only, so the next
// commit carries them) and says so. Never fails the commit.
func (h *liveHost) afterRevert(ctx context.Context) {
	cwd, err := os.Getwd()
	if err != nil || historyInProgress(ctx, h.git(), cwd) {
		return // a revert of several commits: the pre-push refusal names them
	}
	top, _ := livePlace(cwd)
	repo, err := gitref.Open(ctx, top, h.git())
	if err != nil || repo.Bare() {
		return
	}
	gone := revertDeletedRows(ctx, repo, "HEAD")
	var back []string
	for _, p := range gone {
		if _, serr := os.Lstat(filepath.Join(top, filepath.FromSlash(p))); serr == nil {
			continue // already back (the person restored it)
		}
		b, ok, rerr := repo.ReadFile(ctx, "HEAD^", p)
		if rerr != nil || !ok {
			continue
		}
		full := filepath.Join(top, filepath.FromSlash(p))
		if werr := rowfs.WriteShared(full, b); werr != nil { // the work tree's modes, group access in a group's checkout (D-785)
			continue
		}
		back = append(back, p)
	}
	if len(back) == 0 {
		return
	}
	rows := withCommits(ctx, repo, "HEAD^", back)
	h.line("lore: this revert deleted " + countRows(len(rows)) + " you never deleted with lore; lore put the file(s) back in your work tree, so the note stays" + rowList(rows) +
		"\n  commit them before you push: git add " + strings.Join(back, " ") + " && git commit -m \"" + revertCommitMsg + "\"" +
		"\n  drop a note on purpose: " + deleteHint(rows[0]) + " (each row), then commit")
}

// deleteHint is the command that deletes row d through lore (to the
// trash), or the command that names it when its table's noun is not its
// plain singular.
func deleteHint(d droppedRow) string {
	if n := tableNoun(d.table()); n != "" {
		return "lore " + n + " delete --id " + d.id() + " --yes"
	}
	return "lore show --id " + d.id() + " (it names the row's delete command)"
}

// tableNoun is the command noun of a table whose noun is its plain
// singular (decisions → decision, memories → memory); "" otherwise, so a
// hint never names a command that does not exist (engine/handedit.go
// tableNoun is the same rule).
func tableNoun(table string) string {
	switch {
	case strings.Contains(table, "_"):
		return ""
	case strings.HasSuffix(table, "ies"):
		return strings.TrimSuffix(table, "ies") + "y"
	case strings.HasSuffix(table, "s"):
		return strings.TrimSuffix(table, "s")
	}
	return ""
}

// pushRevertedRows are the rows the pushed revert commits delete that the
// pushed tip still lacks, per updated ref: the commits the remote does not
// have yet (remote tip..pushed tip, or everything no remote branch holds
// for a new branch).
func pushRevertedRows(ctx context.Context, repo *gitref.Repo, updates []pushUpdate) []revertedRow {
	var all []revertedRow
	seen := map[string]bool{}
	for _, u := range updates {
		if isZeroOID(u.localSHA) || u.localSHA == u.remoteSHA {
			continue // a deleted branch, nothing moved
		}
		args := []string{"rev-list", "--no-merges", u.localSHA}
		if isZeroOID(u.remoteSHA) || u.remoteSHA == "" {
			args = append(args, "--not", "--remotes")
		} else if _, err := repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "-e", u.remoteSHA + "^{commit}"}}); err == nil {
			args = append(args, "^"+u.remoteSHA)
		} else {
			args = append(args, "--not", "--remotes")
		}
		for _, c := range gitLines(ctx, repo, args...) {
			for _, p := range revertDeletedRows(ctx, repo, c) {
				if seen[p] {
					continue
				}
				if _, ok, err := repo.ReadFile(ctx, u.localSHA, p); err != nil || ok {
					continue // back at the tip (committed again), or unreadable
				}
				seen[p] = true
				all = append(all, revertedRow{droppedRow: withCommits(ctx, repo, c+"^", []string{p})[0], revert: c})
			}
		}
	}
	return all
}

// revertedRow is a row file a pushed revert commit deletes.
type revertedRow struct {
	droppedRow
	// revert is the revert commit's full sha.
	revert string
}

// prePushRevertCheck refuses a push that carries a revert deleting lore
// rows the person never deleted through lore, unless EnvAllowRowDrop is
// set (devco M-6). The hint commits the files back from the revert's
// parent.
func prePushRevertCheck(ctx context.Context, repo *gitref.Repo, updates []pushUpdate) error {
	if os.Getenv(EnvAllowRowDrop) == envAllowOn {
		return nil
	}
	got := pushRevertedRows(ctx, repo, updates)
	if len(got) == 0 {
		return nil
	}
	rows := make([]droppedRow, 0, len(got))
	var order []string
	byRevert := map[string][]string{}
	for _, r := range got {
		rows = append(rows, r.droppedRow)
		if byRevert[r.revert] == nil {
			order = append(order, r.revert)
		}
		byRevert[r.revert] = append(byRevert[r.revert], r.path)
	}
	restore := make([]string, 0, len(order))
	for _, c := range order {
		restore = append(restore, "git checkout "+shortSHA(c)+"^ -- "+strings.Join(byRevert[c], " "))
	}
	return errs.New(errs.NeedsConfirm, "this push carries a git revert that deletes "+countRows(len(rows))+" never deleted with lore (everyone who pulls loses them)"+rowList(rows)).
		WithHint("keep them: " + strings.Join(restore, " && ") + " && git commit -m \"" + revertCommitMsg + "\", then push again; drop them on purpose: " + deleteHint(rows[0]) + " (each row), commit, then push again, or `" + EnvAllowRowDrop + "=1 git push`")
}

// shortSHA is the first shortOIDLen characters of sha.
func shortSHA(sha string) string {
	if len(sha) > shortOIDLen {
		return sha[:shortOIDLen]
	}
	return sha
}
