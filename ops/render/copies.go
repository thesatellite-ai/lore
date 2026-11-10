package render

// copies.go — the generated copies `lore render` keeps (SOURCE_OF_TRUTH
// rows 10 to 12): .lore/LORE.md and LORE.full.md, the private per-store
// files and their import lines, and the tools' pointer files. Each is a
// copy of rows (or of the context.targets setting); the host compares
// each with what its source would give now, and the doctor turns every
// difference into a warning with the command that refreshes it.

import "context"

// CopyKind says how a generated copy differs from its source.
type CopyKind string

// Copy kinds.
const (
	// CopyStale: the file's stamp names other rows or settings than the
	// source has now (a row was added, edited or removed since render).
	CopyStale CopyKind = "stale"
	// CopyEdited: the stamp is current but the bytes differ (edited by hand).
	CopyEdited CopyKind = "edited"
	// CopyMissing: render would write the file, and it is not there.
	CopyMissing CopyKind = "missing"
	// PointerOrphan: a tool file carries lore's pointer but its tool is
	// not in context.targets.
	PointerOrphan CopyKind = "pointer_orphan"
	// PointerMissing: a tool in context.targets has no pointer in its file.
	PointerMissing CopyKind = "pointer_missing"
)

// CopyFinding is one generated copy that does not match its source.
type CopyFinding struct {
	Kind CopyKind `json:"kind"`
	// File is the copy's path (relative to the repo root, or ~/… for a
	// private file).
	File string `json:"file"`
	// Message is the human sentence (without the fix).
	Message string `json:"message"`
	// Fix is the one command that refreshes the copy.
	Fix string `json:"fix"`
}

// CopyChecker is an optional upgrade of Host: the binary that renders
// also compares the copies with their sources for `lore doctor`. root is
// the repo's top folder. A repo that never rendered (no .lore/LORE.md) has
// no copies to compare: nil.
type CopyChecker interface {
	RenderedCopies(ctx context.Context, root string) ([]CopyFinding, error)
}
