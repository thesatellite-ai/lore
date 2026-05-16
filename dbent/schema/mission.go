package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Mission is an overarching initiative containing multiple Tasks.
type Mission struct{ ent.Schema }

func (Mission) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixMission}}
}
func (Mission) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("title").NotEmpty(),
		field.Text("body").Optional().Nillable(),
		field.Enum("status").
			Values(missionStatusValues...).
			Default(string(MissionStatusActive)),
		field.Time("target_date").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}
func (Mission) Edges() []ent.Edge {
	return []ent.Edge{
		// Mission has many Tasks (1:N) via mission_id field on Task.
		edge.To("tasks", Task.Type),
	}
}
func (Mission) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "status"),
	}
}
