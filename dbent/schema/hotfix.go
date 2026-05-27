package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/khanakia/entx/enttui"
)

// Hotfix is a LOUD recurring warning. "we keep hitting this — beware."
//
// Distinct from Rule (which is GUIDANCE) — hotfixes are WARNINGS about
// repeating mistakes. Same row can be promoted hotfix → rule once stable.
//
// Render priority: hotfixes ALWAYS rendered, NEVER truncated. Pinned in
// render-budget allocation (R21 #21).
type Hotfix struct {
	ent.Schema
}

func (Hotfix) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixHotfix},
		LifecycleMixin{},
	}
}

func (Hotfix) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("repo_id").Optional().Nillable(),

		// title: short headline, surfaced in render. Max ~80 chars recommended.
		field.String("title").NotEmpty(),
		// body: full hotfix with reproduction steps and workaround.
		// String (not Text) → enttui includes BodyContainsFold in the
		// global `/` filter Or-chain. SQLite stores both as TEXT.
		field.String("body").NotEmpty().Annotations(enttui.Filterable{}),

		field.Enum("severity").
			Values(hotfixSeverityValues...).
			Default(string(HotfixSeverityHigh)),

		field.String("superseded_by_id").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}

func (Hotfix) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "repo_id"),
		index.Fields("severity"),
		index.Fields("archived_at"),
	}
}
