package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BenchRun represents ONE benchmark execution — N tasks × M arms ×
// R attempts. Holds the configuration used (model, temperature,
// claude.md sha) plus a denormalized summary post-completion.
//
// Status flow: running → complete | aborted | failed.
type BenchRun struct{ ent.Schema }

func (BenchRun) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixBenchRun},
	}
}

func (BenchRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),

		// Human-facing identifier: "RUN-2026-05-11-haiku-1" or whatever
		// caller passed via --code. Unique per project.
		field.String("code").NotEmpty(),

		// Model + decoding config — held constant across arms within
		// one run.
		field.String("model").NotEmpty(),
		field.Float("temperature").Default(0.2),
		field.Int("runs_per_arm").Default(3).NonNegative(),

		// Reproducibility: pin which CLAUDE.md the with-skill arm saw.
		field.String("claude_md_sha256").Optional().Nillable(),
		field.Int("claude_md_size_bytes").Optional().Nillable(),

		// What this run included. Empty array = all evals; otherwise
		// explicit codes ("E1-001", ...).
		field.JSON("eval_codes", []string{}),

		// Which arms were exercised. Typically ["baseline","with_skill"];
		// may include ablations.
		field.JSON("arms", []string{}),

		field.Time("started_at"),
		field.Time("completed_at").Optional().Nillable(),

		field.Enum("status").
			Values(benchRunStatusValues...).
			Default(string(BenchRunStatusRunning)),

		// Aggregated post-completion (BenchResult rows are the source
		// of truth; this is a cached rollup for fast listing).
		field.Float("cost_usd_estimate").Default(0),
		field.Int("total_calls").Default(0).NonNegative(),
		field.JSON("summary", map[string]any{}),

		field.Text("notes").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}

func (BenchRun) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("results", BenchResult.Type),
	}
}

func (BenchRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "code"),
		index.Fields("project_id", "model"),
		index.Fields("project_id", "status"),
		index.Fields("started_at"),
	}
}
