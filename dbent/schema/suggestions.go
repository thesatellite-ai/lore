package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Suggestion — see PLAN.md for table semantics.
type Suggestion struct{ ent.Schema }

func (Suggestion) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixSuggestion}}
}
func (Suggestion) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("title").NotEmpty(),
		field.Text("body").NotEmpty(),
		field.String("status_str").NotEmpty(),
		field.String("created_by_actor_id").NotEmpty().Optional().Nillable(),
	}
}
