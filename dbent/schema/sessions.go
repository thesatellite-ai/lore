package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Session is one coding-agent (or CI, or LORE_SESSION) session on this
// machine (AGENT_IDENTITY_DESIGN 6.2). MACHINE-LOCAL ONLY (R11, OQ14,
// Q-LOCAL): the table is "telemetry" in lsync/registry.go, so it never
// reaches .lore/data, a store, the live ref or a client repo, which is
// why it may hold the tool's raw session id, the model and the cwd.
//
// Found or created by (actor_id, tool, tool_session_id) — see
// saas/pkg/aicoder/sessions. Every detail column is "" when unknown
// (never guessed; OQ8), and `sources` says where each filled one came
// from.
type Session struct{ ent.Schema }

func (Session) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixSession}}
}
func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.Time("started_at"),
		// ended_at is moved to the last write lazily (6.2).
		field.Time("ended_at").Optional().Nillable(),
		field.String("actor_id").NotEmpty().Optional().Nillable(),
		// agent_kind mirrors tool (the v0.1.17 column nothing wrote).
		field.String("agent_kind").NotEmpty().Optional().Nillable(),

		// tool is the detector slug ("codex"); "" for a LORE_SESSION-only person session.
		field.String("tool").Default(""),
		// tool_session_id is the tool's own id, RAW (OQ14), or the synthetic key.
		field.String("tool_session_id").Default(""),
		field.String("tool_version").Default(""),
		// model is never guessed (OQ8); filled by env or the session hook.
		field.String("model").Default(""),
		// machine_id is the _w.m machine id (6.8).
		field.String("machine_id").Default(""),
		// run_id is the per-run id of an ephemeral runtime (CI run/attempt).
		field.String("run_id").Default(""),
		// repo, branch, cwd: repo folder name, HEAD branch, cwd relative to the repo root (never absolute).
		field.String("repo").Default(""),
		field.String("branch").Default(""),
		field.String("cwd").Default(""),
		field.String("terminal").Default(""),
		// ci is one line: "<provider> <run>/<attempt> <workflow> <job> <sha>".
		field.String("ci").Default(""),
		field.String("parent_session_id").Default(""),
		// signal is what decided ("CODEX_THREAD_ID", "process:aider", "session_hook").
		field.String("signal").Default(""),
		// sources maps each filled field to where it came from ({"model": "session_hook"}).
		field.JSON("sources", map[string]string{}).Optional().Annotations(SkipAll),
		field.JSON("outer_tools", []string{}).Optional().Annotations(SkipAll),
		field.Bool("declared_human").Default(false),
		// flags are short snake_case notes (weak_only, model_from_config, ci, tty…).
		field.JSON("flags", []string{}).Optional().Annotations(SkipAll),
	}
}

func (Session) Indexes() []ent.Index {
	return []ent.Index{index.Fields("actor_id", "tool", "tool_session_id")}
}
