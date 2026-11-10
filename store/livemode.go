package store

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
)

// LiveMode is one write's --live / --no-live choice (PENDING-60, v0.1.17
// LORE_TEAM_SPEC §1 "Flags"): where a row of a git repo store is shared.
// The engine puts it on the context of the operation that took the flag
// (engine.LiveFlags); a backend that keeps a live ref (the CLI's live
// overlay of the repo store) reads it in Write. Every other backend
// ignores it: a store without a live ref has one place for a row.
type LiveMode uint8

const (
	// LiveDefault is no flag: the kind's default applies (v0.1.17
	// live.defaults: tasks, missions, handoffs, comments go live; rules,
	// memories and the rest stay with the code), and an existing row
	// stays where it is.
	LiveDefault LiveMode = iota
	// LiveOn is --live: the row goes on the live ref at once; an edit of a
	// branch row moves it into live.
	LiveOn
	// LiveOff is --no-live: a new row stays with the code (this branch's
	// .lore/data). A row that is already live stays live (live wins).
	LiveOff
)

// String is the flag spelling ("default" for LiveDefault).
func (m LiveMode) String() string {
	switch m {
	case LiveOn:
		return "--live"
	case LiveOff:
		return "--no-live"
	case LiveDefault:
	}
	return "default"
}

// LiveChoice is the live mode of one operation and the table it applies
// to: the operation's own kind (`lore task add --live` sends the task
// live, not an actor or tag row the same write creates; those follow the
// companion rule of the live overlay).
type LiveChoice struct {
	Mode  LiveMode
	Table row.Table
}

// For returns the mode that applies to a row of table (LiveDefault for
// any other table).
func (c LiveChoice) For(table row.Table) LiveMode {
	if c.Table == "" || c.Table != table {
		return LiveDefault
	}
	return c.Mode
}

type liveChoiceKey struct{}

// WithLiveChoice returns ctx carrying c (a LiveDefault choice is not
// stored: it is what LiveChoiceFrom returns without one).
func WithLiveChoice(ctx context.Context, c LiveChoice) context.Context {
	if c.Mode == LiveDefault {
		return ctx
	}
	return context.WithValue(ctx, liveChoiceKey{}, c)
}

// LiveChoiceFrom returns the choice on ctx (zero: LiveDefault, no table).
func LiveChoiceFrom(ctx context.Context) LiveChoice {
	c, _ := ctx.Value(liveChoiceKey{}).(LiveChoice)
	return c
}
