package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/khanakia/entx/enttui"
)

// Task is a discrete unit of work.
//
// Belongs to:
//   - exactly one Mission (optional; via mission_id)
//   - exactly one TaskList (optional; via tasklist_id)
//   - exactly one Plan (optional; via plan_id)
//
// Lifecycle: todo → in_progress → done (or cancelled / blocked).
type Task struct{ ent.Schema }

func (Task) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixTask}}
}

// Annotations — declare any related-table projections we want in the
// enttui table view. Each RelatedColumn emits a column whose value is
// drawn from the referenced edge's target row (eager-loaded so no N+1).
func (Task) Annotations() []schema.Annotation {
	return []schema.Annotation{
		enttui.AllowCreate{},
		enttui.AllowDelete{},
		enttui.AllowBulkCopy{},
		enttui.AllowExport{},
		enttui.RelatedColumns(
			enttui.RelatedColumn{Edge: "tasklist", Field: "title", Label: "Tasklist", Pick: true},
			enttui.RelatedColumn{Edge: "mission", Field: "title", Label: "Mission", Pick: true},
			enttui.RelatedColumn{Edge: "plan", Field: "title", Label: "Plan", Pick: true},
		),
	}
}
func (Task) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("repo_id").Optional().Nillable(),
		field.String("title").NotEmpty().
			Annotations(enttui.Editable{}),
		// String (not Text) → enttui includes BodyContainsFold in the
		// global `/` filter Or-chain. SQLite stores both as TEXT.
		field.String("body").Optional().Nillable().
			Annotations(enttui.Editable{}, enttui.Filterable{}),
		field.Enum("status").
			Values(taskStatusValues...).
			Default(string(TaskStatusTodo)).
			Annotations(enttui.Editable{}),
		field.Enum("priority").
			Values(taskPriorityValues...).
			Default(string(TaskPriorityMedium)).
			Annotations(enttui.Editable{}),
		// commitment: is this agreed work? (orthogonal to status). See
		// enums.go. Default `accepted` is safe for non-agent/manual callers;
		// agent callers are forced to pass --commitment at the CLI.
		field.Enum("commitment").
			Values(taskCommitmentValues...).
			Default(string(TaskCommitmentAccepted)).
			Annotations(enttui.Editable{}),
		// deferred_until: snooze. Real+accepted but hidden from active views
		// until this time, then auto-resurfaces (query predicate, no
		// scheduler). Nil = not deferred.
		field.Time("deferred_until").Optional().Nillable(),
		field.Time("due_at").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.String("mission_id").Optional().Nillable(),
		field.String("tasklist_id").Optional().Nillable(),
		field.String("plan_id").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
		// assigned_to_actor_id is the spec's `assignee` (LORE_TEAM_SPEC
		// §3.5): content, lands with the row. Assignment is never a signal.
		field.String("assigned_to_actor_id").Optional().Nillable(),

		// ---- lore for teams (LORE_TEAM_SPEC §7, §10 C14) ----------------
		// Every field below is optional or defaulted so rows written before
		// the team feature stay valid (empty = feature unused on this row).
		// The completion time is the existing completed_at (the spec's
		// `done_at`); assignee is assigned_to_actor_id above.

		// assigned_team: team id (tem_…) when `--assign @team` named a team
		// instead of a person. Refused in linked client repos (TEAM-030).
		field.String("assigned_team").Optional().Nillable(),
		// blocked_by: `--after` edges. Nil/empty = no dependencies.
		field.JSON("blocked_by", []TaskDependency{}).Optional(),
		// claim_epoch: fencing counter mirrored from the claim signal (F7).
		// Raised by every claim, revoke, reassign and cancel; a holder whose
		// claim carries an older epoch is fenced out (E_LOST_CLAIM,
		// E_REASSIGNED, E_CANCELLED). Only ever written by a CAS-pushed live
		// write, so two branches never raise it independently.
		field.Int("claim_epoch").Default(0).NonNegative(),
		// attempts: reclaims after an expired lease; at agents.max_attempts
		// the task turns blocked (poison-task limit, §7.2).
		field.Int("attempts").Default(0).NonNegative(),
		// claim_log: last ClaimLogMax coordination events, newest last.
		field.JSON("claim_log", []ClaimLogEntry{}).Optional(),
		// timebox_seconds: `--timebox 60m` as seconds; nil = no time box.
		// Active lease time is summed across holders by the views.
		field.Int64("timebox_seconds").Optional().Nillable().NonNegative(),
		// gate: nil = ungated; see TaskGate in enums.go.
		field.Enum("gate").Values(taskGateValues...).Optional().Nillable(),
		// approved_by / approved_at / approved_hash: the human approval of
		// the gate. approved_hash covers title + body, so an edit to either
		// voids the approval (§7.4).
		field.String("approved_by").Optional().Nillable(),
		field.Time("approved_at").Optional().Nillable(),
		field.String("approved_hash").Optional().Nillable(),
		// done_by: actor id that finished the task (completed_at is when).
		field.String("done_by").Optional().Nillable(),
		// done_commit / done_pr: the code that finished it (`task done
		// --commit | --pr`), read by the merged check (F9).
		field.String("done_commit").Optional().Nillable(),
		field.Int("done_pr").Optional().Nillable().Positive(),
		// merged_at: set once, to the merge commit's committer date, when
		// done_commit (or the Lore-Task trailer) is seen on the default
		// branch. Deterministic so two finders write the same value (F9).
		field.Time("merged_at").Optional().Nillable(),
		// work_branch: the branch a holder pushes its work to, so a
		// replacement agent can fetch it (§7.4).
		field.String("work_branch").Optional().Nillable(),
		// idempotency_key: plan-file key (`--key`); `lore plan apply` run
		// twice finds the row by (project_id, mission_id, key) and does not
		// add it again. Not a DB unique constraint: uniqueness is the
		// writer's job (two branches may legitimately carry one key until
		// they merge, and DB-level uniqueness would break that import).
		field.String("idempotency_key").Optional().Nillable(),
		// role: plan role (`coordinator`, …) matched by `task next --role`.
		field.String("role").Optional().Nillable(),
	}
}
func (Task) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("mission", Mission.Type).
			Ref("tasks").
			Field("mission_id").
			Unique(),
		edge.From("tasklist", TaskList.Type).
			Ref("tasks").
			Field("tasklist_id").
			Unique(),
		edge.From("plan", Plan.Type).
			Ref("tasks").
			Field("plan_id").
			Unique(),
	}
}
func (Task) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "status"),
		index.Fields("project_id", "commitment"),
		index.Fields("mission_id"),
		index.Fields("tasklist_id"),
		index.Fields("plan_id"),
		index.Fields("due_at"),
		index.Fields("project_id", "mission_id", "idempotency_key"),
		index.Fields("assigned_team"),
	}
}
