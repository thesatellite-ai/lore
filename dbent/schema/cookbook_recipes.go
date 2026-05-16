package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// CookbookRecipe — see PLAN.md for table semantics.
type CookbookRecipe struct{ ent.Schema }

func (CookbookRecipe) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixCookbookRecipe}}
}
func (CookbookRecipe) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("title").NotEmpty(),
		field.Text("body").NotEmpty(),
		field.String("language").NotEmpty().Optional().Nillable(),
		field.String("created_by_actor_id").NotEmpty().Optional().Nillable(),
	}
}
func (CookbookRecipe) Indexes() []ent.Index {
	return []ent.Index{}
}
