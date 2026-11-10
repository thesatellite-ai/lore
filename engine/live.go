package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// LiveFlags are --live / --no-live (PENDING-60: back exactly as in
// v0.1.17 on `task|rule|memory add|edit`). An input embeds it; Run reads
// the two bools from any input (bindInputLive), so no operation handles
// them: the choice rides on the context to the backend write as a
// store.LiveChoice for the operation's own table. Both set is
// E_INVALID_INPUT (v0.1.17: cobra's mutually exclusive flag group).
type LiveFlags struct {
	Live   bool `json:"live,omitempty" flag:"live" desc:"share this row with everyone at once on the live ref (refs/lore/live)"`
	NoLive bool `json:"no_live,omitempty" flag:"no-live" desc:"keep this row with the code (this branch's .lore/data)"`
}

// Input field names of LiveFlags.
const (
	inputLive   = "live"
	inputNoLive = "no_live"
)

// bindInputLive is Run's step: an input's --live / --no-live becomes the
// context's store.LiveChoice for the operation's table (opTable).
func bindInputLive(ctx context.Context, in op.Input) (context.Context, error) {
	on, _ := in.Bool(inputLive)
	off, _ := in.Bool(inputNoLive)
	switch {
	case on && off:
		return nil, errs.New(errs.InvalidInput, "--live and --no-live cannot be used together")
	case on:
		return store.WithLiveChoice(ctx, store.LiveChoice{Mode: store.LiveOn, Table: opTable(in.Op())}), nil
	case off:
		return store.WithLiveChoice(ctx, store.LiveChoice{Mode: store.LiveOff, Table: opTable(in.Op())}), nil
	}
	return ctx, nil
}
