package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"

	"github.com/khanakia/entx/enttui"
)

// TaskList groups tasks under a named list (think GTD lists, sprint backlog).
//
// Has many Tasks (1:N) via tasklist_id field on Task.
type TaskList struct{ ent.Schema }

func (TaskList) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixTaskList}}
}
func (TaskList) Annotations() []schema.Annotation {
	return []schema.Annotation{
		enttui.DetailEdge{Edge: "tasks"},
	}
}
func (TaskList) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("title").NotEmpty(),
		field.Text("body").Optional().Nillable(),
		field.String("status_str").NotEmpty(),
	}
}
func (TaskList) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tasks", Task.Type),
	}
}
func (TaskList) Indexes() []ent.Index {
	return []ent.Index{}
}
