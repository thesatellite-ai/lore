package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BenchEval is a benchmark TASK DEFINITION (template).
// One row per test case — independent of any run. Authored via
// `aicoder bench eval add` and persists across runs.
//
// Replaces the YAML bench/tasks/E?-NNN.yaml format with a queryable,
// editable, audit-logged ent row.
//
// Categories (typed enum):
//
//	rule-trigger     — exercises a `must`-severity rule
//	hotfix-avoid     — exercises a captured hotfix trap
//	decision-respect — exercises a captured decision with revisit criteria
//	convention       — exercises a soft-preference memory
//	capture-back     — tests whether the AI emits aicoder commands
//	custom           — anything else
type BenchEval struct{ ent.Schema }

func (BenchEval) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixBenchEval},
	}
}

func (BenchEval) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),

		// Human-facing identifier: "E1-001", "E2-005", etc. Unique per project.
		field.String("code").NotEmpty(),

		// Typed category.
		field.Enum("category").
			Values(benchEvalCategoryValues...),

		// The task itself.
		field.Text("prompt").NotEmpty(),

		// Polymorphic FK to the artifact this test exercises.
		// linked_id is opaque (rule_id/hotfix_id/decision_id/memory_id).
		// We snapshot the body at author-time so re-renders of the
		// linked artifact don't invalidate the test silently.
		field.Enum("linked_kind").
			Values(benchEvalLinkedKindValues...).
			Default(string(BenchEvalLinkedKindNone)),
		field.String("linked_id").Optional().Nillable(),
		field.Text("linked_body_snapshot").Optional().Nillable(),

		// Grader spec.
		field.Enum("grader_kind").
			Values(benchEvalGraderKindValues...).
			Default(string(BenchEvalGraderProgrammatic)),
		// Free-form JSON keyed by grader_kind:
		//   programmatic → {"cmd": "..."}
		//   llm-judge    → {"rubric": "...", "judge_model": "..."}
		//   golden-diff  → {"golden_file": "...", "threshold": 0.85}
		//   composite    → {"checks": [...], "policy": "all-must-pass"}
		field.JSON("grader_spec", map[string]any{}),

		// Pre-run expectations — used to flag suspicious runs (a task
		// that consistently passes/fails ~0% or ~100% is suspect; the
		// grader is probably wrong).
		field.Float("expected_pass_with").Default(0.85),
		field.Float("expected_pass_baseline").Default(0.30),

		field.Text("notes").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),

		// Soft-delete marker — separate from the heavyweight LifecycleMixin
		// (which would force trust_score/confidence/source_kind, irrelevant
		// to bench tasks).
		field.Time("archived_at").Optional().Nillable(),
	}
}

func (BenchEval) Edges() []ent.Edge {
	return []ent.Edge{
		// 1:N — one eval template generates many results across runs.
		edge.To("results", BenchResult.Type),
	}
}

func (BenchEval) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "code").Unique(),
		index.Fields("project_id", "category"),
		// Speed up "find eval by linked rule/hotfix/decision".
		index.Fields("linked_kind", "linked_id"),
	}
}
