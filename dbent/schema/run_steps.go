package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RunStep belongs to a Run. Sequential, ordered by `seq`.
type RunStep struct{ ent.Schema }

func (RunStep) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixRunStep}}
}
func (RunStep) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("run_id").NotEmpty(),
		field.Int("seq").NonNegative(),
		field.String("kind").NotEmpty(),
		field.String("status_str").NotEmpty(),
		field.Text("payload").Optional().Nillable(),
	}
}
func (RunStep) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("run", Run.Type).
			Ref("steps").
			Field("run_id").
			Unique().
			Required(),
	}
}
func (RunStep) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("run_id", "seq").Unique(),
	}
}
