package stores

// branch.go — the one answer to "which store branch does this entry or url
// name?" (INTEG-SYNC-5). store.list's BRANCH column and store.open's
// checkout both ask PinnedBranch, so the two can never disagree:
//
//	branch (an explicit --branch, or the entry's `branch:` key)
//	  └─ "" ─▶ the url's ?branch= (`lore store init stag '<url>?branch=staging'`)
//	            └─ "" ─▶ "" : the remote's default branch, which only the
//	                         caller can learn (store.list from the local
//	                         clone, DefaultBrancher; store.open from the
//	                         remote)
//
// The entry's key wins over the url's query because that is what opening
// the store does: wire's canonical() sets the entry's branch over any
// ?branch= already on the url.

import (
	"context"
	"net/url"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
)

// PinnedBranch is the store branch rawURL and branch name together:
// branch when set, else rawURL's ?branch= (gitbackend.BranchParam), else
// "" (the remote's default branch). The query is read without parsing the
// rest of the url, so every spelling git accepts (scp-like
// git@host:owner/repo, ssh://, https://, file://) works the same. A url
// that names the parameter twice or empty is refused (E_INVALID_INPUT),
// as the git backend refuses it at open; a query that does not parse is
// refused only when branch does not already decide.
func PinnedBranch(rawURL, branch string) (string, error) {
	if branch != "" {
		return branch, nil
	}
	_, query, ok := strings.Cut(rawURL, "?")
	if !ok {
		return "", nil
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return "", errs.Wrap(errs.InvalidInput, "the store url query does not parse: "+rawURL, err)
	}
	vals, ok := q[gitbackend.BranchParam]
	if !ok {
		return "", nil
	}
	if len(vals) != 1 || vals[0] == "" {
		return "", errs.New(errs.InvalidInput, "the url names more than one branch, or an empty one: "+rawURL).
			WithHint("keep one ?branch=<name> on the url, or set `branch:` on the entry in .lore/stores.yaml")
	}
	return vals[0], nil
}

// DefaultBrancher is the optional Manager upgrade behind the BRANCH column
// of a store that pins no branch: the branch this machine's clone of the
// store follows (the remote's default when it was cloned), read from local
// files only, so store.list still never waits on a remote. "" when there
// is no local clone, or the store has no branches (a file:// folder): the
// column then stays empty rather than guess a name.
type DefaultBrancher interface {
	CloneDefaultBranch(ctx context.Context, e Entry) string
}
