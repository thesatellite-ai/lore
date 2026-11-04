package rowfs

import (
	"bytes"
	"encoding/json"
	"path"
	"slices"
	"strconv"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
)

// A store folder holds two files that are not rows: _meta.json (the
// folder's project id, format version and v0.1.17's links[] of linked
// repos) and _purged.json (ids purged on purpose). Several writers touch
// them — `lore org link` (v0.1.17 store code), each clone's first write
// (ensureMeta), purges — so a git merge can see both sides changed. These
// merges always produce a file (never a refusal) so no user is ever left
// with a store that cannot sync:
//
//   - _meta.json: links[] is the union by (remote, project_id); project_id
//     is the one that existed first — base's when there is a base, else a
//     `_v: 1` side's (v0.1.17's id wins over a core-made one), else the
//     smaller id (both sides then pick the same one); `_v` is the larger;
//     any other key three-way (one side changed it: that side; both, and
//     they differ: the larger canonical value, the same on every clone).
//   - _purged.json: ids is the union, sorted; other keys as above.
//
// Nothing in the core reads a folder's project_id (rows carry their own
// project_id), so adopting one id over another rewrites no row.

// Keys of the two folder files (file formats: they must not change).
const (
	metaKeyProjectID = "project_id"
	metaKeyVersion   = "_v"
	metaKeyLinks     = "links"
	purgedKeyIDs     = "ids"
	// metaV1 is the v0.1.17 format version of a _meta.json.
	metaV1 = 1
)

// IsFolderFile reports whether a store path names a folder's _meta.json or
// _purged.json (the files MergeFolderFile merges).
func IsFolderFile(p string) bool {
	b := path.Base(p)
	return b == MetaFileName || b == PurgedFileName
}

// object is a decoded JSON object with its values kept as raw JSON.
type object map[string]json.RawMessage

// decodeObject decodes b as an object; empty b (a side without the file)
// is an empty object.
func decodeObject(b []byte) (object, error) {
	o := object{}
	if len(bytes.TrimSpace(b)) == 0 {
		return o, nil
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return nil, err
	}
	return o, nil
}

// MergeFolderFile merges the three versions of a folder file at store path
// p (base nil when both sides created it). The result is canonical JSON.
// The only error is a side that is not a JSON object, with a hint naming
// the manual fix.
func MergeFolderFile(p string, base, ours, theirs []byte) ([]byte, error) {
	bo, berr := decodeObject(base)
	oo, oerr := decodeObject(ours)
	to, terr := decodeObject(theirs)
	if berr != nil || oerr != nil || terr != nil {
		return nil, errs.New(errs.Stale, "rowfs: "+p+" is not a JSON object on one side, so it cannot be merged").
			WithHint("in the store clone, keep one valid version of " + p + " (git checkout --ours or --theirs), commit, then run `lore sync` again")
	}
	out := mergeKeys(bo, oo, to)
	switch path.Base(p) {
	case MetaFileName:
		if err := mergeMeta(out, bo, oo, to); err != nil {
			return nil, err
		}
	case PurgedFileName:
		if err := mergeList(out, purgedKeyIDs, bo, oo, to, rawString); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: encode merged "+p, err)
	}
	doc, err := canon.Document(raw)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: encode merged "+p, err)
	}
	return doc, nil
}

// mergeKeys is the generic three-way merge of every key.
func mergeKeys(b, o, t object) object {
	out := object{}
	keys := map[string]bool{}
	for _, m := range []object{b, o, t} {
		for k := range m {
			keys[k] = true
		}
	}
	for k := range keys {
		bv, inB := b[k]
		ov, inO := o[k]
		tv, inT := t[k]
		switch {
		case inO && inT && bytes.Equal(ov, tv):
			out[k] = ov
		case inB && inO && bytes.Equal(ov, bv):
			if inT {
				out[k] = tv // only theirs changed it
			}
		case inB && inT && bytes.Equal(tv, bv):
			if inO {
				out[k] = ov // only ours changed it
			}
		case inO && inT:
			out[k] = larger(ov, tv)
		case inO:
			out[k] = ov
		case inT:
			out[k] = tv
		}
	}
	return out
}

// larger is the canonically larger of two raw values (a choice every clone
// makes the same way).
func larger(a, b json.RawMessage) json.RawMessage {
	ca, aerr := canon.Document(a)
	cb, berr := canon.Document(b)
	if aerr != nil || berr != nil {
		if bytes.Compare(a, b) >= 0 {
			return a
		}
		return b
	}
	if bytes.Compare(ca, cb) >= 0 {
		return a
	}
	return b
}

// mergeMeta applies the _meta.json rules on top of mergeKeys.
func mergeMeta(out, b, o, t object) error {
	if pid, ok := pickProjectID(b, o, t); ok {
		out[metaKeyProjectID] = pid
	}
	if v := max(version(o), version(t), version(b)); v > 0 {
		out[metaKeyVersion] = json.RawMessage(strconv.Itoa(v))
	}
	if err := mergeList(out, metaKeyLinks, b, o, t, linkKey); err != nil {
		return err
	}
	// Q5 (PROV1): a store folder's _meta.json may carry a repo identity
	// too; its keys merge by ResolveRepoIdentity (keep ours on a fork merge,
	// older id + alias on a race), never by the generic "larger value".
	return mergeRepoMeta(out, o, t)
}

// pickProjectID is the project_id that existed first (see the file doc).
func pickProjectID(b, o, t object) (json.RawMessage, bool) {
	if v, ok := b[metaKeyProjectID]; ok {
		return v, true
	}
	ov, inO := o[metaKeyProjectID]
	tv, inT := t[metaKeyProjectID]
	switch {
	case inO && !inT:
		return ov, true
	case inT && !inO:
		return tv, true
	case !inO:
		return nil, false
	case bytes.Equal(ov, tv):
		return ov, true
	case version(o) == metaV1 && version(t) != metaV1:
		return ov, true
	case version(t) == metaV1 && version(o) != metaV1:
		return tv, true
	case bytes.Compare(ov, tv) <= 0:
		return ov, true
	}
	return tv, true
}

// version is an object's `_v` (0 when absent or not a number).
func version(o object) int {
	var v int
	if raw, ok := o[metaKeyVersion]; ok && json.Unmarshal(raw, &v) == nil {
		return v
	}
	return 0
}

// mergeList sets out[key] to the union of the three sides' lists, keyed
// by keyOf (first seen kept: ours, theirs, then base), sorted by key. A
// side whose value is not a list is skipped; no side with the key leaves
// it absent. An entry deleted on one side comes back (a union never
// forgets: links and purged ids only grow).
func mergeList(out object, key string, b, o, t object, keyOf func(json.RawMessage) string) error {
	seen := map[string]json.RawMessage{}
	present := false
	for _, side := range []object{o, t, b} {
		raw, ok := side[key]
		if !ok {
			continue
		}
		present = true
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) != nil {
			continue
		}
		for _, e := range list {
			k := keyOf(e)
			if _, dup := seen[k]; !dup {
				seen[k] = e
			}
		}
	}
	if !present {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	list := make([]json.RawMessage, 0, len(keys))
	for _, k := range keys {
		list = append(list, seen[k])
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode merged "+key, err)
	}
	out[key] = raw
	return nil
}

// linkSep joins a link's key parts (never inside a remote or an id).
const linkSep = "\x00"

// linkKey is a link entry's identity: remote and project_id; an entry that
// is not an object is keyed by its canonical bytes.
func linkKey(e json.RawMessage) string {
	var l struct {
		Remote    string `json:"remote"`
		ProjectID string `json:"project_id"`
	}
	if json.Unmarshal(e, &l) != nil || (l.Remote == "" && l.ProjectID == "") {
		return rawString(e)
	}
	return l.Remote + linkSep + l.ProjectID
}

// rawString keys a list entry by its canonical bytes.
func rawString(e json.RawMessage) string {
	if c, err := canon.Document(e); err == nil {
		return string(c)
	}
	return string(e)
}
