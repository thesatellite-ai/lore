package orgcfg

// linkremove.go — `lore org unlink` (LORE_TEAM_SPEC F11 "lore org unlink
// asks for confirmation, like link"): a finished client project, or a
// link made by mistake, comes out of the org's stores.
//
//	FindLinks ─▶ every store of the entry fetched now (a deliberate change)
//	          ─▶ the links naming the project id (any remote: the repo may
//	             have moved; one folder with --project)
//	RemoveLinks ─▶ per store: commitStore (lock, rebuild on a lost race,
//	             push) with each folder's _meta.json minus those links
//
// The folder and its rows stay: unlinking stops the repo from resolving
// to the org through the link; the internal notes are still the org's.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// unlinkMessage is the store commit subject of an unlink.
const unlinkMessage = "lore: unlink %s"

// FoundLink is one link of a project in an org entry's stores.
type FoundLink struct {
	StoreURL string // normalised store url
	Branch   string // the store branch holding it
	Folder   string // the internal project folder
	Remote   string // the remote the link names
	Own      bool
	Default  bool
}

// FindLinks lists the links naming projectID in entry's stores (folder ""
// = every folder), every store fetched now. A store that cannot be read
// is ErrStoreOffline: an unlink must see every link it would leave.
func (g *GitLinks) FindLinks(ctx context.Context, entry, projectID, folder string) ([]FoundLink, error) {
	o := g.Config.FindOrg(entry)
	if o == nil {
		return nil, fmt.Errorf("orgcfg: no org entry named %s", entry)
	}
	always := g.Always
	g.Always = true // fetch every store now, whatever the fetch ttl
	links, status, detail, err := g.entryLinks(ctx, o, true)
	g.Always = always
	if err != nil {
		return nil, err
	}
	if status == entryUnknown {
		return nil, fmt.Errorf("%w: %s", ErrStoreOffline, detail)
	}
	branches := map[string]string{}
	for _, sc := range o.Stores {
		if n, nerr := repourl.NormalizeStore(sc.URL); nerr == nil {
			branches[n] = sc.EffectiveBranch()
		}
	}
	var out []FoundLink
	for _, l := range links {
		if l.ProjectID != projectID || (folder != "" && l.folder != folder) {
			continue
		}
		out = append(out, FoundLink{StoreURL: l.storeNorm, Branch: branches[l.storeNorm], Folder: l.folder, Remote: l.Remote, Own: l.Own, Default: l.Default})
	}
	return out, nil
}

// LinkRemoveResult says where the unlink went.
type LinkRemoveResult struct {
	// Removed counts the link records taken out.
	Removed int
	// Pushed: every store commit reached its remote; Offline: at least
	// one was committed locally only; NoPush: autopush is off.
	Pushed, Offline, NoPush bool
}

// RemoveLinks takes links (from FindLinks) out of their folders' _meta.json,
// one commit per store, as sig.
func (g *GitLinks) RemoveLinks(ctx context.Context, entry, projectID string, links []FoundLink, sig gitref.Signature) (LinkRemoveResult, error) {
	o := g.Config.FindOrg(entry)
	if o == nil {
		return LinkRemoveResult{}, fmt.Errorf("orgcfg: no org entry named %s", entry)
	}
	res := LinkRemoveResult{NoPush: !g.autopush(o.Live), Pushed: true}
	byStore := map[string][]FoundLink{}
	var stores []string
	for _, l := range links {
		if _, ok := byStore[l.StoreURL]; !ok {
			stores = append(stores, l.StoreURL)
		}
		byStore[l.StoreURL] = append(byStore[l.StoreURL], l)
	}
	sort.Strings(stores)
	for _, st := range stores {
		ls := byStore[st]
		removed := 0
		held := g.held(g.cloneDirOf(st))
		res.NoPush = res.NoPush || held
		cr, err := commitStore(ctx, storeCommit{Dir: g.cloneDirOf(st), Store: st, Branch: ls[0].Branch, NoPush: res.NoPush || held, Route: g.Route,
			Message: fmt.Sprintf(unlinkMessage, projectID), Signature: sig,
			Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
				put, n, cerr := unlinkChange(ctx, repo, tip, st, projectID, ls)
				removed = n
				return put, cerr
			}})
		if err != nil {
			return res, err
		}
		res.Removed += removed
		res.Pushed = res.Pushed && cr.Pushed
		res.Offline = res.Offline || cr.Offline
	}
	if res.Removed == 0 {
		res.Pushed = false
	}
	return res, nil
}

// unlinkChange is the _meta.json of every folder of ls at tip without the
// links of projectID (all of them in that folder: a link per remote).
func unlinkChange(ctx context.Context, repo *gitref.Repo, tip, store, projectID string, ls []FoundLink) (map[string][]byte, int, error) {
	put := map[string][]byte{}
	removed := 0
	seen := map[string]bool{}
	for _, l := range ls {
		if seen[l.Folder] {
			continue
		}
		seen[l.Folder] = true
		path, err := metaPathAt(ctx, repo, tip, l.Folder)
		if err != nil {
			return nil, 0, err
		}
		raw, found, err := repo.ReadFile(ctx, tip, path)
		if err != nil {
			return nil, 0, err
		}
		if !found {
			continue // the folder went since the scan: nothing to take out
		}
		doc := map[string]json.RawMessage{}
		if jerr := json.Unmarshal(raw, &doc); jerr != nil {
			return nil, 0, fmt.Errorf("orgcfg: %s in %s: %w", path, store, jerr)
		}
		var links []linkRecord
		if lr, ok := doc[metaLinksKey]; ok && string(lr) != "null" {
			if jerr := json.Unmarshal(lr, &links); jerr != nil {
				return nil, 0, fmt.Errorf("orgcfg: %s in %s: links: %w", path, store, jerr)
			}
		}
		keep := links[:0]
		for _, r := range links {
			if r.ProjectID == projectID {
				removed++
				continue
			}
			keep = append(keep, r)
		}
		if len(keep) == len(links) {
			continue
		}
		lb, err := json.Marshal(keep)
		if err != nil {
			return nil, 0, err
		}
		if len(keep) == 0 {
			lb = []byte("[]")
		}
		doc[metaLinksKey] = lb
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, 0, err
		}
		put[path] = append(out, '\n')
	}
	return put, removed, nil
}
