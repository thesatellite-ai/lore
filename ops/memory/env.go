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
// --supersedes, --created-by, --validated-by, --project, --rebind-*)
// are restored on the new core; see spec-deltas.md for the removed ones
// (--allow-secrets, --db, --confirm).
package memory

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
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

// Mover is the optional Env upgrade memory.move needs: engine.Engine.Move
// (the row keeps its id, the old copy is tombstoned). An Env without it
// answers memory.move with E_UNSUPPORTED instead of pretending.
type Mover interface {
	Move(ctx context.Context, ref engine.Ref, to store.Source) (engine.Written, error)
}

// ColumnSearcher is the optional Env upgrade a `search --column` needs:
// Search restricted to some of the table's full-text fields (field names,
// as schema TableDef.FTS lists them; the cache maps them to its own FTS
// columns). An Env without it answers --column with E_UNSUPPORTED.
type ColumnSearcher interface {
	SearchColumns(ctx context.Context, table row.Table, match string, fields []string, limit int) ([]Hit, error)
}

// Labeler is the optional Env upgrade that names a store the way a person
// reads it beside a row: wire.App.StoreLabel ("northwind/acme" for the org
// store attached at the acme folder, the bare name for a store attached
// at its root). An Env without it labels a store with its attached name.
// The label is display only: `_source` always stays the store name, so
// `--to`/`--store` values read back from --json keep working.
type Labeler interface {
	StoreLabel(name store.Source) string
}

// LabelerOf is env's Labeler upgrade, or nil when env has none (Label
// then falls back to the store name). Generic so every noun's own Env
// interface (memory, content, comment, tag, link) can pass itself.
func LabelerOf[E any](env E) Labeler {
	l, _ := any(env).(Labeler)
	return l
}

// Label is v0.1.17's `_label` of a row of src (stores_reads.go, the
// cache's Label column): "" for the repo store, "[<label>]" otherwise,
// with the label from l (nil = the store name). v0.1.17 printed the same
// bracketed text at the end of a human list line.
func Label(l Labeler, src store.Source) string {
	if src == store.SourceRepo || src == "" {
		return ""
	}
	name := string(src)
	if l != nil {
		name = l.StoreLabel(src)
	}
	return "[" + name + "]"
}

// Hit is one search result: the row id and the store whose cache holds it.
type Hit struct {
	Source store.Source
	ID     ids.AnyID
	// Score is the FTS5 bm25 rank (lower is better, as v0.1.17 printed
	// it) and Snippet the matched excerpt; both zero when the cache gave
	// none.
	Score   float64
	Snippet string
}

// SearchFields is env.Search, restricted to the named full-text fields of
// table when fields is not empty (`search --column`, v0.1.17 named FTS5
// columns by field name). A field the table does not index is
// E_INVALID_INPUT; an Env without ColumnSearcher is E_UNSUPPORTED. noun
// prefixes the messages.
func SearchFields(ctx context.Context, env Env, noun string, table row.Table, match string, fields []string, limit int) ([]Hit, error) {
	if len(fields) == 0 {
		return env.Search(ctx, table, match, limit)
	}
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, noun+": built-in schema", err)
	}
	def, ok := reg.Table(table)
	if !ok {
		return nil, errs.New(errs.Internal, noun+": no table "+string(table))
	}
	var cols []string
	for _, c := range fields {
		c = strings.TrimSpace(c)
		if !slices.Contains(def.FTS, c) {
			return nil, errs.New(errs.InvalidInput, noun+" search: unknown --column "+strconv.Quote(c)+" ("+string(table)+" has: "+strings.Join(def.FTS, ", ")+")")
		}
		if !slices.Contains(cols, c) {
			cols = append(cols, c)
		}
	}
	cs, ok := env.(ColumnSearcher)
	if !ok {
		return nil, errs.New(errs.Unsupported, noun+" search: --column needs a column search this setup lacks")
	}
	return cs.SearchColumns(ctx, table, match, cols, limit)
}
