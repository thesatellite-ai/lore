package schema

import (
	"saas/pkg/aicoder/ids"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ProjectConfig stores per-project settings as key/value pairs (R36 Layer 2).
//
// Replaces the singleton DBConfig table for per-project values to make
// Mode B (shared DB) safe — each project keeps its own setting rows.
//
// ent's auto-id is unsuitable here (we want consistent string IDs across
// the schema), so AicoderBaseMixin adds the UUIDv7 id; uniqueness across
// (project_id, key) is enforced by an index below.
//
// Resolution chain when reading a setting:
//
//  1. CLI flag
//  2. Env var
//  3. project_config WHERE project_id=? AND key=?  ← this table
//  4. config WHERE key='global_default_<key>'      ← DB-wide fallback
//  5. Hard-coded default
type ProjectConfig struct {
	ent.Schema
}

func (ProjectConfig) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixProjectConfig},
	}
}

func (ProjectConfig) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),

		// key examples (see PLAN.md Round 38 for full list):
		//   'default_behaviour'      enum
		//   'default_model'          'claude-opus-4-7'
		//   'last_global_sync_at'    iso8601
		//   'render_targets'         JSON array
		//   'decay_half_life_days'   '180' (memories), '365' (rules)
		//   'fts_mode'               'unicode61' | 'trigram'
		//   'token_budget_render'    '50000'
		//   'embedding_model_id'     v0.2
		//   'embedding_dim'          v0.2
		field.String("key").NotEmpty().Immutable(),

		// TEXT-encoded value. Caller parses to int/json/enum. NULL = unset.
		field.Text("value").Optional().Nillable(),

		// updated_at is also provided by AicoderBaseMixin but we override here
		// to keep the explicit semantics close to the schema. Drop if redundant.
		field.Time("setting_updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

func (ProjectConfig) Indexes() []ent.Index {
	return []ent.Index{
		// Composite uniqueness — one row per (project, key).
		index.Fields("project_id", "key").Unique(),
	}
}
