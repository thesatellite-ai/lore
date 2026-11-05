package lorecfg

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// orgIDKey is the YAML key of an org entry's permanent id.
const orgIDKey = "id"

// RenameResult says what RenameOrg changed besides the entry's name.
type RenameResult struct {
	// Path is the config file written.
	Path string
	// RepoOrgs counts `repo_orgs` choices moved to the new name.
	RepoOrgs int
	// Defaults counts defaults.client_repos targets moved to the new name.
	Defaults int
	// IDRecorded is true when this call wrote the entry's id.
	IDRecorded bool
}

// RenameOrg renames the org entry oldName to newName in dir/config.yaml
// (`lore org rename`, PROVENANCE 10.7: a label change, the permanent id
// stays). Everything in this file that names the entry by its label moves
// with it, so the file stays valid: the `repo_orgs` choices naming it and
// its own defaults.client_repos targets ("<org>" / "<org>/<folder>"). id,
// when non-empty, is recorded as the entry's `id:` (the org folder's prj_
// id); an entry that already holds a different id is refused, because
// that is another org. Comments and key order survive (yaml.Node edit);
// nothing is written when the result would not validate (for example
// newName already names another entry).
func RenameOrg(ctx context.Context, dir, oldName, newName, id string, o Options) (RenameResult, error) {
	var res RenameResult
	if err := validateOrgName(newName); err != nil {
		return res, fmt.Errorf("lorecfg: new org name %q: %w", newName, err)
	}
	path, err := editTeamDoc(ctx, dir, o, func(path string, root *yaml.Node) error {
		orgs := mapValue(root, orgsKey)
		if orgs == nil || orgs.Kind != yaml.SequenceNode {
			return fmt.Errorf("lorecfg: %s has no org entry named %q", path, oldName)
		}
		var entry *yaml.Node
		for _, item := range orgs.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			n := mapValue(item, orgNameKey)
			if n == nil {
				continue
			}
			if n.Value == newName && newName != oldName {
				return fmt.Errorf("lorecfg: %s already has an org entry named %q", path, newName)
			}
			if n.Value == oldName {
				entry = item
			}
		}
		if entry == nil {
			return fmt.Errorf("lorecfg: %s has no org entry named %q", path, oldName)
		}
		mapValue(entry, orgNameKey).Value = newName
		if id != "" {
			switch cur := mapValue(entry, orgIDKey); {
			case cur == nil:
				entry.Content = append(entry.Content, scalar(orgIDKey), scalar(id))
				res.IDRecorded = true
			case cur.Value != id:
				return fmt.Errorf("lorecfg: org entry %q has id %s, not %s: it is another org", oldName, cur.Value, id)
			}
		}
		res.Defaults = renameTargets(entry, oldName, newName)
		res.RepoOrgs = renameRepoOrgs(root, oldName, newName)
		return nil
	})
	res.Path = path
	return res, err
}

// renameTargets moves the entry's defaults.client_repos targets that name
// oldName ("<old>" or "<old>/<folder>") to newName; it returns the count.
func renameTargets(entry *yaml.Node, oldName, newName string) int {
	d := mapValue(entry, "defaults")
	if d == nil || d.Kind != yaml.MappingNode {
		return 0
	}
	cr := mapValue(d, "client_repos")
	if cr == nil || cr.Kind != yaml.MappingNode {
		return 0
	}
	n := 0
	for i := 1; i < len(cr.Content); i += 2 {
		v := cr.Content[i]
		org, folder, has := strings.Cut(v.Value, refSep)
		if v.Kind != yaml.ScalarNode || org != oldName {
			continue
		}
		v.Value = newName
		if has {
			v.Value += refSep + folder
		}
		n++
	}
	return n
}

// renameRepoOrgs moves every `repo_orgs` value naming oldName (short
// scalar or {org: …} mapping) to newName; it returns the count.
func renameRepoOrgs(root *yaml.Node, oldName, newName string) int {
	m := mapValue(root, repoOrgsKey)
	if m == nil || m.Kind != yaml.MappingNode {
		return 0
	}
	n := 0
	for i := 1; i < len(m.Content); i += 2 {
		v := m.Content[i]
		switch v.Kind {
		case yaml.ScalarNode:
			if v.Value == oldName {
				v.Value = newName
				n++
			}
		case yaml.MappingNode:
			if o := mapValue(v, "org"); o != nil && o.Value == oldName {
				o.Value = newName
				n++
			}
		}
	}
	return n
}
