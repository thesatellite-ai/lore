package live

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"saas/pkg/aicoder/canonjson"
)

// Meta is _live/meta.json: the oldest lore that may write this live ref and
// the features it uses (§1, F2 edge case "a binary below min_lore_version
// refuses to write").
type Meta struct {
	MinLoreVersion string   `json:"min_lore_version"`
	Features       []string `json:"features"`
}

// MinWriterVersion is the min_lore_version a live ref gets on its first
// write: the first lore that understands live rows. Raising it is a team
// decision (an edit of the file), never automatic.
const MinWriterVersion = "0.1.0"

// FeatureLive names the live-row format in Meta.Features.
const FeatureLive = "live"

// DefaultMeta is the meta a new live ref starts with.
func DefaultMeta() Meta {
	return Meta{MinLoreVersion: MinWriterVersion, Features: []string{FeatureLive}}
}

// Encode renders the meta canonically.
func (m Meta) Encode() ([]byte, error) {
	feats := make([]any, len(m.Features))
	for i, f := range m.Features {
		feats[i] = f
	}
	return canonjson.Encode(map[string]any{"min_lore_version": m.MinLoreVersion, "features": feats})
}

// Meta reads _live/meta.json; ok is false when the tree has none.
func (t Tree) Meta() (Meta, bool, error) {
	b, has := t[MetaPath]
	if !has {
		return Meta{}, false, nil
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, true, fmt.Errorf("live: %s: %w", MetaPath, err)
	}
	return m, true, nil
}

// versionParts is how many numeric parts a version comparison uses.
const versionParts = 3

// VersionBelow reports whether binary is older than floor, comparing the
// numeric major.minor.patch core only: a development build "0.1.0-dev"
// counts as 0.1.0, so a dev build of a release is never locked out of the
// format that release writes. An unparseable floor is treated as "not
// below" (a hand-edited meta must not lock everyone out); an unparseable
// binary version is treated as below any parseable floor (fail closed).
func VersionBelow(binary, floor string) bool {
	f, ok := versionCore(floor)
	if !ok {
		return false
	}
	b, ok := versionCore(binary)
	if !ok {
		return true
	}
	for i := 0; i < versionParts; i++ {
		if b[i] != f[i] {
			return b[i] < f[i]
		}
	}
	return false
}

func versionCore(v string) ([versionParts]int, bool) {
	var out [versionParts]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > versionParts {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// purgedKeyIDs is the list key inside _live/purged.json.
const purgedKeyIDs = "ids"

// Purged reads _live/purged.json as a set of row ids.
func (t Tree) Purged() (map[string]bool, error) {
	out := map[string]bool{}
	b, has := t[PurgedPath]
	if !has {
		return out, nil
	}
	var v struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("live: %s: %w", PurgedPath, err)
	}
	for _, id := range v.IDs {
		out[id] = true
	}
	return out, nil
}

// encodePurged renders a purged-id set canonically (sorted).
func encodePurged(set map[string]bool) ([]byte, error) {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	arr := make([]any, len(ids))
	for i, s := range ids {
		arr[i] = s
	}
	return canonjson.Encode(map[string]any{purgedKeyIDs: arr})
}
