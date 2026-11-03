package stores

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

	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/repourl"
)

// IndexedLink is one link found by the scan, with where it lives.
type IndexedLink struct {
	Entry    string `json:"entry"`
	StoreURL string `json:"store"` // normalised
	Branch   string `json:"branch"`
	Folder   string `json:"folder"`
	LinkRecord
}

// ScanLinks is the pure F11 scan of one store branch: metas maps each
// project folder to its parsed _meta.json. Every link becomes an
// IndexedLink of entry. Sorted for stable output.
func ScanLinks(entry, storeURL, branch string, metas map[string]StoreMeta) []IndexedLink {
	var out []IndexedLink
	for folder, m := range metas {
		for _, l := range m.Links {
			out = append(out, IndexedLink{Entry: entry, StoreURL: storeURL, Branch: branch, Folder: folder, LinkRecord: l})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Folder != b.Folder {
			return a.Folder < b.Folder
		}
		return a.key() < b.key()
	})
	return out
}

// ReadMetas reads every project folder's _meta.json at rev. A folder whose
// meta cannot be parsed is skipped and named in bad (doctor reports it; a
// broken meta must not hide every other link).
func (s *Store) ReadMetas(ctx context.Context, rev string) (metas map[string]StoreMeta, bad []string, err error) {
	paths, err := s.Paths(ctx, rev)
	if err != nil {
		return nil, nil, err
	}
	metas = map[string]StoreMeta{}
	for _, folder := range FoldersOf(paths) {
		b, ok, err := s.Repo.ReadFile(ctx, rev, MetaPath(folder))
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		m, err := ParseStoreMeta(b)
		if err != nil {
			bad = append(bad, folder)
			continue
		}
		metas[folder] = m
	}
	return metas, bad, nil
}

// linkIndexFile is the cached index of one org entry (F11):
// ~/.lore/cache/links-<entry>.json.
func linkIndexFile(l Layout, entry string) string {
	return l.CacheFile("links-" + entry + ".json")
}

// cachedIndex is the on-disk link index of one entry.
type cachedIndex struct {
	// Built is when the index was built from the stores.
	Built time.Time `json:"built"`
	// Commits maps "<store>@<branch>" to the store commit scanned.
	Commits map[string]string `json:"commits"`
	Links   []IndexedLink     `json:"links"`
}

func storeKey(norm, branch string) string { return norm + "@" + branch }

// LinkSource is the real orgresolve.LinkSource: it scans every store of
// every non-staging org entry (fetching each at most once per FetchTTL),
// caches the index per entry keyed by the store commits it was built from,
// and fails closed — LinkStatusUnknown — when a store cannot be read and
// no index younger than link_index.max_age exists (F10, F11).
type LinkSource struct {
	// Manager opens and fetches clones.
	Manager *Manager
	// Config is the loaded ~/.lore/config.yaml.
	Config *lorecfg.Config
	// MaxAge is link_index.max_age (0 = Config.LinkIndexMaxAge()).
	MaxAge time.Duration
	// ForceFetch fetches every store now, ignoring live.fetch_ttl: `lore
	// sync` always fetches (TEAM-025), so a link a teammate just pushed is
	// in the index after it, not a fetch_ttl later.
	ForceFetch bool
}

// compile-time check.
var _ orgresolve.LinkSource = (*LinkSource)(nil)

// forkHint is the F11 warning for a project id linked under another remote.
const forkHint = "run `lore project reid` if it is a different project"

// Links implements orgresolve.LinkSource. A store that is listed but is not
// a lore store is an error (*NotAStoreError): every command refuses (E10).
func (ls *LinkSource) Links(ctx context.Context, projectID string, remotes []string) (orgresolve.LinkResult, error) {
	res := orgresolve.LinkResult{Status: orgresolve.LinkStatusKnown}
	if ls.Config == nil || len(ls.Config.Orgs) == 0 {
		res.Detail = "no org entries configured"
		return res, nil
	}
	want := map[string]bool{}
	for _, r := range remotes {
		want[r] = true
	}
	var unknown []string
	var stale []string
	var forks []string
	var unrelated []string
	scanned := 0
	for i := range ls.Config.Orgs {
		o := &ls.Config.Orgs[i]
		idx, status, detail, err := ls.entryIndex(ctx, o, false)
		if err != nil {
			return orgresolve.LinkResult{}, err
		}
		switch status {
		case entryStaging:
			continue
		case entryUnknown:
			// Fail closed only where the unread store could decide this
			// repo: an entry whose own:/repos: patterns match a remote (F10:
			// "a step-5 match whose store cannot be read"). Another org's
			// unreachable store is reported, not fatal.
			if entryMatches(o, remotes) {
				unknown = append(unknown, detail)
			} else {
				unrelated = append(unrelated, detail)
			}
			continue
		case entryStale:
			stale = append(stale, detail)
		}
		scanned++
		for _, l := range idx.Links {
			if l.ProjectID != projectID || projectID == "" {
				continue
			}
			if !want[l.Remote] {
				forks = append(forks, l.Remote)
				continue
			}
			res.Links = append(res.Links, orgresolve.Link{Entry: l.Entry, StoreURL: l.StoreURL, Folder: l.Folder, Remote: l.Remote, Own: l.Own, Default: l.Default})
		}
	}
	var details []string
	if len(unknown) > 0 {
		res.Status = orgresolve.LinkStatusUnknown
		details = append(details, "cannot read "+strings.Join(unknown, "; "))
	} else {
		details = append(details, fmt.Sprintf("%d link(s) for this repo (stores of %d entr%s scanned)", len(res.Links), scanned, plural(scanned, "y", "ies")))
	}
	if len(unrelated) > 0 {
		details = append(details, "not read (no pattern matches this repo): "+strings.Join(unrelated, "; "))
	}
	if len(stale) > 0 {
		details = append(details, "offline, using the cached index for "+strings.Join(stale, "; "))
	}
	if len(forks) > 0 && len(res.Links) == 0 {
		details = append(details, fmt.Sprintf("this repo shares a project id with %s; %s", strings.Join(dedupeStrings(forks), ", "), forkHint))
	}
	res.Detail = strings.Join(details, "; ")
	return res, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func dedupeStrings(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// EntryLinks returns the links of ONE org entry for (projectID, remotes),
// staging entries included: F10 never matches a staging entry on its own,
// but once it is chosen on purpose (LORE_ORG, `lore org use`, `--to
// <staging entry>`) its own links — which exist per store branch (side
// effect 19, TEAM-011) — decide its linked project. ok is false when a
// store of the entry could not be read (fail closed upstream).
func (ls *LinkSource) EntryLinks(ctx context.Context, o *lorecfg.Org, projectID string, remotes []string) ([]orgresolve.Link, bool, error) {
	idx, status, _, err := ls.entryIndex(ctx, o, true)
	if err != nil {
		return nil, false, err
	}
	if status == entryUnknown {
		return nil, false, nil
	}
	want := map[string]bool{}
	for _, r := range remotes {
		want[r] = true
	}
	var out []orgresolve.Link
	for _, l := range idx.Links {
		if l.ProjectID == projectID && projectID != "" && want[l.Remote] {
			out = append(out, orgresolve.Link{Entry: l.Entry, StoreURL: l.StoreURL, Folder: l.Folder, Remote: l.Remote, Own: l.Own, Default: l.Default})
		}
	}
	return out, true, nil
}

// entryMatches reports whether any of the entry's own: or repos: patterns
// matches one of the repo's normalised remotes. A bad pattern counts as a
// match (fail closed): config validation reports it.
func entryMatches(o *lorecfg.Org, remotes []string) bool {
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

// entryStatus is how one entry's index was obtained.
type entryStatus int

const (
	entryFresh   entryStatus = iota // every store read now (or cached by commit)
	entryStale                      // a store offline; cached data younger than max age used
	entryUnknown                    // a store unreadable and nothing usable cached
	entryStaging                    // a staging entry: never scanned (TEAM-022)
)

// entryIndex returns the link index of one org entry.
func (ls *LinkSource) entryIndex(ctx context.Context, o *lorecfg.Org, allowStaging bool) (cachedIndex, entryStatus, string, error) {
	maxAge := ls.MaxAge
	if maxAge <= 0 {
		maxAge = ls.Config.LinkIndexMaxAge()
	}
	now := ls.Manager.now()
	cache, cacheOK := readIndex(ls.Manager.Layout, o.Name)
	type opened struct {
		st     *Store
		branch string
		tip    string
	}
	var stores []opened
	offline := false
	var offlineWhy []string
	for _, sc := range o.Stores {
		st, err := ls.Manager.Open(ctx, sc.URL)
		if err != nil {
			return cachedIndex{}, entryUnknown, "", err
		}
		fr, err := st.Fetch(ctx, ls.ForceFetch)
		if err != nil {
			return cachedIndex{}, entryUnknown, "", err
		}
		branch := sc.EffectiveBranch()
		if branch != st.DefaultBranch() && !allowStaging {
			return cachedIndex{}, entryStaging, "", nil
		}
		tip, ok, err := st.Tip(ctx, branch)
		if err != nil {
			return cachedIndex{}, entryUnknown, "", err
		}
		if fr.Offline {
			offline = true
			age := "never fetched"
			if !fr.At.IsZero() {
				age = "last fetched " + now.Sub(fr.At).Round(time.Second).String() + " ago"
			}
			if !ok || fr.At.IsZero() || now.Sub(fr.At) > maxAge {
				offlineWhy = append(offlineWhy, fmt.Sprintf("%s (%s)", st.Norm, age))
				continue
			}
		}
		if ok {
			if err := st.Check(ctx, branch); err != nil {
				return cachedIndex{}, entryUnknown, "", err
			}
		}
		stores = append(stores, opened{st: st, branch: branch, tip: tip})
	}
	if len(offlineWhy) > 0 {
		if cacheOK && now.Sub(cache.Built) <= maxAge {
			return cache, entryStale, o.Name, nil
		}
		return cachedIndex{}, entryUnknown, o.Name + ": " + strings.Join(offlineWhy, ", "), nil
	}
	commits := map[string]string{}
	for _, s := range stores {
		commits[storeKey(s.st.Norm, s.branch)] = s.tip
	}
	if cacheOK && sameCommits(cache.Commits, commits) {
		st := entryFresh
		if offline {
			st = entryStale
		}
		return cache, st, o.Name, nil
	}
	idx := cachedIndex{Built: now, Commits: commits}
	for _, s := range stores {
		if s.tip == "" {
			continue
		}
		metas, _, err := s.st.ReadMetas(ctx, s.tip)
		if err != nil {
			return cachedIndex{}, entryUnknown, "", err
		}
		idx.Links = append(idx.Links, ScanLinks(o.Name, s.st.Norm, s.branch, metas)...)
	}
	if err := writeIndex(ls.Manager.Layout, o.Name, idx); err != nil {
		return cachedIndex{}, entryUnknown, "", err
	}
	st := entryFresh
	if offline {
		st = entryStale
	}
	return idx, st, o.Name, nil
}

func sameCommits(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func readIndex(l Layout, entry string) (cachedIndex, bool) {
	b, err := os.ReadFile(linkIndexFile(l, entry))
	if err != nil {
		return cachedIndex{}, false
	}
	var idx cachedIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return cachedIndex{}, false // a corrupt cache is rebuilt, never trusted
	}
	return idx, true
}

func writeIndex(l Layout, entry string, idx cachedIndex) error {
	path := linkIndexFile(l, entry)
	if err := os.MkdirAll(filepath.Dir(path), stateDirMode); err != nil {
		return fmt.Errorf("stores: cache dir: %w", err)
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("stores: encode link index: %w", err)
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, b, stateFileMode); err != nil {
		return fmt.Errorf("stores: write link index: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("stores: write link index: %w", err)
	}
	return nil
}

// CachedLinks returns an entry's cached index without touching any store
// (`lore store show --links` offline, the CI-free leak check).
func CachedLinks(l Layout, entry string) ([]IndexedLink, time.Time, bool) {
	idx, ok := readIndex(l, entry)
	return idx.Links, idx.Built, ok
}

// StoreFor returns the configured store of an entry whose normalised URL
// is norm (nil when none). Config holds raw URLs; indexes hold normalised.
func StoreFor(o *lorecfg.Org, norm string) *lorecfg.Store {
	for i := range o.Stores {
		if n, err := repourl.Normalize(o.Stores[i].URL); err == nil && n == norm {
			return &o.Stores[i]
		}
	}
	return nil
}

// ErrFolderElsewhere: a folder name exists in another store of the same
// entry (folder names are unique across an entry's stores, F13).
var ErrFolderElsewhere = errors.New("stores: folder already exists in another store of this org")
