package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Handoff — see PLAN.md for table semantics.
type Handoff struct{ ent.Schema }

func (Handoff) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixHandoff}}
}
func (Handoff) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("from_actor_id").NotEmpty().Optional().Nillable(),
		field.String("to_actor_id").NotEmpty().Optional().Nillable(),
		field.Text("body").NotEmpty(),
		field.String("status_str").NotEmpty(),
	}
}
