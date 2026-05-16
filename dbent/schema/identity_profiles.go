package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// IdentityProfile — see PLAN.md for table semantics.
type IdentityProfile struct{ ent.Schema }

func (IdentityProfile) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixIdentityProfile}}
}
func (IdentityProfile) Fields() []ent.Field {
	return []ent.Field{
		field.String("stable_key").NotEmpty(),
		field.String("display_name").NotEmpty(),
		field.String("kind").NotEmpty(),
		field.Text("notes").NotEmpty().Optional().Nillable(),
	}
}
