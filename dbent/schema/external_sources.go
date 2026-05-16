package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// ExternalSource — see PLAN.md for table semantics.
type ExternalSource struct{ ent.Schema }

func (ExternalSource) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixExternalSource}}
}
func (ExternalSource) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("kind").NotEmpty(),
		field.String("name").NotEmpty(),
		field.Text("config_json").NotEmpty().Optional().Nillable(),
		field.Bool("enabled").Default(false),
	}
}
