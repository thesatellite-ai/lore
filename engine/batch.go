package engine

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// Batch is one bulk action (XQ1): every row it writes carries its one
// bat_ id and its kind on the audit event, so `lore batch list|show` read
// batches from the batch log; a batch is never a row.
type Batch struct {
	ID   ids.ID[ids.Batch]
	Kind string
}

// IsZero reports "no batch".
func (b Batch) IsZero() bool { return b.ID.IsZero() }

// batchKey is the context key of the current batch.
type batchKey struct{}

// BatchTrailer is the commit trailer key naming the bulk action a store
// commit belongs to (XQ1, r3-batch): "Lore-Batch: bat_… <kind>".
const BatchTrailer = "Lore-Batch"

// WithBatch marks every write made with ctx as part of b (XQ1).
func WithBatch(ctx context.Context, b Batch) context.Context {
	return context.WithValue(ctx, batchKey{}, b)
}

// withBatchTrailer attaches the Lore-Batch commit trailer of ctx's batch
// (XQ1, r3-batch), so a backend that commits (a git store clone) names the
// bulk action in its commit; a backend without commits ignores it. It is
// skipped when ctx already carries a trailer naming the same bat_ id (a
// soft delete's Lore-Delete-Batch), so a commit never names it twice.
func withBatchTrailer(ctx context.Context) context.Context {
	b, ok := BatchOf(ctx)
	if !ok {
		return ctx
	}
	for _, t := range store.CommitTrailers(ctx) {
		if strings.Contains(t, b.ID.String()) {
			return ctx
		}
	}
	line := BatchTrailer + ": " + b.ID.String()
	if b.Kind != "" && !strings.ContainsAny(b.Kind, "\n\r") {
		line += " " + b.Kind
	}
	return store.WithCommitTrailers(ctx, line)
}

// BatchOf returns the batch ctx carries.
func BatchOf(ctx context.Context) (Batch, bool) {
	b, ok := ctx.Value(batchKey{}).(Batch)
	return b, ok && !b.IsZero()
}

// importKey is the context key of the current import's _o.import slot.
type importKey struct{}

// WithImport sets the _o.import slot of every row created with ctx
// (XQ9 / PROV9: an importer passes its source and the external id).
func WithImport(ctx context.Context, imp origin.Import) context.Context {
	return context.WithValue(ctx, importKey{}, imp)
}

// importOf returns the import slot ctx carries.
func importOf(ctx context.Context) (origin.Import, bool) {
	i, ok := ctx.Value(importKey{}).(origin.Import)
	return i, ok && !i.IsZero()
}

// Confirmed reports whether the running operation's caller already
// confirmed (--yes, "yes": true). XQ2: a bulk action decides only after
// counting its rows whether it must ask, so it cannot use the spec's
// up-front ConfirmRule; it answers E_NEEDS_CONFIRM itself above
// batch.preview_rows unless this is true (the CLI then prompts a terminal
// and re-runs with yes; REST passes the request's yes). Outside Run: false.
func Confirmed(ctx context.Context) bool {
	c, ok := ctx.Value(callKey{}).(call)
	return ok && c.caller.Yes
}
