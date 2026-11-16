package orgcfg

// orgfileedit.go — `lore org config set` (devco D-453): one list key of
// lore-org.yaml (own, repos) replaced in the org store's clone and
// committed (pushed only when asked), the rest of the file kept as it is.

import (
	"context"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

const orgFileEditMessage = "lore: org %s: set %s"

// OrgFileEdit is one list key of lore-org.yaml set to Values.
type OrgFileEdit struct {
	// URL is the org store holding lore-org.yaml, OrgFolder its folder,
	// Branch its branch ("" = the store's default branch).
	URL, OrgFolder, Branch string
	// Org names the org in the commit message.
	Org string
	// Key is the top-level list key; Values its new list (empty removes
	// the key).
	Key    string
	Values []string
	// Push sends the commit to the org store now.
	Push bool
	// Signature is the commit author (the caller's org identity).
	Signature gitref.Signature
}

// EditOrgFile writes e into the org store's lore-org.yaml. res.Existed is
// true when the file already held exactly Values (nothing committed).
func (g *GitLinks) EditOrgFile(ctx context.Context, e OrgFileEdit) (OrgFileResult, error) {
	norm, dir, err := g.openClone(ctx, e.URL, false)
	if err != nil {
		return OrgFileResult{}, err
	}
	branch := e.Branch
	if branch == "" {
		branch = g.DefaultBranch(ctx, norm)
	}
	res := OrgFileResult{StoreURL: norm, Branch: branch, NoPush: !e.Push}
	path := orgFilePath(e.OrgFolder)
	cr, err := commitStore(ctx, storeCommit{Dir: dir, Store: norm, Branch: branch, NoPush: !e.Push, PushPending: e.Push, Route: g.Route,
		Message: fmt.Sprintf(orgFileEditMessage, e.Org, e.Key), Signature: e.Signature,
		Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
			raw, found, rerr := repo.ReadFile(ctx, tip, path)
			if rerr != nil {
				return nil, rerr
			}
			if !found {
				return nil, fmt.Errorf("%w: %s has no %s", ErrNoOrgFile, norm, path)
			}
			raw = acceptedOrgRaw(ctx, dir, tip, e.OrgFolder, raw)
			body, changed, serr := setOrgFileList(raw, e.Key, e.Values)
			if serr != nil {
				return nil, fmt.Errorf("orgcfg: %s in %s: %w", path, norm, serr)
			}
			if !changed {
				res.Existed = true
				return nil, nil
			}
			return map[string][]byte{path: body}, nil
		}})
	res.Pushed, res.Offline = cr.Pushed, cr.Offline
	return res, err
}

// setOrgFileList is raw (a lore-org.yaml) with the top-level list key set
// to values (removed when values is empty); changed is false when raw
// already held exactly that list. Every other key keeps its place.
func setOrgFileList(raw []byte, key string, values []string) (out []byte, changed bool, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, false, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("not a mapping")
	}
	top := doc.Content[0]
	at := -1
	var cur []string
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != key {
			continue
		}
		at = i
		if derr := top.Content[i+1].Decode(&cur); derr != nil {
			return nil, false, fmt.Errorf("%s is not a list: %w", key, derr)
		}
	}
	if slices.Equal(cur, values) {
		return nil, false, nil
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	switch {
	case at >= 0 && len(values) == 0:
		top.Content = append(top.Content[:at], top.Content[at+2:]...)
	case at >= 0:
		top.Content[at+1] = seq
	default:
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, seq)
	}
	out, err = yaml.Marshal(&doc)
	return out, true, err
}
