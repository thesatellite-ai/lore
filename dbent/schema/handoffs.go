package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Handoff — see PLAN.md for table semantics.
type Handoff struct{ ent.Schema }

func (Handoff) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixHandoff}}
}
func (Handoff) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("from_actor_id").NotEmpty().Optional().Nillable(),
		field.String("to_actor_id").NotEmpty().Optional().Nillable(),
		field.Text("body").NotEmpty(),
		field.String("status_str").NotEmpty(),
		// task_id / mission_id: the work this handoff is about
		// (`lore handoff add --for @x --task tsk_…`), so `task show`,
		// `mission status` ("handoffs N") and the inbox can group it
		// (LORE_TEAM_SPEC §7.2). Optional: a handoff may be about neither.
		// Plain ids, no ent edges: the task may live in another project's
		// store (D8), where no local row exists to point at.
		field.String("task_id").Optional().Nillable(),
		field.String("mission_id").Optional().Nillable(),
	}
}

func (Handoff) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("task_id"),
		index.Fields("mission_id"),
	}
}
