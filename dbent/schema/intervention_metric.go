package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// InterventionMetric: human-override tracking (R33 A6).
// Surfaced via `aicoder stats interventions --since=30d`.
type InterventionMetric struct{ ent.Schema }

func (InterventionMetric) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixInterventionMetric},
	}
}
func (InterventionMetric) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		// correction | manual_edit | retry | rollback | undo
		field.Enum("kind").Values(interventionMetricKindValues...),
		field.String("target_table").Optional().Nillable(),
		field.String("target_id").Optional().Nillable(),
		field.Text("notes").Optional().Nillable(),
		field.String("actor_id").Optional().Nillable(),
	}
}
