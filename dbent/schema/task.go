package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/khanakia/entx/enttui"
)

// Task is a discrete unit of work.
//
// Belongs to:
//   - exactly one Mission (optional; via mission_id)
//   - exactly one TaskList (optional; via tasklist_id)
//   - exactly one Plan (optional; via plan_id)
//
// Lifecycle: todo → in_progress → done (or cancelled / blocked).
type Task struct{ ent.Schema }

func (Task) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixTask}}
}

// Annotations — declare any related-table projections we want in the
// enttui table view. Each RelatedColumn emits a column whose value is
// drawn from the referenced edge's target row (eager-loaded so no N+1).
func (Task) Annotations() []schema.Annotation {
	return []schema.Annotation{
		enttui.AllowCreate{},
		enttui.AllowDelete{},
		enttui.AllowBulkCopy{},
		enttui.AllowExport{},
		enttui.RelatedColumns(
			enttui.RelatedColumn{Edge: "tasklist", Field: "title", Label: "Tasklist", Pick: true},
			enttui.RelatedColumn{Edge: "mission", Field: "title", Label: "Mission", Pick: true},
			enttui.RelatedColumn{Edge: "plan", Field: "title", Label: "Plan", Pick: true},
		),
	}
}
func (Task) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("repo_id").Optional().Nillable(),
		field.String("title").NotEmpty().
			Annotations(enttui.Editable{}),
		field.Text("body").Optional().Nillable().
			Annotations(enttui.Editable{}),
		field.Enum("status").
			Values(taskStatusValues...).
			Default(string(TaskStatusTodo)).
			Annotations(enttui.Editable{}),
		field.Enum("priority").
			Values(taskPriorityValues...).
			Default(string(TaskPriorityMedium)).
			Annotations(enttui.Editable{}),
		field.Time("due_at").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.String("mission_id").Optional().Nillable(),
		field.String("tasklist_id").Optional().Nillable(),
		field.String("plan_id").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
		field.String("assigned_to_actor_id").Optional().Nillable(),
	}
}
func (Task) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("mission", Mission.Type).
			Ref("tasks").
			Field("mission_id").
			Unique(),
		edge.From("tasklist", TaskList.Type).
			Ref("tasks").
			Field("tasklist_id").
			Unique(),
		edge.From("plan", Plan.Type).
			Ref("tasks").
			Field("plan_id").
			Unique(),
	}
}
func (Task) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "status"),
		index.Fields("mission_id"),
		index.Fields("tasklist_id"),
		index.Fields("plan_id"),
		index.Fields("due_at"),
	}
}
