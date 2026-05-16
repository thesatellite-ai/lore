package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// ActivityArchive — see PLAN.md for table semantics.
type ActivityArchive struct{ ent.Schema }

func (ActivityArchive) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixActivityArchive}}
}
func (ActivityArchive) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("kind").NotEmpty(),
		field.Text("body").NotEmpty().Optional().Nillable(),
		field.Time("archived_at"),
	}
}
