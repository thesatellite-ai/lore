package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Workflow — see PLAN.md for table semantics.
type Workflow struct{ ent.Schema }

func (Workflow) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixWorkflow}}
}
func (Workflow) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		field.Text("body").NotEmpty(),
	}
}
