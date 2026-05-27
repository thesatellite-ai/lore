package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/khanakia/entx/enttui"
)

// Pattern: how code SHOULD look. Templates / idioms / conventions.
// Distinct from Rule (constraints): patterns are EXAMPLARS.
type Pattern struct{ ent.Schema }

func (Pattern) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixPattern},
		LifecycleMixin{},
	}
}
func (Pattern) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("repo_id").Optional().Nillable(),
		field.String("title").NotEmpty(),
		// String (not Text) → enttui includes BodyContainsFold in the
		// global `/` filter Or-chain. SQLite stores both as TEXT.
		field.String("body").NotEmpty().Annotations(enttui.Filterable{}),
		field.String("language").Optional().Nillable(),
		field.String("superseded_by_id").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}
func (Pattern) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "repo_id"),
		index.Fields("language"),
	}
}
