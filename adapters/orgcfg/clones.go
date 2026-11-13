package orgcfg

// clones.go — this machine's v0.1.17 store clones as `lore store show`,
// `store open` and `store doctor` report them (CLI_REFERENCE 3.21: the
// v0.1.17 store commands stay as they are; LORE_TEAM_SPEC F12, 5.3),
// ported from v0.1.17's stores.Manager / store_cmd.go onto the store
// clones under <home>/stores (the clones the store backend writes and
// GitLinks reads links from; storeclone.go).
//
//	Inspect ─▶ no network: cloned? opens? last fetch, default branch,
//	           unpushed refs (a check that cannot run is a problem, never
//	           "nothing unpushed")
//	Doctor  ─▶ ensure the clone, fetch (unreachable is a problem), every
//	           entry branch: exists, F12 store tree check; then Inspect's
//	           unpushed check
//	Open    ─▶ fetch; the store's clone for that branch (the default clone
//	           when it has the branch checked out, else <repo>@<branch>)
//	Links   ─▶ the cached link index of one branch (store show --links)
//
// Fail closed: a check that could not run is reported as a problem of its
// own; doctor never passes a store it could not check.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
)

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
	// Now is the clock of the fetch stamps (nil = time.Now); a host passes
	// its app clock so LORE_NOW in a test build reaches `last fetch`.
	Now func() time.Time
	// Route reaches each store as its setup's account (Doctor's and
	// Open's fetch and ls-remote); nil = git as configured.
	Route Router
}

// now is c.Now, or the wall clock.
func (c Clones) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
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

// Dir is the clone folder of a normalised store url (its default-branch
// clone).
func (c Clones) Dir(norm string) string {
	dir, _ := cloneDir(c.Home, norm)
	return dir
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
	dir, own := cloneDir(c.Home, norm)
	info := CloneInfo{Dir: dir, Unpushed: []string{}, Problems: []string{}}
	if err := ensureClone(ctx, dir, url, own); err != nil {
		info.Problems = append(info.Problems, ProblemOpen+err.Error())
		return info
	}
	if err := fetchLocked(withHeadRefresh(ctx), c.Route, dir, c.now()); err != nil {
		info.Problems = append(info.Problems, ProblemUnreachable)
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
			info.Problems = append(info.Problems, fmt.Sprintf("branch %s does not exist (make a store with `lore store init --name <name> --url <url>`; attach an existing branch with `lore store add --name <name> --url <url> --branch %s`)", b, b))
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

// Open fetches the store and returns the clone holding branch, a plain
// git clone a person may browse: the store's default clone when it has
// branch checked out, else the store's clone pinned to branch
// (<repo>@<branch>, made here when missing, as attaching the store with
// --branch would make it). Lore writes these clones too; nothing is
// detached or copied.
func (c Clones) Open(ctx context.Context, norm, url, branch string) (dir, commit string, err error) {
	clone, own := cloneDir(c.Home, norm)
	if err = ensureClone(ctx, clone, url, own); err != nil {
		return "", "", errs.Wrap(errs.Internal, "open the store clone", err)
	}
	if err = fetchLocked(ctx, c.Route, clone, c.now()); err != nil {
		return "", "", errs.Wrap(errs.Offline, "fetch the store", err).WithHint("retry when the store is reachable")
	}
	tip, ok, err := c.tip(ctx, clone, branch)
	if err != nil {
		return "", "", errs.Wrap(errs.Internal, "read the store branch", err)
	}
	if !ok {
		return "", "", errs.New(errs.InvalidInput, fmt.Sprintf("%s has no branch %s", norm, branch))
	}
	if checkedOut(ctx, clone) == branch {
		return clone, tip, nil
	}
	dir, own, err = gitbackend.CloneDirFor(c.Home, gitbackend.Remote{Norm: norm}, branch)
	if err != nil {
		return "", "", errs.Wrap(errs.Internal, "locate the branch clone", err)
	}
	if err = ensureClone(ctx, dir, url, own); err != nil {
		return "", "", errs.Wrap(errs.Internal, "open the branch clone", err)
	}
	if err = fetchLocked(ctx, c.Route, dir, c.now(), branch); err != nil {
		return "", "", errs.Wrap(errs.Offline, "fetch the store", err).WithHint("retry when the store is reachable")
	}
	if got := checkedOut(ctx, dir); got != branch {
		return "", "", errs.New(errs.InvalidInput, fmt.Sprintf("the clone %s has branch %s checked out, not %s", dir, got, branch)).
			WithHint("switch it back: git -C " + dir + " switch " + branch)
	}
	if tip, ok = branchTip(ctx, dir, branch); !ok {
		return "", "", errs.New(errs.InvalidInput, fmt.Sprintf("%s has no branch %s", norm, branch))
	}
	return dir, tip, nil
}

// checkedOut is the branch the clone at dir has checked out ("" when
// detached or unreadable).
func checkedOut(ctx context.Context, dir string) string {
	out, err := gitRun(ctx, "-C", dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
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
