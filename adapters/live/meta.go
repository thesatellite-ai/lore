package live

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	lorerow "github.com/thesatellite-ai/lore/model/row"
)

// Meta is _live/meta.json: the oldest lore that may write this live ref and
// the features it uses (§1, F2 edge case "a binary below min_lore_version
// refuses to write").
type Meta struct {
	MinLoreVersion string   `json:"min_lore_version"`
	Features       []string `json:"features"`
}

// MinWriterVersion is the min_lore_version a live ref starts from: the
// first lore that understands live rows. It is only the starting point:
// every live write raises the meta to the features its rows use and their
// Since (raiseMeta in write.go, FORMAT_EVOLUTION rule 2), in the same commit as the
// rows, so the first 0.1.18 live row lifts the floor to 0.1.18 and a lore
// below it (v0.1.17 checks min_lore_version before every live write) turns
// read-only instead of rewriting the row without the fields it does not
// know (F-COMPAT-LIVE-1). The floor is never lowered automatically.
const MinWriterVersion = "0.1.0"

// FeatureLive names the live-row format in Meta.Features.
const FeatureLive = "live"

// DefaultMeta is the meta a new live ref starts with.
func DefaultMeta() Meta {
	return Meta{MinLoreVersion: MinWriterVersion, Features: []string{FeatureLive}}
}

// Encode renders the meta canonically.
func (m Meta) Encode() ([]byte, error) {
	return canonjson.Encode(canonjson.Object{
		"min_lore_version": canonjson.String(m.MinLoreVersion),
		"features":         stringArray(m.Features),
	})
}

// metaKeyMinLoreVersion and metaKeyFeatures are the two keys lore reads
// in _live/meta.json; any other key is kept as it is (rule 2).
const (
	metaKeyMinLoreVersion = "min_lore_version"
	metaKeyFeatures       = "features"
)

// declared is m as the format gate's declaration.
func (m Meta) declared() formatgate.Declared {
	return formatgate.Declared{Features: m.Features, MinLoreVersion: m.MinLoreVersion}
}

// raiseMetaFile returns the _live/meta.json bytes after adding need to cur
// (nil cur: the ref has none yet, start from DefaultMeta): every feature
// not listed yet is added and min_lore_version is raised to its Since,
// never lowered; keys lore does not know are kept. changed is false when
// cur already declares everything (the file is then left alone, so a
// write that needs nothing new commits no meta change).
func raiseMetaFile(cur []byte, need []string) (out []byte, changed bool, err error) {
	base := DefaultMeta()
	obj := canonjson.Object{}
	if cur != nil {
		if obj, err = canonjson.Decode(cur, canonjson.DefaultMaxDepth); err != nil {
			return nil, false, fmt.Errorf("live: %s: %w", MetaPath, err)
		}
		if base, _, err = (Tree{MetaPath: cur}).Meta(); err != nil {
			return nil, false, err
		}
	}
	next, added := formatgate.Raise(base.declared(), need)
	if cur != nil && len(added) == 0 && next.MinLoreVersion == base.MinLoreVersion {
		return cur, false, nil
	}
	return encodeMetaObject(obj, next)
}

// mergeMetaFiles is the live merge of two _live/meta.json files (rule 2
// "Merging the flags"): features as a set, min_lore_version the higher
// version (version-aware), every other key from theirs over ours.
func mergeMetaFiles(ours, theirs []byte) ([]byte, error) {
	var objs [2]canonjson.Object
	var decl [2]formatgate.Declared
	for i, b := range [][]byte{ours, theirs} {
		o, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
		if err != nil {
			return nil, fmt.Errorf("live: %s: %w", MetaPath, err)
		}
		m, _, err := (Tree{MetaPath: b}).Meta()
		if err != nil {
			return nil, err
		}
		objs[i], decl[i] = o, m.declared()
	}
	obj := objs[0].Clone()
	for k, v := range objs[1] {
		obj[k] = v
	}
	out, _, err := encodeMetaObject(obj, formatgate.Merge(decl[0], decl[1]))
	return out, err
}

// encodeMetaObject writes d's two keys into obj (a copy) and encodes it.
func encodeMetaObject(obj canonjson.Object, d formatgate.Declared) ([]byte, bool, error) {
	o := obj.Clone()
	o[metaKeyMinLoreVersion] = canonjson.String(d.MinLoreVersion)
	o[metaKeyFeatures] = stringArray(d.Features)
	b, err := canonjson.Encode(o)
	return b, err == nil, err
}

// v017RowVersion is the "_v" of v0.1.17's row format (model/row formatV1,
// unexported there); a live body at this version was not written in the
// 0.1.18 format and declares nothing.
const v017RowVersion = 1

// rowFeatures lists the format features the 0.1.18 row documents among
// files use (formatgate.Needs: hlc for every _v: 2 row, task_tree for a
// task with a parent, rank or assignees, and so on). A body that is not a
// _v: 2 row document (a v0.1.17 row carried as it is, a hand-made file)
// adds nothing: only rows this format wrote raise the floor.
func rowFeatures(files []File) []string {
	var out []string
	for _, f := range files {
		b, err := canonjson.Encode(f.Body)
		if err != nil {
			continue
		}
		r, err := lorerow.Decode(b)
		if err != nil || r.SourceVersion <= v017RowVersion {
			continue
		}
		for _, n := range formatgate.Needs(r) {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	slices.Sort(out)
	return out
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
	return canonjson.Encode(canonjson.Object{purgedKeyIDs: stringArray(ids)})
}

// stringArray is a JSON array of strings, in the given order.
func stringArray(ss []string) canonjson.Value {
	arr := make([]canonjson.Value, len(ss))
	for i, s := range ss {
		arr[i] = canonjson.String(s)
	}
	return canonjson.Array(arr...)
}
