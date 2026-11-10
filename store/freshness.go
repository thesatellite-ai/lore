package store

import (
	"context"
	"time"
)

// Freshness is an optional Backend capability (RES 3.6, P2-RES-7): when
// this machine last reached the store's remote and the commit its local
// copy holds. A backend without a remote (folder) does not implement it,
// so callers report "no stamp" for it.
type Freshness interface {
	// Fresh returns the stamp; Known is false when the store was never
	// fetched on this machine (or the stamp cannot be read).
	Fresh(ctx context.Context) (Fresh, error)
}

// Fresh is one store's fetch stamp.
type Fresh struct {
	Known     bool
	FetchedAt time.Time // the last successful fetch (UTC)
	Commit    string    // the local copy's HEAD commit ("" when unknown)
}
