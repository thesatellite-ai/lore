package store

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// RowHistory is the OPTIONAL capability "old versions of one row can be read
// back" (Capabilities.History; TASK 17.2 `task history --git`, METADATA 3.19
// `meta history`, RESOURCES 3.22 `resource history`). It is never part of the
// Backend contract: a caller type-asserts it on the concrete backend and,
// when absent, answers E_UNSUPPORTED with a hint instead of an empty list
// that would read as "no older changes".
//
//	ops (history --git) ─► wire ─► concrete backend.(RowHistory)
//	                                 └─ git: git log over the row file
//	                                    + one cat-file batch for the blobs
type RowHistory interface {
	// Versions returns the committed versions of one row file, oldest
	// first. A row with no committed version returns an empty list and no
	// error (a row written but never committed has no history yet).
	Versions(ctx context.Context, table row.Table, id ids.AnyID) ([]RowVersion, error)
}

// RowVersion is one committed version of a row file.
//
// Invariant: Row.Doc is the file's bytes at Commit, byte for byte, and
// Row.Table/ID name the asked row. A version whose file could not be
// decoded (hand-edited, conflict markers) is left out, never guessed.
type RowVersion struct {
	Commit string    // full commit id
	At     time.Time // the commit's author time
	Author string    // the commit author's name ("" when git has none)
	Email  string    // the commit author's email
	Row    Row
}
