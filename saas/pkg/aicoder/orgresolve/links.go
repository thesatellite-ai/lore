package orgresolve

import "context"

// Link is one store link that names this repo (F11): "client project id P
// at remote R belongs to <entry>'s store folder <Folder>". Written by
// `lore org link`, stored only in the store's `_meta.json`.
type Link struct {
	// Entry is the org entry whose store holds the link.
	Entry string
	// StoreURL is the normalised URL of the store holding the folder.
	StoreURL string
	// Folder is the internal project folder in that store.
	Folder string
	// Remote is the normalised remote the link was made for.
	Remote string
	// Own marks a link made with `--own` (the org's own repo; no guards).
	Own bool
	// Default marks the link `--to <org>` uses when several exist (TEAM-032).
	Default bool
}

// LinkStatus says how far the link answer can be trusted (F10 fail-closed
// rule, F11 link index).
type LinkStatus int

const (
	// LinkStatusNotChecked: no link source is wired (phase P1, before stores
	// exist). Org resolution runs rules 1–3, 5 and 6 only; nothing is
	// claimed about links and nothing fails closed on their account.
	LinkStatusNotChecked LinkStatus = iota
	// LinkStatusKnown: every store of every non-staging entry was read (or
	// its cached index is fresh), so Links is complete.
	LinkStatusKnown
	// LinkStatusUnknown: at least one store could not be read and had no
	// usable cached index; Links may be incomplete. Writes without an
	// explicit --to and internal rendering fail closed (E_LINK_UNKNOWN).
	LinkStatusUnknown
)

// String names the status for whoami / explain output.
func (s LinkStatus) String() string {
	switch s {
	case LinkStatusKnown:
		return "known"
	case LinkStatusUnknown:
		return "unknown"
	default:
		return "not checked"
	}
}

// LinkResult is a LinkSource's answer for one repo.
type LinkResult struct {
	Status LinkStatus
	// Links are every link matching the project id AND one of the remotes,
	// from non-staging entries only (TEAM-022). A fork carrying the same
	// project id under another remote is not linked (F11).
	Links []Link
	// Detail explains the status in one line (which store was unreachable,
	// how old the cached index is, or why nothing was checked).
	Detail string
}

// LinkSource is the seam to the store-side link index (F11). Phase P1 ships
// NoLinks; the store implementation (scan of every store's `_meta.json`,
// cached per store commit) comes with the stores work.
type LinkSource interface {
	// Links returns the links for projectID at any of remotes (normalised).
	// An unreachable store is reported through Status, not as an error;
	// the error is for a broken index (corrupt cache, bad config).
	// Links of staging entries may be included: Resolve never matches on
	// them and attaches them only when that entry is chosen on purpose.
	Links(ctx context.Context, projectID string, remotes []string) (LinkResult, error)
}

// NoLinks is the P1 LinkSource: stores are not read yet, so it reports
// LinkStatusNotChecked with a reason every caller can print — never an
// empty "known" answer that would pass for "this repo has no links".
type NoLinks struct{}

// notCheckedDetail is printed by whoami and explain while no store is read.
const notCheckedDetail = "store links are not read yet (they arrive with `lore org link` and stores)"

// Links implements LinkSource.
func (NoLinks) Links(context.Context, string, []string) (LinkResult, error) {
	return LinkResult{Status: LinkStatusNotChecked, Detail: notCheckedDetail}, nil
}

// StaticLinks is a LinkSource over a fixed list, for tests and for
// `lore org link` until links are written to stores. It filters by project
// id and remote exactly like the real index (id AND remote must match).
type StaticLinks struct {
	Status LinkStatus
	Detail string
	// All holds every link known, keyed by project id.
	All map[string][]Link
}

// Links implements LinkSource.
func (s StaticLinks) Links(_ context.Context, projectID string, remotes []string) (LinkResult, error) {
	res := LinkResult{Status: s.Status, Detail: s.Detail}
	want := make(map[string]bool, len(remotes))
	for _, r := range remotes {
		want[r] = true
	}
	for _, l := range s.All[projectID] {
		if want[l.Remote] {
			res.Links = append(res.Links, l)
		}
	}
	return res, nil
}
