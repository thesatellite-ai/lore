package stores

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/merge3"
)

// Keys of a store project's _meta.json this package reads or writes. The
// row engine writes project_id and the format version; links and global
// exist only in store projects (§1 "Files and folders").
const (
	metaKeyProjectID = "project_id"
	metaKeyLinks     = "links"
	metaKeyGlobal    = "global"
	metaKeyVersion   = "_v"
	// metaFormatVersion is lsync.FormatVersion: a _meta.json lore creates
	// in a new store folder must read as a normal project to the engine.
	metaFormatVersion = 1
)

// Keys inside one links entry.
const (
	linkKeyProjectID = "project_id"
	linkKeyRemote    = "remote"
	linkKeyOwn       = "own"
	linkKeyDefault   = "default"
)

// LinkRecord is one entry of a store project's _meta.json "links" (F11):
// "client project id ProjectID at remote Remote belongs to this folder".
type LinkRecord struct {
	ProjectID string `json:"project_id"`
	// Remote is normalised (host/owner/repo).
	Remote string `json:"remote"`
	// Own marks the org's own repo (--own): no client guards.
	Own bool `json:"own,omitempty"`
	// Default marks the link `--to <org>` uses when a repo has several.
	Default bool `json:"default,omitempty"`
}

// key identifies a link: one (project id, remote) pair per folder.
func (l LinkRecord) key() string { return l.ProjectID + "\x00" + l.Remote }

// StoreMeta is the parsed _meta.json of a store project. Unknown keys are
// kept in Extra and written back untouched, so this package never drops a
// field another lore version added.
type StoreMeta struct {
	ProjectID string
	Links     []LinkRecord
	Global    bool
	Extra     map[string]any
}

// ParseStoreMeta parses a store project's _meta.json.
func ParseStoreMeta(b []byte) (StoreMeta, error) {
	doc, err := decodeObject(b)
	if err != nil {
		return StoreMeta{}, fmt.Errorf("stores: %s: %w", metaFileName, err)
	}
	var m StoreMeta
	if v, ok := doc[metaKeyProjectID].(string); ok {
		m.ProjectID = v
	}
	if v, ok := doc[metaKeyGlobal].(bool); ok {
		m.Global = v
	}
	if raw, ok := doc[metaKeyLinks]; ok && raw != nil {
		arr, isArr := raw.([]any)
		if !isArr {
			return StoreMeta{}, fmt.Errorf("stores: %s: links must be a list", metaFileName)
		}
		for i, it := range arr {
			obj, isObj := it.(map[string]any)
			if !isObj {
				return StoreMeta{}, fmt.Errorf("stores: %s: links[%d] must be an object", metaFileName, i)
			}
			l := LinkRecord{}
			l.ProjectID, _ = obj[linkKeyProjectID].(string)
			l.Remote, _ = obj[linkKeyRemote].(string)
			l.Own, _ = obj[linkKeyOwn].(bool)
			l.Default, _ = obj[linkKeyDefault].(bool)
			if l.ProjectID == "" || l.Remote == "" {
				return StoreMeta{}, fmt.Errorf("stores: %s: links[%d] needs project_id and remote", metaFileName, i)
			}
			m.Links = append(m.Links, l)
		}
	}
	m.Extra = map[string]any{}
	for k, v := range doc {
		switch k {
		case metaKeyProjectID, metaKeyLinks, metaKeyGlobal:
		default:
			m.Extra[k] = v
		}
	}
	return m, nil
}

// Encode renders the meta canonically (sorted keys, links sorted by
// remote then project id, so two writers adding the same link produce the
// same bytes).
func (m StoreMeta) Encode() ([]byte, error) {
	doc := map[string]any{}
	for k, v := range m.Extra {
		doc[k] = v
	}
	if _, ok := doc[metaKeyVersion]; !ok {
		doc[metaKeyVersion] = json.Number(fmt.Sprint(metaFormatVersion))
	}
	if m.ProjectID != "" {
		doc[metaKeyProjectID] = m.ProjectID
	}
	if m.Global {
		doc[metaKeyGlobal] = true
	}
	if len(m.Links) > 0 {
		links := append([]LinkRecord(nil), m.Links...)
		sort.Slice(links, func(i, j int) bool {
			if links[i].Remote != links[j].Remote {
				return links[i].Remote < links[j].Remote
			}
			return links[i].ProjectID < links[j].ProjectID
		})
		arr := make([]any, len(links))
		for i, l := range links {
			obj := map[string]any{linkKeyProjectID: l.ProjectID, linkKeyRemote: l.Remote}
			if l.Own {
				obj[linkKeyOwn] = true
			}
			if l.Default {
				obj[linkKeyDefault] = true
			}
			arr[i] = obj
		}
		doc[metaKeyLinks] = arr
	}
	b, err := canonjson.Encode(doc)
	if err != nil {
		return nil, fmt.Errorf("stores: encode %s: %w", metaFileName, err)
	}
	return b, nil
}

// AddLink sets l in the meta's links (replacing an entry with the same
// project id and remote). With l.Default, every other link of the same
// remote loses its default mark: one default per repo (TEAM-032).
// Returns whether anything changed.
func (m *StoreMeta) AddLink(l LinkRecord) bool {
	changed := false
	found := false
	for i := range m.Links {
		if m.Links[i].key() == l.key() {
			found = true
			if m.Links[i] != l {
				m.Links[i] = l
				changed = true
			}
		}
	}
	if !found {
		m.Links = append(m.Links, l)
		changed = true
	}
	return changed
}

// RemoveLink drops the link for (projectID, remote); reports a change.
func (m *StoreMeta) RemoveLink(projectID, remote string) bool {
	out := m.Links[:0]
	removed := false
	for _, l := range m.Links {
		if l.ProjectID == projectID && l.Remote == remote {
			removed = true
			continue
		}
		out = append(out, l)
	}
	m.Links = out
	return removed
}

// MergeStoreFile merges one file both sides of a store branch changed
// (reconcile after an offline write). base is nil when the file is new on
// both sides. _meta.json merges its links as a set (concurrent links both
// survive, F11 race); a row file is field-merged with ours (the local,
// later write) winning a clash, because a store row has no conflict
// workflow of its own; anything that is not a JSON object keeps ours.
func MergeStoreFile(path string, base, ours, theirs []byte) ([]byte, error) {
	o, oerr := decodeObject(ours)
	t, terr := decodeObject(theirs)
	if oerr != nil || terr != nil {
		return ours, nil
	}
	b := map[string]any{}
	if base != nil {
		if bb, err := decodeObject(base); err == nil {
			b = bb
		}
	}
	isMeta := strings.HasSuffix(path, "/"+metaFileName) || path == metaFileName
	pol := merge3.Policy{Newer: merge3.SideOurs, StrategyFor: func(key string) merge3.Strategy {
		if isMeta && key == metaKeyLinks {
			return merge3.StrategySet
		}
		return merge3.StrategyNewest
	}}
	res, err := merge3.Merge(b, o, t, pol)
	if err != nil {
		return nil, fmt.Errorf("stores: merge %s: %w", path, err)
	}
	out, err := canonjson.Encode(res.Merged)
	if err != nil {
		return nil, fmt.Errorf("stores: encode merged %s: %w", path, err)
	}
	if isMeta {
		// Re-encode through StoreMeta so the link list is canonical.
		m, err := ParseStoreMeta(out)
		if err != nil {
			return nil, err
		}
		return m.Encode()
	}
	return out, nil
}

// errNotObject: a file that is not a JSON object.
var errNotObject = errors.New("not a JSON object")

// decodeObject decodes a JSON object keeping numbers exact.
func decodeObject(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errNotObject
	}
	return obj, nil
}
