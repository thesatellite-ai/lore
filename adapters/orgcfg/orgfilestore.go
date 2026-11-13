package orgcfg

// orgfilestore.go — the store side of `lore org store add` (B-4): one
// more entry under `stores:` of the org's lore-org.yaml, committed in the
// org store's clone, pushed only when asked. The file is edited as a YAML
// node, so every other key, comment and order is kept.
//
//	AddOrgFileStore ─▶ clone + fetch ─▶ commitStore: read lore-org.yaml at
//	                   tip (none: ErrNoOrgFile) ─▶ url already listed:
//	                   Existed (an unpushed earlier add is pushed when
//	                   Push) ─▶ else append {url, name, branch} ─▶ commit
//	                   as the caller's org identity ─▶ push when Push

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// orgFileStoreMessage is the commit subject of AddOrgFileStore.
const orgFileStoreMessage = "lore: org %s: add store %s"

// orgFileStoresKey is lore-org.yaml's list of stores.
const orgFileStoresKey = "stores"

// OrgFileStoreAdd is one store to add to an org's lore-org.yaml.
type OrgFileStoreAdd struct {
	// URL is the org store holding lore-org.yaml, OrgFolder its folder,
	// Branch its branch ("" = the store's default branch).
	URL, OrgFolder, Branch string
	// Org names the org in the commit message.
	Org string
	// Store is the entry to add.
	Store OrgFileStore
	// Push sends the commit to the org store now; without it the commit
	// stays in the clone and the next write to that store sends it.
	Push bool
	// Signature is the commit author (the caller's org identity).
	Signature gitref.Signature
}

// AddOrgFileStore adds a.Store to the org's lore-org.yaml (see the file
// comment). Existed reports a store the file already lists (by
// normalised url): nothing is committed then.
func (g *GitLinks) AddOrgFileStore(ctx context.Context, a OrgFileStoreAdd) (OrgFileResult, error) {
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
		Message: fmt.Sprintf(orgFileStoreMessage, a.Org, want), Signature: a.Signature,
		Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
			raw, found, rerr := repo.ReadFile(ctx, tip, path)
			if rerr != nil {
				return nil, rerr
			}
			if !found {
				return nil, fmt.Errorf("%w: %s has no %s", ErrNoOrgFile, norm, path)
			}
			body, existed, aerr := appendOrgFileStore(raw, want, a.Store)
			if aerr != nil {
				return nil, fmt.Errorf("orgcfg: %s in %s: %w", path, norm, aerr)
			}
			if existed {
				res.Existed = true
				return nil, nil
			}
			return map[string][]byte{path: body}, nil
		}})
	res.Pushed, res.Offline = cr.Pushed, cr.Offline
	return res, err
}

// appendOrgFileStore is raw (a lore-org.yaml) with s appended under
// stores:, or existed when a store of the normalised url want is listed.
func appendOrgFileStore(raw []byte, want string, s OrgFileStore) (out []byte, existed bool, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, false, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("not a mapping")
	}
	top := doc.Content[0]
	var stores *yaml.Node
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == orgFileStoresKey {
			stores = top.Content[i+1]
		}
	}
	if stores == nil {
		stores = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: orgFileStoresKey}, stores)
	}
	if stores.Kind != yaml.SequenceNode {
		return nil, false, fmt.Errorf("%s is not a list", orgFileStoresKey)
	}
	for _, item := range stores.Content {
		var cur OrgFileStore
		if derr := item.Decode(&cur); derr != nil {
			return nil, false, derr
		}
		if n, nerr := repourl.NormalizeStore(cur.URL); nerr == nil && n == want {
			return nil, true, nil
		}
	}
	item := &yaml.Node{}
	if err := item.Encode(s); err != nil {
		return nil, false, err
	}
	stores.Content = append(stores.Content, item)
	out, err = yaml.Marshal(&doc)
	return out, false, err
}
