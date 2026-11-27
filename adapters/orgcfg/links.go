// links.go — the store link source (F11): which org store folders link
// this repo, read from each store folder's _meta.json `links` list in the
// store's clone.
//
// The clone is the one the git store backend opens for the store's
// default branch (<home>/stores/<owner>/<repo>, or a person's own clone;
// adapters/backend/git clonelayout.go), its state in <clone>/.git/lore/
// (fetch-stamp, default-branch, the one store lock): there is no second
// copy of a store on the machine, and a link `lore org link` just wrote is
// seen at once, even offline. Links are read from refs with git plumbing
// (ls-tree, cat-file at a branch tip), so reading never touches the files
// a person browses (storeclone.go).
//
//	Links ─▶ per non-staging entry, per store:
//	           open (PrepareClone: git init + origin) ─▶ fetch unless younger than
//	           live.fetch_ttl ─▶ tip of the branch (local ahead of tracking
//	           wins) ─▶ ls-tree, read every <folder>/_meta.json
//	         unreachable and older than link_index.max_age ─▶ unknown (fail
//	         closed where the entry's patterns match this repo)

package orgcfg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// Store clone names (adapters/backend/git clonelayout.go owns the layout).
const (
	// remoteName is the clone's remote.
	remoteName = gitbackend.RemoteName
	// fetchStampFile records the last successful fetch (RFC 3339), in the
	// clone's .git/lore (shared with the backend).
	fetchStampFile = gitbackend.FetchStampFile
	// defaultBranchFile records the store's HEAD branch, in .git/lore.
	defaultBranchFile = "default-branch"
	// cloneDirMode / cloneFileMode: machine state for the user only.
	cloneDirMode  = 0o700
	cloneFileMode = 0o600
	// oldMetaPathSuffix ends a store folder's project marker path in the
	// old layout (<folder>/.lore/data/_meta.json; read until the store is
	// moved, brief 1.6). The current one is <folder>/_meta.json
	// (rowfs.LayoutStore).
	oldMetaPathSuffix = ".lore/data/_meta.json"
	// headRefPrefix starts the symref line of `ls-remote --symref`.
	headRefPrefix = "ref: refs/heads/"
	// forkHint is F11's warning for a project id linked under another remote.
	forkHint = "run `lore project reid` if it is a different project"
)

// fetchRefspecs are v0.1.17's refspecs, passed on each fetch (never
// written to the clone's config: a person's plain `git fetch` keeps git's
// default).
var fetchRefspecs = []string{
	"+refs/heads/*:refs/remotes/" + remoteName + "/*",
	"+refs/lore/*:refs/lore-remote/" + remoteName + "/*",
}

// GitLinks reads store links from the store clones under Home. The zero value
// is not usable; set Home and Config.
type GitLinks struct {
	// Home is the lore home (~/.lore).
	Home string
	// Config is the loaded config (its orgs and their stores).
	Config *orgconf.Config
	// Now is the clock (default time.Now).
	Now func() time.Time
	// Always fetches every store this process reads, ignoring
	// live.fetch_ttl (`lore sync`: v0.1.17 refreshLinkIndex).
	Always bool
	// Route reaches each store as its setup's account (fetch, ls-remote,
	// push); nil = git as configured.
	Route Router
	// Only, when non-nil, names the org entries whose stores Links may read
	// (fetch, ls-remote, the clone itself); every other entry is skipped
	// and listed in the detail. Gather sets it from what the repo already
	// says about its org (scopeOrgs): two orgs on one laptop never touch
	// each other's stores, so a command in org A's repo makes no call to
	// org B's remote and never needs B's clone to resolve (devco D-278).
	// nil reads every entry (no hint: the store links decide).
	Only map[string]bool
	// Held reports whether a commit lore makes in the store clone at dir
	// waits there for a person's `lore sync` (the store's push mode; C5,
	// B-16) instead of being pushed at once. nil pushes at once (the
	// adapter's own tests); wire always sets it.
	Held func(dir string) bool
}

// held asks Held about the clone at dir (false when unset).
func (g *GitLinks) held(dir string) bool { return g.Held != nil && g.Held(dir) }

// compile-time check.
var _ LinkSource = (*GitLinks)(nil)

// now is the clock.
func (g *GitLinks) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// linkRecord is one entry of a store folder's _meta.json `links`.
type linkRecord struct {
	ProjectID string `json:"project_id"`
	Remote    string `json:"remote"`
	Own       bool   `json:"own,omitempty"`
	Default   bool   `json:"default,omitempty"`
}

// storeMeta is the part of a store folder's _meta.json read here.
type storeMeta struct {
	Links []linkRecord `json:"links"`
}

// indexedLink is one link with where it lives.
type indexedLink struct {
	entry, storeNorm, folder string
	linkRecord
}

// entryStatus is how one entry's links were obtained.
type entryStatus int

const (
	entryFresh   entryStatus = iota // every store read now
	entryStale                      // a store offline; a copy younger than max age used
	entryUnknown                    // a store unreadable and no usable copy
	entryStaging                    // a staging entry: never scanned (TEAM-022)
)

// Links implements orgresolve.LinkSource. With no project id no store is
// read (no link can name this repo; init must not depend on a store).
func (g *GitLinks) Links(ctx context.Context, projectID string, remotes []string) (orgresolve.LinkResult, error) {
	res := orgresolve.LinkResult{Status: orgresolve.LinkStatusKnown}
	if g.Config == nil || len(g.Config.Orgs) == 0 {
		res.Detail = "no org entries configured"
		return res, nil
	}
	if projectID == "" {
		res.Detail = "no project id here yet, so no store link can name this repo"
		return res, nil
	}
	want := setOf(remotes)
	var unknown, stale, forks, unrelated, otherOrgs []string
	scanned := 0
	// every org's due stores at the same time, not one org after another
	// (devco:D-1026): each entryLinks below takes its stores' results
	var orgs []*orgconf.Org
	for i := range g.Config.Orgs {
		if g.Only == nil || g.Only[g.Config.Orgs[i].Name] {
			orgs = append(orgs, &g.Config.Orgs[i])
		}
	}
	fctx := ctx
	if g.Always {
		fctx = withHeadRefresh(ctx) // lore sync asks each store for its HEAD branch again, as entryLinks does
	}
	ctx = context.WithValue(ctx, prefetchedKey{}, g.fetchDue(fctx, orgs))
	for i := range g.Config.Orgs {
		o := &g.Config.Orgs[i]
		if g.Only != nil && !g.Only[o.Name] {
			otherOrgs = append(otherOrgs, o.Name)
			continue
		}
		links, status, detail, err := g.entryLinks(ctx, o, false)
		if err != nil {
			return orgresolve.LinkResult{}, err
		}
		switch status {
		case entryStaging:
			continue
		case entryUnknown:
			// Fail closed only where the unread store could decide this
			// repo: an entry whose own:/repos: patterns match a remote.
			if entryMatches(o, remotes) {
				unknown = append(unknown, detail)
			} else {
				unrelated = append(unrelated, detail)
			}
			// links of a copy too old to trust are kept apart (Old): reads
			// attach their folders, nothing decides by them
			for _, l := range links {
				if l.ProjectID == projectID && wantRemote(want, l.Remote) {
					res.Old = append(res.Old, orgresolve.Link{Entry: l.entry, StoreURL: l.storeNorm, Folder: l.folder, Remote: l.Remote, Own: l.Own, Default: l.Default})
				}
			}
			continue
		case entryStale:
			stale = append(stale, detail)
		case entryFresh:
		}
		scanned++
		for _, l := range links {
			if l.ProjectID != projectID {
				continue
			}
			if !wantRemote(want, l.Remote) {
				forks = append(forks, l.Remote)
				res.Moved = append(res.Moved, orgresolve.Link{Entry: l.entry, StoreURL: l.storeNorm, Folder: l.folder, Remote: l.Remote, Own: l.Own, Default: l.Default})
				continue
			}
			res.Links = append(res.Links, orgresolve.Link{Entry: l.entry, StoreURL: l.storeNorm, Folder: l.folder, Remote: l.Remote, Own: l.Own, Default: l.Default})
		}
	}
	var details []string
	if len(unknown) > 0 {
		res.Status = orgresolve.LinkStatusUnknown
		details = append(details, "cannot read "+strings.Join(unknown, "; "))
	} else {
		suffix := "ies"
		if scanned == 1 {
			suffix = "y"
		}
		details = append(details, fmt.Sprintf("%d link(s) for this repo (stores of %d entr%s scanned)", len(res.Links), scanned, suffix))
	}
	if len(unrelated) > 0 {
		details = append(details, "not read (no pattern matches this repo): "+strings.Join(unrelated, "; "))
	}
	if len(otherOrgs) > 0 {
		details = append(details, "not read (another org's stores; this repo names its org): "+strings.Join(otherOrgs, ", "))
	}
	if len(stale) > 0 {
		details = append(details, "offline, using the local copy for "+strings.Join(stale, "; "))
	}
	if len(forks) > 0 && len(res.Links) == 0 {
		details = append(details, fmt.Sprintf("this repo shares a project id with %s; %s", strings.Join(sortedUnique(forks), ", "), forkHint))
	}
	res.Detail = strings.Join(details, "; ")
	return res, nil
}

// EntryLinks implements LinkSource: one entry's links, staging included.
func (g *GitLinks) EntryLinks(ctx context.Context, o *orgconf.Org, projectID string, remotes []string) ([]orgresolve.Link, bool, error) {
	links, status, _, err := g.entryLinks(ctx, o, true)
	if err != nil {
		return nil, false, err
	}
	if status == entryUnknown {
		return nil, false, nil
	}
	want := setOf(remotes)
	var out []orgresolve.Link
	for _, l := range links {
		if l.ProjectID == projectID && projectID != "" && wantRemote(want, l.Remote) {
			out = append(out, orgresolve.Link{Entry: l.entry, StoreURL: l.storeNorm, Folder: l.folder, Remote: l.Remote, Own: l.Own, Default: l.Default})
		}
	}
	return out, true, nil
}

// DefaultBranch implements LinkSource: the branch the store's HEAD named
// at the last fetch, orgconf.DefaultStoreBranch when unknown.
func (g *GitLinks) DefaultBranch(_ context.Context, storeNorm string) string {
	dir, _ := cloneDir(g.Home, storeNorm)
	b, err := os.ReadFile(filepath.Join(gitbackend.StateDirOf(dir), defaultBranchFile))
	if err != nil {
		return orgconf.DefaultStoreBranch
	}
	if n := strings.TrimSpace(string(b)); n != "" {
		return n
	}
	return orgconf.DefaultStoreBranch
}

// cloneDirOf is the clone folder of a normalised store url.
func (g *GitLinks) cloneDirOf(norm string) string {
	dir, _ := cloneDir(g.Home, norm)
	return dir
}

// entryLinks reads every link of one entry's stores.
func (g *GitLinks) entryLinks(ctx context.Context, o *orgconf.Org, allowStaging bool) ([]indexedLink, entryStatus, string, error) {
	maxAge := g.Config.LinkIndexMaxAge()
	ttl := g.Config.FetchTTL(o)
	status := entryFresh
	var out []indexedLink
	var offlineWhy []string
	var stale []indexedLink // links of copies older than maxAge (offline)
	if g.Always {
		ctx = withHeadRefresh(ctx) // lore sync asks each store for its HEAD branch again
	}
	fetched := g.prefetch(ctx, o)
	for _, sc := range o.Stores {
		if sc.AdminsOnly() {
			// D-442: no repo links to an admins-only store, so the link
			// index (and every client-repo check built on it) never reads
			// it: an admin's guards are a member's guards, and an admins'
			// store that is offline never makes a repo "unknown".
			continue
		}
		norm, err := repourl.NormalizeStore(sc.URL)
		if err != nil {
			return nil, entryUnknown, "", fmt.Errorf("orgcfg: store url %q of %s: %w", sc.URL, o.Name, err)
		}
		dir, own := cloneDir(g.Home, norm)
		if berr := ensureClone(ctx, dir, sc.URL, own); berr != nil {
			return nil, entryUnknown, "", berr
		}
		if gitbackend.NoAccessSkips(dir) && !g.Always {
			continue // D-726: refused, and no copy here; skipped until the next lore sync (D-741)
		}
		last := lastFetch(dir)
		offline := false
		var ferr error
		if res, done := fetched[dir]; done {
			offline, ferr = res != nil, res
		} else if g.Always || last.IsZero() || g.now().Sub(last) >= ttl {
			if ferr = fetchLocked(ctx, g.Route, dir, g.now()); ferr != nil {
				offline = true
			} else {
				last = g.now()
			}
		}
		if k, refused := noAccessFault(ferr); refused {
			// D-726: the store refused this login (or is gone): not an
			// outage, so it never makes the entry unknown; it is skipped
			// for this person and named once by whoami and doctor
			markNoAccess(dir, k, g.now())
			if last.IsZero() {
				continue
			}
			// D-741: a copy fetched here before is read as it is (the
			// refusal may be this run's credentials, not the person's), as
			// fresh as its last fetch says, never as an outage
			offline = false
		}
		branch := sc.EffectiveBranch()
		if branch != g.DefaultBranch(ctx, norm) && !allowStaging {
			return nil, entryStaging, "", nil
		}
		tip, ok := branchTip(ctx, dir, branch)
		if offline {
			status = entryStale
			if !ok || last.IsZero() || g.now().Sub(last) > maxAge {
				age := "never fetched"
				if !last.IsZero() {
					age = "last fetched " + g.now().Sub(last).Round(time.Second).String() + " ago"
				}
				offlineWhy = append(offlineWhy, fmt.Sprintf("%s (%s)", norm, age))
				if ok {
					old, _ := readLinks(ctx, dir, tip) // best effort: only reads attach by them (LinkResult.Old)
					for _, l := range old {
						l.entry, l.storeNorm = o.Name, norm
						l.Own = orgresolve.EffectiveOwn(o, l.Remote, l.Own) // D-428: a repos: client entry wins
						stale = append(stale, l)
					}
				}
				continue
			}
		}
		if !ok {
			continue // the store has no such branch yet: no links
		}
		// F12/E10: a code repo listed as a store is refused, never read.
		if cerr := checkStoreTree(ctx, dir, tip, norm, branch); cerr != nil {
			return nil, entryUnknown, "", cerr
		}
		links, err := readLinks(ctx, dir, tip)
		if err != nil {
			return nil, entryUnknown, o.Name + ": " + err.Error(), nil
		}
		for _, l := range links {
			l.entry, l.storeNorm = o.Name, norm
			// D-428: a link recorded as own for a repo the entry lists under
			// repos: is read as a client link, so every reader (F10, the
			// write target, relink, unlink) sees one classification.
			l.Own = orgresolve.EffectiveOwn(o, l.Remote, l.Own)
			out = append(out, l)
		}
	}
	if len(offlineWhy) > 0 {
		// the copies too old to trust, with every other store's links: an
		// unknown entry's links are only ever kept as LinkResult.Old
		return append(out, stale...), entryUnknown, o.Name + ": " + strings.Join(offlineWhy, ", "), nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].folder != out[j].folder {
			return out[i].folder < out[j].folder
		}
		return out[i].ProjectID+out[i].Remote < out[j].ProjectID+out[j].Remote
	})
	return out, status, o.Name, nil
}

// prefetchedKey is the context key of the stores Links fetched for every
// org at once (fetchDue): map clone folder → fetch result.
type prefetchedKey struct{}

// prefetch is the fetch results entryLinks takes for o's stores: the ones
// Links already fetched for every org at once when it did, else o's due
// stores fetched now (fetchDue). A store in neither is fetched by
// entryLinks itself when due.
func (g *GitLinks) prefetch(ctx context.Context, o *orgconf.Org) map[string]error {
	if pre, ok := ctx.Value(prefetchedKey{}).(map[string]error); ok {
		return pre
	}
	return g.fetchDue(ctx, []*orgconf.Org{o})
}

// fetchDue fetches every store of orgs that is due (g.Always, never
// fetched, or its last fetch older than its org's fetch TTL) at the same
// time, one goroutine per clone, and returns each fetched clone's result
// by folder (nil: fetched). Owner item M-4: a read on a Mac fetched three
// org stores one after another, ~3 s each over ssh; devco:D-1026: the
// stores of two orgs still went one org after the other. A store whose
// url does not parse or whose clone cannot be made is left to entryLinks,
// which reports it.
func (g *GitLinks) fetchDue(ctx context.Context, orgs []*orgconf.Org) map[string]error {
	type job struct{ dir, url, norm string }
	var jobs []job
	seen := map[string]bool{}
	for _, o := range orgs {
		ttl := g.Config.FetchTTL(o)
		for _, sc := range o.Stores {
			norm, err := repourl.NormalizeStore(sc.URL)
			if err != nil || sc.AdminsOnly() {
				continue // entryLinks never reads an admins-only store (D-442)
			}
			dir, _ := cloneDir(g.Home, norm)
			if seen[dir] {
				continue
			}
			seen[dir] = true
			if gitbackend.NoAccessSkips(dir) && !g.Always {
				continue // D-726: skipped until the next lore sync (D-741: only with no copy here)
			}
			last := lastFetch(dir)
			if g.Always || last.IsZero() || g.now().Sub(last) >= ttl {
				jobs = append(jobs, job{dir: dir, url: sc.URL, norm: norm})
			}
		}
	}
	out := map[string]error{}
	if len(jobs) < 2 {
		return out // one store: entryLinks fetches it as before
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dir, own := cloneDir(g.Home, j.norm)
			if err := ensureClone(ctx, dir, j.url, own); err != nil {
				return // entryLinks makes the clone again and reports the error
			}
			err := fetchLocked(ctx, g.Route, dir, g.now())
			mu.Lock()
			out[j.dir] = err
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// branchTip is the commit to read a store branch at: the local branch when
// it is ahead of (or equal to) the tracking ref (it holds writes not pushed
// yet), else the tracking ref. ok is false when the branch exists nowhere.
func branchTip(ctx context.Context, dir, branch string) (string, bool) {
	// The tip is a function of what the two refs hold (gitmemo.go).
	fp := refFingerprint(dir, branch)
	if t, ok := memoTip(dir, branch, fp); ok {
		return t.tip, t.ok
	}
	tip, ok := resolveBranchTip(ctx, dir, branch)
	keepTip(dir, branch, fp, tip, ok)
	return tip, ok
}

// resolveBranchTip is branchTip asking git.
func resolveBranchTip(ctx context.Context, dir, branch string) (string, bool) {
	resolve := func(ref string) string {
		out, err := gitRun(ctx, "-C", dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	local := resolve("refs/heads/" + branch)
	track := resolve("refs/remotes/" + remoteName + "/" + branch)
	switch {
	case local == "" && track == "":
		return "", false
	case track == "", local == track:
		return local, true
	case local == "":
		return track, true
	}
	if _, err := gitRun(ctx, "-C", dir, "merge-base", "--is-ancestor", local, track); err == nil {
		return track, true // local is behind
	}
	return local, true
}

// readLinks lists every store folder's links at rev. A folder whose
// _meta.json cannot be parsed is skipped (a broken meta must not hide
// every other link; doctor reports it).
func readLinks(ctx context.Context, dir, rev string) ([]indexedLink, error) {
	metas, err := metaFiles(ctx, dir, rev)
	if err != nil {
		return nil, err
	}
	var links []indexedLink
	for _, p := range metas {
		folder, _ := metaFolder(p)
		raw, rerr := gitRun(ctx, "-C", dir, "cat-file", "blob", rev+":"+p)
		if rerr != nil {
			return nil, rerr
		}
		var m storeMeta
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		for _, l := range m.Links {
			if l.ProjectID == "" || l.Remote == "" {
				continue
			}
			links = append(links, indexedLink{folder: folder, linkRecord: l})
		}
	}
	return links, nil
}

// metaFiles lists the project markers at rev (every path metaFolder
// takes), in the order git lists them: the top-level entries first, then
// only the marker paths those can hold. Why (bug hunt H-277,
// devco:D-1015): it listed every file of the store recursively to find a
// few markers, a string per row file (1.2M at 1M commits) held by every
// org links read, and the git memo kept the listing for the run.
func metaFiles(ctx context.Context, dir, rev string) ([]string, error) {
	top, err := gitRun(ctx, "-C", dir, "ls-tree", "-z", "--name-only", rev)
	if err != nil {
		return nil, err
	}
	var want []string
	try := func(p string) {
		if _, ok := metaFolder(p); ok {
			want = append(want, p)
		}
	}
	try(rowfs.MetaFileName)
	try(rowfs.LoreDir + "/" + rowfs.DataDir + "/" + rowfs.MetaFileName)
	eachZ(top, func(name string) {
		try(name + "/" + rowfs.MetaFileName)
		try(name + "/" + rowfs.LoreDir + "/" + rowfs.DataDir + "/" + rowfs.MetaFileName)
	})
	if len(want) == 0 {
		return nil, nil
	}
	out, err := gitRun(ctx, append([]string{"-C", dir, "ls-tree", "-r", "-z", "--name-only", rev, "--"}, want...)...)
	if err != nil {
		return nil, err
	}
	var metas []string
	eachZ(out, func(p string) {
		if _, ok := metaFolder(p); ok {
			metas = append(metas, p)
		}
	})
	return metas, nil
}

// metaFolder reports whether p is a top-level folder's project marker
// (<folder>/_meta.json, or <folder>/.lore/data/_meta.json in a store not
// moved yet) and of which folder ("." for the store root).
func metaFolder(p string) (string, bool) {
	folder, name, ok := rowfs.LayoutStore.SplitFolderFile(p)
	if !ok {
		folder, name, ok = rowfs.LayoutRepo.SplitFolderFile(p)
	}
	if !ok || name != rowfs.MetaFileName || strings.Contains(folder, "/") {
		return "", false
	}
	return folder, true
}

// entryMatches reports whether any own:/repos: pattern of o matches a
// remote. A bad pattern counts as a match (fail closed).
func entryMatches(o *orgconf.Org, remotes []string) bool {
	for _, raw := range append(append([]string{}, o.Own...), o.Repos...) {
		p, err := repourl.Compile(raw)
		if err != nil {
			return true
		}
		for _, r := range remotes {
			if p.Match(r) {
				return true
			}
		}
	}
	return false
}

// setOf is a membership set of xs.
func setOf(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// sortedUnique returns xs sorted, once each.
func sortedUnique(xs []string) []string {
	m := setOf(xs)
	out := make([]string, 0, len(m))
	for x := range m {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

// orgDefaultsFile is the team's org file in the org folder (v0.1.17
// lore-org.yaml); its defaults.client_repos is read until `lore migrate
// run` moves it into a routing.client_repos policy row (CFG8).
const orgDefaultsFile = OrgFile

// OrgFile is the org file's name in an org folder (org/lore-org.yaml; at
// the store's top when org_folder is "."; D-e), exported so the onboarding
// probes (store marker, org.free; ONBOARDING_DESIGN 4.6) name it without
// another copy.
const OrgFile = rowfs.OrgFileName

// OrgFileKept is the untouched copy of lore-org.yaml that `lore migrate`
// keeps beside the rewritten file when it moves defaults.client_repos into
// a policy row (wire step org_defaults_to_policy, owner rule Q-MODEB: never
// delete user config). Lore commits it into the org store, so the store
// check (storeAllowed) must count it as a lore file.
const OrgFileKept = rowfs.OrgFileKeptName

// orgDefaultsDoc is the part of lore-org.yaml read here.
type orgDefaultsDoc struct {
	// Format is lore_org: a newer format is refused (orgfileformat.go)
	// rather than its defaults guessed at.
	Format   *int `yaml:"lore_org,omitempty"`
	Defaults struct {
		ClientRepos map[string]string `yaml:"client_repos,omitempty"`
	} `yaml:"defaults,omitempty"`
}

// ClientRepoDefaults implements LinkSource: the defaults.client_repos of
// o's lore-org.yaml in its org folder store; nil when the entry has no org
// folder or the file is absent or migrated. An unreachable store with a
// local copy reads that copy; a file that cannot be parsed is an error.
func (g *GitLinks) ClientRepoDefaults(ctx context.Context, o *orgconf.Org) (map[string]string, error) {
	sc := o.OrgFolderStore()
	if sc == nil {
		return nil, nil
	}
	norm, err := repourl.NormalizeStore(sc.URL)
	if err != nil {
		return nil, fmt.Errorf("orgcfg: store url %q of %s: %w", sc.URL, o.Name, err)
	}
	dir, own := cloneDir(g.Home, norm)
	if berr := ensureClone(ctx, dir, sc.URL, own); berr != nil {
		return nil, berr
	}
	if last := lastFetch(dir); last.IsZero() || g.now().Sub(last) >= g.Config.FetchTTL(o) {
		_ = fetchLocked(ctx, g.Route, dir, g.now()) // offline reads the local copy
	}
	tip, ok := branchTip(ctx, dir, sc.EffectiveBranch())
	if !ok {
		return nil, nil
	}
	var raw, at string
	for _, p := range orgFilePaths(sc.OrgFolder) {
		if raw, err = gitRun(ctx, "-C", dir, "cat-file", "blob", tip+":"+p); err == nil {
			at = p
			break
		}
	}
	if at == "" {
		return nil, nil // no lore-org.yaml (or migrated): no team defaults
	}
	var doc orgDefaultsDoc
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("orgcfg: %s of %s: %w", orgDefaultsFile, o.Name, err)
	}
	if err := CheckOrgFileFormat(doc.Format); err != nil {
		return nil, orgFileError(at, norm, err)
	}
	return doc.Defaults.ClientRepos, nil
}

// CloneLinkRemotes are the remotes every link in the store clone at dir
// names (normalised, sorted, once each), read at the tip of the store's
// default branch (the local branch when it is ahead) with no network:
// `lore fix --all` finds the local checkouts of linked repos by them
// (owner item 26). A clone with no such branch has none.
func CloneLinkRemotes(ctx context.Context, dir string) ([]string, error) {
	branch := orgconf.DefaultStoreBranch
	if b, err := os.ReadFile(filepath.Join(gitbackend.StateDirOf(dir), defaultBranchFile)); err == nil && strings.TrimSpace(string(b)) != "" {
		branch = strings.TrimSpace(string(b))
	}
	tip, ok := branchTip(ctx, dir, branch)
	if !ok {
		return nil, nil
	}
	links, err := readLinks(ctx, dir, tip)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range links {
		if n, nerr := repourl.Normalize(l.Remote); nerr == nil {
			out = append(out, CanonicalRemote(n)) // an alias spelling recorded before D-531
		}
	}
	return sortedUnique(out), nil
}

// wantRemote reports a link row's remote among the repo's remotes, also
// when the row recorded the remote under an ssh alias spelling (written
// before D-531, when Remotes kept the alias host): Remotes now resolves
// the alias, and the row must still name this repo, not a fork of it.
func wantRemote(want map[string]bool, remote string) bool {
	return want[remote] || want[CanonicalRemote(remote)]
}
