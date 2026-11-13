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

// LinkRefusal is a link request PlanLink refuses: what is wrong (Msg)
// and the next step (Next), so the CLI prints the cause and the fix on
// their own lines (errs hint). Is is the sentinel a caller tests with
// errors.Is (ErrLinkNoRemote, ErrLinkOwnPattern), nil for the others.
//
// Why a type: the v0.1.17 texts carried the package name ("orgresolve:
// …") into what a person reads and named no next step; the wire layer
// turned them into E_INVALID_INPUT with no hint.
type LinkRefusal struct {
	Msg, Next string
	Is        error
}

// Error implements error: the cause only (Next is the hint).
func (e *LinkRefusal) Error() string { return e.Msg }

// Unwrap exposes Is to errors.Is.
func (e *LinkRefusal) Unwrap() error { return e.Is }

// refuse is a LinkRefusal of msg and next.
func refuse(msg, next string) *LinkRefusal { return &LinkRefusal{Msg: msg, Next: next} }

// Sentinels of PlanLink's refusals (errors.Is through LinkRefusal).
var (
	ErrLinkNoRemote   = errors.New("this repo has no hosted remote; a link names a project id and a remote")
	ErrLinkOwnPattern = errors.New("--own is only for repos matching the org entry's own: patterns")
)

// configFile names the machine config in a refusal's next step.
const configFile = "~/.lore/config.yaml"

// PlanLink validates a link request against the config without touching
// any store: the entry exists, the folder name is valid, the project has
// remote is meant, the store is one of the entry's, and --own only on a
// repo matching the entry's own: patterns. A repo with no project id (no
// lore data in it) is linked by its remote: ProjectID is RemoteProjectID.
// Every refusal is a *LinkRefusal naming the next step.
func PlanLink(cfg *orgconf.Config, req LinkRequest) (LinkPlan, error) {
	o := cfg.FindOrg(req.Entry)
	if o == nil {
		next := "import the org first: lore org import --url <store-url>"
		if names := cfg.OrgNames(); len(names) > 0 {
			next = "name one of the org entries: " + strings.Join(names, ", ")
		}
		return LinkPlan{}, refuse(fmt.Sprintf("no org entry named %q in %s", req.Entry, configFile), next)
	}
	if err := orgconf.ValidateFolderName(req.Folder); err != nil {
		return LinkPlan{}, refuse(fmt.Sprintf("--project %q is not a folder name lore can use: %v", req.Folder, err),
			"pick another folder name: letters, digits, '.', '_' or '-'")
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
		return LinkPlan{}, refuse("org entry "+o.Name+" lists no store",
			"run lore org refresh --org "+o.Name+" to copy its stores from the team's lore-org.yaml, or add one under stores: in "+configFile)
	}
	st := o.Stores[0]
	if req.StoreURL != "" {
		want, nerr := repourl.NormalizeStore(req.StoreURL)
		if nerr != nil {
			return LinkPlan{}, refuse(fmt.Sprintf("--store %q is not a store url: %v", req.StoreURL, nerr), "pass one of "+o.Name+"'s stores: "+storeURLs(o))
		}
		found := false
		for _, s := range o.Stores {
			if n, serr := repourl.NormalizeStore(s.URL); serr == nil && n == want {
				st, found = s, true
				break
			}
		}
		if !found {
			return LinkPlan{}, refuse("--store "+want+" is not one of "+o.Name+"'s stores", "pass one of them: "+storeURLs(o))
		}
	}
	if req.Own && !matchesAny(o.Own, []string{remote}) {
		return LinkPlan{}, &LinkRefusal{Is: ErrLinkOwnPattern,
			Msg:  fmt.Sprintf("%v: %s does not match %s's own: patterns (%s)", ErrLinkOwnPattern, remote, o.Name, listOrNone(o.Own)),
			Next: "link it without --own (as a client repo), or add a matching pattern to own: in " + o.Name + "'s lore-org.yaml and run lore org refresh --org " + o.Name}
	}
	storeURL, err := repourl.NormalizeStore(st.URL)
	if err != nil {
		return LinkPlan{}, refuse(fmt.Sprintf("store url %q of org entry %s is not valid: %v", st.URL, o.Name, err),
			"fix it in "+configFile+" (lore config check lists every problem)")
	}
	return LinkPlan{Entry: o.Name, StoreURL: storeURL, Branch: st.EffectiveBranch(), Folder: req.Folder,
		ProjectID: pid, Remote: remote, Own: req.Own, Default: req.Default, StoreExplicit: req.StoreURL != "", FromRemote: fromRemote}, nil
}

// storeURLs lists o's store urls for a refusal's next step.
func storeURLs(o *orgconf.Org) string {
	urls := make([]string, 0, len(o.Stores))
	for _, s := range o.Stores {
		urls = append(urls, s.URL)
	}
	return listOrNone(urls)
}

// pickRemote returns the one remote a link is for: --remote when given (it
// must be one of the repo's), else the only remote; several without
// --remote is refused rather than guessed.
func pickRemote(flag string, remotes []string) (string, error) {
	if flag != "" {
		n, err := repourl.Normalize(flag)
		if err != nil {
			return "", refuse(fmt.Sprintf("--remote %q is not a repo url: %v", flag, err), "pass one of this repo's remotes: "+listOrNone(remotes))
		}
		for _, r := range remotes {
			if r == n {
				return n, nil
			}
		}
		next := "pass one of this repo's remotes: " + listOrNone(remotes)
		if len(remotes) == 0 {
			next = "add it first: git remote add origin " + n
		}
		return "", refuse("--remote "+n+" is not a remote of this repo", next)
	}
	switch len(remotes) {
	case 0:
		return "", &LinkRefusal{Msg: ErrLinkNoRemote.Error(), Next: "add the repo's remote first: git remote add origin <url>", Is: ErrLinkNoRemote}
	case 1:
		return remotes[0], nil
	default:
		return "", refuse(fmt.Sprintf("this repo has %d remotes (%s)", len(remotes), strings.Join(remotes, ", ")), "choose one with --remote <url>")
	}
}
