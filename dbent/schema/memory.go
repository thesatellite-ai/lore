package schema

import (
	"saas/pkg/aicoder/ids"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/khanakia/entx/enttui"
)

// Memory is the flexible knowledge type — free-form learned notes.
//
// 5-tier kind taxonomy (R33 A2):
//   - core       always rendered, never decays, pinned
//   - retrieved  fetched on query match, 180d half-life (default)
//   - episodic   tied to runs/incidents, 90d half-life
//   - procedural loaded by activation rule, 365d half-life
//   - archival   global by promotion, no decay, excluded from default
//
// Bitemporal (R16 + R31):
//   - id (UUIDv7) — when row was created (DB-write moment)
//   - tx_at        — same as id-time but explicit; monotonic per R27
//   - valid_at     — when the FACT became true in the world
//   - valid_until  — when the fact expires
//
// Lifecycle (R37 Block 3): trust_score, confidence, last_accessed_at,
// last_validated_at, archived_at, superseded_by_id, source_kind, source_ref.
//
// Embedding cols (R34.6): nullable at v0.1, populated in v0.2.
//
// FTS5: external-content table memories_fts kept in sync via triggers
// (defined in dbent_migrate as raw SQL post-step — ent doesn't manage FTS5).
type Memory struct {
	ent.Schema
}

func (Memory) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixMemory},
		LifecycleMixin{},
	}
}

func (Memory) Fields() []ent.Field {
	return []ent.Field{
		// project_id: NOT NULL — every memory belongs to exactly one project.
		field.String("project_id").
			NotEmpty().
			Immutable(),

		// repo_id: NULL = project-master scope. Set = repo-specific.
		field.String("repo_id").
			Optional().
			Nillable(),

		// kind: 5-tier taxonomy. Default 'retrieved' (most common).
		field.Enum("kind").
			Values(memoryKindValues...).
			Default(string(MemoryKindRetrieved)),

		// body: actual knowledge text. Markdown allowed. NotEmpty enforced
		// at app layer via textnorm.Normalize before INSERT.
		// field.String (not Text) so enttui codegen treats it as IsString
		// and includes BodyContainsFold in the global `/` filter Or-chain;
		// SQLite stores both as TEXT — no migration delta.
		field.String("body").
			NotEmpty().
			Annotations(enttui.Filterable{}),

		// Bitemporal axes ──
		field.Time("valid_at").
			Optional().
			Nillable(),
		field.Time("valid_until").
			Optional().
			Nillable(),
		field.Time("tx_at").
			Default(time.Now),

		// Replacement chain. Self-referencing FK declared via edge.
		field.String("superseded_by_id").
			Optional().
			Nillable(),

		// Audit attribution.
		field.String("created_by_actor_id").
			Optional().
			Nillable(),
		field.String("validated_by_actor_id").
			Optional().
			Nillable(),

		// Embedding (v0.2 nullable):
		field.String("embedding_model_id").
			Optional().
			Nillable(),
		field.Int("embedding_dim").
			Optional().
			Nillable(),
		field.Bytes("embedding").
			Optional().
			Nillable(),
	}
}

func (Memory) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).
			Ref("memories").
			Field("project_id").
			Unique().
			Required().
			Immutable(),
		edge.From("repo", Repo.Type).
			Ref("memories").
			Field("repo_id").
			Unique(),
		edge.From("created_by", Actor.Type).
			Ref("created_memories").
			Field("created_by_actor_id").
			Unique(),
		edge.From("validated_by", Actor.Type).
			Ref("validated_memories").
			Field("validated_by_actor_id").
			Unique(),
		// Self-edge for supersession chain.
		edge.To("superseded_by", Memory.Type).
			From("supersedes").
			Field("superseded_by_id").
			Unique(),
	}
}

func (Memory) Indexes() []ent.Index {
	return []ent.Index{
		// Hot-path: scope filter for retrieval.
		index.Fields("project_id", "repo_id"),
		// Hot-path: kind filter for render.
		index.Fields("project_id", "kind"),
		// Hot-path: exclude archived from default search.
		index.Fields("archived_at"),
		// Successor chain: find live memory after following supersession.
		index.Fields("superseded_by_id"),
		// Pretty-ID uniqueness within project.
	}
}
