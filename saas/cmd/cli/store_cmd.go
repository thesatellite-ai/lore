// store_cmd.go — `lore store init|show|open|forget|doctor` (LORE_TEAM_SPEC
// §1 Commands, F12, §5.3). A store is a lore-only repo; these commands make
// one, show what this machine knows about each configured store, open an
// optional browsing worktree, drop a clone, and check a clone's health.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/stores"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// Flag names of the store commands.
const (
	flagStoreBranch    = "branch"
	flagStoreOrgFolder = "org-folder"
	flagStoreLinks     = "links"
)

// defaultMarkerOrgFolder is the org folder lore-store.yaml suggests when no
// configured entry names one (§5.1 example).
const defaultMarkerOrgFolder = "org"

func newStoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store",
		Short: "Manage lore stores: lore-only repos holding internal and personal projects",
		Long: `A store is a separate lore-only git repo (never a code repo) with one folder
per project. Lore keeps one bare clone per store URL under ~/.lore/clones and
reads and writes it without a checkout.`,
	}
	cmd.AddCommand(newStoreInitCommand(), newStoreShowCommand(), newStoreOpenCommand(), newStoreForgetCommand(), newStoreDoctorCommand())
	return cmd
}

func newStoreInitCommand() *cobra.Command {
	var branch, orgFolder string
	cmd := &cobra.Command{
		Use:   "init <url>",
		Short: "Make an empty repo a lore store (writes lore-store.yaml and pushes)",
		Example: `  lore store init git@github.com:agency/notes
  lore store init git@github.com:agency/notes --branch staging`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			url := args[0]
			cfg, err := loadTeamConfig(ctx)
			if err != nil {
				return err
			}
			org, sc := orgForStoreURL(cfg, url)
			if branch == "" {
				branch = lorecfg.DefaultStoreBranch
				if sc != nil && !cmd.Flags().Changed(flagStoreBranch) {
					branch = sc.EffectiveBranch()
				}
			}
			if err := stores.ValidateBranch(branch); err != nil {
				return errcodes.New(errcodes.InvalidInput, err.Error())
			}
			if orgFolder == "" {
				orgFolder = defaultMarkerOrgFolder
				if sc != nil && sc.OrgFolder != "" {
					orgFolder = sc.OrgFolder
				}
			}
			m, err := newStoreManager(cfg, org)
			if err != nil {
				return err
			}
			st, err := m.Open(ctx, url)
			if err != nil {
				return storeCLIError(err, "open the store clone")
			}
			res, err := st.Init(ctx, branch, stores.NewMarker(cliClock().Now(), orgFolder), storeSignature(org))
			if err != nil {
				return storeCLIError(err, "init the store")
			}
			switch {
			case res.Already:
				fmt.Printf("✓ %s (branch %s) is already a lore store; nothing changed\n", st.Norm, branch)
			case res.Created:
				fmt.Printf("✓ wrote %s to %s (new branch %s, first commit) and pushed\n", stores.MarkerFile, st.Norm, branch)
			default:
				fmt.Printf("✓ wrote %s to %s (branch %s) and pushed\n", stores.MarkerFile, st.Norm, branch)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, flagStoreBranch, "", "the store branch to make a lore (default: the entry's branch, else main); may not contain /")
	cmd.Flags().StringVar(&orgFolder, flagStoreOrgFolder, "", "org folder recorded in lore-store.yaml (default: the entry's org_folder, else org)")
	return cmd
}

// storeView is one configured store as `lore store show` reports it.
type storeView struct {
	URL           string        `json:"url"`
	Entries       []string      `json:"entries"`
	Branches      []string      `json:"branches"`
	Clone         string        `json:"clone"`
	Cloned        bool          `json:"cloned"`
	LastFetch     *time.Time    `json:"last_fetch,omitempty"`
	DefaultBranch string        `json:"default_branch,omitempty"`
	Unpushed      []string      `json:"unpushed"`
	Links         []linkView    `json:"links,omitempty"`
	Problems      []string      `json:"problems"`
	store         *stores.Store `json:"-"`
}

// linkView is one link of the index, for --links.
type linkView struct {
	Entry     string `json:"entry"`
	Folder    string `json:"folder"`
	Branch    string `json:"branch"`
	ProjectID string `json:"project_id"`
	Remote    string `json:"remote"`
	Own       bool   `json:"own,omitempty"`
	Default   bool   `json:"default,omitempty"`
}

// collectStores groups configured stores by clone (entries on one URL share
// one clone), without touching the network.
func collectStores(ctx context.Context, cfg *lorecfg.Config, m *stores.Manager) ([]*storeView, error) {
	byDir := map[string]*storeView{}
	var order []string
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		for _, sc := range o.Stores {
			dir, err := m.Layout.CloneDir(sc.URL)
			if err != nil {
				return nil, storeCLIError(err, "store url")
			}
			v := byDir[dir]
			if v == nil {
				v = &storeView{URL: sc.URL, Clone: dir, Unpushed: []string{}, Problems: []string{}}
				byDir[dir] = v
				order = append(order, dir)
			}
			v.Entries = appendUnique(v.Entries, o.Name)
			v.Branches = appendUnique(v.Branches, sc.EffectiveBranch())
		}
	}
	var out []*storeView
	for _, dir := range order {
		v := byDir[dir]
		st, err := m.Lookup(ctx, v.URL)
		switch {
		case errors.Is(err, stores.ErrNoClone):
			// Not cloned on this machine yet: Cloned stays false.
		case err != nil:
			// A clone that exists but cannot be opened is a problem, not
			// "not cloned" (it may hold unpushed work).
			v.Problems = append(v.Problems, "open the clone: "+redactIfQuiet(err.Error()))
		default:
			v.Cloned, v.store = true, st
			if t := st.LastFetch(); !t.IsZero() {
				v.LastFetch = &t
			}
			v.DefaultBranch = st.DefaultBranch()
			un, err := st.Unpushed(ctx)
			switch {
			case err != nil:
				v.Problems = append(v.Problems, unpushedUnknownProblem+redactIfQuiet(err.Error()))
			case un != nil:
				v.Unpushed = un
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// doctorCheckStore runs every doctor check of one store and appends what is
// wrong to v.Problems. A check that cannot run is itself a problem: doctor
// never passes a store it could not check.
func doctorCheckStore(ctx context.Context, m *stores.Manager, v *storeView) {
	st, err := m.Open(ctx, v.URL)
	if err != nil {
		v.Problems = append(v.Problems, redactIfQuiet(err.Error()))
		return
	}
	fr, err := st.Fetch(ctx, true)
	switch {
	case err != nil:
		v.Problems = append(v.Problems, "fetch: "+redactIfQuiet(err.Error()))
	case fr.Offline:
		v.Problems = append(v.Problems, "unreachable (using the last fetched state)")
	default:
		t := fr.At
		v.LastFetch = &t
	}
	v.DefaultBranch = st.DefaultBranch()
	for _, b := range v.Branches {
		_, ok, err := st.Tip(ctx, b)
		if err != nil {
			v.Problems = append(v.Problems, fmt.Sprintf("branch %s: %s", b, redactIfQuiet(err.Error())))
			continue
		}
		if !ok {
			v.Problems = append(v.Problems, fmt.Sprintf("branch %s does not exist (`lore store init <url> --branch %s`)", b, b))
			continue
		}
		if err := st.Check(ctx, b); err != nil {
			v.Problems = append(v.Problems, redactIfQuiet(err.Error()))
		}
	}
	un, err := st.Unpushed(ctx)
	switch {
	case err != nil:
		// Doctor must not pass a store it could not check.
		v.Problems = append(v.Problems, unpushedUnknownProblem+redactIfQuiet(err.Error()))
	case len(un) > 0:
		v.Unpushed = un
		v.Problems = append(v.Problems, "unpushed work: "+strings.Join(un, ", "))
	}
}

// unpushedUnknownProblem prefixes the problem reported when a clone's
// unpushed work cannot be listed: "could not check" is never shown as
// "nothing waiting".
const unpushedUnknownProblem = "cannot list unpushed work: "

func appendUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

func newStoreShowCommand() *cobra.Command {
	var links, asJSON bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "List the configured stores, their clones and (with --links) the link index",
		Example: `  lore store show
  lore store show --links --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, err := loadTeamConfig(ctx)
			if err != nil {
				return err
			}
			m, err := newStoreManager(cfg, nil)
			if err != nil {
				return err
			}
			views, err := collectStores(ctx, cfg, m)
			if err != nil {
				return err
			}
			if links {
				printHereLinks(ctx, cfg)
				for _, v := range views {
					for _, e := range v.Entries {
						ls, _, ok := stores.CachedLinks(m.Layout, e)
						if !ok {
							continue
						}
						for _, l := range ls {
							if dir, err := m.Layout.CloneDir(l.StoreURL); err == nil && dir == v.Clone {
								v.Links = append(v.Links, linkView{Entry: l.Entry, Folder: l.Folder, Branch: l.Branch, ProjectID: l.ProjectID, Remote: l.Remote, Own: l.Own, Default: l.Default})
							}
						}
					}
				}
			}
			if asJSON {
				return writeJSONOut(map[string]any{"data": views})
			}
			if len(views) == 0 {
				fmt.Println(style.Muted("no stores configured (orgs[].stores in ~/.lore/config.yaml)"))
				return nil
			}
			for _, v := range views {
				fmt.Printf("%s\n", redactIfQuiet(v.URL))
				fmt.Printf("  entries   %s   branches %s\n", strings.Join(v.Entries, ", "), strings.Join(v.Branches, ", "))
				for _, p := range v.Problems {
					fmt.Println("  problem   " + style.Warn(p))
				}
				if !v.Cloned {
					fmt.Println("  clone     " + style.Muted("not cloned yet (the next command that needs it clones it)"))
					continue
				}
				fetched := "never"
				if v.LastFetch != nil {
					fetched = v.LastFetch.Local().Format(time.RFC3339)
				}
				fmt.Printf("  clone     %s (bare)   default branch %s   last fetch %s\n", v.Clone, v.DefaultBranch, fetched)
				if len(v.Unpushed) > 0 {
					fmt.Printf("  unpushed  %s\n", strings.Join(v.Unpushed, ", "))
				}
				for _, l := range v.Links {
					d := ""
					if l.Default {
						d = " (default)"
					}
					o := ""
					if l.Own {
						o = " own"
					}
					fmt.Printf("  link      %s/ on %s ← %s %s%s%s\n", l.Folder, l.Branch, l.Remote, l.ProjectID, o, d)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&links, flagStoreLinks, false, "also print the cached link index of every entry")
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "machine-readable output")
	return cmd
}

// printHereLinks prints what org resolution's link source answers for the
// repo in the working directory (the F11 debug view of `store show
// --links`), with the store default branches staging is decided against.
func printHereLinks(ctx context.Context, cfg *lorecfg.Config) {
	// A debug view: every failure is printed as the answer, never skipped
	// (a silent gap reads as "this repo has no links").
	dir, err := os.Getwd()
	if err != nil {
		fmt.Println("this repo: " + err.Error())
		return
	}
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		fmt.Println("this repo: " + redactIfQuiet(err.Error()))
		return
	}
	if s.ProjectID == "" {
		return // not a lore project: nothing to look up
	}
	src, err := storeLinkSource(cfg)
	if err != nil {
		fmt.Println("this repo: " + redactIfQuiet(err.Error()))
		return
	}
	res, err := src.Links(ctx, s.ProjectID, s.Remotes)
	if err != nil {
		fmt.Println("this repo: " + redactIfQuiet(err.Error()))
		return
	}
	fmt.Printf("this repo (%s): links %s — %s\n", s.ProjectID, res.Status, redactIfQuiet(res.Detail))
	def := storeDefaultBranchFunc(cfg)
	for _, l := range res.Links {
		d := ""
		if l.Default {
			d = " (default)"
		}
		branch := lorecfg.DefaultStoreBranch
		if def != nil {
			branch = def(l.StoreURL)
		}
		fmt.Printf("  %s → %s %s/ (store default branch %s)%s\n", l.Entry, redactIfQuiet(l.StoreURL), l.Folder, branch, d)
	}
}

// writeJSONOut prints v as indented JSON on stdout.
func writeJSONOut(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errcodes.New(errcodes.Internal, "encode json").WithCause(err)
	}
	fmt.Println(string(b))
	return nil
}

// resolveStoreArg finds a configured store by URL (any form), or accepts an
// unconfigured URL as is.
func resolveStoreArg(cfg *lorecfg.Config, url string) (*lorecfg.Org, string) {
	org, sc := orgForStoreURL(cfg, url)
	if sc != nil {
		return org, sc.URL
	}
	return org, url
}

func newStoreOpenCommand() *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "open <url>",
		Short: "Check out a store branch for browsing (detached, under ~/.lore/worktrees)",
		Long: `Lore never needs a checkout of a store. This makes one for reading: a detached
worktree, so lore can keep writing the branch itself (a ref checked out in any
worktree is never written by lore).`,
		Example: `  lore store open git@github.com:agency/notes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := loadTeamConfig(ctx)
			if err != nil {
				return err
			}
			org, url := resolveStoreArg(cfg, args[0])
			m, err := newStoreManager(cfg, org)
			if err != nil {
				return err
			}
			st, err := m.Open(ctx, url)
			if err != nil {
				return storeCLIError(err, "open the store clone")
			}
			if _, err := st.Fetch(ctx, false); err != nil {
				return storeCLIError(err, "fetch the store")
			}
			if branch == "" {
				branch = st.DefaultBranch()
				if _, sc := orgForStoreURL(cfg, url); sc != nil {
					branch = sc.EffectiveBranch()
				}
			}
			tip, ok, err := st.Tip(ctx, branch)
			if err != nil {
				return storeCLIError(err, "read the store branch")
			}
			if !ok {
				return storeCLIError(fmt.Errorf("%w: %s has no branch %s", stores.ErrNoBranch, st.Norm, branch), "open")
			}
			dir, err := m.Layout.WorktreeDir(url, branch)
			if err != nil {
				return storeCLIError(err, "worktree path")
			}
			if _, err := os.Stat(dir); err == nil {
				if _, err := st.Repo.Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"checkout", "--quiet", "--detach", tip}}); err != nil {
					return errcodes.New(errcodes.Internal, "refresh the worktree").WithCause(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
					return errcodes.New(errcodes.Internal, "create worktree dir").WithCause(err)
				}
				if _, err := st.Repo.Run(ctx, gitref.Invocation{Args: []string{"worktree", "add", "--quiet", "--detach", dir, tip}}); err != nil {
					return errcodes.New(errcodes.Internal, "git worktree add").WithCause(err)
				}
			}
			fmt.Printf("✓ %s at %s (%s, detached; read-only by convention — lore writes the branch itself)\n", dir, branch, tip[:12])
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, flagStoreBranch, "", "store branch (default: the entry's branch)")
	return cmd
}

func newStoreForgetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "forget <url>",
		Short:   "Delete this machine's clone of a store (refused while it holds unpushed work)",
		Example: `  lore store forget git@github.com:agency/notes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := loadTeamConfig(ctx)
			if err != nil {
				return err
			}
			org, url := resolveStoreArg(cfg, args[0])
			m, err := newStoreManager(cfg, org)
			if err != nil {
				return err
			}
			st, err := m.Lookup(ctx, url)
			if errors.Is(err, stores.ErrNoClone) {
				fmt.Println(style.Muted("no clone of " + redactIfQuiet(url) + " on this machine; nothing to forget"))
				return nil
			}
			if err != nil {
				// Not "nothing to forget": a clone that cannot be opened
				// (or a URL lore cannot place) may still hold unpushed work.
				return storeCLIError(err, "open the clone of "+redactIfQuiet(url))
			}
			// Try to deliver waiting work first (PushPending takes the
			// clone's lock itself); whatever still waits blocks the forget.
			if storeAutopush(cfg, org) {
				if _, _, err := st.PushPending(ctx, storeSignature(org)); err != nil {
					fmt.Fprintln(os.Stderr, "lore: push waiting store writes: "+redactIfQuiet(err.Error()))
				}
			}
			unlock, err := st.Lock()
			if err != nil {
				return storeCLIError(err, "lock the clone")
			}
			defer unlock()
			pending, err := st.Unpushed(ctx)
			if err != nil {
				return storeCLIError(err, "list unpushed work")
			}
			if len(pending) > 0 {
				return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("the clone holds unpushed work (%s); forgetting it would lose it", strings.Join(pending, ", "))).
					WithHint("run `lore sync` when the store is reachable, then forget it")
			}
			if err := removeStoreWorktrees(m, cfg, url); err != nil {
				return err
			}
			if err := os.RemoveAll(st.Dir); err != nil {
				return errcodes.New(errcodes.Internal, "remove the clone").WithCause(err)
			}
			fmt.Printf("✓ forgot %s (removed %s; the next command that needs it clones it again)\n", redactIfQuiet(st.Norm), st.Dir)
			return nil
		},
	}
	return cmd
}

// removeStoreWorktrees deletes the browsing worktrees of one store.
func removeStoreWorktrees(m *stores.Manager, cfg *lorecfg.Config, url string) error {
	dir, err := m.Layout.WorktreeDir(url, "")
	if err != nil {
		return storeCLIError(err, "locate the store worktrees")
	}
	if err := os.RemoveAll(dir); err != nil {
		return errcodes.New(errcodes.Internal, "remove the store worktrees").WithCause(err)
	}
	return nil
}

func newStoreDoctorCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check every configured store: reachable, marker and files, unpushed work, entries sharing the clone",
		Example: `  lore store doctor
  lore store doctor --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, err := loadTeamConfig(ctx)
			if err != nil {
				return err
			}
			m, err := newStoreManager(cfg, nil)
			if err != nil {
				return err
			}
			m.FetchTTL = 0 // doctor always asks the store
			views, err := collectStores(ctx, cfg, m)
			if err != nil {
				return err
			}
			bad := 0
			for _, v := range views {
				doctorCheckStore(ctx, m, v)
				if len(v.Problems) > 0 {
					bad++
				}
			}
			if asJSON {
				if err := writeJSONOut(map[string]any{"data": views}); err != nil {
					return err
				}
			} else {
				sort.SliceStable(views, func(i, j int) bool { return len(views[i].Problems) > len(views[j].Problems) })
				for _, v := range views {
					mark := style.Success("✓")
					if len(v.Problems) > 0 {
						mark = style.Warn("!")
					}
					fmt.Printf("%s %s  (entries %s; branches %s)\n", mark, redactIfQuiet(v.URL), strings.Join(v.Entries, ", "), strings.Join(v.Branches, ", "))
					for _, p := range v.Problems {
						fmt.Println("    " + p)
					}
				}
				fmt.Println(style.Muted("  direct pushes to a store branch are confirmed on the next write; a refusal is reported there"))
			}
			if bad > 0 {
				return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("%d store(s) need attention", bad))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "machine-readable output")
	return cmd
}
