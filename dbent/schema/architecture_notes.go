package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// ArchitectureNote — see PLAN.md for table semantics.
type ArchitectureNote struct{ ent.Schema }

func (ArchitectureNote) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixArchitectureNote}}
}
func (ArchitectureNote) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("title").NotEmpty(),
		field.Text("body").NotEmpty(),
		field.String("created_by_actor_id").NotEmpty().Optional().Nillable(),
	}
}
func (ArchitectureNote) Indexes() []ent.Index {
	return []ent.Index{}
}
