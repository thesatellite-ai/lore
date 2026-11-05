// org_create_cmd.go — `lore org create <name> --store <store>`: make a new
// org on a store made with `lore store init` (DECISIONS_LOG 18 STORES
// DIRECT, CFG1, CFG8). An org is an optional team convenience bundling a
// store, identities and the client-repo rules; this command is the one way
// to start one from nothing (`lore org import` adopts an existing one):
//
//  1. it writes <org_folder>/.lore/lore-org.yaml into the store (structure
//     only, CFG8: the org name, its store, own:/repos: patterns; never an
//     identity), so teammates run `lore org import <url>`;
//  2. it adds the entry, with YOUR identity, to ~/.lore/config.yaml (CFG9);
//  3. it registers you on the roster (actors in the org folder).
//
// Like import it shows the entry and asks first (the org.create
// operation, ops/orgcmd: E_NEEDS_CONFIRM, --yes without a terminal). Client repos are tied to the org afterwards with
// `lore org link`, which writes only into the store.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/stores"

	"github.com/thesatellite-ai/lore/engine/attach"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// runOrgCreate is org.create: validate, print the plan, and (confirmed)
// write lore-org.yaml into the store, add the entry, join the roster. It
// returns the question when it stopped unconfirmed (nothing written).
func runOrgCreate(ctx context.Context, w *bytes.Buffer, in opsorgcmd.CreateIn, yes bool) (string, error) {
	orgName, orgFolder := in.Org, in.OrgFolder
	url, err := resolveCreateStore(in.Store)
	if err != nil {
		return "", err
	}
	cfg, err := loadTeamConfig(ctx)
	if err != nil {
		return "", err
	}
	if cfg.FindOrg(orgName) != nil {
		return "", errcodes.New(errcodes.InvalidInput, "an org entry named "+orgName+" already exists in ~/.lore/config.yaml").
			WithHint("pick another name, or link repos to the existing org with `lore org link`")
	}
	entry := lorecfg.Org{Name: orgName, Own: in.Own, Repos: in.Repos,
		Stores:   []lorecfg.Store{{URL: url, OrgFolder: orgFolder}},
		Identity: lorecfg.OrgIdentity{Key: in.Key, Name: in.Name, Handle: in.Handle}}
	if entry.Identity.Key == "" || entry.Identity.Name == "" {
		return "", errcodes.New(errcodes.InvalidInput, "your identity in "+orgName+" is required: --key human:<your email there> --name \"Your Name\" [--handle h]")
	}
	body, err := yaml.Marshal(entry)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "encode the entry").WithCause(err)
	}
	fmt.Fprintf(w, "This writes %s/.lore/%s into the store (no identity) and adds to ~/.lore/config.yaml:\n\n%s\n",
		orgFolder, stores.OrgDefaultsFile, indentBlock(string(body)))
	for _, r := range riskyImport(entry) {
		fmt.Fprintln(w, "  note: "+r)
	}
	if !yes {
		return "Create it?", nil
	}
	m, err := newStoreManager(cfg, &entry)
	if err != nil {
		return "", err
	}
	st, err := m.Open(ctx, url)
	if err != nil {
		return "", storeCLIError(err, "open the store")
	}
	sugg := stores.OrgFileDefaults{Name: orgName, Own: in.Own, Repos: in.Repos,
		Stores: []stores.SuggestedStore{{URL: url, OrgFolder: orgFolder}}}
	res, existed, err := st.WriteOrgFile(ctx, st.DefaultBranch(), orgFolder, sugg, storeSignature(&entry), !storeAutopush(cfg, &entry))
	if errors.Is(err, stores.ErrOrgFileExists) {
		return "", errcodes.New(errcodes.InvalidInput, err.Error()).
			WithHint("that store already suggests an org: adopt it with `lore org import " + url + "`")
	}
	if err != nil {
		return "", storeCLIError(err, "write lore-org.yaml")
	}
	switch {
	case existed:
		fmt.Fprintf(w, "✓ the store already suggests org %s (%s/.lore/%s kept)\n", orgName, orgFolder, stores.OrgDefaultsFile)
	case res.Offline:
		fmt.Fprintf(w, "✓ wrote %s/.lore/%s (committed in the local clone; offline, pushed by the next command)\n", orgFolder, stores.OrgDefaultsFile)
	case !res.Pushed:
		fmt.Fprintf(w, "✓ wrote %s/.lore/%s (committed in the local clone; `lore sync` pushes it)\n", orgFolder, stores.OrgDefaultsFile)
	default:
		fmt.Fprintf(w, "✓ wrote %s/.lore/%s (pushed)\n", orgFolder, stores.OrgDefaultsFile)
	}
	home, err := loreHome()
	if err != nil {
		return "", err
	}
	if err := appendOrgEntry(ctx, home, entry); err != nil {
		return "", err
	}
	fmt.Fprintf(w, "✓ added org entry %s\n", orgName)
	if err := registerAfterImport(ctx, orgName); err != nil {
		fmt.Fprintln(os.Stderr, "lore: the org is created, but registering on the roster failed: "+redactIfQuiet(err.Error()))
		fmt.Fprintln(os.Stderr, "      run `LORE_ORG="+orgName+" lore actor register` in one of "+orgName+"'s repos or outside any repo")
	}
	fmt.Fprintln(w, "  teammates join with: lore org import "+url+" --key human:<their email> --name \"Their Name\"")
	fmt.Fprintln(w, "  tie a client repo to it (written only into the store): lore org link "+orgName+" --project <folder>")
	return "", nil
}

// resolveCreateStore turns --store into a store url: the url of the entry
// of that name in the working repo's .lore/stores.yaml, else the argument
// itself when it looks like a url (it holds ":" or "/").
func resolveCreateStore(arg string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, projresolve.MarkerDir, filepath.Base(attach.FileName))))
	if err == nil {
		c, perr := attach.Parse(b)
		if perr != nil {
			return "", errcodes.New(errcodes.ConfigInvalid, ".lore/stores.yaml is not valid").WithCause(perr)
		}
		for _, s := range c.Stores {
			if string(s.Name) == arg {
				if s.Folder != "" {
					return "", errcodes.New(errcodes.InvalidInput, "store "+arg+" attaches one folder ("+s.Folder+"); an org needs the whole store").
						WithHint("pass the store's url with --store")
				}
				return s.URL, nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errcodes.New(errcodes.Internal, "read .lore/stores.yaml").WithCause(err)
	}
	if strings.ContainsAny(arg, ":/") {
		return arg, nil
	}
	return "", errcodes.New(errcodes.InvalidInput, "no store named "+arg+" in this repo's .lore/stores.yaml").
		WithHint("make one with `lore store init " + arg + " <url>`, or pass the store url")
}
