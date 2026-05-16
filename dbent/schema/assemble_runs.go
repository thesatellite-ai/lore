package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type AssembleRun struct{ ent.Schema }

func (AssembleRun) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixAssembleRun}}
}
func (AssembleRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.Text("query_text").Optional().Nillable(),
		field.String("rendered_target").Optional().Nillable(),
		field.Int("total_bytes").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}
func (AssembleRun) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("citations", AssembleCitation.Type),
	}
}
func (AssembleRun) Indexes() []ent.Index {
	return []ent.Index{}
}
