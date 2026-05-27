package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/khanakia/entx/enttui"
)

// Decision is an Architectural Decision Record. WHY we chose something.
//
// Distinct from Memory: decisions are STRUCTURED records of choice moments.
// Body should answer: context + chosen option + alternatives + reasoning.
//
// Status lifecycle: proposed → accepted → (later) superseded / deprecated.
// Superseding writes a new decision pointing back via supersedes edge.
//
// Decisions don't decay — they're history. trust_score defaults high.
type Decision struct {
	ent.Schema
}

func (Decision) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixDecision},
		LifecycleMixin{},
	}
}

func (Decision) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("repo_id").Optional().Nillable(),

		field.String("title").NotEmpty(),
		// body: markdown — context + chosen + alternatives + reasoning.
		// String (not Text) → enttui includes BodyContainsFold in the
		// global `/` filter Or-chain. SQLite stores both as TEXT.
		field.String("body").NotEmpty().Annotations(enttui.Filterable{}),

		field.Enum("status").
			Values(decisionStatusValues...).
			Default(string(DecisionStatusAccepted)),

		field.String("superseded_by_id").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}

func (Decision) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "repo_id"),
		index.Fields("status"),
		index.Fields("archived_at"),
	}
}
