// stores_route.go — F14 end to end for one write command: parse --to, run
// org resolution, decide repo vs store project vs personal folder, and —
// for anything that is not this repo — point the command at that project's
// mirror (stores_project.go) so the command's normal code writes there.
//
// Entity commands call routeWrite once, before resolveContext:
//
//	route, err := routeWrite(ctx, &c.commonFlags, c.to, "memory", body)
//	if err != nil { return err }
//	client := resolveContext(&c.commonFlags)   // the routed project's DB
//	… write …
//	fmt.Println("✓", id, "→", route.Label)
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/personalplace"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/stores"
)

// personalLabel marks a row of the personal store (rule P) in lists and
// write confirmations, the way "[org/project]" marks a store row. It is
// also how a route is recognised as personal (share, xref), so it is
// printed output and a decision value at once: never change one without
// the other.
const personalLabel = "[me]"

// writeRoute is where one write command writes.
type writeRoute struct {
	// Repo: this repo's own project (the command's usual DB).
	Repo bool
	// Project is the external project (store or personal) otherwise.
	Project *externalProject
	// Label is shown after the new id: "[northwind/acme]", "[me]", "".
	Label string
	// Rule is the F14 rule that decided (printed by --dry-run).
	Rule string
	// Situation is the org resolution used.
	Situation *orgSituation
}

// identity.Resolved.Kind values for agent and human actors.
const (
	identityKindAgent = "agent"
	identityKindHuman = "human"
)

// routeWrite decides the target project of one write (F14, F17) and, for a
// store or personal project, opens it and points c at it. kind is the row
// kind as defaults.client_repos spells it (memory, rule, decision, task,
// comment, …); text is the row's text (F17's --everywhere name check).
func routeWrite(ctx context.Context, c *commonFlags, f toFlags, kind, text string) (writeRoute, error) {
	wt, err := resolveWriteTarget(ctx, f, text)
	if err != nil {
		return writeRoute{}, err
	}
	s := wt.Situation
	route := writeRoute{Situation: s}
	if wt.Personal != nil {
		p, openPersonalErr := openPersonalProject(ctx, s.Config, wt.Personal.Folder)
		if openPersonalErr != nil {
			return writeRoute{}, openPersonalErr
		}
		useExternalProject(c, p)
		route.Project, route.Label, route.Rule = p, p.Label, "--to me → personal "+wt.Personal.Folder+"/"
		return route, nil
	}
	in, err := targetInputs(ctx, s, wt.Target, kind)
	if err != nil {
		return writeRoute{}, err
	}
	dec, err := stores.ResolveTarget(in)
	if err != nil {
		return writeRoute{}, targetCLIError(err)
	}
	route.Rule = dec.Rule
	if dec.Repo {
		route.Repo = true
		// F16 at write time (trial F8, F9): a row routed to this repo is
		// leak-checked before anything is written (leak_write.go).
		if leakErr := leakCheckRouteWrite(ctx, route, kind, text); leakErr != nil {
			return writeRoute{}, leakErr
		}
		return route, nil
	}
	org := s.Result.Org
	ref, err := findStoreFolderOpt(ctx, s.Config, org, dec.Folder, f.Create)
	if err != nil {
		return writeRoute{}, err
	}
	p, err := openStoreProject(ctx, s.Config, ref)
	if err != nil {
		return writeRoute{}, err
	}
	useExternalProject(c, p)
	route.Project, route.Label = p, p.Label
	ensureCheckoutGuards(ctx, s.Dir)
	return route, nil
}

// ensureCheckoutGuards installs lore's git wiring (hooks incl. the leak
// checks) in the checkout even when this command wrote nothing there: in a
// client repo every write may go to the store, and the pre-commit and
// commit-msg checks must still guard the client repo (§6.3). Best effort;
// a non-lore or non-git directory is left alone.
func ensureCheckoutGuards(ctx context.Context, root string) {
	if root == "" || !lsync.MetaExists(filepath.Join(root, projresolve.MarkerDir, lsync.DataDirName)) {
		return
	}
	if os.Getenv(envSyncGit) == envValueOff || os.Getenv(envReadOnly) == envValueOn {
		return
	}
	_, _ = ensureGitWiring(ctx, root) // best effort: the next command that opens the checkout retries and reports
}

// entryLinks reads one entry's links (staging included) for this repo.
func entryLinks(ctx context.Context, s *orgSituation, org *lorecfg.Org) ([]orgresolve.Link, bool, error) {
	m, err := newStoreManager(s.Config, org)
	if err != nil {
		return nil, false, err
	}
	ls := &stores.LinkSource{Manager: m, Config: s.Config}
	l, ok, err := ls.EntryLinks(ctx, org, s.ProjectID, s.Remotes)
	if err != nil {
		return nil, false, storeCLIError(err, "read the store links")
	}
	return l, ok, nil
}

// targetCLIError maps F14 refusals to codes.
func targetCLIError(err error) error {
	switch {
	case errors.Is(err, stores.ErrLinkUnknown):
		return errcodes.New(errcodes.LinkUnknown, err.Error()+"; nothing written").
			WithHint("pass --to (repo, <org> or <org>/<folder>) or retry when the store is reachable")
	case errors.Is(err, stores.ErrNeedsExplicitTo), errors.Is(err, stores.ErrTwoLinksNoDefault), errors.Is(err, stores.ErrNoLinkedProject):
		return errcodes.New(errcodes.InvalidInput, err.Error())
	}
	return errcodes.New(errcodes.Internal, "decide the write target").WithCause(err)
}

// targetInputs gathers stores.TargetInputs from the org situation.
func targetInputs(ctx context.Context, s *orgSituation, t personalplace.Target, kind string) (stores.TargetInputs, error) {
	r := s.Result
	in := stores.TargetInputs{
		Org: r.OrgName(), Kind: kind, Role: stores.Role(r.Role), TTY: stdinIsTTY(),
		LinkUnknown: r.LinkUnknown,
	}
	switch t.Kind {
	case personalplace.TargetThisRepo:
		in.To = stores.ToRepo
	case personalplace.TargetOrg:
		in.To = stores.ToOrg
	case personalplace.TargetOrgFolder:
		in.To, in.Folder = stores.ToOrgFolder, t.Folder
	case personalplace.TargetNone, personalplace.TargetPersonal, personalplace.TargetPersonalOrg:
		// no store --to (none given, or a personal target): in.To stays unset
	}
	links := r.Links
	if r.Org != nil && len(links) == 0 && s.ProjectID != "" {
		// A staging entry chosen on purpose has its own links on its own
		// store branch (F10 skips them only for automatic matching).
		el, ok, err := entryLinks(ctx, s, r.Org)
		if err != nil {
			return in, err
		}
		if ok {
			links = el
		}
	}
	for _, l := range links {
		in.Links = append(in.Links, stores.TargetLink{Folder: l.Folder, Default: l.Default, Own: l.Own})
	}
	if s.Committed != nil {
		in.CommittedProject = s.Committed.Project
	}
	if id, err := s.identity(); err == nil {
		in.Agent = id.Kind == identityKindAgent
	}
	if r.Org != nil {
		in.PersonalDefaults = r.Org.Defaults.ClientRepos
		d, err := storeOrgDefaults(ctx, s.Config, r.Org)
		if err != nil {
			return in, err
		}
		in.StoreDefaults = d
	}
	return in, nil
}

// storeOrgDefaults reads defaults.client_repos from the org folder's
// lore-org.yaml (read on every resolve, §5.2). No org folder or no file =
// no team defaults.
func storeOrgDefaults(ctx context.Context, cfg *lorecfg.Config, org *lorecfg.Org) (map[string]string, error) {
	sc := org.OrgFolderStore()
	if sc == nil {
		return nil, nil
	}
	m, err := newStoreManager(cfg, org)
	if err != nil {
		return nil, err
	}
	st, err := m.Open(ctx, sc.URL)
	if err != nil {
		return nil, storeCLIError(err, "open the store")
	}
	if _, fetchErr := st.Fetch(ctx, false); fetchErr != nil {
		return nil, storeCLIError(fetchErr, "fetch the store")
	}
	f, ok, err := st.ReadOrgFile(ctx, sc.EffectiveBranch(), sc.OrgFolder)
	if err != nil {
		return nil, storeCLIError(err, "read lore-org.yaml")
	}
	if !ok {
		return nil, nil
	}
	return f.Defaults.ClientRepos, nil
}

// personalLocalDir is the local personal store (§5.3: ~/.lore/personal).
const personalLocalDir = "personal"

// openPersonalProject opens a personal folder (F17, D11): the local store
// ~/.lore/personal/<folder> (its mirror is its home), or a git personal
// store (personal.store: <url>), handled exactly like an org store on its
// default branch.
func openPersonalProject(ctx context.Context, cfg *lorecfg.Config, folder string) (*externalProject, error) {
	store := personalStoreV017(cfg)
	if store == "" || store == lorecfg.PersonalStoreLocal {
		home, err := loreHome()
		if err != nil {
			return nil, err
		}
		root := filepath.Join(home, personalLocalDir, folder)
		if p := openedMirror(root); p != nil {
			return p, nil
		}
		data := filepath.Join(root, ".lore", "data")
		files, err := readDirFiles(data)
		if err != nil {
			return nil, err
		}
		stored := make(map[string][]byte, len(files))
		for k, v := range files {
			stored[k] = v
		}
		// No shared identity: the local personal store has one writer.
		projectID, err := ensureProjectRow(files, storeFolderKey{Folder: folder})
		if err != nil {
			return nil, err
		}
		p := &externalProject{Label: personalLabel, Root: root, ProjectID: projectID}
		if err := p.materialize(ctx, files, stored); err != nil {
			return nil, err
		}
		warnStrayProjectRows(p.Label, projectID, strayProjectRows(files, projectID))
		openedExternal = append(openedExternal, p)
		return p, nil
	}
	pseudo := &lorecfg.Org{Name: "me", Stores: []lorecfg.Store{{URL: store}}}
	if r, err := identityForPersonal(ctx); err == nil {
		pseudo.Identity = r
	}
	p, err := openStoreProject(ctx, cfg, storeProjectRef{Org: pseudo, Store: pseudo.Stores[0], Folder: folder})
	if err != nil {
		return nil, err
	}
	p.Label = personalLabel
	return p, nil
}

// identityForPersonal signs personal store commits with the org identity
// of the current repo when there is one, else the fallback identity.
func identityForPersonal(ctx context.Context) (lorecfg.OrgIdentity, error) {
	r, err := resolveIdentityHere(ctx)
	if err != nil {
		return lorecfg.OrgIdentity{}, err
	}
	return lorecfg.OrgIdentity{Key: r.StableKey, Name: r.DisplayName}, nil
}

// readDirFiles reads every file under dir (relative slash paths); a
// missing dir is empty.
func readDirFiles(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	// Read through an os.Root on dir (P2-LINT-5), so no read leaves it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, errcodes.New(errcodes.Internal, fmt.Sprintf("read %s", dir)).WithCause(err)
	}
	defer func() { _ = root.Close() }() // read-only use
	fsys := root.FS()
	err = fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return err
		}
		out[rel] = b
		return nil
	})
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, fmt.Sprintf("read %s", dir)).WithCause(err)
	}
	return out, nil
}
