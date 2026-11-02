package lorecfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// repoOrgsKey is the YAML key `lore org use` edits.
const repoOrgsKey = "repo_orgs"

// SetRepoOrg records `lore org use` for one project id in dir/config.yaml.
//
// It edits the YAML document in place (yaml.Node), so the user's comments,
// key order and every other entry survive — a config written by hand is
// never re-serialised from the struct. The edited document is validated
// before it is written; an edit that would make the file invalid (an
// unknown org, a bad project id) is refused and nothing changes.
func SetRepoOrg(ctx context.Context, dir, projectID string, ro RepoOrg, o Options) (string, error) {
	return editRepoOrgs(ctx, dir, o, func(m *yaml.Node) error {
		var val yaml.Node
		if err := val.Encode(ro); err != nil {
			return fmt.Errorf("encode repo_orgs value: %w", err)
		}
		val.Style = yaml.FlowStyle
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == projectID {
				m.Content[i+1] = &val
				return nil
			}
		}
		m.Content = append(m.Content, scalar(projectID), &val)
		return nil
	})
}

// RemoveRepoOrg deletes the `lore org use` choice for projectID. Removing an
// absent key is not an error (reports false).
func RemoveRepoOrg(ctx context.Context, dir, projectID string, o Options) (bool, error) {
	removed := false
	_, err := editRepoOrgs(ctx, dir, o, func(m *yaml.Node) error {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == projectID {
				m.Content = append(m.Content[:i], m.Content[i+2:]...)
				removed = true
				return nil
			}
		}
		return nil
	})
	return removed, err
}

// editRepoOrgs hands the `repo_orgs` mapping (created when missing) to edit.
func editRepoOrgs(ctx context.Context, dir string, o Options, edit func(m *yaml.Node) error) (string, error) {
	return editTeamDoc(ctx, dir, o, func(path string, root *yaml.Node) error {
		m := mapValue(root, repoOrgsKey)
		if m == nil {
			m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			root.Content = append(root.Content, scalar(repoOrgsKey), m)
		}
		if m.Kind != yaml.MappingNode {
			return fmt.Errorf("lorecfg: %s line %d: %s must be a mapping", path, m.Line, repoOrgsKey)
		}
		// A flow-style empty map ({}) would keep the new entries on one line.
		m.Style = 0
		return edit(m)
	})
}

// SetOrgIdentity writes the identity block of the existing entry orgName
// (`lore identity set --org`, F18), keeping the rest of the file as written.
// The entry must exist: identities are never invented for an unknown org.
func SetOrgIdentity(ctx context.Context, dir, orgName string, id OrgIdentity, o Options) (string, error) {
	return editTeamDoc(ctx, dir, o, func(path string, root *yaml.Node) error {
		orgs := mapValue(root, orgsKey)
		if orgs == nil || orgs.Kind != yaml.SequenceNode {
			return fmt.Errorf("lorecfg: %s has no orgs: list; add the entry %q first", path, orgName)
		}
		for _, item := range orgs.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			if n := mapValue(item, orgNameKey); n == nil || n.Value != orgName {
				continue
			}
			var val yaml.Node
			if err := val.Encode(id); err != nil {
				return fmt.Errorf("encode identity: %w", err)
			}
			if cur := mapValue(item, orgIdentityKey); cur != nil {
				*cur = val
			} else {
				item.Content = append(item.Content, scalar(orgIdentityKey), &val)
			}
			return nil
		}
		return fmt.Errorf("lorecfg: %s has no org entry named %q", path, orgName)
	})
}

// YAML keys the editors look up.
const (
	orgsKey        = "orgs"
	orgNameKey     = "name"
	orgIdentityKey = "identity"
)

// editTeamDoc loads dir/config.yaml as a node tree (an empty document when
// absent), hands the top-level mapping to edit, validates the result and
// writes it atomically. Nothing is written when edit or validation fails.
func editTeamDoc(ctx context.Context, dir string, o Options, edit func(path string, root *yaml.Node) error) (string, error) {
	if err := ensureDir(dir); err != nil {
		return "", err
	}
	loc, err := Locate(ctx, dir, TeamFileBase, o)
	if err != nil {
		return "", err
	}
	if loc.Format == FormatTOML {
		return "", fmt.Errorf("lorecfg: %s could not be migrated to YAML; fix that first", loc.Path)
	}
	path := teamPath(dir)
	raw, perm, err := readWithPerm(path)
	if err != nil {
		return "", err
	}

	var doc yaml.Node
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return "", fmt.Errorf("lorecfg: parse %s: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("lorecfg: %s: the top level must be a mapping", path)
	}
	if err := edit(path, doc.Content[0]); err != nil {
		return "", err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", fmt.Errorf("lorecfg: encode %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("lorecfg: encode %s: %w", path, err)
	}
	var check Config
	if err := yaml.Unmarshal(buf.Bytes(), &check); err != nil {
		return "", fmt.Errorf("lorecfg: re-read edited %s: %w", path, err)
	}
	if err := check.Validate(); err != nil {
		return "", fmt.Errorf("lorecfg: refusing to write %s: %w", path, err)
	}
	if err := writeAtomic(path, buf.Bytes(), perm); err != nil {
		return "", err
	}
	return path, nil
}

// mapValue returns the value node for key in mapping m, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// scalar is a plain string key node.
func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// readWithPerm returns the file's bytes and permissions; an absent file is
// empty with TeamFilePerm.
func readWithPerm(path string) ([]byte, fs.FileMode, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, TeamFilePerm, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("lorecfg: stat %s: %w", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("lorecfg: read %s: %w", path, err)
	}
	return raw, info.Mode().Perm(), nil
}
