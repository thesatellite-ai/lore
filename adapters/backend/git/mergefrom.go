package git

// mergefrom.go — take in what another address of the same store holds
// (devco D-699 follow-up). After `lore store set-url` the clone's origin
// is the new repository, while a teammate whose lore had not followed yet
// may have pushed to the old one, before or after the move. Those commits
// were on no address the admin's lore fetched, so they never reached the
// new repository, and the push of the move to the old address was refused
// as if the teammate had pushed after it. MergeFrom fetches the old
// address's store branch and merges it the way a sync merges origin
// (mergeRemote: fast-forward, or a three-way merge whose rows go through
// the Merger), so the next push to the new address carries every commit
// and the push of the move to the old address is a fast-forward.

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// fromRefNS is where MergeFrom keeps the fetched branch of another
// address: outside refs/remotes, so no tracking ref of origin (the reset
// and rewind checks read those) ever names another repository's commit.
const fromRefNS = "refs/lore-from/"

// MergedFrom is what MergeFrom took in from another address.
type MergedFrom struct {
	Commits int             // commits that arrived (the address held them, the clone did not)
	Refused []store.Refusal // rows that could not be merged: nothing was merged, the clone is as it was
	Offline bool            // the address did not answer: nothing changed
	Detail  string          // git's words when Offline
}

// MergeFrom fetches the store branch of url (another address of this
// store, never origin) and merges it into the clone's store branch under
// the store lock, as a sync merges origin. A url without the branch, or
// one whose commits the clone already holds, takes nothing. Nothing is
// pushed: the caller's sync sends the merge to origin.
func (b *Backend) MergeFrom(ctx context.Context, url string) (_ MergedFrom, err error) {
	if b.mode != modeClone || b.branch == "" || url == "" {
		return MergedFrom{}, nil
	}
	if b.unwritable {
		return MergedFrom{}, b.refusedWrite()
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return MergedFrom{}, err
	}
	defer func() { err = releaseKeep(err, lock) }()
	ref := fromRefNS + b.branch
	defer func() { _, _ = b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", "-d", ref}}) }() // best effort: the next MergeFrom overwrites it anyway
	if _, ferr := b.git.run(ctx, call{dir: b.work, args: []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--", url, "+" + headsPrefix + b.branch + ":" + ref}}); ferr != nil {
		if _, ran := exitCode(ferr); !ran {
			return MergedFrom{}, ferr
		}
		if strings.Contains(stderrOf(ferr), msgMissingRemoteRef) {
			return MergedFrom{}, nil // the old address never had the branch: nothing to take
		}
		return MergedFrom{Offline: true, Detail: stderrOf(ferr)}, nil
	}
	tip, err := b.resolve(ctx, b.work, ref)
	if err != nil || tip == "" {
		return MergedFrom{}, err
	}
	putBack, err := b.setAsideHandEdits(ctx)
	if err != nil {
		return MergedFrom{}, err
	}
	defer func() { err = errors.Join(err, putBack()) }()
	h, err := b.head(ctx)
	if err != nil {
		return MergedFrom{}, err
	}
	n := 0
	spec := tip
	if h != "" {
		spec = h + ".." + tip
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-list", "--count", spec}})
	if err != nil {
		return MergedFrom{}, errs.Wrap(errs.Internal, "git: count the commits of "+url, err)
	}
	if n, err = strconv.Atoi(trimmed(out)); err != nil {
		return MergedFrom{}, errs.Wrap(errs.Internal, "git: count the commits of "+url, err)
	}
	if n == 0 {
		return MergedFrom{}, nil
	}
	_, refused, _, err := b.mergeRemote(ctx, tip)
	if err != nil {
		return MergedFrom{}, err
	}
	if len(refused) > 0 {
		return MergedFrom{Refused: refused}, nil
	}
	return MergedFrom{Commits: n}, nil
}
