package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/khanakia/entx/enttui"
)

// Rule is a hard constraint. "must do X" / "must not do Y."
//
// Activation modes (R15 #2 MDC pattern):
//
//	always   — always rendered (default, highest priority)
//	glob     — rendered when assemble matches one of `globs` patterns
//	semantic — rendered when query matches `applies_to_description`
//	manual   — only when user explicitly references
//
// Severity drives verifier-runs (must=block, should=warn, may=suggest).
//
// Linkage to verifiers via rule_verifier_refs typed FK table (Round 37 Block 4).
type Rule struct {
	ent.Schema
}

func (Rule) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixRule},
		LifecycleMixin{},
	}
}

func (Rule) Fields() []ent.Field {
	return []ent.Field{
		// project_id: NULL = global rule (lives in global.db typically).
		field.String("project_id").
			Optional().
			Nillable(),
		// repo_id: NULL = project-master rule.
		field.String("repo_id").
			Optional().
			Nillable(),
		// String (not Text) → enttui includes BodyContainsFold in the
		// global `/` filter Or-chain. SQLite stores both as TEXT.
		field.String("body").NotEmpty().Annotations(enttui.Filterable{}),

		// Activation: when does this rule appear in render?
		field.Enum("activation").
			Values(ruleActivationValues...).
			Default(string(RuleActivationAlways)),
		// JSON array of glob patterns. Required when activation='glob'.
		field.String("globs").
			Optional().
			Nillable(),
		// Used as embedding key when activation='semantic'.
		field.Text("applies_to_description").
			Optional().
			Nillable(),

		// Severity: must (block) / should (warn) / may (suggest).
		field.Enum("severity").
			Values(ruleSeverityValues...).
			Default(string(RuleSeverityMust)),

		field.String("superseded_by_id").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}

func (Rule) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "repo_id"),
		index.Fields("project_id", "activation"),
		index.Fields("archived_at"),
	}
}
