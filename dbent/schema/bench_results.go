package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BenchResult is one TASK × ARM × ATTEMPT outcome — the unit of
// statistical analysis. Each row captures the prompt sent, the raw
// LLM response, grader trace, timing, and cost.
//
// For a typical run: 30 tasks × 2 arms × 3 attempts = 180 results.
//
// Path-(b) note (E5 capture-back): a result can pass via dual-path
// graders even when no aicoder command was emitted, IF the model
// correctly identified an existing capture. grader_trace records
// which path matched.
type BenchResult struct{ ent.Schema }

func (BenchResult) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixBenchResult},
	}
}

func (BenchResult) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("bench_run_id").NotEmpty(),
		field.String("bench_eval_id").NotEmpty(),

		// Which arm — baseline / with_skill / ablation variants for
		// future cross-tool comparisons (e.g. raw .cursorrules).
		field.Enum("arm").
			Values(benchArmValues...),

		// 1..N within the run; multiple attempts on the same task×arm
		// are the basis of variance control + Cohen's d.
		field.Int("attempt").Positive(),

		// Exact bytes sent + received. Stored verbatim for forensics
		// (regrade-after-bug-fix, side-by-side compare, audit).
		field.Text("prompt_sent").NotEmpty(),
		field.Text("output_received"),
		field.Int("output_chars").Default(0).NonNegative(),
		field.Int("input_tokens_estimate").Optional().Nillable(),
		field.Int("output_tokens_estimate").Optional().Nillable(),
		field.Int("elapsed_ms").Default(0).NonNegative(),

		// Grade verdict + step-by-step trace.
		field.Enum("grade").
			Values(benchGradeValues...),
		// grader_trace shape (per grader_kind):
		//   programmatic → {"cmd_exit": 0|1, "stdout": "...", "stderr": "..."}
		//   llm-judge    → {"verdict": "PASS"|"FAIL", "raw": "..."}
		//   composite    → {"checks": [{kind, result}, ...], "policy": "..."}
		field.JSON("grader_trace", map[string]any{}),

		field.Float("cost_usd_estimate").Default(0),

		// Populated only for llm-judge graders — lets us audit judge
		// consistency over time without re-invoking the judge.
		field.String("judge_model").Optional().Nillable(),
		field.Text("judge_rubric").Optional().Nillable(),
		field.Text("judge_response").Optional().Nillable(),
	}
}

func (BenchResult) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("bench_run", BenchRun.Type).
			Ref("results").
			Field("bench_run_id").
			Unique().
			Required(),
		edge.From("bench_eval", BenchEval.Type).
			Ref("results").
			Field("bench_eval_id").
			Unique().
			Required(),
	}
}

func (BenchResult) Indexes() []ent.Index {
	return []ent.Index{
		// "Show me all attempts for one task×arm in one run."
		index.Fields("bench_run_id", "bench_eval_id", "arm"),
		// "Show me everything failed across all runs."
		index.Fields("project_id", "grade"),
		// "Show me trend for one eval over time."
		index.Fields("bench_eval_id", "arm"),
	}
}
