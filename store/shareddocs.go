package store

import "context"

// A shared-documents promise lets a backend hand out the document bytes it
// keeps (its read cache) instead of a copy of each. Without it a backend
// never hands out its own buffer (storetest), so a caller may change what
// Read returns. The engine's read snapshot (engine.WithReadSnapshot) never
// writes into a document and makes the promise for its fills. Why (bug
// hunt H-277, devco:D-1015): the git backend kept a copy of its last full
// read and copied it again for every reader, so lore doctor held each
// store's documents two or three times: ~120 MB of copies at 100k commits.
type sharedDocsKey struct{}

// WithSharedDocs returns ctx with the promise that the caller never writes
// into the documents a Read returns.
func WithSharedDocs(ctx context.Context) context.Context {
	return context.WithValue(ctx, sharedDocsKey{}, true)
}

// SharedDocs reports whether ctx carries the WithSharedDocs promise.
func SharedDocs(ctx context.Context) bool {
	v, _ := ctx.Value(sharedDocsKey{}).(bool)
	return v
}
