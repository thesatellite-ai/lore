package merge3

import "fmt"

// File-level merge (LORE_TEAM_SPEC.md F4 step 1, C12).
//
// Merge settles keys of two versions of one object. Before that can run, a
// caller merging rows must settle the FILE: one side may have added it,
// deleted it, or written a tombstone (a file that says "deleted" so lookups
// do not fall through to an older copy). MergeFile decides that first and
// only calls Merge when both sides hold an edited row, so the outcome is
// always a whole file — one side's row, a field-merged row, a tombstone or
// nothing — and never half a row.
//
// What counts as a tombstone is the caller's decision (lore marks it with
// `_live.deleted: true`); MergeFile only sees the FileKind it is given.

// FileKind says what one side holds at the row's path.
type FileKind int

const (
	// FileAbsent means no file at that path.
	FileAbsent FileKind = iota
	// FileRow means a live or branch row.
	FileRow
	// FileTombstone means a file recording that the row was deleted.
	FileTombstone
)

// String names the kind for traces and test failures.
func (k FileKind) String() string {
	switch k {
	case FileAbsent:
		return "absent"
	case FileRow:
		return "row"
	case FileTombstone:
		return "tombstone"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// File is one side's version of a row's file.
//
// Invariant: Doc is nil exactly when Kind is FileAbsent.
type File struct {
	Kind FileKind
	Doc  map[string]any
}

// Absent is the "no file" side.
func Absent() File { return File{Kind: FileAbsent} }

// Row wraps a row document.
func Row(doc map[string]any) File { return File{Kind: FileRow, Doc: doc} }

// Tombstone wraps a tombstone document.
func Tombstone(doc map[string]any) File { return File{Kind: FileTombstone, Doc: doc} }

// deleted reports whether the side removed the row (no file or tombstone).
func (f File) deleted() bool { return f.Kind != FileRow }

// FileDecision names the rule that settled a file merge, so `lore explain
// --merge` and --trace can say why a version won.
type FileDecision int

const (
	// DecisionSame: both sides hold the same file.
	DecisionSame FileDecision = iota
	// DecisionOursOnly: only ours changed it (or only ours added it).
	DecisionOursOnly
	// DecisionTheirsOnly: only theirs changed it (or only theirs added it).
	DecisionTheirsOnly
	// DecisionFieldMerge: both edited (or both added) a row; Merge settled
	// the keys (an add/add merges with an empty base).
	DecisionFieldMerge
	// DecisionModifyBeatsDelete: one side deleted the row, the other edited
	// it; the edited row is kept, the deletion dropped (F4: modify wins).
	DecisionModifyBeatsDelete
	// DecisionBothDeleted: both sides removed it, differently (one by a
	// tombstone, or two different tombstones); the result is a tombstone.
	DecisionBothDeleted
)

// String names the decision.
func (d FileDecision) String() string {
	switch d {
	case DecisionSame:
		return "same"
	case DecisionOursOnly:
		return "ours-only"
	case DecisionTheirsOnly:
		return "theirs-only"
	case DecisionFieldMerge:
		return "field-merge"
	case DecisionModifyBeatsDelete:
		return "modify-beats-delete"
	case DecisionBothDeleted:
		return "both-deleted"
	}
	return fmt.Sprintf("decision(%d)", int(d))
}

// FileResult is the outcome of MergeFile.
type FileResult struct {
	// File is the merged file. A FileRow result whose Fields has conflicts
	// holds conflict markers (see Result).
	File File
	// Decision is the rule that settled it.
	Decision FileDecision
	// DeletedBy is the side whose deletion lost (DecisionModifyBeatsDelete
	// only), so the caller can print "deleted by X but edited by Y; kept".
	DeletedBy Side
	// Fields holds the key-level outcome for DecisionFieldMerge (zero
	// otherwise).
	Fields Result
}

// Conflicted reports whether a human must finish the merged row.
func (r FileResult) Conflicted() bool { return len(r.Fields.Conflicts) > 0 }

// MergeFile three-way merges one row's file. Rules, in order:
//
//  1. ours and theirs are the same file → that file;
//  2. only one side differs from base → that side (an add on one side, a
//     delete of an unchanged row, an edit);
//  3. both edited (or both added) a row → Merge, with base's doc when base
//     was a row and an empty base otherwise;
//  4. one side holds an edited row and the other deleted it → the row
//     (modify beats delete);
//  5. both deleted it differently → a tombstone (the EARLIER side's, i.e.
//     not Newer, when both wrote one: DEL-TOMB2).
//
// The result is always one whole file; base is used only to tell what each
// side changed.
func MergeFile(base, ours, theirs File, p Policy) (FileResult, error) {
	for _, f := range []File{base, ours, theirs} {
		if (f.Kind == FileAbsent) != (f.Doc == nil) {
			return FileResult{}, fmt.Errorf("merge3: %v file with doc %v", f.Kind, f.Doc != nil)
		}
	}
	oEqT, err := sameFile(ours, theirs)
	if err != nil {
		return FileResult{}, err
	}
	if oEqT {
		return FileResult{File: ours, Decision: DecisionSame}, nil
	}
	oEqB, err := sameFile(ours, base)
	if err != nil {
		return FileResult{}, err
	}
	if oEqB {
		return FileResult{File: theirs, Decision: DecisionTheirsOnly}, nil
	}
	tEqB, err := sameFile(theirs, base)
	if err != nil {
		return FileResult{}, err
	}
	if tEqB {
		return FileResult{File: ours, Decision: DecisionOursOnly}, nil
	}
	switch {
	case !ours.deleted() && !theirs.deleted():
		var baseDoc map[string]any
		if base.Kind == FileRow {
			baseDoc = base.Doc
		}
		fields, err := Merge(baseDoc, ours.Doc, theirs.Doc, p)
		if err != nil {
			return FileResult{}, err
		}
		return FileResult{File: Row(fields.Merged), Decision: DecisionFieldMerge, Fields: fields}, nil
	case !ours.deleted():
		return FileResult{File: ours, Decision: DecisionModifyBeatsDelete, DeletedBy: SideTheirs}, nil
	case !theirs.deleted():
		return FileResult{File: theirs, Decision: DecisionModifyBeatsDelete, DeletedBy: SideOurs}, nil
	}
	// Both deleted and they differ, so at least one wrote a tombstone.
	// DEL-TOMB2 (DELETION 3.7): two tombstones keep the EARLIER one (the
	// side that is not Newer), so the first deleter and time stay on record,
	// the same rule as the core merge (logic/merge).
	winner := ours
	if theirs.Kind == FileTombstone && (ours.Kind != FileTombstone || p.Newer == SideOurs) {
		winner = theirs
	}
	return FileResult{File: winner, Decision: DecisionBothDeleted}, nil
}

// sameFile compares kind and content (canonical JSON, so key order and
// number spelling do not matter).
func sameFile(a, b File) (bool, error) {
	if a.Kind != b.Kind {
		return false, nil
	}
	if a.Kind == FileAbsent {
		return true, nil
	}
	return samePresence(presence{ok: true, val: a.Doc}, presence{ok: true, val: b.Doc})
}
