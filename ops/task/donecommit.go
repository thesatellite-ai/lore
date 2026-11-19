package task

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// CommitRefResolver is the optional upgrade of the env that turns a
// --commit value (HEAD, a branch, a short sha) into the full sha of the
// commit it names in the working repository (wire runs git rev-parse).
// devco:M-5: task done stores that sha, never the spelling: a stored
// "HEAD" or branch name was resolved later by whoever ran the merge
// watch, in their clone, and marked unmerged work merged.
type CommitRefResolver interface {
	ResolveCommitRef(ctx context.Context, ref string) (string, error)
}

// Commit id lengths: a full sha-1 id, and the bounds of a hex id an env
// without git may keep (git's shortest abbreviation to a full sha-256).
const (
	fullSHAHex = 40
	minHexID   = 4
	maxHexID   = 64
)

// resolveDoneCommit returns the full sha task done records for --commit
// raw ("" stays ""). With a resolver a value that does not resolve to a
// commit here is refused; an env without one (no git) keeps a hex commit
// id as given and refuses anything else, so a symbolic name is never stored.
func (o ops) resolveDoneCommit(ctx context.Context, id, raw string) (string, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return "", nil
	}
	if r, ok := o.env.(CommitRefResolver); ok {
		sha, err := r.ResolveCommitRef(ctx, ref)
		if err == nil && isFullSHA(sha) {
			return strings.ToLower(sha), nil
		}
	} else if isHexID(ref) {
		return strings.ToLower(ref), nil
	}
	return "", errs.New(errs.InvalidInput, "--commit "+ref+" does not name a commit in this repository; task done records the commit's full sha, so it must resolve here").
		WithHint("lore task done --id " + id + " --commit <sha from git log> (or --commit HEAD from the branch that holds the work)")
}

// isFullSHA reports a full hex commit id (sha-1 or sha-256).
func isFullSHA(s string) bool {
	return (len(s) == fullSHAHex || len(s) == maxHexID) && isHexID(s)
}

// isHexID reports a hex commit id of minHexID to maxHexID characters.
func isHexID(s string) bool {
	if len(s) < minHexID || len(s) > maxHexID {
		return false
	}
	for _, c := range strings.ToLower(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
