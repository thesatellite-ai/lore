package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Workspace — see PLAN.md for table semantics.
type Workspace struct{ ent.Schema }

func (Workspace) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixWorkspace}}
}
func (Workspace) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		field.Text("body").NotEmpty().Optional().Nillable(),
	}
}
