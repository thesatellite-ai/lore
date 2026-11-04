package stores

import (
	"context"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"saas/pkg/aicoder/gitref"
)

// OrgDefaultsFile is <org_folder>/.lore/lore-org.yaml (§5.1, §5.2): the
// team's shared suggestions, read on every resolve. Lore reads the keys it
// enforces here; `lore org import` reads the rest (stores, patterns).
const OrgDefaultsFile = "lore-org.yaml"

// OrgFileDefaults is lore-org.yaml: the team's suggested org entry (§5.2:
// "stores, patterns, defaults, no identity"). F14 applies Defaults on every
// resolve; `lore org import` reads the rest.
type OrgFileDefaults struct {
	// Name is the suggested entry name.
	Name string `yaml:"name,omitempty"`
	// Stores are the org's stores (url, branch, org_folder).
	Stores []SuggestedStore `yaml:"stores,omitempty"`
	// Own and Repos are the repo patterns.
	Own      []string `yaml:"own,omitempty"`
	Repos    []string `yaml:"repos,omitempty"`
	Defaults struct {
		// ClientRepos is defaults.client_repos: kind → repo | <org> |
		// <org>/<folder>. It wins over a looser personal value.
		ClientRepos map[string]string `yaml:"client_repos,omitempty"`
	} `yaml:"defaults,omitempty"`
}

// SuggestedStore is one store in lore-org.yaml.
type SuggestedStore struct {
	URL       string `yaml:"url"`
	Branch    string `yaml:"branch,omitempty"`
	OrgFolder string `yaml:"org_folder,omitempty"`
}

// OrgFilePath is the path of lore-org.yaml for an org folder.
func OrgFilePath(orgFolder string) string { return ProjectPrefix(orgFolder) + OrgDefaultsFile }

// ParseOrgFile parses lore-org.yaml.
func ParseOrgFile(b []byte) (OrgFileDefaults, error) {
	var f OrgFileDefaults
	if err := yaml.Unmarshal(b, &f); err != nil {
		return OrgFileDefaults{}, fmt.Errorf("stores: %s: %w", OrgDefaultsFile, err)
	}
	return f, nil
}

// ReadOrgFile reads lore-org.yaml of orgFolder at the branch tip; ok is
// false when the store has none (no team defaults).
func (s *Store) ReadOrgFile(ctx context.Context, branch, orgFolder string) (OrgFileDefaults, bool, error) {
	tip, ok, err := s.Tip(ctx, branch)
	if err != nil || !ok {
		return OrgFileDefaults{}, false, err
	}
	b, found, err := s.Repo.ReadFile(ctx, tip, OrgFilePath(orgFolder))
	if err != nil || !found {
		return OrgFileDefaults{}, false, err
	}
	f, err := ParseOrgFile(b)
	if err != nil {
		return OrgFileDefaults{}, false, err
	}
	return f, true, nil
}

// msgOrgFile is the commit subject of WriteOrgFile.
const msgOrgFile = "lore: org %s"

// ErrOrgFileExists: the org folder already holds a lore-org.yaml naming
// another org (WriteOrgFile never overwrites a team's suggestion).
var ErrOrgFileExists = errors.New("stores: the org folder already has a lore-org.yaml")

// WriteOrgFile commits <orgFolder>/.lore/lore-org.yaml with f on branch
// and pushes it (`lore org create`): the suggestion teammates adopt with
// `lore org import`. The store must already be a lore store (its marker is
// checked) and the branch must exist. A lore-org.yaml naming the same org
// is kept as it is (existed true, nothing written); one naming another org
// is refused with ErrOrgFileExists.
func (s *Store) WriteOrgFile(ctx context.Context, branch, orgFolder string, f OrgFileDefaults, sig gitref.Signature, noPush bool) (res WriteResult, existed bool, err error) {
	body, err := yaml.Marshal(f)
	if err != nil {
		return WriteResult{}, false, fmt.Errorf("stores: encode %s: %w", OrgDefaultsFile, err)
	}
	res, err = s.Write(ctx, WriteRequest{
		Branch: branch, Message: fmt.Sprintf(msgOrgFile, f.Name), Signature: sig, NoPush: noPush,
		Apply: func(ctx context.Context, base Snapshot) (gitref.Changes, error) {
			if err := s.checkSnapshot(ctx, base, branch); err != nil {
				return gitref.Changes{}, err
			}
			old, ok, err := base.ReadFile(ctx, OrgFilePath(orgFolder))
			if err != nil {
				return gitref.Changes{}, err
			}
			if ok {
				cur, perr := ParseOrgFile(old)
				if perr != nil || cur.Name != f.Name {
					return gitref.Changes{}, fmt.Errorf("%w: %s (org %q)", ErrOrgFileExists, OrgFilePath(orgFolder), cur.Name)
				}
				existed = true
				return gitref.Changes{}, nil
			}
			return gitref.Changes{Put: map[string][]byte{OrgFilePath(orgFolder): body}}, nil
		},
	})
	return res, existed, err
}
