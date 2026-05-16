package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Tag: orthogonal classification across all knowledge types (R22 #24).
// PER-PROJECT — same name allowed across different projects.
type Tag struct{ ent.Schema }

func (Tag) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixTag},
	}
}
func (Tag) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		// color: optional hex for display in TUI dashboards.
		field.String("color").Optional().Nillable(),
	}
}
func (Tag) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "name").Unique(),
	}
}
