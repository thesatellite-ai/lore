package orgresolve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/repourl"
)

// LinkRequest is what `lore org link` asks for (F11, client side).
type LinkRequest struct {
	Entry     string   // org entry name
	Folder    string   // --project <folder>
	Own       bool     // --own
	Default   bool     // --default
	StoreURL  string   // --store <url>; "" = the entry's first store
	Remote    string   // --remote <url>; needed only when the repo has several
	ProjectID string   // this project's id
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
	// StoreExplicit is true when --store named the store, so the writer can
	// refuse a folder that lives in another store instead of following it
	// (F13, cookbook 14); false means StoreURL is the entry's first store
	// by default.
	StoreExplicit bool
}

// LinkWriter is the store-side half of `lore org link` (F11 steps 2–5:
// store check, create the folder project, set-union the link into
// _meta.json, commit and push with retry). It arrives with stores; until
// then the CLI shows the plan and says the write is not available.
type LinkWriter interface {
	WriteLink(ctx context.Context, p LinkPlan) error
}

// Errors from PlanLink.
var (
	ErrLinkNoProjectID = errors.New("orgresolve: this project has no project id yet (.lore/data/_meta.json)")
	ErrLinkNoRemote    = errors.New("orgresolve: this repo has no hosted remote; a link is made for a project id AND a remote")
	ErrLinkOwnPattern  = errors.New("orgresolve: --own is allowed only for repos matching the entry's own: patterns")
)

// PlanLink validates a link request against the config (F11 step 1 and 4),
// without touching any store. Picking "the store already holding <folder>"
// needs the store index and is the writer's job; here the store is --store
// or the entry's first store.
func PlanLink(cfg *lorecfg.Config, req LinkRequest) (LinkPlan, error) {
	o := cfg.FindOrg(req.Entry)
	if o == nil {
		return LinkPlan{}, fmt.Errorf("orgresolve: no org entry named %q (have: %s)", req.Entry, strings.Join(cfg.OrgNames(), ", "))
	}
	if err := lorecfg.ValidateFolderName(req.Folder); err != nil {
		return LinkPlan{}, fmt.Errorf("orgresolve: --project %q: %w", req.Folder, err)
	}
	if req.ProjectID == "" {
		return LinkPlan{}, ErrLinkNoProjectID
	}
	remote, err := pickRemote(req.Remote, req.Remotes)
	if err != nil {
		return LinkPlan{}, err
	}
	if len(o.Stores) == 0 {
		return LinkPlan{}, fmt.Errorf("orgresolve: entry %s lists no stores under stores", o.Name)
	}
	store := o.Stores[0]
	if req.StoreURL != "" {
		want, err := repourl.Normalize(req.StoreURL)
		if err != nil {
			return LinkPlan{}, fmt.Errorf("orgresolve: --store %q: %w", req.StoreURL, err)
		}
		found := false
		for _, s := range o.Stores {
			if n, err := repourl.Normalize(s.URL); err == nil && n == want {
				store, found = s, true
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
	storeURL, err := repourl.Normalize(store.URL)
	if err != nil {
		return LinkPlan{}, fmt.Errorf("orgresolve: store url %q: %w", store.URL, err)
	}
	return LinkPlan{Entry: o.Name, StoreURL: storeURL, Branch: store.EffectiveBranch(), Folder: req.Folder,
		ProjectID: req.ProjectID, Remote: remote, Own: req.Own, Default: req.Default, StoreExplicit: req.StoreURL != ""}, nil
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
