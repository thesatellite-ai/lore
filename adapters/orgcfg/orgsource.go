package orgcfg

// orgsource.go — lore-org.yaml as the source of an org entry's shared
// fields (SOURCE_OF_TRUTH row 9) and the store side of a store move
// (row 7):
//
//	LocalSuggestion ─▶ the shared bare clone as fetched last (no network)
//	                ─▶ suggestionAt: the file, its git blob id (the
//	                   version a config copy is stamped with) and the
//	                   store's sto_ id from its marker
//	MoveOrgFileStore ─▶ clone + fetch the store at its NEW url ─▶ commit
//	                   lore-org.yaml with every stores[].url naming an old
//	                   address rewritten to the new one ─▶ push (autopush
//	                   rules as for a link) ─▶ the new blob id

import (
	"context"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
)

// orgMoveMessage is the commit subject of MoveOrgFileStore.
const orgMoveMessage = "lore: org %s store moved to %s"

// LocalSuggestion is ReadSuggestion from this machine's copy only: the
// store's clone as last fetched, no network. A store never
// cloned here is ErrStoreOffline.
func (g *GitLinks) LocalSuggestion(ctx context.Context, url, branch string) (Suggestion, error) {
	norm, err := repourl.NormalizeStore(url)
	if err != nil {
		return Suggestion{}, fmt.Errorf("orgcfg: store url %q: %w", url, err)
	}
	dir := g.cloneDirOf(norm)
	if lastFetch(dir).IsZero() {
		return Suggestion{}, fmt.Errorf("%w: %s (no local copy yet)", ErrStoreOffline, norm)
	}
	return g.suggestionAt(ctx, dir, norm, branch)
}

// suggestionAt reads the suggestion of the clone dir at branch ("" = the
// default branch), with its blob id and the marker's store id.
func (g *GitLinks) suggestionAt(ctx context.Context, dir, norm, branch string) (Suggestion, error) {
	if branch == "" {
		branch = g.DefaultBranch(ctx, norm)
	}
	tip, ok := orgFileTip(ctx, dir, branch)
	if !ok {
		if cloneHasNoBranches(ctx, dir) {
			// plainfiles:4: `lore org create` on a brand-new repo said "has
			// no branch main" with a hint about the repo's other branches.
			return Suggestion{}, fmt.Errorf("%w (%w): %s has no commits yet", ErrStoreEmpty, ErrNoStoreBranch, norm)
		}
		return Suggestion{}, fmt.Errorf("%w: %s has no branch %s", ErrNoStoreBranch, norm, branch)
	}
	if err := checkStoreTree(ctx, dir, tip, norm, branch); err != nil {
		return Suggestion{}, err
	}
	candidates := []string{fallbackOrgFolder, orgconf.OrgFolderRoot}
	storeID := ""
	if raw, merr := gitRun(ctx, "-C", dir, "cat-file", "blob", tip+":"+storeMarkerFile); merr == nil {
		var mk struct {
			ID        string `yaml:"id"`
			OrgFolder string `yaml:"org_folder"`
		}
		if yaml.Unmarshal([]byte(raw), &mk) == nil {
			storeID = mk.ID
			if mk.OrgFolder != "" {
				candidates = append([]string{mk.OrgFolder}, candidates...)
			}
		}
	}
	for _, cand := range candidates {
		for _, p := range orgFilePaths(cand) {
			sg, found, serr := readSuggestionFile(ctx, dir, tip, p, cand, norm, branch, storeID)
			if serr != nil || found {
				return sg, serr
			}
		}
	}
	return Suggestion{}, fmt.Errorf("%w: %s@%s", ErrNoOrgFile, norm, branch)
}

// readSuggestionFile reads the lore-org.yaml at p in tip (folder its org
// folder); found is false when tip has no file there.
func readSuggestionFile(ctx context.Context, dir, tip, p, folder, norm, branch, storeID string) (Suggestion, bool, error) {
	raw, rerr := gitRun(ctx, "-C", dir, "cat-file", "blob", tip+":"+p)
	if rerr != nil {
		return Suggestion{}, false, nil // no lore-org.yaml here
	}
	doc, perr := parseOrgFileDoc(raw)
	if perr != nil {
		return Suggestion{}, false, fmt.Errorf("orgcfg: %s in %s: %w", p, norm, perr)
	}
	if serr := doc.checkShortNames(); serr != nil {
		return Suggestion{}, false, fmt.Errorf("orgcfg: %s in %s: %w", p, norm, serr)
	}
	blob, berr := gitRun(ctx, "-C", dir, "rev-parse", tip+":"+p)
	if berr != nil {
		return Suggestion{}, false, berr
	}
	return Suggestion{Doc: doc, Folder: folder, StoreURL: norm, Branch: branch, Version: strings.TrimSpace(blob), StoreID: storeID, Path: p}, true, nil
}

// Shared is the suggestion's shared fields as an org entry holds them.
func (s Suggestion) Shared() orgconf.Shared {
	sh := orgconf.Shared{Own: s.Doc.Own, Repos: s.Doc.Repos, Defaults: orgconf.OrgDefaults{ClientRepos: s.Doc.Defaults.ClientRepos}}
	for _, st := range s.Doc.Stores {
		sh.Stores = append(sh.Stores, orgconf.Store{URL: st.URL, Name: st.Name, Branch: st.Branch, OrgFolder: st.OrgFolder})
	}
	return sh
}

// Source is the stamp a config copy of this suggestion carries.
func (s Suggestion) Source() orgconf.OrgSource {
	return orgconf.OrgSource{Store: s.StoreID, Version: s.Version}
}

// OrgFileMove is one store move to record in lore-org.yaml.
type OrgFileMove struct {
	// URL is the org store's address now (where lore-org.yaml is written).
	URL string
	// Branch is the store branch ("" = its default branch).
	Branch string
	// Old are the addresses the store had; every stores[].url naming one
	// of them becomes New.
	Old []string
	New string
	// Org names the org in the commit message.
	Org string
	// Signature is the commit author.
	Signature gitref.Signature
}

// OrgFileMoveResult says what MoveOrgFileStore did.
type OrgFileMoveResult struct {
	// Changed is false when lore-org.yaml already named New (or named
	// none of Old): nothing was committed.
	Changed bool
	// Pushed / Offline / NoPush as for a link.
	Pushed, Offline, NoPush bool
	// Suggestion is the file as it now reads (its new blob id).
	Suggestion Suggestion
}

// MoveOrgFileStore rewrites the store urls in lore-org.yaml after a store
// moved (see the file comment). A store with no lore-org.yaml is
// ErrNoOrgFile.
func (g *GitLinks) MoveOrgFileStore(ctx context.Context, m OrgFileMove) (OrgFileMoveResult, error) {
	norm, dir, err := g.openClone(ctx, m.URL, false)
	if err != nil {
		return OrgFileMoveResult{}, err
	}
	cur, err := g.suggestionAt(ctx, dir, norm, m.Branch)
	if err != nil {
		return OrgFileMoveResult{}, err
	}
	branch := cur.Branch
	path := cur.Path
	res := OrgFileMoveResult{NoPush: !g.autopush(nil) || g.held(dir)}
	cr, err := commitStore(ctx, storeCommit{Dir: dir, Store: norm, Branch: branch, NoPush: res.NoPush, Route: g.Route,
		Message: fmt.Sprintf(orgMoveMessage, m.Org, m.New), Signature: m.Signature,
		Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
			old, found, rerr := repo.ReadFile(ctx, tip, path)
			if rerr != nil {
				return nil, rerr
			}
			if !found {
				return nil, fmt.Errorf("%w: %s@%s", ErrNoOrgFile, norm, branch)
			}
			doc, perr := parseOrgFileDoc(string(old))
			if perr != nil {
				return nil, fmt.Errorf("orgcfg: %s in %s: %w", path, norm, perr)
			}
			if !MoveStoreURLs(&doc, m.Old, m.New) {
				return nil, nil // already so
			}
			body, eerr := yaml.Marshal(doc)
			if eerr != nil {
				return nil, fmt.Errorf("orgcfg: encode %s: %w", orgDefaultsFile, eerr)
			}
			res.Changed = true
			return map[string][]byte{path: body}, nil
		}})
	res.Pushed, res.Offline = cr.Pushed, cr.Offline
	if err != nil {
		return res, err
	}
	if res.Suggestion, err = g.suggestionAt(ctx, dir, norm, branch); err != nil {
		return res, err
	}
	return res, nil
}

// MoveStoreURLs points every stores[].url of doc naming one of old at
// newURL; it reports whether anything changed.
func MoveStoreURLs(doc *OrgFileDoc, old []string, newURL string) bool {
	changed := false
	for i := range doc.Stores {
		u := doc.Stores[i].URL
		if u == newURL {
			continue
		}
		for _, o := range old {
			if storeaddr.Same(u, o) {
				doc.Stores[i].URL = newURL
				changed = true
				break
			}
		}
	}
	return changed
}

// orgFileTip is the commit the org file is read from: branchTip, except
// when this clone's branch and the fetched one have diverged and the local
// side changed neither an org file nor the store marker since they split.
// Then the fetched tip is read: a store clone holds local commits until
// lore sync (C5: notes, folders), and those must not hide a lore-org.yaml
// an admin pushed since (its own:/repos: patterns would be copied back to
// the old ones). A local change to the org file itself still wins, as
// before.
func orgFileTip(ctx context.Context, dir, branch string) (string, bool) {
	tip, ok := branchTip(ctx, dir, branch)
	if !ok {
		return tip, ok
	}
	track, err := gitRun(ctx, "-C", dir, "rev-parse", "--verify", "--quiet", trackingRef(branch)+"^{commit}")
	track = strings.TrimSpace(track)
	if err != nil || track == "" || track == tip {
		return tip, true
	}
	base, err := gitRun(ctx, "-C", dir, "merge-base", tip, track)
	base = strings.TrimSpace(base)
	if err != nil || base == "" {
		return tip, true
	}
	changed, err := gitRun(ctx, "-C", dir, "diff", "--name-only", base, tip)
	if err != nil {
		return tip, true
	}
	for _, f := range strings.Split(strings.TrimSpace(changed), "\n") {
		if f == storeMarkerFile || path.Base(f) == OrgFile {
			return tip, true
		}
	}
	return track, true
}
