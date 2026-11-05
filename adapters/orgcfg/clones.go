package orgcfg

// clones.go — this machine's v0.1.17 store clones as `lore store show`,
// `store open` and `store doctor` report them (CLI_REFERENCE 3.21: the
// v0.1.17 store commands stay as they are; LORE_TEAM_SPEC F12, 5.3),
// ported from v0.1.17's stores.Manager / store_cmd.go onto the bare clones
// the org layer keeps under <home>/clones-v017 (the same clones GitLinks
// reads links from).
//
//	Inspect ─▶ no network: cloned? opens? last fetch, default branch,
//	           unpushed refs (a check that cannot run is a problem, never
//	           "nothing unpushed")
//	Doctor  ─▶ ensure the clone, fetch (unreachable is a problem), every
//	           entry branch: exists, F12 store tree check; then Inspect's
//	           unpushed check
//	Open    ─▶ fetch, detached worktree <home>/worktrees/<norm>/<branch>
//	Links   ─▶ the cached link index of one branch (store show --links)
//
// Fail closed: a check that could not run is reported as a problem of its
// own; doctor never passes a store it could not check.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// WorktreesDir holds the browsing worktrees of `lore store open` (v0.1.17
// stores.WorktreesDir).
const WorktreesDir = "worktrees"

// Problem texts (v0.1.17's).
const (
	// ProblemOpen prefixes a clone folder git cannot open (it may hold
	// unpushed work, so it is never "not cloned").
	ProblemOpen = "open the clone: "
	// ProblemUnpushedUnknown prefixes an unpushed check that could not run.
	ProblemUnpushedUnknown = "cannot list unpushed work: "
	// ProblemUnreachable is a doctor fetch that could not reach the store.
	ProblemUnreachable = "unreachable (using the last fetched state)"
	// problemUnpushed prefixes the refs doctor found waiting.
	problemUnpushed = "unpushed work: "
)

// Ref namespaces the unpushed check compares (local, tracking).
var unpushedPairs = [][2]string{
	{"refs/heads/", "refs/remotes/" + remoteName + "/"},
	{"refs/lore/", "refs/lore-remote/" + remoteName + "/"},
}

// Clones inspects the store clones under one lore home. Run is the git
// runner (nil = the git binary); it is the one seam tests use to make a
// git call fail, as a broken or locked clone would.
type Clones struct {
	Home string
	Run  func(ctx context.Context, args ...string) (string, error)
}

// CloneInfo is what one store clone looks like on this machine.
type CloneInfo struct {
	Dir           string
	Cloned        bool
	LastFetch     time.Time // zero = never fetched
	DefaultBranch string
	Unpushed      []string
	Problems      []string
}

// CloneLink is one link of a store branch's cached index.
type CloneLink struct {
	Folder, ProjectID, Remote string
	Own, Default              bool
}

func (c Clones) git(ctx context.Context, args ...string) (string, error) {
	if c.Run != nil {
		return c.Run(ctx, args...)
	}
	return gitRun(ctx, args...)
}

// Dir is the bare clone folder of a normalised store url.
func (c Clones) Dir(norm string) string {
	return filepath.Join(c.Home, ClonesDir, filepath.FromSlash(norm)+bareSuffix)
}

// WorktreeDir is the `store open` worktree of one store branch.
func (c Clones) WorktreeDir(norm, branch string) string {
	return filepath.Join(c.Home, WorktreesDir, filepath.FromSlash(norm), branch)
}

// Inspect reads one clone without touching the network.
func (c Clones) Inspect(ctx context.Context, norm string) CloneInfo {
	info := CloneInfo{Dir: c.Dir(norm), Unpushed: []string{}, Problems: []string{}}
	if _, err := os.Stat(info.Dir); errors.Is(err, os.ErrNotExist) {
		return info // not cloned on this machine yet: not a problem
	}
	if _, err := c.git(ctx, "-C", info.Dir, "rev-parse", "--git-dir"); err != nil {
		info.Problems = append(info.Problems, ProblemOpen+err.Error())
		return info
	}
	info.Cloned = true
	info.LastFetch = lastFetch(info.Dir)
	info.DefaultBranch = (&GitLinks{Home: c.Home}).DefaultBranch(ctx, norm)
	un, err := c.unpushed(ctx, info.Dir)
	if err != nil {
		info.Problems = append(info.Problems, ProblemUnpushedUnknown+err.Error())
	} else {
		info.Unpushed = un
	}
	return info
}

// unpushed lists the local refs the store's tracking refs do not hold
// (v0.1.17 Store.Unpushed): no tracking ref, or not an ancestor of it.
func (c Clones) unpushed(ctx context.Context, dir string) ([]string, error) {
	out := []string{}
	for _, pair := range unpushedPairs {
		refs, err := c.git(ctx, "-C", dir, "for-each-ref", "--format=%(refname) %(objectname)", pair[0])
		if err != nil {
			return nil, err
		}
		lines := strings.Split(strings.TrimSpace(refs), "\n")
		sort.Strings(lines)
		for _, l := range lines {
			name, oid, ok := strings.Cut(l, " ")
			if !ok {
				continue
			}
			track := pair[1] + strings.TrimPrefix(name, pair[0])
			tid, terr := c.git(ctx, "-C", dir, "rev-parse", "--verify", "--quiet", track)
			if terr != nil {
				out = append(out, name) // no tracking ref: never pushed
				continue
			}
			if strings.TrimSpace(tid) == oid {
				continue
			}
			if _, aerr := c.git(ctx, "-C", dir, "merge-base", "--is-ancestor", oid, strings.TrimSpace(tid)); aerr != nil {
				out = append(out, name)
			}
		}
	}
	return out, nil
}

// Doctor runs every check of one store (always asking the remote): url is
// the store's fetch url, branches the entry branches that must exist.
func (c Clones) Doctor(ctx context.Context, norm, url string, branches []string) CloneInfo {
	dir := c.Dir(norm)
	info := CloneInfo{Dir: dir, Unpushed: []string{}, Problems: []string{}}
	if err := ensureBare(ctx, dir, url); err != nil {
		info.Problems = append(info.Problems, ProblemOpen+err.Error())
		return info
	}
	if err := fetch(ctx, dir); err != nil {
		info.Problems = append(info.Problems, ProblemUnreachable)
	} else {
		recordFetch(ctx, dir, time.Now())
	}
	base := c.Inspect(ctx, norm)
	info.Cloned, info.LastFetch, info.DefaultBranch = base.Cloned, base.LastFetch, base.DefaultBranch
	if !base.Cloned {
		return CloneInfo{Dir: dir, Unpushed: []string{}, Problems: append(info.Problems, base.Problems...)}
	}
	for _, b := range branches {
		tip, ok, err := c.tip(ctx, dir, b)
		switch {
		case err != nil:
			info.Problems = append(info.Problems, fmt.Sprintf("branch %s: %s", b, err.Error()))
		case !ok:
			info.Problems = append(info.Problems, fmt.Sprintf("branch %s does not exist (make a store with `lore store init <name> <url>`; attach an existing branch with `lore store add <name> <url> --branch %s`)", b, b))
		default:
			if cerr := checkStoreTree(ctx, dir, tip, norm, b); cerr != nil {
				info.Problems = append(info.Problems, cerr.Error())
			}
		}
	}
	info.Problems = append(info.Problems, base.Problems...)
	if len(base.Unpushed) > 0 {
		info.Unpushed = base.Unpushed
		info.Problems = append(info.Problems, problemUnpushed+strings.Join(base.Unpushed, ", "))
	}
	return info
}

// tip is the commit to read branch at (branchTip's rule), with the
// errors: a ref listing that fails is an error, never "no branch".
func (c Clones) tip(ctx context.Context, dir, branch string) (string, bool, error) {
	out, err := c.git(ctx, "-C", dir, "for-each-ref", "--format=%(refname)", "refs/heads/"+branch, "refs/remotes/"+remoteName+"/"+branch)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(out) == "" {
		return "", false, nil
	}
	tip, ok := branchTip(ctx, dir, branch)
	return tip, ok, nil
}

// Open fetches the store and checks branch out as a detached worktree
// under <home>/worktrees (lore keeps writing the branch itself: a ref
// checked out in a worktree is never written by lore).
func (c Clones) Open(ctx context.Context, norm, url, branch string) (dir, commit string, err error) {
	clone := c.Dir(norm)
	if err = ensureBare(ctx, clone, url); err != nil {
		return "", "", errs.Wrap(errs.Internal, "open the store clone", err)
	}
	if err = fetch(ctx, clone); err != nil {
		return "", "", errs.Wrap(errs.Offline, "fetch the store", err).WithHint("retry when the store is reachable")
	}
	recordFetch(ctx, clone, time.Now())
	tip, ok, err := c.tip(ctx, clone, branch)
	if err != nil {
		return "", "", errs.Wrap(errs.Internal, "read the store branch", err)
	}
	if !ok {
		return "", "", errs.New(errs.InvalidInput, fmt.Sprintf("%s has no branch %s", norm, branch))
	}
	dir = c.WorktreeDir(norm, branch)
	if _, serr := os.Stat(dir); serr == nil {
		if _, err = c.git(ctx, "-C", dir, "checkout", "--quiet", "--detach", tip); err != nil {
			return "", "", errs.Wrap(errs.Internal, "refresh the worktree", err)
		}
		return dir, tip, nil
	}
	if err = os.MkdirAll(filepath.Dir(dir), cloneDirMode); err != nil {
		return "", "", errs.Wrap(errs.Internal, "create the worktree dir", err)
	}
	if _, err = c.git(ctx, "-C", clone, "worktree", "add", "--quiet", "--detach", dir, tip); err != nil {
		return "", "", errs.Wrap(errs.Internal, "git worktree add", err)
	}
	return dir, tip, nil
}

// Links is the cached link index of one store branch (no fetch); a clone
// or branch that is not there has none.
func (c Clones) Links(ctx context.Context, norm, branch string) ([]CloneLink, error) {
	dir := c.Dir(norm)
	tip, ok, err := c.tip(ctx, dir, branch)
	if err != nil || !ok {
		return nil, err
	}
	ls, err := readLinks(ctx, dir, tip)
	if err != nil {
		return nil, err
	}
	out := make([]CloneLink, 0, len(ls))
	for _, l := range ls {
		out = append(out, CloneLink{Folder: l.folder, ProjectID: l.ProjectID, Remote: l.Remote, Own: l.Own, Default: l.Default})
	}
	return out, nil
}
