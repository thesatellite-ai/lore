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

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
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
	hintNotAStore = "make it a store with `lore store init <name> <url>`, or fix the url in ~/.lore/config.yaml"
	hintNoBranch  = "a store branch is made only on purpose; 0.1.18 makes stores with `lore store init <name> <url>` and ties repos to an org with `lore org create` / `lore org link` (v0.1.17 stores: `lore migrate run`)"
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

// situation is the org situation of this folder; a refusal stays in
// ResolveErr (link and use work in an ambiguous repo: they fix it).
func (h *standaloneOrg) situation(ctx context.Context) (*orgcfg.Situation, error) {
	return h.res.Situation(ctx)
}

// Use implements opsorgcmd.Host: the machine-only choice for this project.
func (h *standaloneOrg) Use(ctx context.Context, in opsorgcmd.UseIn) (opsorgcmd.Report, error) {
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
		v.Style = yaml.FlowStyle
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
	s, err := h.situation(ctx)
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	plan, err := orgresolve.PlanLink(s.Config, orgresolve.LinkRequest{Entry: in.Org, ProjectID: s.ProjectID, Remotes: s.Remotes,
		Folder: in.Project, Own: in.Own, Default: in.Default, StoreURL: in.Store, Remote: in.Remote})
	if err != nil {
		return opsorgcmd.Report{}, errs.New(errs.InvalidInput, err.Error())
	}
	role := "a CLIENT repo"
	if plan.Own {
		role = "one of " + plan.Entry + "'s OWN repos"
	}
	lines := []string{fmt.Sprintf("This marks %s as %s of %s: internal notes default to %s %s/,", plan.Remote, role, plan.Entry, plan.StoreURL, plan.Folder)}
	if !plan.Own {
		lines = append(lines, fmt.Sprintf("and the leak guards turn on for everyone in %s.", plan.Entry))
	}
	lines = append(lines, fmt.Sprintf("  link: project %s at %s → %s@%s:%s/ (default=%v)", plan.ProjectID, plan.Remote, plan.StoreURL, plan.Branch, plan.Folder, plan.Default))
	if in.DryRun {
		return opsorgcmd.Report{Lines: append(lines, "  --dry-run: nothing written")}, nil
	}
	if !yes {
		return opsorgcmd.Report{Lines: lines, Ask: "Continue?"}, nil
	}
	org := s.Config.FindOrg(plan.Entry)
	g := &orgcfg.GitLinks{Home: h.home, Config: s.Config, Now: h.wall()}
	res, err := g.WriteLink(ctx, orgcfg.LinkWrite{Plan: plan, Signature: storeSignature(org), FolderProjectID: storeFolderProjectID})
	if err != nil {
		return opsorgcmd.Report{Lines: lines}, linkError(err)
	}
	state := "pushed"
	switch {
	case res.Offline:
		state = "committed in the local clone; offline, pushed by the next command"
	case res.NoPush && res.Changed:
		state = "committed in the local clone (autopush off; `lore sync` pushes it)"
	case !res.Changed && !res.CreatedFolder:
		state = "already so, nothing changed"
	}
	created := ""
	if res.CreatedFolder {
		created = "created; "
	}
	lines = append(lines, fmt.Sprintf("✓ %s (%s, %s) linked to %s %s/ (%s%s)", plan.Remote, plan.ProjectID, plan.Remote, res.StoreURL, plan.Folder, created, state),
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
		return errs.New(errs.InvalidInput, err.Error())
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
