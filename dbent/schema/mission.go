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
		// approvers: human actor ids allowed to approve this mission's task
		// gates (`mission add --approvers`, `approvers:` in a plan file).
		// Nil/empty = any human on the org roster may approve; an agent
		// actor never can (LORE_TEAM_SPEC §7.4, TEAM-028).
		field.Strings("approvers").Optional(),
		// idempotency_key: plan-file key (`mission add --key`, `mission: {key: …}`
		// in a plan); `lore plan apply` run twice finds the mission by
		// (project_id, key) and does not add it again. Not a DB unique
		// constraint, same as tasks: uniqueness is the writer's job (two
		// branches may carry one key until they merge).
		field.String("idempotency_key").Optional().Nillable(),
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
		index.Fields("project_id", "idempotency_key"),
	}
}
