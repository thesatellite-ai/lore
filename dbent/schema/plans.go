package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Plan is a higher-level rollup containing tasks and rationale.
//
// Has many Tasks (1:N) via plan_id field on Task.
type Plan struct{ ent.Schema }

func (Plan) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixPlan}}
}
func (Plan) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("title").NotEmpty(),
		field.Text("body").NotEmpty(),
		field.String("status_str").NotEmpty(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}
func (Plan) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tasks", Task.Type),
	}
}
func (Plan) Indexes() []ent.Index {
	return []ent.Index{}
}
