package schema

import (
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Telemetry tables that the 0.1.18 design keeps (P3-LOCAL-3): machine-local
// rows that never leave this machine (schema.MachineLocal, P3-LOCAL-1).
// runs keeps v0.1.17's columns (dbent/schema/runs.go) so the one-time copy
// from .lore/lore.db is column for column; id and created_at are envelope
// fields. sessions, render_histories and tech_doc_pages keep their
// v0.1.17 columns for the same reason (P3-LOCAL-5). Every other v0.1.17
// telemetry table (run_steps, query_logs, bench_*, assemble_*,
// compress_runs, …) is dropped (p3/dropped.md).
const (
	TableRuns            = "runs"
	TableSessions        = "sessions"
	TableRenderHistories = "render_histories"
	TableTechDocPages    = "tech_doc_pages"
	// TablePIIPatterns holds the v0.1.17 custom PII patterns, machine-local
	// as they were in lore.db (CLI_REFERENCE 2.25). Read-only leftovers: no
	// scanner reads them and nothing writes them but the one-time
	// machine_local copy of `lore migrate run` (secret rules are settings
	// rows now, FORMAT_EVOLUTION step 17); fields are the v0.1.17 column
	// names so that copy needs no code (P3-PII-1).
	TablePIIPatterns = "pii_patterns"
)

// Session fields (AGENT_IDENTITY 6.2, dbent/schema/sessions.go): one row
// per coding-agent, CI or LORE_SESSION session on this machine, found by
// (actor_id, tool, tool_session_id). Every detail is "" when unknown
// (never guessed, OQ8); sources says where each filled one came from.
const (
	FieldSessionProjectID = "project_id"
	FieldSessionStartedAt = "started_at"
	// FieldSessionEndedAt moves to the last write lazily (6.2).
	FieldSessionEndedAt   = "ended_at"
	FieldSessionActorID   = "actor_id"
	FieldSessionAgentKind = "agent_kind"
	FieldSessionTool      = "tool"
	// FieldSessionToolSessionID is the tool's own id, raw (OQ14), or the
	// synthetic key; it may be stored here because the row never leaves
	// the machine.
	FieldSessionToolSessionID   = "tool_session_id"
	FieldSessionToolVersion     = "tool_version"
	FieldSessionModel           = "model"
	FieldSessionMachineID       = "machine_id"
	FieldSessionRunID           = "run_id"
	FieldSessionRepo            = "repo"
	FieldSessionBranch          = "branch"
	FieldSessionCwd             = "cwd"
	FieldSessionTerminal        = "terminal"
	FieldSessionCI              = "ci"
	FieldSessionParentSessionID = "parent_session_id"
	FieldSessionSignal          = "signal"
	// FieldSessionSources is a JSON object field -> where it came from.
	FieldSessionSources = "sources"
	// FieldSessionOuterTools lists the tool slugs around the session's tool.
	FieldSessionOuterTools    = "outer_tools"
	FieldSessionDeclaredHuman = "declared_human"
	// FieldSessionFlags lists short snake_case notes (weak_only, tty, …).
	FieldSessionFlags = "flags"
)

// Render history fields (CONTEXT_BUDGET 6.11, dbent/schema/render_history.go):
// one row per `lore render`, read by `render --trend` and `why-context`.
const (
	FieldRenderProjectID = "project_id"
	FieldRenderRepoID    = "repo_id"
	FieldRenderTarget    = "target_path"
	FieldRenderText      = "rendered_text"
	FieldRenderSHA256    = "rendered_sha256"
	FieldRenderBytes     = "total_bytes"
	FieldRenderTokens    = "total_tokens"
	// FieldRenderScopeSummary is JSON text kept as text, column for
	// column (it also carries the cut_summary, FA-CTX-1).
	FieldRenderScopeSummary = "scope_summary"
	FieldRenderActorID      = "rendered_by_actor_id"
)

// Tech doc page fields (dbent/schema/tech_doc_pages.go): a fetched page
// body of a registered tech doc; large and re-fetchable, so machine-local.
const (
	FieldTechDocPageProjectID  = "project_id"
	FieldTechDocPageTechDocID  = "tech_doc_id"
	FieldTechDocPageURL        = "url"
	FieldTechDocPageTitle      = "title"
	FieldTechDocPageBody       = "body"
	FieldTechDocPageContentSHA = "content_sha"
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
		// Why the tool and parent session ids are LoreRecord: they are the
		// session's own bookkeeping that lore writes, so the session-id
		// rule (which keeps them out of shared rows) must not refuse the
		// one row meant to hold them (OQ14, P3-LOCAL-5).
		Table(TableSessions, ids.PrefixSession, MachineLocal(), Description("a coding agent's, CI's or person's session on this machine"),
			Text(FieldSessionProjectID, Required()),
			DateTime(FieldSessionStartedAt, Required()),
			DateTime(FieldSessionEndedAt),
			Text(FieldSessionActorID),
			Text(FieldSessionAgentKind),
			Text(FieldSessionTool),
			Text(FieldSessionToolSessionID, LoreRecord()),
			Text(FieldSessionToolVersion),
			Text(FieldSessionModel),
			Text(FieldSessionMachineID),
			Text(FieldSessionRunID),
			Text(FieldSessionRepo),
			Text(FieldSessionBranch),
			Text(FieldSessionCwd),
			Text(FieldSessionTerminal),
			Text(FieldSessionCI),
			Text(FieldSessionParentSessionID, LoreRecord()),
			Text(FieldSessionSignal),
			JSON(FieldSessionSources),
			List(FieldSessionOuterTools, value.KindText),
			Bool(FieldSessionDeclaredHuman),
			List(FieldSessionFlags, value.KindText),
			IndexOn(FieldSessionActorID, FieldSessionTool, FieldSessionToolSessionID),
		),
		Table(TableRenderHistories, ids.PrefixRenderHistory, MachineLocal(), Description("one `lore render` of this machine: target, text, bytes, scope summary"),
			Text(FieldRenderProjectID, Required()),
			Text(FieldRenderRepoID),
			Text(FieldRenderTarget, Required()),
			Text(FieldRenderText, Required()),
			Text(FieldRenderSHA256, Required()),
			Number(FieldRenderBytes, Required()),
			Number(FieldRenderTokens),
			Text(FieldRenderScopeSummary, Required(), WithWidget(WidgetJSON)),
			Text(FieldRenderActorID),
			IndexOn(FieldRenderProjectID, FieldRenderTarget),
		),
		// tech_doc_id is text, not a ref: the tech doc lives in a store,
		// the page only on this machine (P3-LOCAL-2).
		Table(TableTechDocPages, ids.PrefixTechDocPage, MachineLocal(), Description("a fetched page of a registered tech doc, kept on this machine"),
			Text(FieldTechDocPageProjectID, Required()),
			Text(FieldTechDocPageTechDocID, Required()),
			Text(FieldTechDocPageURL, Required()),
			Text(FieldTechDocPageTitle),
			Text(FieldTechDocPageBody, Required()),
			Text(FieldTechDocPageContentSHA),
			IndexOn(FieldTechDocPageTechDocID),
			IndexOn(FieldTechDocPageURL),
		),
		Table(TablePIIPatterns, ids.PrefixPiiPattern, MachineLocal(), Description("a v0.1.17 custom PII pattern kept read-only; secret rules replaced it"),
			Text(FieldPIIName, Required()),
			Text(FieldPIIRegex, Required()),
			Bool(FieldPIIEnabled),
			Text(FieldPIISourceKind, Required()),
		),
	}
}
