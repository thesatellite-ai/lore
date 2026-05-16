package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Session — see PLAN.md for table semantics.
type Session struct{ ent.Schema }

func (Session) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixSession}}
}
func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.Time("started_at"),
		field.Time("ended_at").Optional().Nillable(),
		field.String("actor_id").NotEmpty().Optional().Nillable(),
		field.String("agent_kind").NotEmpty().Optional().Nillable(),
	}
}
