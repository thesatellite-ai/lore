// links.go — the store link source (F11): which org store folders link
// this repo, read from each store folder's _meta.json `links` list in a
// bare clone of the store.
//
// The clones are v0.1.17's (<home>/clones-v017/<host/path>.git, state in
// <clone>/lore/: fetch-stamp, default-branch), so both binaries share one
// copy of each store: a link `lore org link` just wrote into the clone is
// seen at once, even offline.
//
//	Links ─▶ per non-staging entry, per store:
//	           open (git init --bare + origin) ─▶ fetch unless younger than
//	           live.fetch_ttl ─▶ tip of the branch (local ahead of tracking
//	           wins) ─▶ ls-tree, read every <folder>/.lore/data/_meta.json
//	         unreachable and older than link_index.max_age ─▶ unknown (fail
//	         closed where the entry's patterns match this repo)

package orgcfg

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

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// Clone layout shared with v0.1.17 (saas/pkg/aicoder/stores clone.go).
const (
	// ClonesDir holds the bare store clones under the lore home.
	ClonesDir = "clones-v017"
	// remoteName is the clone's remote.
	remoteName = "origin"
	// bareSuffix ends every clone folder.
	bareSuffix = ".git"
	// cloneStateDir holds lore's state inside a clone.
	cloneStateDir = "lore"
	// fetchStampFile records the last successful fetch (RFC 3339).
	fetchStampFile = "fetch-stamp"
	// defaultBranchFile records the store's HEAD branch.
	defaultBranchFile = "default-branch"
	// cloneDirMode / cloneFileMode: machine state for the user only.
	cloneDirMode  = 0o700
	cloneFileMode = 0o600
	// metaPathSuffix ends a store folder's project marker path.
	metaPathSuffix = ".lore/data/_meta.json"
	// headRefPrefix starts the symref line of `ls-remote --symref`.
	headRefPrefix = "ref: refs/heads/"
	// forkHint is F11's warning for a project id linked under another remote.
	forkHint = "run `lore project reid` if it is a different project"
)

// fetchRefspecs are v0.1.17's refspecs, so `git fetch` in the clone gives
// the same layout.
var fetchRefspecs = []string{
	"+refs/heads/*:refs/remotes/" + remoteName + "/*",
	"+refs/lore/*:refs/lore-remote/" + remoteName + "/*",
}

// GitLinks reads store links from bare clones under Home. The zero value
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
}

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
	var unknown, stale, forks, unrelated []string
	scanned := 0
	for i := range g.Config.Orgs {
		o := &g.Config.Orgs[i]
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
			if !want[l.Remote] {
				forks = append(forks, l.Remote)
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
		if l.ProjectID == projectID && projectID != "" && want[l.Remote] {
			out = append(out, orgresolve.Link{Entry: l.entry, StoreURL: l.storeNorm, Folder: l.folder, Remote: l.Remote, Own: l.Own, Default: l.Default})
		}
	}
	return out, true, nil
}

// DefaultBranch implements LinkSource: the branch the store's HEAD named
// at the last fetch, orgconf.DefaultStoreBranch when unknown.
func (g *GitLinks) DefaultBranch(_ context.Context, storeNorm string) string {
	b, err := os.ReadFile(filepath.Join(g.cloneDirOf(storeNorm), cloneStateDir, defaultBranchFile))
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
	return filepath.Join(g.Home, ClonesDir, filepath.FromSlash(norm)+bareSuffix)
}

// entryLinks reads every link of one entry's stores.
func (g *GitLinks) entryLinks(ctx context.Context, o *orgconf.Org, allowStaging bool) ([]indexedLink, entryStatus, string, error) {
	maxAge := g.Config.LinkIndexMaxAge()
	ttl := g.Config.FetchTTL(o)
	status := entryFresh
	var out []indexedLink
	var offlineWhy []string
	for _, sc := range o.Stores {
		norm, err := repourl.Normalize(sc.URL)
		if err != nil {
			return nil, entryUnknown, "", fmt.Errorf("orgcfg: store url %q of %s: %w", sc.URL, o.Name, err)
		}
		dir := g.cloneDirOf(norm)
		if berr := ensureBare(ctx, dir, sc.URL); berr != nil {
			return nil, entryUnknown, "", berr
		}
		last := lastFetch(dir)
		offline := false
		if g.Always || last.IsZero() || g.now().Sub(last) >= ttl {
			if ferr := fetch(ctx, dir); ferr != nil {
				offline = true
			} else {
				last = g.now()
				recordFetch(ctx, dir, last)
			}
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
			out = append(out, l)
		}
	}
	if len(offlineWhy) > 0 {
		return nil, entryUnknown, o.Name + ": " + strings.Join(offlineWhy, ", "), nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].folder != out[j].folder {
			return out[i].folder < out[j].folder
		}
		return out[i].ProjectID+out[i].Remote < out[j].ProjectID+out[j].Remote
	})
	return out, status, o.Name, nil
}

// ensureBare makes an empty bare clone with origin and lore's refspecs
// when dir is not one yet. Two processes racing here both succeed: a
// second `remote add` finding origin is fine.
func ensureBare(ctx context.Context, dir, url string) error {
	if fi, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil && fi.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dir, cloneStateDir), cloneDirMode); err != nil {
		return fmt.Errorf("orgcfg: create clone dir: %w", err)
	}
	if _, err := gitRun(ctx, "-C", dir, "init", "--quiet", "--bare", "."); err != nil {
		return err
	}
	if _, err := gitRun(ctx, "-C", dir, "remote", "add", remoteName, url); err != nil {
		if cur, gerr := gitRun(ctx, "-C", dir, "config", "--get", "remote."+remoteName+".url"); gerr != nil || strings.TrimSpace(cur) == "" {
			return err
		}
	}
	// Replace git's default fetch refspec with lore's two. Exit 5 means
	// the key was absent, which is fine.
	_, _ = gitRun(ctx, "-C", dir, "config", "--unset-all", "remote."+remoteName+".fetch") // absent key is the expected case
	for _, rs := range fetchRefspecs {
		if _, err := gitRun(ctx, "-C", dir, "config", "--add", "remote."+remoteName+".fetch", rs); err != nil {
			return err
		}
	}
	return nil
}

// fetch updates the clone's tracking refs; an error means unreachable.
func fetch(ctx context.Context, dir string) error {
	args := append([]string{"-C", dir, "fetch", "--quiet", "--prune", remoteName}, fetchRefspecs...)
	_, err := gitRun(ctx, args...)
	return err
}

// recordFetch writes the fetch stamp and the store's HEAD branch (best
// effort: an empty store has no HEAD).
func recordFetch(ctx context.Context, dir string, at time.Time) {
	writeState(dir, fetchStampFile, at.UTC().Format(time.RFC3339Nano))
	out, err := gitRun(ctx, "-C", dir, "ls-remote", "--symref", "--", remoteName, "HEAD")
	if err != nil {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, headRefPrefix); ok {
			if name, _, _ := strings.Cut(rest, "\t"); name != "" {
				writeState(dir, defaultBranchFile, name)
			}
			return
		}
	}
}

// writeState writes one state file atomically (a unique temp file per
// writer: several processes share a clone). Best effort: a failed stamp
// only means the next command fetches again.
func writeState(dir, name, value string) {
	sdir := filepath.Join(dir, cloneStateDir)
	if os.MkdirAll(sdir, cloneDirMode) != nil {
		return
	}
	f, err := os.CreateTemp(sdir, name+".*.tmp")
	if err != nil {
		return
	}
	tmp := f.Name()
	_, werr := f.WriteString(value)
	cerr := f.Close()
	if errors.Join(werr, cerr, os.Chmod(tmp, cloneFileMode)) != nil || os.Rename(tmp, filepath.Join(sdir, name)) != nil {
		_ = os.Remove(tmp) // best effort cleanup of a failed stamp
	}
}

// lastFetch reads the fetch stamp (zero when never fetched).
func lastFetch(dir string) time.Time {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, cloneStateDir, fetchStampFile)))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// branchTip is the commit to read a store branch at: the local branch when
// it is ahead of (or equal to) the tracking ref (it holds writes not pushed
// yet), else the tracking ref. ok is false when the branch exists nowhere.
func branchTip(ctx context.Context, dir, branch string) (string, bool) {
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
	out, err := gitRun(ctx, "-C", dir, "ls-tree", "-r", "-z", "--name-only", rev)
	if err != nil {
		return nil, err
	}
	var links []indexedLink
	for _, p := range strings.Split(out, "\x00") {
		folder, ok := metaFolder(p)
		if !ok {
			continue
		}
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

// metaFolder reports whether p is a project marker and of which folder
// ("." for the store root).
func metaFolder(p string) (string, bool) {
	if p == metaPathSuffix {
		return orgconf.OrgFolderRoot, true
	}
	folder, ok := strings.CutSuffix(p, "/"+metaPathSuffix)
	if !ok || folder == "" || strings.Contains(folder, "/") {
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
const orgDefaultsFile = "lore-org.yaml"

// orgDefaultsDoc is the part of lore-org.yaml read here.
type orgDefaultsDoc struct {
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
	norm, err := repourl.Normalize(sc.URL)
	if err != nil {
		return nil, fmt.Errorf("orgcfg: store url %q of %s: %w", sc.URL, o.Name, err)
	}
	dir := g.cloneDirOf(norm)
	if berr := ensureBare(ctx, dir, sc.URL); berr != nil {
		return nil, berr
	}
	if last := lastFetch(dir); last.IsZero() || g.now().Sub(last) >= g.Config.FetchTTL(o) {
		if fetch(ctx, dir) == nil {
			recordFetch(ctx, dir, g.now())
		}
	}
	tip, ok := branchTip(ctx, dir, sc.EffectiveBranch())
	if !ok {
		return nil, nil
	}
	p := ".lore/" + orgDefaultsFile
	if sc.OrgFolder != orgconf.OrgFolderRoot {
		p = sc.OrgFolder + "/" + p
	}
	raw, err := gitRun(ctx, "-C", dir, "cat-file", "blob", tip+":"+p)
	if err != nil {
		return nil, nil // no lore-org.yaml (or migrated): no team defaults
	}
	var doc orgDefaultsDoc
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("orgcfg: %s of %s: %w", orgDefaultsFile, o.Name, err)
	}
	return doc.Defaults.ClientRepos, nil
}
