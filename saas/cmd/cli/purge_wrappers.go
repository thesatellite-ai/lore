// purge_wrappers.go — engine.Purger through the CLI's backend wrappers.
//
// Why it exists: the engine purges (tombstone + _purged.json in one batch)
// only in a backend that is an engine.Purger. The repo store is wrapped by
// liveBackend (live rows) and a store clone by storePushBackend (push
// after write), and a wrapper that does not pass Purger through made every
// purge answer E_UNSUPPORTED "cannot record purged ids" even though the
// row folder underneath records them (lore sync purge of a core row).
package main

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

var (
	_ engine.Purger = (*liveBackend)(nil)
	_ engine.Purger = (*storePushBackend)(nil)
)

// errNoPurge is the answer of a wrapper whose inner backend cannot purge.
func errNoPurge() error {
	return errs.New(errs.Unsupported, "this store cannot record purged ids")
}

// WritePurge writes changes through the live routing (Write), then lists
// purged in the inner backend's _purged.json.
func (b *liveBackend) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error {
	p, ok := b.inner.(engine.Purger)
	if !ok {
		return errNoPurge()
	}
	if len(changes) > 0 {
		if err := b.Write(ctx, changes); err != nil {
			return err
		}
	}
	return p.WritePurge(ctx, nil, purged)
}

// Purged is the inner backend's list (empty when it keeps none).
func (b *liveBackend) Purged(ctx context.Context) (map[string]bool, error) {
	if p, ok := b.inner.(engine.Purger); ok {
		return p.Purged(ctx)
	}
	return map[string]bool{}, nil
}

// WritePurge purges in the store clone in one batch, then pushes like
// Write.
func (b *storePushBackend) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error {
	p, ok := b.inner.(engine.Purger)
	if !ok {
		return errNoPurge()
	}
	if err := p.WritePurge(ctx, changes, purged); err != nil {
		return err
	}
	b.pushAfterWrite(ctx)
	return nil
}

// Purged is the inner backend's list (empty when it keeps none).
func (b *storePushBackend) Purged(ctx context.Context) (map[string]bool, error) {
	if p, ok := b.inner.(engine.Purger); ok {
		return p.Purged(ctx)
	}
	return map[string]bool{}, nil
}
