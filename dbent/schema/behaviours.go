package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Behaviour — see PLAN.md for table semantics.
type Behaviour struct{ ent.Schema }

func (Behaviour) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixBehaviour}}
}
func (Behaviour) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		field.Text("body").NotEmpty(),
		field.String("created_by_actor_id").NotEmpty().Optional().Nillable(),
	}
}
