package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RuleVerifierRef: typed M2M rules ↔ verifiers. Mini stores; ai-coder-go executes.
type RuleVerifierRef struct{ ent.Schema }

func (RuleVerifierRef) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixRuleVerifierRef}}
}
func (RuleVerifierRef) Fields() []ent.Field {
	return []ent.Field{
		field.String("rule_id").NotEmpty(),
		field.Enum("verifier_kind").Values(ruleVerifierKindValues...),
		field.String("verifier_ref").NotEmpty(),
		field.Text("notes").Optional().Nillable(),
	}
}
func (RuleVerifierRef) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("rule_id", "verifier_kind", "verifier_ref").Unique(),
	}
}
