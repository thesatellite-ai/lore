package wire

// org_standalone.go — the `lore org` host of cmd/lore (ops/orgcmd.Host,
// P3-CMDLORE-4): link, use and set as v0.1.17's org_cmd.go did them, over
// the process's OrgResolution, ~/.lore/config.yaml and the shared v0.1.17
// bare store clones (adapters/orgcfg.WriteLink); create, import and rename
// are in org_create_standalone.go (P3-ORG-G-2). The old binary
// keeps its own host (Options.OrgCommands); StandaloneOptionsOrg installs
// this one.
//
//	link ─▶ Situation ─▶ PlanLink ─▶ plan lines ─▶ --dry-run? stop ─▶ not
//	        confirmed? ask ─▶ WriteLink (pick store, set-union, commit as
//	        the org identity, push) ─▶ result line
//	use  ─▶ project id ─▶ entry exists ─▶ repo_orgs[<id>] = {org, remotes}
//	set  ─▶ dry-run F10 rule 3 with the file ─▶ write .lore/org.yaml

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/yamlfmt"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// org host constants.
const (
	// storeFolderIDDomain separates store-folder project ids from every
	// other natural id (v0.1.17 stores_project.go), so a link and another
	// clone's first write to the folder name the same project.
	storeFolderIDDomain = "store-folder-project"
	// fallbackStoreName / fallbackStoreEmail sign a store commit when the
	// entry has no identity (v0.1.17's last resort).
	fallbackStoreName  = "lore"
	fallbackStoreEmail = "lore@localhost"
	// configKeyRepoOrgs is config.yaml's repo_orgs mapping (F10 rule 2).
	configKeyRepoOrgs = "repo_orgs"
	// orgFileName is the committed org choice under .lore/.
	orgFileName = "org.yaml"
	orgYAMLMode = 0o644 // committed: world-readable like every repo file
	loreDirName = ".lore"
	// hintNotAStore is v0.1.17's storeHintInit.
	hintNotAStore = "make it a store with `lore store init --name <name> --url <url>`, or fix the url in ~/.lore/config.yaml"
	hintNoBranch  = "the store repo has branches but not this one (an empty repo gets its branch with the first link): check the url and the branch the org lists for it with lore store list, and fix lore-org.yaml then lore org refresh --org <org> if they are wrong"
)

// standaloneOrg implements opsorgcmd.Host for cmd/lore.
type standaloneOrg struct {
	home string
	res  *OrgResolution
	// reg builds the Apps create, import and rename need (roster
	// registration, the org folder's id).
	reg orgRegistrar
}

var _ opsorgcmd.Host = (*standaloneOrg)(nil)

// router reaches each store as the setup that owns it (storeRoute), the
// active setup of this folder breaking a tie.
func (h *standaloneOrg) router() orgcfg.Router {
	dir := ""
	if h.res != nil {
		dir = h.res.dir
	}
	return storeRouter(h.home, dir)
}

// situation is the org situation of this folder; a refusal stays in
// ResolveErr (link and use work in an ambiguous repo: they fix it).
func (h *standaloneOrg) situation(ctx context.Context) (*orgcfg.Situation, error) {
	return h.res.Situation(ctx)
}

// needsRepo refuses an org verb that is about this repo (link, unlink,
// set, use) run outside any git work tree, naming that cause and the next
// step: the deeper checks would blame a missing project id or remote of a
// repo that is not there.
func (h *standaloneOrg) needsRepo(ctx context.Context, verb, what string) error {
	if h.res == nil || insideWorkTree(ctx, h.res.dir) {
		return nil
	}
	return errs.New(errs.NotAStore, "org "+verb+": it "+what+", and "+h.res.dir+" is not inside a git repo").
		WithHint("cd into the repo (run `lore init` there first if it is not a lore project yet); a store's folders are made from anywhere in the setup with `lore store folder add --store team --folder <folder>`")
}

// Use implements opsorgcmd.Host: the machine-only choice for this project.
func (h *standaloneOrg) Use(ctx context.Context, in opsorgcmd.UseIn) (opsorgcmd.Report, error) {
	if err := h.needsRepo(ctx, "use", "records the org of this repo's project"); err != nil {
		return opsorgcmd.Report{}, err
	}
	s, err := h.situation(ctx)
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	if s.ProjectID == "" {
		return opsorgcmd.Report{}, errs.New(errs.NotProjectRoot, "this directory has no lore project id (.lore/data/_meta.json)").
			WithHint("run it in a lore project root that shares its data through git")
	}
	if in.Clear {
		removed := false
		cerr := editMachineConfig(h.home, func(root *yaml.Node) error {
			m := mapGet(root, configKeyRepoOrgs)
			if m == nil || m.Kind != yaml.MappingNode {
				return nil
			}
			for i := 0; i+1 < len(m.Content); i += 2 {
				if m.Content[i].Value == s.ProjectID {
					m.Content = append(m.Content[:i], m.Content[i+2:]...)
					removed = true
					yamlfmt.DropEmpty(root, configKeyRepoOrgs)
					return nil
				}
			}
			return nil
		})
		if cerr != nil {
			return opsorgcmd.Report{}, cerr
		}
		if !removed {
			return opsorgcmd.Report{Lines: []string{"(no choice was recorded for this project)"}}, nil
		}
		return opsorgcmd.Report{Lines: []string{"✓ cleared the org choice for " + s.ProjectID}}, nil
	}
	if in.Entry == "" || s.Config.FindOrg(in.Entry) == nil {
		msg := "name the org entry to use"
		if in.Entry != "" {
			msg = fmt.Sprintf("no org entry named %q", in.Entry)
		}
		return opsorgcmd.Report{}, errs.New(errs.InvalidInput, msg).WithHint("entries: " + strings.Join(s.Config.OrgNames(), ", "))
	}
	err = editMachineConfig(h.home, func(root *yaml.Node) error {
		v := &yaml.Node{}
		if eerr := v.Encode(orgconf.RepoOrg{Org: in.Entry, Remotes: s.Remotes}); eerr != nil {
			return errs.Wrap(errs.Internal, "wire: encode repo_orgs", eerr)
		}
		mapSet(mapChild(root, configKeyRepoOrgs), s.ProjectID, v)
		return nil
	})
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	return opsorgcmd.Report{Lines: []string{
		fmt.Sprintf("✓ %s uses org %s on this machine", s.ProjectID, in.Entry),
		fmt.Sprintf("  recorded in %s (machine-only, nothing committed)", configPath(h.home)),
	}}, nil
}

// Set implements opsorgcmd.Host: write the committed .lore/org.yaml after
// checking that F10 rule 3 would accept it.
func (h *standaloneOrg) Set(ctx context.Context, in opsorgcmd.SetIn) (opsorgcmd.Report, error) {
	if err := h.needsRepo(ctx, "set", "writes this repo's committed .lore/org.yaml"); err != nil {
		return opsorgcmd.Report{}, err
	}
	s, err := h.situation(ctx)
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	f := &orgconf.OrgFile{Org: in.Org, Project: in.Project}
	if verr := f.Validate(); verr != nil {
		return opsorgcmd.Report{}, errs.New(errs.InvalidInput, verr.Error())
	}
	res, rerr := orgresolve.Resolve(orgresolve.Inputs{Config: s.Config, Remotes: s.Remotes, Committed: f,
		Links: orgresolve.LinkResult{Status: s.Result.LinkStatus, Links: s.Result.Links, Detail: s.Result.LinkDetail}})
	if rerr != nil || res.Step != orgresolve.StepCommitted {
		reason := "rule 3 would not accept it"
		for _, t := range res.Trace {
			if t.Step == orgresolve.StepCommitted {
				reason = t.Reason
			}
		}
		return opsorgcmd.Report{}, errs.New(errs.InvalidInput, "refusing to write .lore/org.yaml: "+reason).
			WithHint("only an org's own repos (matching its own: patterns) may name it; use `lore org use` for a machine-only choice")
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return opsorgcmd.Report{}, errs.Wrap(errs.Internal, "wire: encode .lore/org.yaml", err)
	}
	path := filepath.Join(s.Dir, loreDirName, orgFileName)
	if err := writeFileAtomic(path, b, orgYAMLMode); err != nil {
		return opsorgcmd.Report{}, err
	}
	return opsorgcmd.Report{Lines: []string{fmt.Sprintf("✓ wrote %s (org %s)", path, in.Org), "  commit it with git so teammates resolve the same org"}}, nil
}

// Link implements opsorgcmd.Host: validate, show the plan, and (confirmed,
// not a dry run) write the link into the store.
func (h *standaloneOrg) Link(ctx context.Context, in opsorgcmd.LinkIn, yes bool) (opsorgcmd.Report, error) {
	if err := h.needsRepo(ctx, "link", "links this repo to a folder of the org's store"); err != nil {
		return opsorgcmd.Report{}, err
	}
	s, err := h.situation(ctx)
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	plan, err := orgresolve.PlanLink(s.Config, orgresolve.LinkRequest{Entry: in.Org, ProjectID: s.ProjectID, Remotes: s.Remotes,
		Folder: in.Project, Own: in.Own, Default: in.Default, StoreURL: in.Store, Remote: in.Remote})
	if err != nil {
		return opsorgcmd.Report{}, planLinkError(err)
	}
	lines := linkPlanLines(plan)
	if in.DryRun {
		return opsorgcmd.Report{Lines: append(lines, "  --dry-run: nothing written")}, nil
	}
	if !yes {
		return opsorgcmd.Report{Lines: lines, Ask: "Continue?"}, nil
	}
	org := s.Config.FindOrg(plan.Entry)
	g := &orgcfg.GitLinks{Held: orgStoreHeld, Home: h.home, Config: s.Config, Now: h.wall(), Route: h.router()}
	res, err := g.WriteLink(ctx, orgcfg.LinkWrite{Plan: plan, Signature: storeSignature(org), FolderProjectID: storeFolderProjectID,
		NewMarker: func() ([]byte, error) {
			m, merr := rowfs.NewMarker(h.wall()())
			if merr != nil {
				return nil, merr
			}
			return m.Encode()
		}})
	if err != nil {
		return opsorgcmd.Report{Lines: lines}, linkError(err)
	}
	state := "pushed"
	switch {
	case res.Offline:
		state = "committed in the local clone; offline, pushed by the next command"
	case res.NoPush && res.Changed:
		state = "committed in the local clone; `lore sync` sends it"
	case !res.Changed && !res.CreatedFolder:
		state = "already so, nothing changed"
	}
	created := ""
	if res.CreatedFolder {
		created = "created; "
	}
	lines = append(lines, linkDoneLine(plan, res.StoreURL, created+state), "  nothing was written to this repo")
	return opsorgcmd.Report{Lines: lines}, nil
}

// linkPlanLines are the plan lines `lore org link` prints before it asks
// (and alone with --dry-run). A client repo's sentence goes on to the
// leak guards; an own repo's ends after the default, so it has no comma
// left hanging. The org is named once: "one of <org>'s OWN repos of
// <org>" said it twice (owner report, 0.1.18-dev).
func linkPlanLines(plan orgresolve.LinkPlan) []string {
	notes := fmt.Sprintf("internal notes default to %s %s/", plan.StoreURL, plan.Folder)
	var lines []string
	if plan.Own {
		lines = []string{fmt.Sprintf("This marks %s as one of %s's OWN repos: %s.", plan.Remote, plan.Entry, notes)}
	} else {
		lines = []string{fmt.Sprintf("This marks %s as a CLIENT repo of %s: %s,", plan.Remote, plan.Entry, notes),
			fmt.Sprintf("and the leak guards turn on for everyone in %s.", plan.Entry)}
	}
	return append(lines, fmt.Sprintf("  link: project %s at %s → %s@%s:%s/ (default=%v)", plan.ProjectID, plan.Remote, plan.StoreURL, plan.Branch, plan.Folder, plan.Default))
}

// linkDoneLine is the success line of `lore org link`: the remote and its
// project id once each (it printed the remote twice: "<remote> (<id>,
// <remote>)").
func linkDoneLine(plan orgresolve.LinkPlan, storeURL, state string) string {
	return fmt.Sprintf("✓ %s (%s) linked to %s %s/ (%s)", plan.Remote, plan.ProjectID, storeURL, plan.Folder, state)
}

// planLinkError is a refused link plan as a lore error: the cause and,
// from orgresolve.LinkRefusal, the next step as the hint. Anything else
// PlanLink returns is not the request's fault (a project id that could
// not be derived): E_INTERNAL.
func planLinkError(err error) error {
	var lr *orgresolve.LinkRefusal
	if errors.As(err, &lr) {
		return errs.New(errs.InvalidInput, lr.Msg).WithHint(lr.Next)
	}
	return errs.Wrap(errs.Internal, "wire: plan the link: "+err.Error(), err).
		WithHint("run it again; if it fails the same way, report it with the output of lore doctor --json")
}

// Unlink implements opsorgcmd.Host (F11 `lore org unlink`): list every
// link of this repo's project in the entry's stores (any remote, so a
// moved repo goes whole; one folder with --project), ask, and take them
// out. The folders and their rows stay. Afterwards the repo resolves by
// its own rules (own:/repos: patterns, setups), as before it was linked.
func (h *standaloneOrg) Unlink(ctx context.Context, in opsorgcmd.UnlinkIn, yes bool) (opsorgcmd.Report, error) {
	if err := h.needsRepo(ctx, "unlink", "removes this repo's links"); err != nil {
		return opsorgcmd.Report{}, err
	}
	s, err := h.situation(ctx)
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	org := s.Config.FindOrg(in.Org)
	if org == nil {
		hint := "this machine has no org entry yet; import the org first: lore org import --url <store-url>"
		if names := s.Config.OrgNames(); len(names) > 0 {
			hint = "name one of the org entries: " + strings.Join(names, ", ")
		}
		return opsorgcmd.Report{}, errs.New(errs.NotFound, fmt.Sprintf("no org entry named %q in ~/.lore/config.yaml", in.Org)).WithHint(hint)
	}
	if s.ProjectID == "" {
		return opsorgcmd.Report{}, errs.New(errs.NotProjectRoot, "no store links this repo: it has no project id (.lore/data/_meta.json) and no link names its remote").
			WithHint("`lore whoami` says what this repo resolves to; `lore org link --org <org> --project <folder>` links it by its remote")
	}
	g := &orgcfg.GitLinks{Held: orgStoreHeld, Home: h.home, Config: s.Config, Now: h.wall(), Route: h.router()}
	found, err := g.FindLinks(ctx, org.Name, s.ProjectID, strings.Trim(in.Project, "/"))
	if err != nil {
		return opsorgcmd.Report{}, linkError(err)
	}
	if len(found) == 0 {
		where := org.Name + "'s stores"
		if in.Project != "" {
			where += " (folder " + in.Project + "/)"
		}
		return opsorgcmd.Report{Lines: []string{fmt.Sprintf("no link of %s in %s; nothing to unlink", s.ProjectID, where)}}, nil
	}
	lines := []string{fmt.Sprintf("This removes %d link(s) of project %s from %s; their folders and notes stay in the store:", len(found), s.ProjectID, org.Name)}
	for _, l := range found {
		kind := "client"
		if l.Own {
			kind = "own"
		}
		lines = append(lines, fmt.Sprintf("  unlink: %s (%s) → %s@%s:%s/", l.Remote, kind, l.StoreURL, l.Branch, l.Folder))
	}
	lines = append(lines, "Afterwards this repo follows its own rules (own:/repos: patterns, setups) for everyone in "+org.Name+".")
	if in.DryRun {
		return opsorgcmd.Report{Lines: append(lines, "  --dry-run: nothing written")}, nil
	}
	if !yes {
		return opsorgcmd.Report{Lines: lines, Ask: "Continue?"}, nil
	}
	res, err := g.RemoveLinks(ctx, org.Name, s.ProjectID, found, storeSignature(org))
	if err != nil {
		return opsorgcmd.Report{Lines: lines}, linkError(err)
	}
	state := "pushed"
	switch {
	case res.Removed == 0:
		state = "already so, nothing changed"
	case res.Offline:
		state = "committed in the local clone; offline, pushed by the next command"
	case res.NoPush:
		state = "committed in the local clone; `lore sync` sends it"
	}
	lines = append(lines, fmt.Sprintf("✓ unlinked %s from %s (%d link(s) removed; %s)", s.ProjectID, org.Name, res.Removed, state),
		"  nothing was written to this repo")
	return opsorgcmd.Report{Lines: lines}, nil
}

// storeSignature is the author of a store commit: the entry's identity
// (§6.5: never a personal git email), else lore's last resort.
func storeSignature(o *orgconf.Org) gitref.Signature {
	when := time.Now()
	if o != nil && o.Identity.Key != "" {
		name := o.Identity.Name
		if name == "" {
			name = o.Name
		}
		if email := emailOfKey(o.Identity.Key); email != "" {
			return gitref.Signature{Name: name, Email: email, When: when}
		}
	}
	return gitref.Signature{Name: fallbackStoreName, Email: fallbackStoreEmail, When: when}
}

// storeFolderProjectID is the prj_ id of a store folder a link creates
// (v0.1.17 storeFolderProjectID: natural id of store, branch, folder).
func storeFolderProjectID(store, branch, folder string) (string, error) {
	id, err := identity.Deterministic(ids.PrefixProject, storeFolderIDDomain, store, branch, folder)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// linkError maps a store write error onto a lore error code (v0.1.17
// storeCLIError).
func linkError(err error) error {
	switch {
	case errors.Is(err, orgcfg.ErrNotAStore):
		return errs.New(errs.NotAStore, err.Error()).WithHint(hintNotAStore)
	case errors.Is(err, orgcfg.ErrNoStoreBranch):
		return errs.New(errs.InvalidInput, err.Error()).WithHint(hintNoBranch)
	case errors.Is(err, orgcfg.ErrStoreOffline):
		return errs.New(errs.Offline, err.Error()).WithHint("retry when the store is reachable")
	case errors.Is(err, orgcfg.ErrFolderElsewhere):
		// --store named one store and the folder lives in another (F13):
		// the refusal named both but no way on.
		return errs.New(errs.InvalidInput, err.Error()).
			WithHint("drop --store to link into the store that holds the folder, or pick another --project folder for this store")
	case errors.Is(err, orgcfg.ErrStoreRefused):
		// The store answered and said no (a hook, branch protection): the
		// git backend's remote refusal code (adapters/backend/git sync).
		return errs.New(errs.PolicyDenied, err.Error()).
			WithHint("the commit stays in the local clone; ask the store's owner why the push was refused")
	case errors.Is(err, orgcfg.ErrLinkRace):
		return errs.New(errs.BusyTimeout, err.Error()).WithHint("other writers keep moving the store branch: run the command again")
	}
	var le *errs.Error
	if errors.As(err, &le) {
		return err
	}
	return errs.Wrap(errs.Internal, "wire: write the link: "+err.Error(), err)
}
