package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AssembleCitation struct{ ent.Schema }

func (AssembleCitation) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixAssembleCitation}}
}
func (AssembleCitation) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("assemble_run_id").NotEmpty(),
		field.String("entity_table").NotEmpty(),
		field.String("entity_id").NotEmpty().Match(idValidatorRE),
		field.Float("score").Optional().Nillable(),
	}
}
func (AssembleCitation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("assemble_run", AssembleRun.Type).
			Ref("citations").
			Field("assemble_run_id").
			Unique().
			Required(),
	}
}
func (AssembleCitation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("assemble_run_id"),
	}
}
