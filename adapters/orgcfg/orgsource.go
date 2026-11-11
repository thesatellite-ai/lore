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
// shared bare clone of url as last fetched, no network. A store never
// cloned here is ErrStoreOffline.
func (g *GitLinks) LocalSuggestion(ctx context.Context, url, branch string) (Suggestion, error) {
	norm, err := repourl.Normalize(url)
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
	tip, ok := branchTip(ctx, dir, branch)
	if !ok {
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
	for _, folder := range candidates {
		p := orgFilePath(folder)
		raw, rerr := gitRun(ctx, "-C", dir, "cat-file", "blob", tip+":"+p)
		if rerr != nil {
			continue // no lore-org.yaml in this folder
		}
		doc, perr := parseOrgFileDoc(raw)
		if perr != nil {
			return Suggestion{}, fmt.Errorf("orgcfg: %s in %s: %w", p, norm, perr)
		}
		blob, berr := gitRun(ctx, "-C", dir, "rev-parse", tip+":"+p)
		if berr != nil {
			return Suggestion{}, berr
		}
		return Suggestion{Doc: doc, Folder: folder, StoreURL: norm, Branch: branch, Version: strings.TrimSpace(blob), StoreID: storeID}, nil
	}
	return Suggestion{}, fmt.Errorf("%w: %s@%s", ErrNoOrgFile, norm, branch)
}

// Shared is the suggestion's shared fields as an org entry holds them.
func (s Suggestion) Shared() orgconf.Shared {
	sh := orgconf.Shared{Own: s.Doc.Own, Repos: s.Doc.Repos, Defaults: orgconf.OrgDefaults{ClientRepos: s.Doc.Defaults.ClientRepos}}
	for _, st := range s.Doc.Stores {
		sh.Stores = append(sh.Stores, orgconf.Store{URL: st.URL, Branch: st.Branch, OrgFolder: st.OrgFolder})
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
	path := orgFilePath(cur.Folder)
	res := OrgFileMoveResult{NoPush: !g.autopush(nil)}
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
