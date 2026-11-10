// Package settings defines the operations of the settings nouns (layer L5,
// STORE_SERVER_SPEC 4.3, 4.4, 9.6; CLI_REFERENCE 3.1, 3.2, 3.11-3.13):
// resource kinds and their fields, resources, project variables, policy
// rows, metadata keys and values, and secret rules and allow entries.
//
// Why one package: every one of these nouns is "settings rows" (SETTINGS
// ARE DATA): small synced tables written at a level (repo, store folder,
// org folder, store root, personal store) through the engine's one write
// path. They share the same lookups (a row by name across the attached
// stores, the level of a store, a typed value parsed from CLI text), so
// they share one Env and one set of helpers instead of six copies.
//
// Invariant: every write goes through Env.Create, Update or Delete (scan,
// validate, policy, audit); nothing here writes a file.
package settings

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// Env is the engine row API the settings operations use (wire.App
// implements it, as for memory).
type Env interface {
	Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error)
	Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error)
	Delete(ctx context.Context, ref engine.Ref) (engine.Written, error)
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	// Stores lists the attached stores in precedence order.
	Stores() []store.Source
}
