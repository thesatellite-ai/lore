package git

// layerrowmerge.go — the one merge of a branch's copy of a row over the
// copy a target holds now, against the base the branch copied it from:
// lore branch promote (the shared notes), lore branch land --into (the
// parent layer) and lore sync promote --to <branch> (a repo's own row
// files, onto another code branch) all merge through MergeRowOver. Fields
// only the target changed keep the target's value, fields only the branch
// changed take the branch's, and a text both changed stops the write
// unless --take chose a side (#155: promote wrote the branch's whole row
// over newer edits on main, so an archive, metadata, a newer body and a
// done status were lost silently).

import "bytes"

// RowMerge is how one branch copy of a row met the target's copy.
type RowMerge int

const (
	// RowMergeBranch: the target holds the base's copy (or none), so the
	// branch's copy is written as it is.
	RowMergeBranch RowMerge = iota
	// RowMergeSame: nothing to write (both agree, or only the target
	// changed the row).
	RowMergeSame
	// RowMergeFields: both changed the row, merged field by field.
	RowMergeFields
	// RowMergeTaken: both changed the same text; --take chose the side
	// of those fields, every other field still merged.
	RowMergeTaken
	// RowMergeClash: both changed the same text and no side was chosen;
	// nothing may be written.
	RowMergeClash
)

// mergeLayerRow is MergeRowOver with this backend's Merger.
func (b *Backend) mergeLayerRow(base, ours, theirs []byte, take *bool) ([]byte, RowMerge, []string, error) {
	return MergeRowOver(b.cfg.merger, base, ours, theirs, take)
}

// MergeRowOver merges theirs (the branch's copy) over ours (the target's
// copy now, nil when it has none) against base (the copy the branch
// started from, nil when the branch added the row), with m (nil = no
// field merge: any change on both sides clashes). take nil chooses no
// side; else true keeps the branch's text of the clashing fields, false
// the target's. It returns the document to write (nil for RowMergeSame
// and RowMergeClash) and, for a clash, the clashing fields when m can
// name them. Exported so every command that writes one branch's copy of a
// row over another's shares these rules instead of copying them (H-207).
func MergeRowOver(m Merger, base, ours, theirs []byte, take *bool) ([]byte, RowMerge, []string, error) {
	switch {
	case ours == nil || bytes.Equal(ours, base):
		return theirs, RowMergeBranch, nil, nil
	case bytes.Equal(ours, theirs) || bytes.Equal(theirs, base):
		return nil, RowMergeSame, nil, nil
	case m == nil:
		if take == nil {
			return nil, RowMergeClash, nil, nil
		}
		if *take {
			return theirs, RowMergeTaken, nil, nil
		}
		return ours, RowMergeTaken, nil, nil
	}
	merged, conflict, err := m.Merge(base, ours, theirs)
	if err != nil {
		return nil, RowMergeClash, nil, err
	}
	if !conflict {
		if bytes.Equal(merged, ours) {
			// the target already holds the merge (an earlier promote of the
			// same copy): nothing to write, not counted as merged again
			return nil, RowMergeSame, nil, nil
		}
		return merged, RowMergeFields, nil, nil
	}
	if take == nil {
		return nil, RowMergeClash, ClashFields(m, base, ours, theirs), nil
	}
	if pm, ok := m.(PreferMerger); ok {
		// only the clashing fields take the chosen side (devco V-14)
		pm2, perr := pm.MergePrefer(base, ours, theirs, *take)
		if perr != nil {
			return nil, RowMergeClash, nil, perr
		}
		return pm2, RowMergeTaken, nil, nil
	}
	if *take {
		return theirs, RowMergeTaken, nil, nil
	}
	return ours, RowMergeTaken, nil, nil
}

// ClashFields names the fields whose text both sides changed, when m can
// (ClashNamer); nil otherwise.
func ClashFields(m Merger, base, ours, theirs []byte) []string {
	cn, ok := m.(ClashNamer)
	if !ok || ours == nil || theirs == nil {
		return nil
	}
	f, err := cn.Clashes(base, ours, theirs)
	if err != nil {
		return nil
	}
	return f
}
