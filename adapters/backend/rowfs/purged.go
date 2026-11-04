package rowfs

// A folder's _purged.json (STORE_SERVER_SPEC 6.1: "ids purged on
// purpose"): the ids a purge removed for good, so neither an old clone
// (v0.1.17 honours the list on ADOPT) nor the core (engine refuses to
// write a live row under a listed id) ever brings one back. Format, shared
// with v0.1.17 (lsync meta.go encodePurged): a canonical JSON object
// {"_v": 1, "ids": [sorted ids]}; other keys a newer writer added are
// kept. Git merges it as a set union (MergeFolderFile).

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// purgedFormatV1 is the "_v" of a _purged.json (v0.1.17's FormatVersion).
const purgedFormatV1 = 1

// Purged returns the set of ids the folder's _purged.json lists (empty
// when the file is absent). A file that is not a JSON object, or whose
// "ids" is not a list, is an error and never an empty set: an empty set
// would let purged rows come back. Non-string entries name no row and are
// skipped (v0.1.17's reading).
func (f *Folder) Purged() (map[string]bool, error) {
	_, set, err := f.readPurged()
	return set, err
}

// readPurged returns the file's members (nil when absent) and its id set.
func (f *Folder) readPurged() (object, map[string]bool, error) {
	rel := f.rel + "/" + PurgedFileName
	if err := checkChain(f.root.dir, rel); err != nil {
		return nil, nil, err
	}
	b, err := readCapped(filepath.Join(f.dataDir, PurgedFileName), rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, map[string]bool{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	o, err := decodeObject(b)
	if err != nil {
		return nil, nil, errs.Wrap(errs.InvalidInput, "rowfs: "+rel+" is not a JSON object", err)
	}
	set := map[string]bool{}
	raw, has := o[purgedKeyIDs]
	if !has {
		return o, set, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, nil, errs.Wrap(errs.InvalidInput, "rowfs: "+rel+": \"ids\" is not a list of ids", err)
	}
	for _, item := range list {
		// Non-string entries are skipped, as before: only an id string
		// names a purged row.
		var s string
		if json.Unmarshal(item, &s) == nil {
			set[s] = true
		}
	}
	return o, set, nil
}

// ApplyPurge is Apply plus, in the same all-or-none batch, the ids of
// purged added to the folder's _purged.json (a.Paths names the file, so a
// git store commits it with the tombstones). changes may be empty: an id
// whose tombstone is already written is only listed. The caller holds the
// store lock, as for Apply.
func (f *Folder) ApplyPurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) (*Applied, error) {
	if len(purged) == 0 {
		return nil, errs.New(errs.InvalidInput, "rowfs: a purge names no id")
	}
	a := &Applied{}
	if len(changes) > 0 {
		var err error
		if a, err = f.Apply(ctx, changes); err != nil {
			return nil, err
		}
	}
	if err := f.addPurged(a, purged); err != nil {
		return nil, errors.Join(err, a.Undo())
	}
	return a, nil
}

// addPurged writes the union of the file's ids and add, recording the
// undo step and the path in a. A file already listing every id is left
// byte for byte (nothing to commit).
func (f *Folder) addPurged(a *Applied, add []ids.AnyID) error {
	o, set, err := f.readPurged()
	if err != nil {
		return err
	}
	grew := false
	for _, id := range add {
		if !set[id.String()] {
			set[id.String()], grew = true, true
		}
	}
	if !grew {
		return nil
	}
	if o == nil {
		o = object{}
	}
	if _, has := o[metaKeyVersion]; !has {
		o[metaKeyVersion] = json.RawMessage(strconv.Itoa(purgedFormatV1))
	}
	list := make([]string, 0, len(set))
	for id := range set {
		list = append(list, id)
	}
	slices.Sort(list)
	rawIDs, err := json.Marshal(list)
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode "+PurgedFileName, err)
	}
	o[purgedKeyIDs] = rawIDs
	raw, err := json.Marshal(o)
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode "+PurgedFileName, err)
	}
	doc, err := canon.Document(raw)
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode "+PurgedFileName, err)
	}
	full := filepath.Join(f.dataDir, PurgedFileName)
	prev, err := readCapped(full, f.rel+"/"+PurgedFileName)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := writeAtomic(full, doc); err != nil {
		return err
	}
	a.undo = append(a.undo, undoStep{full: full, prev: prev})
	a.Paths = append(a.Paths, f.rel+"/"+PurgedFileName)
	return nil
}
