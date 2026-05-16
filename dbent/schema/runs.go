package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Run is an executable invocation. Has many RunSteps (1:N).
type Run struct{ ent.Schema }

func (Run) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixRun}}
}
func (Run) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("kind").NotEmpty(),
		field.String("status_str").NotEmpty(),
		field.String("actor_id").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Text("notes").Optional().Nillable(),
	}
}
func (Run) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("steps", RunStep.Type),
	}
}
func (Run) Indexes() []ent.Index {
	return []ent.Index{}
}
