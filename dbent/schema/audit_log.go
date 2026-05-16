package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AuditLog is the tamper-evident hash-chained log of every meaningful write.
//
// Hash chain (R16 + R27 #9):
//
//	prev_log_hash = sha256 of previous audit_log row's full content.
//	Computed at TXN-COMMIT time (not per-row) for write amplification.
//	Verify with `aicoder audit verify` — walks chain row-by-row.
//
// Override semantic (R22 #45):
//
//	acting_project_id = where command was launched (cwd's toml)
//	target_project_id = where data was actually written (--project flag)
//	override = 1 when acting != target — flagged for review
type AuditLog struct {
	ent.Schema
}

func (AuditLog) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixAuditLog},
	}
}

func (AuditLog) Fields() []ent.Field {
	return []ent.Field{
		// Where the command was LAUNCHED (cwd's project context).
		field.String("acting_project_id").Optional().Nillable(),
		// Where data was actually WRITTEN (after --project= flag override).
		field.String("target_project_id").Optional().Nillable(),

		// Who did it. Always set (identity falls through to ephemeral if needed).
		field.String("actor_id").NotEmpty(),

		// What action: 'memory.add' | 'rule.update' | 'project.purge' | etc.
		// Stable namespace for filtering: --action=memory.*
		field.String("action").NotEmpty(),

		// Which row was affected. NULL for non-row-level actions (e.g., 'vacuum').
		field.String("target_table").Optional().Nillable(),
		field.String("target_id").Optional().Nillable().Match(idValidatorRE),

		// 1 when --project flag overrode cwd's project (= cross-project write).
		field.Bool("override").Default(false),

		// sha256 of row state BEFORE write. NULL for INSERT.
		field.String("before_hash").Optional().Nillable(),
		// sha256 of row state AFTER write. NULL for DELETE.
		field.String("after_hash").Optional().Nillable(),

		// Hash chain link. NULL for the first row.
		field.String("prev_log_hash").Optional().Nillable(),

		// Optional human reason via `--reason="..."` on commands that support it.
		field.Text("reason").Optional().Nillable(),
	}
}

func (AuditLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("target_table", "target_id"),
		index.Fields("actor_id"),
		index.Fields("target_project_id"),
	}
}
