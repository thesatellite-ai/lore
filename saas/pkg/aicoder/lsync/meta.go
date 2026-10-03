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

func writeProjectMeta(dataDir string, m ProjectMeta) error {
	doc := map[string]any{keyVersion: int64(FormatVersion)}
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
// empty set.
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
	list, _ := doc[purgedKeyIDs].([]any)
	for _, v := range list {
		if s, ok := v.(string); ok {
			out[s] = true
		}
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
