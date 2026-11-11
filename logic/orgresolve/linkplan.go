package orgresolve

// linkplan.go — PlanLink, the client-side validation of `lore org link`
// (LORE_TEAM_SPEC F11 steps 1 and 4), ported from v0.1.17's
// saas/pkg/aicoder/orgresolve/link_plan.go for cmd/lore (P3-CMDLORE-4).
// Pure: no store is read; picking the store that already holds the folder
// is the writer's job (adapters/orgcfg.WriteLink).

import (
	"errors"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/ids"
)

// LinkRequest is what `lore org link` asks for (F11, client side).
type LinkRequest struct {
	Entry     string   // org entry name
	Folder    string   // --project <folder>
	Own       bool     // --own
	Default   bool     // --default
	StoreURL  string   // --store <url>; "" = the entry's first store
	Remote    string   // --remote <url>; needed only when the repo has several
	ProjectID string   // this project's id; "" = the repo holds no lore data (RemoteProjectID)
	Remotes   []string // this repo's normalised remotes
}

// LinkPlan is the validated link, ready for the store-side writer: add
// {ProjectID, Remote, Own, Default} to <StoreURL>@<Branch>:<Folder>/.lore/
// data/_meta.json "links", commit and push (F11 steps 2–5).
type LinkPlan struct {
	Entry     string
	StoreURL  string // normalised
	Branch    string
	Folder    string
	ProjectID string
	Remote    string // normalised
	Own       bool
	Default   bool
	// StoreExplicit is true when --store named the store, so the writer
	// refuses a folder that lives in another store instead of following
	// it (F13, cookbook 14).
	StoreExplicit bool
	// FromRemote is true when the repo holds no project id (no
	// .lore/data/_meta.json: a client repo that must never hold lore
	// files) and ProjectID is RemoteProjectID(Remote): the id lives only
	// in the store's link row.
	FromRemote bool
}

// remoteProjectDomain keys the natural project id of a repo that holds no
// lore data, so it can never equal a folder's or a `lore init` id.
const remoteProjectDomain = "lore-remote-project"

// RemoteProjectID is the project id of a repo that holds no lore data,
// derived from its normalised remote: every machine computes the same id
// from the same remote, so the link written into the store is found again
// by the repo's remote alone, and nothing is written into the repo.
func RemoteProjectID(remote string) (string, error) {
	id, err := ids.Natural(ids.PrefixProject, remoteProjectDomain, remote)
	if err != nil {
		return "", fmt.Errorf("orgresolve: project id of %s: %w", remote, err)
	}
	return id.String(), nil
}

// Errors from PlanLink (same text as v0.1.17).
var (
	ErrLinkNoRemote   = errors.New("orgresolve: this repo has no hosted remote; a link is made for a project id AND a remote")
	ErrLinkOwnPattern = errors.New("orgresolve: --own is allowed only for repos matching the entry's own: patterns")
)

// PlanLink validates a link request against the config without touching
// any store: the entry exists, the folder name is valid, the project has
// remote is meant, the store is one of the entry's, and --own only on a
// repo matching the entry's own: patterns. A repo with no project id (no
// lore data in it) is linked by its remote: ProjectID is RemoteProjectID.
func PlanLink(cfg *orgconf.Config, req LinkRequest) (LinkPlan, error) {
	o := cfg.FindOrg(req.Entry)
	if o == nil {
		return LinkPlan{}, fmt.Errorf("orgresolve: no org entry named %q (have: %s)", req.Entry, strings.Join(cfg.OrgNames(), ", "))
	}
	if err := orgconf.ValidateFolderName(req.Folder); err != nil {
		return LinkPlan{}, fmt.Errorf("orgresolve: --project %q: %w", req.Folder, err)
	}
	remote, err := pickRemote(req.Remote, req.Remotes)
	if err != nil {
		return LinkPlan{}, err
	}
	pid, fromRemote := req.ProjectID, false
	if pid == "" {
		if pid, err = RemoteProjectID(remote); err != nil {
			return LinkPlan{}, err
		}
		fromRemote = true
	}
	if len(o.Stores) == 0 {
		return LinkPlan{}, fmt.Errorf("orgresolve: entry %s lists no stores under stores", o.Name)
	}
	st := o.Stores[0]
	if req.StoreURL != "" {
		want, nerr := repourl.Normalize(req.StoreURL)
		if nerr != nil {
			return LinkPlan{}, fmt.Errorf("orgresolve: --store %q: %w", req.StoreURL, nerr)
		}
		found := false
		for _, s := range o.Stores {
			if n, serr := repourl.Normalize(s.URL); serr == nil && n == want {
				st, found = s, true
				break
			}
		}
		if !found {
			return LinkPlan{}, fmt.Errorf("orgresolve: --store %s is not one of %s's stores", want, o.Name)
		}
	}
	if req.Own && !matchesAny(o.Own, []string{remote}) {
		return LinkPlan{}, fmt.Errorf("%w (%s does not match %s)", ErrLinkOwnPattern, remote, strings.Join(o.Own, ", "))
	}
	storeURL, err := repourl.Normalize(st.URL)
	if err != nil {
		return LinkPlan{}, fmt.Errorf("orgresolve: store url %q: %w", st.URL, err)
	}
	return LinkPlan{Entry: o.Name, StoreURL: storeURL, Branch: st.EffectiveBranch(), Folder: req.Folder,
		ProjectID: pid, Remote: remote, Own: req.Own, Default: req.Default, StoreExplicit: req.StoreURL != "", FromRemote: fromRemote}, nil
}

// pickRemote returns the one remote a link is for: --remote when given (it
// must be one of the repo's), else the only remote; several without
// --remote is refused rather than guessed.
func pickRemote(flag string, remotes []string) (string, error) {
	if flag != "" {
		n, err := repourl.Normalize(flag)
		if err != nil {
			return "", fmt.Errorf("orgresolve: --remote %q: %w", flag, err)
		}
		for _, r := range remotes {
			if r == n {
				return n, nil
			}
		}
		return "", fmt.Errorf("orgresolve: --remote %s is not a remote of this repo (have: %s)", n, listOrNone(remotes))
	}
	switch len(remotes) {
	case 0:
		return "", ErrLinkNoRemote
	case 1:
		return remotes[0], nil
	default:
		return "", fmt.Errorf("orgresolve: this repo has %d remotes (%s); choose one with --remote", len(remotes), strings.Join(remotes, ", "))
	}
}
