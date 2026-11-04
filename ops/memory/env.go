// Package memory defines the `memory` noun's operations (STORE_SERVER_SPEC
// 9.1, layer L5): add, list, show, edit, delete and search. Each is an
// op.Define spec with typed input and output; the CLI commands, the REST
// routes and the generated reference are all built from these specs.
//
// Why an Env and not the engine directly: the operations need the engine's
// write and read API plus two things only the composition root knows (the
// cache search across every attached store, and the project new memories
// belong to). Taking a small interface keeps this package free of adapter
// imports and lets tests run the operations without a disk.
//
// Flag spellings follow v0.1.17 `lore memory …` where the field still
// exists (--body, --kind, --source, --source-ref, --allow-secrets, --to,
// --archived); flags whose feature has no 0.1.18 home yet (--tag,
// --supersedes, --created-by, --validated-by, --repo scoping) are dropped
// and listed in spec-deltas.md.
package memory

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// Env is everything the memory operations use. *engine.Engine provides the
// row API; the composition root (wire) adds Search and Project.
//
// Invariant: every write goes through Create, Update or Delete, which are
// the engine's one write path (scan, validate, policy, audit); an Env must
// never write around them.
type Env interface {
	// Create, Update, Delete, Get and List are engine.Engine's methods.
	Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error)
	Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error)
	Delete(ctx context.Context, ref engine.Ref) (engine.Written, error)
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	// Stores lists the attached stores in precedence order.
	Stores() []store.Source
	// Search runs a full-text match over table in every attached store's
	// cache, best first, at most limit hits in total.
	Search(ctx context.Context, table row.Table, match string, limit int) ([]Hit, error)
	// Project is the project a new memory belongs to (the repo's own
	// project row): memories.project_id is required.
	Project(ctx context.Context) (ids.ID[ids.Project], error)
	// RepoID resolves a --repo value (a repo's mount name in the project,
	// or a rep_ id) to the repo row's id: E_NOT_FOUND when no such repo.
	RepoID(ctx context.Context, mountOrID string) (ids.ID[ids.Repo], error)
}

// Hit is one search result: the row id and the store whose cache holds it.
type Hit struct {
	Source store.Source
	ID     ids.AnyID
}
