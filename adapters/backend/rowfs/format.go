package rowfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// The format declaration in a folder's _meta.json (FORMAT_EVOLUTION rule
// 2): "features" (a set of names) and "min_lore_version" (the oldest lore
// that may write). Every write batch raises them in the SAME batch as the
// rows that need them (declare), so there is no window in which a row
// needs a feature the folder does not declare; the engine refuses writes
// the declaration forbids (engine/format.go). Every other key of the file
// is kept byte for byte.
//
//	Folder.Apply ─► ensureMeta ─► declare(rows) ─► Raise ─► _meta.json (+undo, +a.Paths)
//	engine.put   ─► Folder.Format ─► formatgate.Check ─► E_OLD_LORE

// Keys of the declaration (file format: they must not change).
const (
	MetaKeyFeatures = "features"
	MetaKeyMinLore  = "min_lore_version"
)

// decodeFormat reads the declaration from a decoded _meta.json. A key of
// the wrong shape is unreadable: it fails closed (FORMAT_EVOLUTION 4).
func decodeFormat(o object, rel string) (formatgate.Declared, error) {
	var d formatgate.Declared
	if raw, ok := o[MetaKeyFeatures]; ok {
		if err := json.Unmarshal(raw, &d.Features); err != nil {
			return formatgate.Declared{}, formatgate.Unreadable(rel, "features is not a list of strings")
		}
	}
	if raw, ok := o[MetaKeyMinLore]; ok {
		if err := json.Unmarshal(raw, &d.MinLoreVersion); err != nil {
			return formatgate.Declared{}, formatgate.Unreadable(rel, "min_lore_version is not a string")
		}
	}
	return d, nil
}

// readFormatFile reads the declaration of the _meta.json at full (rel is
// its store path, for messages). A missing file declares nothing; a file
// that does not parse (conflict markers, truncated) fails closed.
func readFormatFile(full, rel string) (d formatgate.Declared, doc []byte, exists bool, err error) {
	doc, err = os.ReadFile(filepath.Clean(full))
	if errors.Is(err, fs.ErrNotExist) {
		return formatgate.Declared{}, nil, false, nil
	}
	if err != nil {
		return formatgate.Declared{}, nil, false, errs.Wrap(errs.Internal, "rowfs: read "+rel, err)
	}
	o, derr := decodeObject(doc)
	if derr != nil {
		return formatgate.Declared{}, doc, true, unreadableDoc(rel, doc)
	}
	d, err = decodeFormat(o, rel)
	return d, doc, true, err
}

// Format is the folder's declaration (zero when its _meta.json is absent,
// e.g. a store root, whose identity is lore-store.yaml). An unreadable
// file is an E_OLD_LORE error: the folder is read-only until fixed.
func (f *Folder) Format(_ context.Context) (formatgate.Declared, error) {
	rel := f.storePath(MetaFileName)
	if err := checkChain(f.root.dir, rel); err != nil {
		return formatgate.Declared{}, err
	}
	d, _, _, err := readFormatFile(filepath.Join(f.dataDir, MetaFileName), rel)
	return d, err
}

// declare raises the folder's declaration to cover the rows of the batch
// (formatgate.Needs + the format steps' features), as part of the batch.
// A folder without _meta.json (a store root) declares nothing here.
func (f *Folder) declare(a *Applied, changes []store.Change) error {
	need, err := neededBy(changes)
	if err != nil || len(need) == 0 {
		return err
	}
	_, err = f.raise(a, need)
	return err
}

// raise adds need to the folder's declaration (see raiseFile).
func (f *Folder) raise(a *Applied, need []string) ([]string, error) {
	return raiseFile(f.root.dir, f.storePath(MetaFileName), filepath.Join(f.dataDir, MetaFileName), a, need, f.root.modes)
}

// raiseFile adds need to the declaration of the _meta.json at full (rel
// its path under root) and returns the names that were new. a, when set,
// records the undo step and the path; nil writes the file outside a batch.
// A missing file declares nothing (it is created by lore init or a
// folder's first write).
func raiseFile(root, rel, full string, a *Applied, need []string, m fileModes) ([]string, error) {
	if err := checkChain(root, rel); err != nil {
		return nil, err
	}
	cur, doc, exists, err := readFormatFile(full, rel)
	if err != nil || !exists {
		return nil, err
	}
	next, added := formatgate.Raise(cur, need)
	if len(added) == 0 {
		return nil, nil
	}
	o, err := decodeObject(doc)
	if err != nil {
		return nil, unreadableDoc(rel, doc)
	}
	if err = setFormat(o, next); err != nil {
		return nil, err
	}
	out, err := encodeObject(o, rel)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(out, doc) {
		return nil, nil
	}
	if err := writeAtomic(full, out, m); err != nil {
		return nil, err
	}
	if a != nil {
		a.undo = append(a.undo, undoStep{full: full, prev: doc, m: m})
		a.Paths = append(a.Paths, rel)
	}
	return added, nil
}

// ReadRepoFormat is the declaration of the checkout's .lore/data/_meta.json
// (top is the folder holding .lore); exists=false when there is none.
func ReadRepoFormat(top string) (formatgate.Declared, bool, error) {
	if err := checkChain(top, RepoMetaPath); err != nil {
		return formatgate.Declared{}, false, err
	}
	d, _, exists, err := readFormatFile(filepath.Join(top, LoreDir, DataDir, MetaFileName), RepoMetaPath)
	return d, exists, err
}

// DeclareRepo adds need to the checkout's _meta.json (lore migrate enable)
// and returns the names that were new; the file is committed with the
// person's next commit, like every .lore/data change. A checkout without
// _meta.json is E_NOT_FOUND (run lore init first).
func DeclareRepo(top string, need []string) ([]string, error) {
	if _, exists, err := ReadRepoFormat(top); err != nil {
		return nil, err
	} else if !exists {
		return nil, errs.New(errs.NotFound, "rowfs: "+RepoMetaPath+" does not exist").WithHint("run `lore init` in the repository first")
	}
	// A repo's _meta.json is committed: shared modes.
	return raiseFile(top, RepoMetaPath, filepath.Join(top, LoreDir, DataDir, MetaFileName), nil, need, sharedModes)
}

// neededBy is the features the rows of a batch use.
func neededBy(changes []store.Change) ([]string, error) {
	rows := make([]row.Row, 0, len(changes))
	var steps []string
	for _, c := range changes {
		r, err := c.Row.Decode()
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
		steps = append(steps, row.StepFeatures(r.Table, row.WriteVersion(r))...)
	}
	all, _ := formatgate.Raise(formatgate.Declared{Features: formatgate.NeedsAll(rows)}, steps)
	return all.Features, nil
}

// setFormat writes d into o (an empty declaration removes the keys).
func setFormat(o object, d formatgate.Declared) error {
	if len(d.Features) == 0 {
		delete(o, MetaKeyFeatures)
	} else {
		raw, err := json.Marshal(d.Features)
		if err != nil {
			return errs.Wrap(errs.Internal, "rowfs: encode features", err)
		}
		o[MetaKeyFeatures] = raw
	}
	if d.MinLoreVersion == "" {
		delete(o, MetaKeyMinLore)
		return nil
	}
	raw, err := json.Marshal(d.MinLoreVersion)
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode min_lore_version", err)
	}
	o[MetaKeyMinLore] = raw
	return nil
}

// encodeObject is o as a canonical document.
func encodeObject(o object, rel string) ([]byte, error) {
	raw, err := json.Marshal(o)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: encode "+rel, err)
	}
	doc, err := canon.Document(raw)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: encode "+rel, err)
	}
	return doc, nil
}

// mergeFormat applies formatgate.Merge to a merged _meta.json: features as
// a set, the floor the higher version (never the generic "larger value",
// which would put 0.1.9 above 0.1.19). An unreadable side keeps what
// mergeKeys made; doctor reports it.
func mergeFormat(out, o, t object) error {
	_, inO := o[MetaKeyFeatures]
	_, inT := t[MetaKeyFeatures]
	_, fO := o[MetaKeyMinLore]
	_, fT := t[MetaKeyMinLore]
	if !inO && !inT && !fO && !fT {
		return nil
	}
	od, oerr := decodeFormat(o, MetaFileName)
	td, terr := decodeFormat(t, MetaFileName)
	if oerr != nil || terr != nil {
		return nil
	}
	return setFormat(out, formatgate.Merge(od, td))
}

// unreadableDoc is the fail-closed error of a format file doc that is not
// a JSON object: a conflict-marked file (a stopped merge) says so and
// names the way out (formatgate.UnreadableConflict).
func unreadableDoc(rel string, doc []byte) error {
	if merge3.ContainsConflictMarkers(string(doc)) {
		return formatgate.UnreadableConflict(rel)
	}
	return formatgate.Unreadable(rel, "not a JSON object")
}
