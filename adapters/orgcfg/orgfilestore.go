package orgcfg

// orgfilestore.go — the store side of `lore org store add` (B-4): one
// more entry under `stores:` of the org's lore-org.yaml, committed in the
// org store's clone, pushed only when asked. The file is edited as a YAML
// node, so every other key, comment and order is kept.
//
//	AddOrgFileStore ─▶ clone + fetch ─▶ commitStore: read lore-org.yaml at
//	                   tip (none: ErrNoOrgFile) ─▶ url already listed:
//	                   Existed (an unpushed earlier add is pushed when
//	                   Push; SetAudience on a listed store changes its
//	                   audience: key, D-442) ─▶ else append {url, name,
//	                   branch, audience} ─▶ commit as the caller's org
//	                   identity ─▶ push when Push

import (
	"context"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// orgFileStoreMessage is the commit subject of AddOrgFileStore.
const orgFileStoreMessage = "lore: org %s: add store %s"

// orgFileStoreAudienceMessage is the commit subject of an audience change.
const orgFileStoreAudienceMessage = "lore: org %s: store %s audience %s"

// orgFileStoresKey is lore-org.yaml's list of stores; orgFileAudienceKey
// a store's audience.
const (
	orgFileStoresKey   = "stores"
	orgFileAudienceKey = "audience"
)

// ErrOrgFolderAudience refuses an admins-only audience on the org folder's
// store: it holds the roster and company rules every member reads.
var ErrOrgFolderAudience = errors.New("orgcfg: the org folder's store is used by every member; it cannot be admins-only")

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
	// stays in the clone until `lore sync` or `lore store push` sends it
	// (under push auto, the next write to that store does too).
	Push bool
	// SetAudience makes Store.Audience the audience of a store the file
	// already lists (`lore org store audience`, `org store add
	// --audience`); without it a listed store is left as it is.
	SetAudience bool
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
	msg := fmt.Sprintf(orgFileStoreMessage, a.Org, want)
	if a.SetAudience {
		msg = fmt.Sprintf(orgFileStoreAudienceMessage, a.Org, want, orgconf.Audience(a.Store.Audience))
	}
	cr, err := commitStore(ctx, storeCommit{Dir: dir, Store: norm, Branch: branch, NoPush: !a.Push, PushPending: a.Push, Route: g.Route,
		Message: msg, Signature: a.Signature,
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
			body, existed, aerr := appendOrgFileStore(raw, want, a.Store, a.SetAudience)
			if aerr != nil {
				return nil, orgFileError(path, norm, aerr)
			}
			if existed && body != nil {
				res.Existed, res.AudienceChanged = true, true
				return map[string][]byte{path: body}, nil
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
// With setAudience a listed store whose audience differs from s's gets
// s's (out is then the changed file, existed true); an admins-only
// audience on the org folder's store is ErrOrgFolderAudience.
func appendOrgFileStore(raw []byte, want string, s OrgFileStore, setAudience bool) (out []byte, existed bool, err error) {
	if setAudience && s.OrgFolder != "" && !orgconf.Audience(s.Audience).ForMembers() {
		return nil, false, ErrOrgFolderAudience
	}
	doc, top, err := orgFileMapping(raw)
	if err != nil {
		return nil, false, err
	}
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
			if !setAudience || cur.Audience == s.Audience {
				return nil, true, nil
			}
			if cur.OrgFolder != "" && !orgconf.Audience(s.Audience).ForMembers() {
				return nil, true, ErrOrgFolderAudience
			}
			setMapKey(item, orgFileAudienceKey, s.Audience)
			out, err = yaml.Marshal(doc)
			return out, true, err
		}
	}
	item := &yaml.Node{}
	if err := item.Encode(s); err != nil {
		return nil, false, err
	}
	stores.Content = append(stores.Content, item)
	out, err = yaml.Marshal(doc)
	return out, false, err
}

// setMapKey sets key of mapping node m to the string v, removing the key
// when v is empty (a store's audience: everyone is no key at all).
func setMapKey(m *yaml.Node, key, v string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		if v == "" {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
		m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
		return
	}
	if v != "" {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
}
