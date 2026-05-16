package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// PiiPattern — see PLAN.md for table semantics.
type PiiPattern struct{ ent.Schema }

func (PiiPattern) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixPiiPattern}}
}
func (PiiPattern) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.Text("regex").NotEmpty(),
		field.Bool("enabled").Default(false),
		field.String("source_kind").NotEmpty(),
	}
}
