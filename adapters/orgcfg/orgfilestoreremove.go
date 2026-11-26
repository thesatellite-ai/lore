package orgcfg

// orgfilestoreremove.go — the store side of `lore org store remove`
// (devco T-22): a client contract ends, so the client's store leaves
// `stores:` of the org's lore-org.yaml (members stop cloning and fetching
// it after their next org refresh). The store itself, its remote and every
// note in it are kept; nothing is deleted.
//
//	RemoveOrgFileStore ─▶ clone + fetch ─▶ commitStore: read lore-org.yaml
//	                      at tip ─▶ url not listed: Existed=false, nothing
//	                      committed ─▶ else drop that entry ─▶ commit as the
//	                      caller's org identity ─▶ push when Push

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// orgFileStoreRemoveMessage is the commit subject of RemoveOrgFileStore.
const orgFileStoreRemoveMessage = "lore: org %s: retire store %s"

// RemoveOrgFileStore drops the store of normalised url want from the org's
// lore-org.yaml (a.Store.URL names it; a.Store's other fields are unused).
// Existed reports whether the file listed it: when it did not, nothing is
// committed.
func (g *GitLinks) RemoveOrgFileStore(ctx context.Context, a OrgFileStoreAdd) (OrgFileResult, error) {
	want, err := repourl.NormalizeStore(a.Store.URL)
	if err != nil {
		return OrgFileResult{}, fmt.Errorf("orgcfg: store url %q: %w", a.Store.URL, err)
	}
	norm, dir, err := g.openClone(ctx, a.URL, false)
	if err != nil {
		return OrgFileResult{}, err
	}
	branch := a.Branch
	if branch == "" {
		branch = g.DefaultBranch(ctx, norm)
	}
	res := OrgFileResult{StoreURL: norm, Branch: branch, NoPush: !a.Push}
	path := orgFilePath(a.OrgFolder)
	cr, err := commitStore(ctx, storeCommit{Dir: dir, Store: norm, Branch: branch, NoPush: !a.Push, PushPending: a.Push, Route: g.Route,
		Message: fmt.Sprintf(orgFileStoreRemoveMessage, a.Org, want), Signature: a.Signature,
		Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
			raw, found, rerr := repo.ReadFile(ctx, tip, path)
			if rerr != nil {
				return nil, rerr
			}
			if !found {
				return nil, fmt.Errorf("%w: %s has no %s", ErrNoOrgFile, norm, path)
			}
			raw, err = acceptedOrgRaw(ctx, dir, tip, a.OrgFolder, raw)
			if err != nil {
				return nil, err
			}
			body, listed, derr := dropOrgFileStore(raw, want)
			if derr != nil {
				return nil, orgFileError(path, norm, derr)
			}
			res.Existed = listed
			if !listed {
				return nil, nil
			}
			return map[string][]byte{path: body}, nil
		}})
	res.Pushed, res.Offline = cr.Pushed, cr.Offline
	res.NoPush = res.NoPush || cr.Held // committed on top of waiting work; lore sync sends it
	return res, err
}

// dropOrgFileStore is raw (a lore-org.yaml) without the stores: entry of
// normalised url want; listed is false (and out nil) when no entry has it.
func dropOrgFileStore(raw []byte, want string) (out []byte, listed bool, err error) {
	doc, top, err := orgFileMapping(raw)
	if err != nil {
		return nil, false, err
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != orgFileStoresKey || top.Content[i+1].Kind != yaml.SequenceNode {
			continue
		}
		stores := top.Content[i+1]
		for j, item := range stores.Content {
			var cur OrgFileStore
			if derr := item.Decode(&cur); derr != nil {
				return nil, false, derr
			}
			if n, nerr := repourl.NormalizeStore(cur.URL); nerr == nil && n == want {
				stores.Content = append(stores.Content[:j:j], stores.Content[j+1:]...)
				out, err = yaml.Marshal(doc)
				return out, true, err
			}
		}
	}
	return nil, false, nil
}
