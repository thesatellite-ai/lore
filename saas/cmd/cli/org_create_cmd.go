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
// Like import it shows the entry and asks first (--yes without a
// terminal). Client repos are tied to the org afterwards with
// `lore org link`, which writes only into the store.
package main

import (
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

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/engine/attach"
)

// Flags of org create (identity flags are shared with org import).
const (
	flagCreateOrgFolder = "org-folder"
	flagCreateOwn       = "own"
	flagCreateRepos     = "repos"
)

// newOrgCreateCommand builds `lore org create`.
func newOrgCreateCommand() *cobra.Command {
	var storeArg, orgFolder, key, name, handle string
	var own, repos []string
	var yes bool
	cmd := &cobra.Command{
		Use:   "create <org> --store <store>",
		Short: "Start a new org on a lore store: suggest it to teammates, add it with your identity, join its roster",
		Long: `Writes <org folder>/.lore/lore-org.yaml into the store (the org's name, its
store and repo patterns: what teammates adopt with ` + "`lore org import <url>`" + `),
adds the entry with your identity to ~/.lore/config.yaml and registers you on
the roster. --store is a store this repo attaches (a name in .lore/stores.yaml,
as made by ` + "`lore store init <name> <url>`" + `) or a store url.`,
		Example: `  lore store init agency git@github.com:agency/notes
  lore org create agency --store agency --own 'github.com/agency/*' --repos 'github.com/acme/*' \
    --key human:mira@agency.example --name Mira --handle mira`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			orgName := args[0]
			if strings.TrimSpace(storeArg) == "" {
				return errcodes.New(errcodes.InvalidInput, "name the org's store with --store <name in .lore/stores.yaml | url>").
					WithHint("make one first with `lore store init <name> <url>`")
			}
			url, err := resolveCreateStore(storeArg)
			if err != nil {
				return err
			}
			cfg, err := loadTeamConfig(ctx)
			if err != nil {
				return err
			}
			if cfg.FindOrg(orgName) != nil {
				return errcodes.New(errcodes.InvalidInput, "an org entry named "+orgName+" already exists in ~/.lore/config.yaml").
					WithHint("pick another name, or link repos to the existing org with `lore org link`")
			}
			entry := lorecfg.Org{Name: orgName, Own: own, Repos: repos,
				Stores:   []lorecfg.Store{{URL: url, OrgFolder: orgFolder}},
				Identity: lorecfg.OrgIdentity{Key: key, Name: name, Handle: handle}}
			if entry.Identity.Key == "" || entry.Identity.Name == "" {
				return errcodes.New(errcodes.InvalidInput, "your identity in "+orgName+" is required: --key human:<your email there> --name \"Your Name\" [--handle h]")
			}
			body, err := yaml.Marshal(entry)
			if err != nil {
				return errcodes.New(errcodes.Internal, "encode the entry").WithCause(err)
			}
			fmt.Printf("This writes %s/.lore/%s into the store (no identity) and adds to ~/.lore/config.yaml:\n\n%s\n",
				orgFolder, stores.OrgDefaultsFile, indentBlock(string(body)))
			risky := riskyImport(entry)
			for _, r := range risky {
				fmt.Println("  note: " + r)
			}
			if !yes {
				if !stdinIsTTY() {
					return errcodes.New(errcodes.InvalidInput, "confirm with --yes when there is no terminal; nothing written")
				}
				if !confirmYes("Create it? [y/N] ") {
					fmt.Println("nothing written")
					return nil
				}
			}
			m, err := newStoreManager(cfg, &entry)
			if err != nil {
				return err
			}
			st, err := m.Open(ctx, url)
			if err != nil {
				return storeCLIError(err, "open the store")
			}
			sugg := stores.OrgFileDefaults{Name: orgName, Own: own, Repos: repos,
				Stores: []stores.SuggestedStore{{URL: url, OrgFolder: orgFolder}}}
			res, existed, err := st.WriteOrgFile(ctx, st.DefaultBranch(), orgFolder, sugg, storeSignature(&entry), !storeAutopush(cfg, &entry))
			if errors.Is(err, stores.ErrOrgFileExists) {
				return errcodes.New(errcodes.InvalidInput, err.Error()).
					WithHint("that store already suggests an org: adopt it with `lore org import " + url + "`")
			}
			if err != nil {
				return storeCLIError(err, "write lore-org.yaml")
			}
			switch {
			case existed:
				fmt.Printf("✓ the store already suggests org %s (%s/.lore/%s kept)\n", orgName, orgFolder, stores.OrgDefaultsFile)
			case res.Offline:
				fmt.Printf("✓ wrote %s/.lore/%s (committed in the local clone; offline, pushed by the next command)\n", orgFolder, stores.OrgDefaultsFile)
			case !res.Pushed:
				fmt.Printf("✓ wrote %s/.lore/%s (committed in the local clone; `lore sync` pushes it)\n", orgFolder, stores.OrgDefaultsFile)
			default:
				fmt.Printf("✓ wrote %s/.lore/%s (pushed)\n", orgFolder, stores.OrgDefaultsFile)
			}
			home, err := loreHome()
			if err != nil {
				return err
			}
			if err := appendOrgEntry(ctx, home, entry); err != nil {
				return err
			}
			fmt.Printf("✓ added org entry %s\n", orgName)
			if err := registerAfterImport(ctx, orgName); err != nil {
				fmt.Fprintln(os.Stderr, "lore: the org is created, but registering on the roster failed: "+redactIfQuiet(err.Error()))
				fmt.Fprintln(os.Stderr, "      run `lore actor register` in one of "+orgName+"'s repos")
			}
			fmt.Println("  teammates join with: lore org import " + url + " --key human:<their email> --name \"Their Name\"")
			fmt.Println("  tie a client repo to it (written only into the store): lore org link " + orgName + " --project <folder>")
			return nil
		},
	}
	cmd.Flags().StringVar(&storeArg, flagStore, "", "the org's store: a name in this repo's .lore/stores.yaml, or a store url (required)")
	cmd.Flags().StringVar(&orgFolder, flagCreateOrgFolder, defaultMarkerOrgFolder, "the store folder for company rules and the roster (CFG1)")
	cmd.Flags().StringSliceVar(&own, flagCreateOwn, nil, "pattern of the org's own repos, e.g. github.com/agency/* (repeatable)")
	cmd.Flags().StringSliceVar(&repos, flagCreateRepos, nil, "pattern of client repos the org works in (repeatable)")
	cmd.Flags().StringVar(&key, flagImportKey, "", "your identity key in this org: human:<your email there>")
	cmd.Flags().StringVar(&name, flagImportName, "", "your display name")
	cmd.Flags().StringVar(&handle, flagImportHandle, "", "your @handle on the roster")
	cmd.Flags().BoolVar(&yes, flagYes, false, "confirm without a prompt")
	return cmd
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
