package git

// layerrowmerge.go — the one merge of a branch layer's copy of a row over
// the copy a target holds now (the shared notes for lore branch promote,
// the parent layer for lore branch land --into), against the layer base
// the branch copied it from. Fields only the target changed keep the
// target's value, fields only the branch changed take the branch's, and a
// text both changed stops the write unless --take chose a side (#155:
// promote wrote the branch's whole row over newer edits on main, so an
// archive, metadata, a newer body and a done status were lost silently).

import "bytes"

// layerMerge is how one branch layer row met the target's copy.
type layerMerge int

const (
	// layerMergeBranch: the target holds the base's copy (or none), so the
	// branch's copy is written as it is.
	layerMergeBranch layerMerge = iota
	// layerMergeSame: nothing to write (both agree, or only the target
	// changed the row).
	layerMergeSame
	// layerMergeFields: both changed the row, merged field by field.
	layerMergeFields
	// layerMergeTaken: both changed the same text; --take chose the side
	// of those fields, every other field still merged.
	layerMergeTaken
	// layerMergeClash: both changed the same text and no side was chosen;
	// nothing may be written.
	layerMergeClash
)

// mergeLayerRow merges theirs (the branch layer's copy) over ours (the
// target's copy now, nil when it has none) against base (the layer base's
// copy, nil when the branch added the row). take nil chooses no side; else
// true keeps the branch's text of the clashing fields, false the target's.
// It returns the document to write (nil for layerMergeSame and
// layerMergeClash) and, for a clash, the clashing fields when the Merger
// can name them.
func (b *Backend) mergeLayerRow(base, ours, theirs []byte, take *bool) ([]byte, layerMerge, []string, error) {
	switch {
	case ours == nil || bytes.Equal(ours, base):
		return theirs, layerMergeBranch, nil, nil
	case bytes.Equal(ours, theirs) || bytes.Equal(theirs, base):
		return nil, layerMergeSame, nil, nil
	case b.cfg.merger == nil:
		if take == nil {
			return nil, layerMergeClash, nil, nil
		}
		if *take {
			return theirs, layerMergeTaken, nil, nil
		}
		return ours, layerMergeTaken, nil, nil
	}
	m, conflict, err := b.cfg.merger.Merge(base, ours, theirs)
	if err != nil {
		return nil, layerMergeClash, nil, err
	}
	if !conflict {
		return m, layerMergeFields, nil, nil
	}
	if take == nil {
		return nil, layerMergeClash, b.clashFields(base, ours, theirs), nil
	}
	if pm, ok := b.cfg.merger.(PreferMerger); ok {
		// only the clashing fields take the chosen side (devco V-14)
		pm2, perr := pm.MergePrefer(base, ours, theirs, *take)
		if perr != nil {
			return nil, layerMergeClash, nil, perr
		}
		return pm2, layerMergeTaken, nil, nil
	}
	if *take {
		return theirs, layerMergeTaken, nil, nil
	}
	return ours, layerMergeTaken, nil, nil
}
