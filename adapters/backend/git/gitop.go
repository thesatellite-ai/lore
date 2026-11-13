package git

// gitop.go — a person's git operation in progress in a store clone (B2-9).
//
// A store clone is set up for plain git (handgit.go), so a person may be
// in the middle of `git pull` (a merge with conflicts), a rebase, a
// cherry-pick or a bisect there. Lore used to take every MERGE_HEAD for
// the debris of its own crashed sync and ran `git merge --abort`, and its
// hand-edit pick-up ran `git reset`: either one silently threw away the
// merge the person was doing. Now:
//
//	lore's own three-way merge ─▶ <git dir>/lore-merging (removed when the
//	                              merge is committed or aborted)
//	MERGE_HEAD + lore-merging   ─▶ lore's crashed merge: aborted as before
//	any other in-progress state ─▶ the person's: writes, syncs and the
//	                              pick-up leave the clone alone, a write or
//	                              sync is refused naming the way out

import (
	"context"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
)

// ownMergeFile marks a merge lore itself started in the clone's git dir.
const ownMergeFile = "lore-merging"

// ownMergeMode is the marker file's mode (it holds nothing secret, but
// lives in .git, which only the owner writes).
const ownMergeMode = 0o600

// gitOpMarkers maps a file in the git dir to the git operation it means,
// in the order they are checked.
var gitOpMarkers = []struct{ file, op, way string }{
	{"rebase-merge", "rebase", "git rebase --continue or git rebase --abort"},
	{"rebase-apply", "rebase", "git rebase --continue (git am --continue) or git rebase --abort"},
	{"MERGE_HEAD", "merge", "git add the files and git commit, or git merge --abort"},
	{"CHERRY_PICK_HEAD", "cherry-pick", "git cherry-pick --continue or git cherry-pick --abort"},
	{"REVERT_HEAD", "revert", "git revert --continue or git revert --abort"},
	{"sequencer", "cherry-pick", "git cherry-pick --continue or git cherry-pick --abort"},
	{"BISECT_LOG", "bisect", "git bisect reset"},
}

// gitDirOf is the absolute git dir of the checkout at dir ("" when it
// cannot be read).
func (b *Backend) gitDirOf(ctx context.Context, dir string) string { return gitDirIn(ctx, b.git, dir) }

// gitDirIn is gitDirOf through runner r. A store clone is a plain clone
// whose git dir is the .git folder at its top: that is read from the file
// system, with no git process (lore doctor --all checks every clone and
// stays within its git budget); anything else (a .git file of a worktree,
// a folder below the top) asks git.
func gitDirIn(ctx context.Context, r runner, dir string) string {
	if gd, ok := plainGitDir(dir); ok {
		return gd
	}
	out, err := r.run(ctx, call{dir: dir, args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return ""
	}
	return trimmed(out)
}

// personGitOp is the git operation a person has in progress in the clone
// at dir (op "" when none), with the commands that end it. A merge lore
// started itself (ownMergeFile) is not the person's.
func (b *Backend) personGitOp(ctx context.Context, dir string) (op, way string) {
	return personGitOpIn(ctx, b.git, dir)
}

// personGitOpIn is personGitOp through runner r.
func personGitOpIn(ctx context.Context, r runner, dir string) (op, way string) {
	gd := gitDirIn(ctx, r, dir)
	if gd == "" {
		return "", ""
	}
	own := exists(filepath.Join(gd, ownMergeFile))
	for _, m := range gitOpMarkers {
		if m.file == "MERGE_HEAD" && own {
			continue
		}
		if exists(filepath.Join(gd, m.file)) {
			return m.op, m.way
		}
	}
	return "", ""
}

// PersonGitOp is personGitOp for the backend's own clone (clone mode;
// "" in repo mode, where the code repo's hooks handle it).
func (b *Backend) PersonGitOp(ctx context.Context) (op, way string) {
	if b.mode != modeClone {
		return "", ""
	}
	return b.personGitOp(ctx, b.work)
}

// refusePersonGitOp is the refusal of a write or sync while a person's git
// operation is in progress in the clone at dir; nil when there is none.
func (b *Backend) refusePersonGitOp(ctx context.Context, dir string) error {
	return refusePersonGitOpIn(ctx, b.git, dir)
}

// refusePersonGitOpIn is refusePersonGitOp through runner r.
func refusePersonGitOpIn(ctx context.Context, r runner, dir string) error {
	op, way := personGitOpIn(ctx, r, dir)
	if op == "" {
		return nil
	}
	return GitOpInProgressError(dir, op, way)
}

// GitOpInProgressError is the refusal lore gives while the store clone at
// dir is in the middle of the person's git op; the pick-up prints the same
// words as a note.
func GitOpInProgressError(dir, op, way string) *errs.Error {
	return errs.New(errs.Refused, "store clone "+dir+" is in the middle of a git "+op+"; lore leaves it alone until it is finished").
		WithHint("in " + dir + ": " + way + "; then run the lore command again")
}

// markOwnMerge records that lore is starting a merge in the clone.
func (b *Backend) markOwnMerge(ctx context.Context) error {
	gd := b.gitDirOf(ctx, b.work)
	if gd == "" {
		return errs.New(errs.Internal, "git: cannot find the git folder of "+b.work)
	}
	if err := os.WriteFile(filepath.Join(gd, ownMergeFile), nil, ownMergeMode); err != nil {
		return errs.FileIO("write", filepath.Join(gd, ownMergeFile), err)
	}
	return nil
}

// clearOwnMerge removes lore's merge mark (its merge was committed or
// aborted). Best effort: a mark left with no MERGE_HEAD means nothing.
func (b *Backend) clearOwnMerge(ctx context.Context) {
	if gd := b.gitDirOf(ctx, b.work); gd != "" {
		_ = os.Remove(filepath.Join(gd, ownMergeFile)) // absent is fine
	}
}

// PersonGitOpAt is personGitOp for the clone at dir (lore doctor and store
// doctor report it, B2-26): op "" when none, else the operation and the
// commands that finish or abort it.
func PersonGitOpAt(ctx context.Context, dir string) (op, way string) {
	b, err := sharedBackend(dir, false)
	if err != nil {
		return "", ""
	}
	return b.personGitOp(ctx, dir)
}

// plainGitDir is the absolute .git folder at the top of the checkout at
// dir, when dir is such a top (a .git folder holding HEAD).
func plainGitDir(dir string) (string, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	gd := filepath.Join(abs, ".git")
	if st, err := os.Lstat(gd); err != nil || !st.IsDir() {
		return "", false
	}
	if st, err := os.Lstat(filepath.Join(gd, "HEAD")); err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return gd, true
}

// RecoverOwnMerge aborts a merge a killed run of lore left in the clone
// (MERGE_HEAD beside lore's own mark), under the store lock, as the next
// sync would. The hand-edit pick-up calls it first: it runs before any
// sync, and would otherwise take the half-merged row files for hand edits
// and commit them. A person's merge is never touched (personGitOp); no
// leftover merge costs no git process and no lock.
func (b *Backend) RecoverOwnMerge(ctx context.Context) error {
	if b.mode != modeClone {
		return nil
	}
	gd := b.gitDirOf(ctx, b.work)
	if gd == "" || !exists(filepath.Join(gd, ownMergeFile)) || !exists(filepath.Join(gd, "MERGE_HEAD")) {
		return nil
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return err
	}
	return releaseKeep(b.recoverMerge(ctx), lock)
}
