package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// TrustedPlugin — see PLAN.md for table semantics.
type TrustedPlugin struct{ ent.Schema }

func (TrustedPlugin) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixTrustedPlugin}}
}
func (TrustedPlugin) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("sha256").NotEmpty(),
		field.Time("trusted_at"),
		field.String("trusted_by_actor_id").NotEmpty().Optional().Nillable(),
	}
}
