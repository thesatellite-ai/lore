package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// LearnRun — see PLAN.md for table semantics.
type LearnRun struct{ ent.Schema }

func (LearnRun) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixLearnRun}}
}
func (LearnRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("source_kind").NotEmpty(),
		field.Int("snapshots_count").Optional().Nillable(),
		field.Int("candidates_count").Optional().Nillable(),
		field.String("status_str").NotEmpty(),
	}
}
