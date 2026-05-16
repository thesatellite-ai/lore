package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// TastePref — see PLAN.md for table semantics.
type TastePref struct{ ent.Schema }

func (TastePref) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixTastePref}}
}
func (TastePref) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.Text("body").NotEmpty(),
		field.String("scope").NotEmpty().Optional().Nillable(),
		field.String("created_by_actor_id").NotEmpty().Optional().Nillable(),
	}
}
func (TastePref) Indexes() []ent.Index {
	return []ent.Index{}
}
