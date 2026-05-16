package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// TechDoc is an external technical documentation source.
// Has many TechDocPages (1:N).
type TechDoc struct{ ent.Schema }

func (TechDoc) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixTechDoc}}
}
func (TechDoc) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		field.String("base_url").Optional().Nillable(),
		field.Text("description").Optional().Nillable(),
	}
}
func (TechDoc) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("pages", TechDocPage.Type),
	}
}
func (TechDoc) Indexes() []ent.Index {
	return []ent.Index{}
}
