package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// LearnCandidate: background-learning staging (R33 A5).
// Agents write here, NEVER to active knowledge tables.
// User reviews via `aicoder learn promote`.
type LearnCandidate struct{ ent.Schema }

func (LearnCandidate) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixLearnCandidate},
	}
}
func (LearnCandidate) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("repo_id").Optional().Nillable(),
		// 'memories' | 'rules' | 'decisions' | 'hotfixes' | 'patterns'
		field.String("proposed_kind").NotEmpty(),
		field.Text("proposed_body").NotEmpty(),
		// 'agent-proposal' | 'learn-from' | 'plugin'
		field.Enum("source_kind").Values(learnCandidateSourceKindValues...),
		field.String("source_ref").Optional().Nillable(),
		field.Float("trust_score").Default(0.5).Min(0).Max(1),
		field.String("proposed_by_actor_id").Optional().Nillable(),
		// pending | accepted | rejected | expired
		field.Enum("status").
			Values(learnCandidateStatusValues...).
			Default(string(LearnCandidateStatusPending)),
		field.String("reviewed_by_actor_id").Optional().Nillable(),
		field.Time("reviewed_at").Optional().Nillable(),
		// Groups candidates from one learn-from invocation.
		field.String("job_id").Optional().Nillable(),
	}
}
func (LearnCandidate) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "status"),
		index.Fields("job_id"),
	}
}
