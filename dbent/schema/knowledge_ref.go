package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// KnowledgeRef: polymorphic graph edges between knowledge entities.
// Use for FLEXIBLE relationships. For HOT-PATH typed relationships,
// see MemoryCodeRef and RuleVerifierRef.
type KnowledgeRef struct{ ent.Schema }

func (KnowledgeRef) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixKnowledgeRef},
	}
}
func (KnowledgeRef) Fields() []ent.Field {
	return []ent.Field{
		field.String("src_table").NotEmpty(),
		field.String("src_id").NotEmpty(),
		field.String("dst_table").NotEmpty(),
		field.String("dst_id").NotEmpty(),
		// 'mentions' | 'derived_from' | 'caused_by' | 'conflicts_with' | 'supersedes'
		field.String("relation").NotEmpty(),
		// 'extracted' (user/regex) | 'inferred' (LLM) | 'ambiguous' (multi-target)
		field.Enum("confidence").
			Values(knowledgeRefConfidenceValues...).
			Default(string(KnowledgeRefConfidenceExtracted)),
	}
}
func (KnowledgeRef) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("src_table", "src_id"),
		index.Fields("dst_table", "dst_id"),
	}
}
