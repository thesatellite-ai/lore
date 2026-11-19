package git

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// authorityHold is why the push of tip must not run: the local commits
// (on tip, not on the remote branch's tracking tip) that made a change
// every reader of the store holds back (authority.go, Backend.Held), as
// one line naming the first held row, the commit, its author, the reason
// and the way out; "" when no local commit made a held change.
//
// Why (devco D-694): a member's hand-made commit the roster check holds
// back (a team change, an admin stint) was still pushed by their sync, so
// the remote's row file carried the change while every reader's lore held
// it back. Every push of the store branch (sync, its partial send, a
// layer's landing) asks this first and keeps such commits on this machine
// like the push hold of pushmodes.go. A commit undone later (git revert
// back to the accepted content) holds nothing, so the undo is the way out
// for the author; the change itself is an admin's to make through lore.
func (b *Backend) authorityHold(ctx context.Context, tip string) (string, error) {
	if b.mode != modeClone || tip == "" {
		return "", nil
	}
	a, err := b.authorityOf(ctx, tip)
	if err != nil {
		return "", err // fail closed: a check that cannot run never lets a push through
	}
	if a == nil || len(a.held) == 0 {
		return "", nil
	}
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return "", err
	}
	args := []string{"rev-list", tip}
	if remote != "" {
		args = append(args, "--not", remote)
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: list the local commits for the roster check", err)
	}
	order := strings.Fields(string(out)) // newest first: the order a revert undoes them in
	local := map[string]bool{}
	for _, c := range order {
		local[c] = true
	}
	var held []HeldChange
	commits := map[string]bool{}
	for _, h := range a.held {
		if local[h.Commit] {
			held = append(held, h)
			commits[h.Commit] = true
		}
	}
	if len(held) == 0 {
		return "", nil
	}
	sort.Slice(held, func(i, j int) bool { return held[i].Path < held[j].Path })
	first := held[0]
	undo := make([]string, 0, len(commits))
	for _, c := range order {
		if commits[c] {
			undo = append(undo, c[:min(len(c), shortCommitLen)])
		}
	}
	return first.Table + " " + first.ID + " (commit " + first.Short() + " by " + first.Author + "): " + first.Reason +
		"; " + strconv.Itoa(len(local)) + " local commit(s) kept on this machine, not pushed" +
		" (an admin makes that change with lore; to drop yours run: git -C " + b.work + " revert --no-edit " + strings.Join(undo, " ") + ", then lore sync)", nil
}
