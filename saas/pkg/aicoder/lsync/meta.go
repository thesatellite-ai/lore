package lsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/ids"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// Keys inside _meta.json and _purged.json.
const (
	metaKeyProjectID = "project_id"
	purgedKeyIDs     = "ids"
)

// ProjectMeta is the parsed .lore/data/_meta.json.
//
// It pins the project identity for everyone sharing the repo (E1): `lore
// init` on a clone that already has it adopts this id instead of minting a
// new project, and ADOPT rewrites a pre-existing local project id to it.
type ProjectMeta struct {
	ProjectID string
}

// ReadProjectMeta parses _meta.json. Returns fs.ErrNotExist (wrapped) when
// the file is absent.
func ReadProjectMeta(dataDir string) (ProjectMeta, error) {
	b, err := readRowFile(filepath.Join(dataDir, MetaFileName))
	if err != nil {
		return ProjectMeta{}, err
	}
	doc, err := decodeDoc(b)
	if err != nil {
		return ProjectMeta{}, fmt.Errorf("lsync: %s: %w", MetaFileName, err)
	}
	var m ProjectMeta
	if v, ok := doc[metaKeyProjectID]; ok && v != nil {
		s, isStr := v.(string)
		if !isStr || ids.Validate(s, ids.PrefixProject) != nil {
			return ProjectMeta{}, fmt.Errorf("lsync: %s: invalid %s", MetaFileName, metaKeyProjectID)
		}
		m.ProjectID = s
	}
	return m, nil
}

// MetaExists reports whether dataDir holds a _meta.json — the marker that a
// directory is a lore-sync checkout (used by project resolution to
// materialize lore.db on a fresh clone).
func MetaExists(dataDir string) bool {
	_, err := os.Lstat(filepath.Join(dataDir, MetaFileName))
	return err == nil
}

// EnsureProjectMeta writes _meta.json's _v and project_id now (keeping any
// other key), so `lore init` can give the checkout its permanent repo id
// beside project_id before the post-run bootstrap (PROV1, PROVENANCE_DESIGN
// 10.1). The bootstrap's later write keeps the repo identity keys, since
// lsync owns only _v and project_id.
func EnsureProjectMeta(dataDir, projectID string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return writeProjectMeta(dataDir, ProjectMeta{ProjectID: projectID})
}

// writeProjectMeta sets _v and project_id in _meta.json and keeps every
// other key the file already holds (a store project's links and global
// flag, F11): lsync owns only these two keys.
func writeProjectMeta(dataDir string, m ProjectMeta) error {
	doc := map[string]any{}
	if b, err := readRowFile(filepath.Join(dataDir, MetaFileName)); err == nil {
		if cur, err := canonjson.Decode(b, canonjson.DefaultMaxDepth); err == nil {
			doc = cur
		}
	}
	doc[keyVersion] = int64(FormatVersion)
	delete(doc, metaKeyProjectID)
	if m.ProjectID != "" {
		doc[metaKeyProjectID] = m.ProjectID
	}
	b, err := canonjson.Encode(doc)
	if err != nil {
		return err
	}
	full := filepath.Join(dataDir, MetaFileName)
	if cur, err := os.ReadFile(full); err == nil && string(cur) == string(b) {
		return nil
	}
	return writeFileAtomic(full, b)
}

// readPurged returns the set of ids purged on main (E47). Absent file ⇒
// empty set; an "ids" value that is not a list is an error, never an empty
// set (that would let purged rows be imported again).
func readPurged(dataDir string) (map[string]bool, error) {
	out := map[string]bool{}
	b, err := readRowFile(filepath.Join(dataDir, PurgedFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	doc, err := decodeDoc(b)
	if err != nil {
		return nil, fmt.Errorf("lsync: %s: %w", PurgedFileName, err)
	}
	ids, err := purgedIDList(doc)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func writePurged(dataDir string, set map[string]bool) error {
	b, err := encodePurged(set)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dataDir, PurgedFileName), b)
}

// encodePurged renders a purged-id set canonically (sorted ids).
func encodePurged(set map[string]bool) ([]byte, error) {
	idsList := make([]string, 0, len(set))
	for id := range set {
		idsList = append(idsList, id)
	}
	sort.Strings(idsList)
	arr := make([]any, len(idsList))
	for i, s := range idsList {
		arr[i] = s
	}
	return canonjson.Encode(map[string]any{keyVersion: json.Number(fmt.Sprint(FormatVersion)), purgedKeyIDs: arr})
}

// ResolveRepoIdentity settles the repo identity keys (repo_id, urls,
// forked_from, split_from, repo_id_aliases) of a checkout's _meta.json
// before the merge driver's field merge, so they never become a conflict
// and never take the generic "newer side" value. It sets the same resolved
// identity on both sides (and on base, so the field merge sees no
// three-way change against it); every other key is left to the driver.
//
// Q5 (PROV1): the rule is rowfs.ResolveRepoIdentity, the one the core's
// store folder merge uses: keep ours (%A, the branch being merged into)
// when a fork or split meets its upstream; the older UUIDv7 wins with the
// other id in repo_id_aliases when two clones raced; the same id unions
// the addresses. A side whose identity cannot be read is left untouched
// (the driver then reports it as it would any clash).
func ResolveRepoIdentity(base, ours, theirs map[string]any) {
	om, ook := repoMetaOfDoc(ours)
	tm, tok := repoMetaOfDoc(theirs)
	if !ook || !tok || (om.RepoID == "" && tm.RepoID == "") {
		return
	}
	resolved := rowfs.ResolveRepoIdentity(om, tm)
	raw, err := json.Marshal(resolved)
	if err != nil {
		return
	}
	var sub map[string]any
	if json.Unmarshal(raw, &sub) != nil {
		return
	}
	for _, doc := range []map[string]any{base, ours, theirs} {
		if doc == nil {
			continue
		}
		for _, k := range repoIdentityKeys {
			delete(doc, k)
		}
		for k, v := range sub {
			doc[k] = v
		}
	}
}

// repoIdentityKeys are the _meta.json keys ResolveRepoIdentity owns.
var repoIdentityKeys = []string{rowfs.MetaKeyRepoID, rowfs.MetaKeyURLs, rowfs.MetaKeyForkedFrom, rowfs.MetaKeySplitFrom, rowfs.MetaKeyAliases}

// repoMetaOfDoc reads the identity keys of a decoded _meta.json; ok is
// false when they do not have the expected shape.
func repoMetaOfDoc(doc map[string]any) (rowfs.RepoMeta, bool) {
	sub := map[string]any{}
	for _, k := range repoIdentityKeys {
		if v, ok := doc[k]; ok {
			sub[k] = v
		}
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		return rowfs.RepoMeta{}, false
	}
	var m rowfs.RepoMeta
	if json.Unmarshal(raw, &m) != nil {
		return rowfs.RepoMeta{}, false
	}
	return m, true
}
