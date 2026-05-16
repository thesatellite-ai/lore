package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// CodeSymbol — see PLAN.md for table semantics.
type CodeSymbol struct{ ent.Schema }

func (CodeSymbol) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixCodeSymbol}}
}
func (CodeSymbol) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("code_file_id").NotEmpty(),
		field.String("name").NotEmpty(),
		field.String("kind").NotEmpty(),
		field.Int("line_start").Optional().Nillable(),
		field.Int("line_end").Optional().Nillable(),
		field.Text("signature").NotEmpty().Optional().Nillable(),
	}
}
