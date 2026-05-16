package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// KnowledgeRevision — see PLAN.md for table semantics.
type KnowledgeRevision struct{ ent.Schema }

func (KnowledgeRevision) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixKnowledgeRevision}}
}
func (KnowledgeRevision) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("entity_table").NotEmpty(),
		field.String("entity_id").NotEmpty().Match(idValidatorRE),
		field.Int("revision_num"),
		field.Text("body").NotEmpty(),
		field.String("actor_id").NotEmpty().Optional().Nillable(),
	}
}
