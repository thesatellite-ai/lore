package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Incident — see PLAN.md for table semantics.
type Incident struct{ ent.Schema }

func (Incident) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixIncident}}
}
func (Incident) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("title").NotEmpty(),
		field.Text("body").NotEmpty(),
		field.String("severity_str").NotEmpty().Optional().Nillable(),
		field.Time("resolved_at").Optional().Nillable(),
		field.String("created_by_actor_id").NotEmpty().Optional().Nillable(),
	}
}
func (Incident) Indexes() []ent.Index {
	return []ent.Index{}
}
