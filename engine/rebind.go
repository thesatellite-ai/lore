package engine

import (
	"context"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// writeMark records that the engine has begun a write (refuseNeverClient
// marks it on every write path). Its only reader is RebindActor.
type writeMark struct{ b atomic.Bool }

func (m *writeMark) mark()      { m.b.Store(true) }
func (m *writeMark) done() bool { return m.b.Load() }

// RebindActor changes the actor every write is stamped with (WithActor)
// to a, with a fresh HLC clock for a that is seeded above every stamp the
// old clock issued.
//
// Why it exists (TEAM_PROFILES 6.3, TP-4): wire fixes the writing actor
// from the identity chain before any store is open (identity.ActorID of
// the stable key), while the identity contacts that say "this second
// email is Mira" live in the org roster store and are readable only after
// the caches are refreshed. wire therefore builds the engine, refreshes,
// asks logic/identmatch, and rebinds here, so the second email never
// becomes a second actor.
//
// Invariant: it is called only during open, before the engine has written
// anything and before the engine is shared between goroutines. A rebind
// after a write would leave rows stamped by two actors for one command, so
// it is E_INTERNAL (never hit by a correct open), and the actor is left
// unchanged.
func (e *Engine) RebindActor(a ids.ID[ids.Actor]) error {
	if a.IsZero() {
		return errs.New(errs.Internal, "rebind to an empty actor")
	}
	if e.written.done() {
		return errs.New(errs.Internal, "the writing actor cannot change after a write (rebind to "+a.String()+")").
			WithHint("this is a bug in lore: report it with the command you ran")
	}
	if a == e.actor {
		return nil
	}
	seed := e.clock.Now()
	e.actor = a
	e.clock = hlc.NewClock(a, e.wall)
	e.clock.Now(seed)
	return nil
}

// Actor returns the actor writes are stamped with when the caller names
// none (WithActor, or the last RebindActor).
func (e *Engine) Actor() ids.ID[ids.Actor] { return e.actor }

// RefuseWrites makes every later write to the attached store name answer
// err (Attached.WriteRefused); reads keep working. Why it exists: wire
// learns that the caller was removed from the org (the roster's actor row
// says left or archived, B2-12) only after the caches are refreshed, so
// it marks the org's stores here during open, as RebindActor does for the
// actor. An unknown name or a nil err changes nothing.
//
// Invariant: called only during open, before the engine is shared between
// goroutines (the stores map is not locked).
func (e *Engine) RefuseWrites(name store.Source, err error) { e.RefuseWritesUnless(name, err, nil) }

// RefuseWritesUnless is RefuseWrites whose refusal lifted answers for a
// write's ctx: true lets that write through (nil: never).
func (e *Engine) RefuseWritesUnless(name store.Source, err error, lifted func(context.Context) bool) {
	a, ok := e.stores[name]
	if !ok || err == nil {
		return
	}
	a.WriteRefused, a.WriteRefusedLifted = err, lifted
	e.stores[name] = a
}
