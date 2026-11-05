package schema

import "github.com/thesatellite-ai/lore/model/ids"

// Telemetry tables that the 0.1.18 design keeps (P3-LOCAL-3): machine-local
// rows that never leave this machine (schema.MachineLocal, P3-LOCAL-1).
// runs keeps v0.1.17's columns (dbent/schema/runs.go) so the one-time copy
// from .lore/lore.db is column for column; id and created_at are envelope
// fields. Every other v0.1.17 telemetry table (run_steps, query_logs,
// bench_*, assemble_*, compress_runs, …) is dropped (p3/dropped.md).
const (
	TableRuns = "runs"
	// TablePIIPatterns holds the v0.1.17 custom PII patterns, machine-local
	// as they were in lore.db (CLI_REFERENCE 2.25). Read-only leftovers: no
	// scanner reads them and nothing writes them but the one-time
	// machine_local copy of `lore migrate run` (secret rules are settings
	// rows now, FORMAT_EVOLUTION step 17); fields are the v0.1.17 column
	// names so that copy needs no code (P3-PII-1).
	TablePIIPatterns = "pii_patterns"
)

// PII pattern fields (v0.1.17 dbent/schema/pii_pattern.go).
const (
	FieldPIIName       = "name"
	FieldPIIRegex      = "regex"
	FieldPIIEnabled    = "enabled"
	FieldPIISourceKind = "source_kind"
)

// Run fields (TIME_TRACKING 4: `lore run start|end|cancel`).
const (
	FieldRunProjectID   = "project_id"
	FieldRunKind        = "kind"
	FieldRunStatus      = "status_str"
	FieldRunActorID     = "actor_id"
	FieldRunStartedAt   = "started_at"
	FieldRunCompletedAt = "completed_at"
	// FieldRunNotes is v0.1.17's JSON text of the run's details (task_id,
	// mission_id, agent_kind, model_id, retry_of_run_id, goal, outcome,
	// summary, error_message, tokens_in, tokens_out, cost); ops/run owns its
	// typed shape (runNotes).
	FieldRunNotes = "notes"
)

// Run statuses: a run is in_progress until `run end --outcome` sets one of
// the outcomes, or `run cancel` sets cancelled.
const (
	RunInProgress = "in_progress"
	RunSuccess    = "success"
	RunPartial    = "partial"
	RunFailed     = "failed"
	RunCancelled  = "cancelled"
)

// RunOutcomes are the values `run end --outcome` takes, in v0.1.17 help order.
func RunOutcomes() []string { return []string{RunSuccess, RunPartial, RunFailed, RunCancelled} }

// RunStatuses are every status a run row may hold.
func RunStatuses() []string { return append([]string{RunInProgress}, RunOutcomes()...) }

// telemetryDefs returns the machine-local telemetry tables.
//
// Why project_id and actor_id are text, not refs: the project and actor
// rows live in the repo or an org store, never in the machine store, so a
// relation would cross stores; the machine store holds every repo's runs
// and ops/run filters by project_id (P3-LOCAL-2).
func telemetryDefs() []TableDef {
	return []TableDef{
		Table(TableRuns, ids.PrefixRun, MachineLocal(), Description("an agent's or person's attempt at a task, logged on this machine"),
			Text(FieldRunProjectID),
			Text(FieldRunKind, Required()),
			Text(FieldRunStatus, Required(), OneOf(RunStatuses()...)),
			Text(FieldRunActorID),
			DateTime(FieldRunStartedAt),
			DateTime(FieldRunCompletedAt),
			Text(FieldRunNotes, WithWidget(WidgetJSON)),
			IndexOn(FieldRunProjectID, FieldRunStartedAt),
		),
		Table(TablePIIPatterns, ids.PrefixPiiPattern, MachineLocal(), Description("a v0.1.17 custom PII pattern kept read-only; secret rules replaced it"),
			Text(FieldPIIName, Required()),
			Text(FieldPIIRegex, Required()),
			Bool(FieldPIIEnabled),
			Text(FieldPIISourceKind, Required()),
		),
	}
}
