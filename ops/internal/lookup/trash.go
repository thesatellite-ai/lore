package lookup

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// Trasher is the optional Env upgrade that tells a trashed row from a gone
// one (engine.Engine.InTrash, wire.App.InTrash): E_IN_TRASH naming who
// deleted it and `lore trash restore <id>`, or nil.
type Trasher interface {
	InTrash(ctx context.Context, ref engine.Ref) error
}

// Goner is the optional Env upgrade that explains a tombstone a read found
// (engine.Engine.Gone, wire.App.Gone): E_IN_TRASH, E_PURGED or
// E_NOT_FOUND, never nil for a deleted row (INTEG-TRASH-4).
type Goner interface {
	Gone(ctx context.Context, ref engine.Ref) error
}

// Gone is the answer for a lookup whose row reads as deleted (Get found a
// tombstone, or E_NOT_FOUND): E_IN_TRASH when the row is in the trash
// (DELETION 3.2, one rule for every noun's show, edit and delete), else
// notFound, the noun's own "not found" error. An Env without the upgrade
// answers notFound.
//
//	show/edit/delete ─► env.Get ─► deleted? ─► Gone ─► InTrash? E_IN_TRASH : notFound
func Gone[E any](ctx context.Context, env E, ref engine.Ref, notFound error) error {
	if err := InTrash(ctx, env, ref); err != nil {
		return err
	}
	return notFound
}

// InTrash is E_IN_TRASH when the row ref names is in the trash, else nil
// (also nil for an Env without the upgrade). For a read that otherwise
// shows a tombstone as it is (row.show).
func InTrash[E any](ctx context.Context, env E, ref engine.Ref) error {
	t, ok := upgrade.As[Trasher](env)
	if !ok {
		return nil
	}
	return t.InTrash(ctx, ref)
}
