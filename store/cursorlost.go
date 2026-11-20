package store

import "context"

// CursorChecker is an optional Backend feature: it tells a cursor that no
// longer describes a past state of the backend's rows. A store clone reset
// by hand (git reset --hard to an older commit, a clone made again) leaves
// the cache's cursor on a commit HEAD does not descend from; a Read from it
// lists only the files changed since, and a row file the reset dropped is
// not a delete (only tombstones are), so the cache would keep the dropped
// rows for ever (owner P-5, devco D-763). The engine asks before each
// refresh and rebuilds the source's cache from the zero cursor when the
// answer is yes. A backend without history never implements it.
type CursorChecker interface {
	// CursorLost reports whether cursor names a state the backend's
	// current rows do not descend from. The zero cursor is never lost.
	CursorLost(ctx context.Context, cursor Cursor) (bool, error)
}
