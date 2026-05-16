package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// CompressRun — see PLAN.md for table semantics.
type CompressRun struct{ ent.Schema }

func (CompressRun) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixCompressRun}}
}
func (CompressRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("source_kind").NotEmpty(),
		field.Int("input_tokens").Optional().Nillable(),
		field.Int("output_tokens").Optional().Nillable(),
	}
}
