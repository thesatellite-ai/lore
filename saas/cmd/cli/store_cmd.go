// store_cmd.go — opsstores.Cloner for this binary: the rules behind the
// core's `lore store show|open|forget|doctor` operations (ops/stores
// clones.go, P3-CFG-1; LORE_TEAM_SPEC §1 Commands, F12, §5.3): what this
// machine knows about each configured v0.1.17 store, an optional browsing
// worktree, dropping a clone, a clone's health. `lore store init|add|
// remove|list` are core operations too (ops/stores).
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

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// defaultMarkerOrgFolder is the org folder lore-store.yaml suggests when no
// configured entry names one (§5.1 example).
const defaultMarkerOrgFolder = "org"

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
		_, ok, tipErr := st.Tip(ctx, b)
		if tipErr != nil {
			v.Problems = append(v.Problems, fmt.Sprintf("branch %s: %s", b, redactIfQuiet(tipErr.Error())))
			continue
		}
		if !ok {
			v.Problems = append(v.Problems, fmt.Sprintf("branch %s does not exist (make a store with `lore store init <name> <url>`; attach an existing branch with `lore store add <name> <url> --branch %s`)", b, b))
			continue
		}
		if checkErr := st.Check(ctx, b); checkErr != nil {
			v.Problems = append(v.Problems, redactIfQuiet(checkErr.Error()))
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

// clonePort is opsstores.Cloner for this binary.
type clonePort struct{}

// cloneViews converts the views for the operation output.
func cloneViews(views []*storeView) []opsstores.CloneView {
	out := make([]opsstores.CloneView, 0, len(views))
	for _, v := range views {
		cv := opsstores.CloneView{URL: v.URL, Shown: redactIfQuiet(v.URL), Entries: v.Entries, Branches: v.Branches,
			Clone: v.Clone, Cloned: v.Cloned, DefaultBranch: v.DefaultBranch, Unpushed: v.Unpushed, Problems: v.Problems}
		if v.LastFetch != nil {
			cv.LastFetch = v.LastFetch.Local().Format(time.RFC3339)
		}
		for _, l := range v.Links {
			cv.Links = append(cv.Links, opsstores.CloneLink(l))
		}
		out = append(out, cv)
	}
	return out
}

// ShowClones lists the configured stores and their clones, without
// touching the network; Links adds the cached link index and this repo's
// links (the F11 debug view).
func (clonePort) ShowClones(ctx context.Context, in opsstores.ShowIn) (opsstores.ClonesOut, error) {
	cfg, err := loadTeamConfig(ctx)
	if err != nil {
		return opsstores.ClonesOut{}, coreErr(err)
	}
	m, err := newStoreManager(cfg, nil)
	if err != nil {
		return opsstores.ClonesOut{}, coreErr(err)
	}
	views, err := collectStores(ctx, cfg, m)
	if err != nil {
		return opsstores.ClonesOut{}, coreErr(err)
	}
	var here []string
	if in.Links {
		here = hereLinks(ctx, cfg)
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
	return opsstores.ClonesOut{Stores: cloneViews(views), Here: here}, nil
}

// hereLinks is what org resolution's link source answers for the repo in
// the working directory (the F11 debug view of `store show --links`),
// with the store default branches staging is decided against.
func hereLinks(ctx context.Context, cfg *lorecfg.Config) []string {
	// A debug view: every failure is the answer, never skipped (a silent
	// gap reads as "this repo has no links").
	dir, err := os.Getwd()
	if err != nil {
		return []string{"this repo: " + err.Error()}
	}
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return []string{"this repo: " + redactIfQuiet(err.Error())}
	}
	if s.ProjectID == "" {
		return nil // not a lore project: nothing to look up
	}
	src, err := storeLinkSource(cfg)
	if err != nil {
		return []string{"this repo: " + redactIfQuiet(err.Error())}
	}
	res, err := src.Links(ctx, s.ProjectID, s.Remotes)
	if err != nil {
		return []string{"this repo: " + redactIfQuiet(err.Error())}
	}
	out := []string{fmt.Sprintf("this repo (%s): links %s — %s", s.ProjectID, res.Status, redactIfQuiet(res.Detail))}
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
		out = append(out, fmt.Sprintf("  %s → %s %s/ (store default branch %s)%s", l.Entry, redactIfQuiet(l.StoreURL), l.Folder, branch, d))
	}
	return out
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

// OpenClone fetches the store and checks the branch out as a detached
// worktree under ~/.lore/worktrees (lore keeps writing the branch itself:
// a ref checked out in any worktree is never written by lore).
func (clonePort) OpenClone(ctx context.Context, in opsstores.OpenIn) (opsstores.OpenOut, error) {
	out, err := openClone(ctx, in)
	return out, coreErr(err)
}

func openClone(ctx context.Context, in opsstores.OpenIn) (opsstores.OpenOut, error) {
	cfg, err := loadTeamConfig(ctx)
	if err != nil {
		return opsstores.OpenOut{}, err
	}
	org, url := resolveStoreArg(cfg, in.URL)
	m, err := newStoreManager(cfg, org)
	if err != nil {
		return opsstores.OpenOut{}, err
	}
	st, err := m.Open(ctx, url)
	if err != nil {
		return opsstores.OpenOut{}, storeCLIError(err, "open the store clone")
	}
	if _, fetchErr := st.Fetch(ctx, false); fetchErr != nil {
		return opsstores.OpenOut{}, storeCLIError(fetchErr, "fetch the store")
	}
	branch := in.Branch
	if branch == "" {
		branch = st.DefaultBranch()
		if _, sc := orgForStoreURL(cfg, url); sc != nil {
			branch = sc.EffectiveBranch()
		}
	}
	tip, ok, err := st.Tip(ctx, branch)
	if err != nil {
		return opsstores.OpenOut{}, storeCLIError(err, "read the store branch")
	}
	if !ok {
		return opsstores.OpenOut{}, storeCLIError(fmt.Errorf("%w: %s has no branch %s", stores.ErrNoBranch, st.Norm, branch), "open")
	}
	dir, err := m.Layout.WorktreeDir(url, branch)
	if err != nil {
		return opsstores.OpenOut{}, storeCLIError(err, "worktree path")
	}
	if _, err := os.Stat(dir); err == nil {
		if _, err := st.Repo.Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"checkout", "--quiet", "--detach", tip}}); err != nil {
			return opsstores.OpenOut{}, errcodes.New(errcodes.Internal, "refresh the worktree").WithCause(err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return opsstores.OpenOut{}, errcodes.New(errcodes.Internal, "create worktree dir").WithCause(err)
		}
		if _, err := st.Repo.Run(ctx, gitref.Invocation{Args: []string{"worktree", "add", "--quiet", "--detach", dir, tip}}); err != nil {
			return opsstores.OpenOut{}, errcodes.New(errcodes.Internal, "git worktree add").WithCause(err)
		}
	}
	return opsstores.OpenOut{Dir: dir, Branch: branch, Commit: tip}, nil
}

// ForgetClone deletes this machine's clone of a store, refused while it
// (or the 0.1.18 core's clone of the same url, side effect 20) holds
// unpushed work; waiting work is pushed first when autopush is on.
func (clonePort) ForgetClone(ctx context.Context, in opsstores.ForgetIn) (opsstores.ForgetOut, error) {
	out, err := forgetClone(ctx, in.URL)
	return out, coreErr(err)
}

// unpushedForgetError is the refusal of a forget that would lose work.
func unpushedForgetError(pending []string) error {
	return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("the clone holds unpushed work (%s); forgetting it would lose it", strings.Join(pending, ", "))).
		WithHint("run `lore sync` when the store is reachable, then forget it")
}

func forgetClone(ctx context.Context, arg string) (opsstores.ForgetOut, error) {
	cfg, err := loadTeamConfig(ctx)
	if err != nil {
		return opsstores.ForgetOut{}, err
	}
	org, url := resolveStoreArg(cfg, arg)
	m, err := newStoreManager(cfg, org)
	if err != nil {
		return opsstores.ForgetOut{}, err
	}
	// The 0.1.18 core's clone of the same url: unpushed work there
	// blocks the forget too (side effect 20).
	home, err := loreHome()
	if err != nil {
		return opsstores.ForgetOut{}, errcodes.New(errcodes.Internal, "resolve the lore home").WithCause(err)
	}
	cores, err := coreClonesOf(home, url)
	if err != nil {
		return opsstores.ForgetOut{}, err
	}
	var corePending []string
	for _, c := range cores {
		corePending = append(corePending, coreCloneUnpushed(ctx, c)...)
	}
	if len(corePending) > 0 {
		return opsstores.ForgetOut{}, unpushedForgetError(corePending)
	}
	st, err := m.Lookup(ctx, url)
	if errors.Is(err, stores.ErrNoClone) {
		out := opsstores.ForgetOut{URL: url, Shown: redactIfQuiet(url)}
		if len(cores) > 0 {
			if rerr := removeCoreClones(cores); rerr != nil {
				return opsstores.ForgetOut{}, rerr
			}
			out.Removed = cores[0].base
		}
		return out, nil
	}
	if err != nil {
		// Not "nothing to forget": a clone that cannot be opened (or a
		// URL lore cannot place) may still hold unpushed work.
		return opsstores.ForgetOut{}, storeCLIError(err, "open the clone of "+redactIfQuiet(url))
	}
	// Try to deliver waiting work first (PushPending takes the clone's
	// lock itself); whatever still waits blocks the forget.
	if storeAutopush(cfg, org) {
		if _, _, pushErr := st.PushPending(ctx, storeSignature(org)); pushErr != nil {
			fmt.Fprintln(os.Stderr, "lore: push waiting store writes: "+redactIfQuiet(pushErr.Error()))
		}
	}
	unlock, err := st.Lock()
	if err != nil {
		return opsstores.ForgetOut{}, storeCLIError(err, "lock the clone")
	}
	defer unlock()
	pending, err := st.Unpushed(ctx)
	if err != nil {
		return opsstores.ForgetOut{}, storeCLIError(err, "list unpushed work")
	}
	if len(pending) > 0 {
		return opsstores.ForgetOut{}, unpushedForgetError(pending)
	}
	if err := removeStoreWorktrees(m, url); err != nil {
		return opsstores.ForgetOut{}, err
	}
	if err := os.RemoveAll(st.Dir); err != nil {
		return opsstores.ForgetOut{}, errcodes.New(errcodes.Internal, "remove the clone").WithCause(err)
	}
	if rerr := removeCoreClones(cores); rerr != nil {
		return opsstores.ForgetOut{}, rerr
	}
	return opsstores.ForgetOut{URL: st.Norm, Shown: redactIfQuiet(st.Norm), Removed: st.Dir}, nil
}

// removeStoreWorktrees deletes the browsing worktrees of one store.
func removeStoreWorktrees(m *stores.Manager, url string) error {
	dir, err := m.Layout.WorktreeDir(url, "")
	if err != nil {
		return storeCLIError(err, "locate the store worktrees")
	}
	if err := os.RemoveAll(dir); err != nil {
		return errcodes.New(errcodes.Internal, "remove the store worktrees").WithCause(err)
	}
	return nil
}

// DoctorClones runs every check of every configured store (always
// asking the remote); the operation turns any problem into an error.
func (clonePort) DoctorClones(ctx context.Context) (opsstores.ClonesOut, error) {
	cfg, err := loadTeamConfig(ctx)
	if err != nil {
		return opsstores.ClonesOut{}, coreErr(err)
	}
	m, err := newStoreManager(cfg, nil)
	if err != nil {
		return opsstores.ClonesOut{}, coreErr(err)
	}
	m.FetchTTL = 0 // doctor always asks the store
	views, err := collectStores(ctx, cfg, m)
	if err != nil {
		return opsstores.ClonesOut{}, coreErr(err)
	}
	for _, v := range views {
		doctorCheckStore(ctx, m, v)
	}
	// Stores with problems first, as v0.1.17 printed them.
	sort.SliceStable(views, func(i, j int) bool { return len(views[i].Problems) > len(views[j].Problems) })
	return opsstores.ClonesOut{Stores: cloneViews(views)}, nil
}
