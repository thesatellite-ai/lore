// Package period is the company-period noun of an actor (TEAM_PROFILES_DESIGN
// 3.4, 5.1-5.3): actor.status, actor.offboard, actor.rejoin and
// actor.period.list|add|edit, over the periods logic/teamprofile reads
// (PeriodsOf, CheckPeriods; TP-14, TP-22).
//
// Every op reads and writes the org roster store through
// profilestore.Store (TS-1) and runs direct on the roster App
// (profilestore.IsDirect); write ops open it with write=true, which refuses
// an agent unless agents.may_manage_people (TS-4, TP-20).
//
// Flow:
//
//	actor.status   ──▶ resolve ──▶ statusMove (5.3 table) ──▶ actor row only
//	actor.offboard ──▶ resolve ──▶ preview (period, memberships, open tasks,
//	                   owned agents, new status) ──▶ not confirmed: E_NEEDS_CONFIRM
//	                   ──▶ memberships, agents, period, actor (5.1 order) ──▶ Push
//	actor.rejoin   ──▶ resolve ──▶ preview ──▶ confirm ──▶ new period, actor ──▶ Push
//	actor.period.* ──▶ PeriodsOf ──▶ CheckPeriods ──▶ one row write ──▶ Push
//
// Why the write order of offboard puts the actor last: the status it writes
// is what offboard checks first, so a run that stopped part-way is finished
// by running the command again, and a full success answers "already left".
package period

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// Env is what the period ops need; *wire.App implements it. TaskReader is
// an optional upgrade of it (the offboarding preview's open tasks).
type Env interface {
	profilestore.Store
}

// OpenTask is one open task a person is on (TP-17): shown in the
// offboarding preview with the line that reassigns it, never changed.
type OpenTask struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Store names the store the task row lives in.
	Store string `json:"store"`
	// How is how the person is on it: TaskOnLead, TaskOnAssignee or
	// TaskOnClaim.
	How string `json:"how"`
}

// How a person is on an open task (OpenTask.How). The lead is the task's
// assigned_to_actor_id; an assignee is in its assignees list (TASK 14.2);
// the claim holder holds an active claim on its signals ref (a claim lives
// there, not on the row, so a task can be claimed by someone who is
// neither its lead nor an assignee). Lead wins over assignee over claim
// when a person is on a task more than one way.
const (
	TaskOnLead     = "lead"
	TaskOnAssignee = "assignee"
	TaskOnClaim    = "claim"
)

// ClaimsUnreadError is what a TaskReader returns, with the tasks it could
// read, when the open tasks were listed but the claim holders could not
// be read (no git repository here, a signals ref that cannot be reached).
// The preview then lists the tasks and says the claims were not read,
// rather than show the list as complete or drop it.
type ClaimsUnreadError struct {
	Tasks  []OpenTask
	Reason string
}

func (e *ClaimsUnreadError) Error() string { return "claim holders not read: " + e.Reason }

// TaskReader is the open-tasks upgrade of Env (TEAM_PROFILES 5.1 step 3):
// every open task (todo, in_progress, blocked) of actor in every store the
// host can read, as lead, assignee or claim holder. An Env without it
// previews no tasks and says so; a host whose stores cannot be read
// returns the error, which the preview shows as a warning instead of
// failing the offboard; a *ClaimsUnreadError carries the tasks it read.
type TaskReader interface {
	OpenTasks(ctx context.Context, actor ids.AnyID) ([]OpenTask, error)
}

// opErrors are the codes the period ops can return.
var opErrors = []errs.Code{
	errs.InvalidInput, errs.NotFound, errs.AmbiguousID, errs.StatusChange, errs.PeriodOverlap,
	errs.NeedsConfirm, errs.PolicyDenied, errs.Refused, errs.LeakBlocked,
}

// ops binds the operations to env.
type ops struct{ env Env }

// Ops returns the period operations bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{Name: profilestore.OpActorStatus, Summary: "Set a person's status: active, on_leave (--from, --until), left or archived",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: opErrors,
			Examples: []op.Example{
				{Title: "on leave", CLI: "lore actor status --actor @mira --to on_leave --from 2026-06-01 --until 2026-06-30", Want: "on_leave"},
				{Title: "back from leave", CLI: "lore actor status --actor @mira --to active", Want: "active"},
			}}, o.status),
		op.Define(op.Spec{Name: profilestore.OpActorOffboard, Summary: "A person leaves: end their stint and project memberships, list their open tasks, mark their agents inactive; asks first",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: opErrors, Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "offboard", CLI: `lore actor offboard --actor @mira --on 2026-09-30 --reason "moved abroad" --yes`, Want: "offboarded on"}}}, o.offboard),
		op.Define(op.Spec{Name: profilestore.OpActorRejoin, Summary: "A person who left comes back: a new stint on the same actor (memberships are not restored); asks first",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: opErrors, Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "rejoin as a contractor", CLI: `lore actor rejoin --actor @mira --type contractor --on 2027-02-01 --org-role "Contract engineer" --yes`, Want: "rejoined on"}}}, o.rejoin),
		op.Define(op.Spec{Name: profilestore.OpPeriodList, Summary: "List a person's company stints (with the one read from active/left_at)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: opErrors,
			Examples: []op.Example{{Title: "stints", CLI: "lore actor period list --actor @mira", Want: "→"}}}, o.periodList),
		op.Define(op.Spec{Name: profilestore.OpPeriodAdd, Summary: "Record a past or current stint by hand (overlaps are refused)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: opErrors,
			Examples: []op.Example{{Title: "record a stint", CLI: `lore actor period add --actor @mira --type employee --started-on 2026-01-12 --org-role "Senior engineer"`, Want: "period"}}}, o.periodAdd),
		op.Define(op.Spec{Name: profilestore.OpPeriodEdit, Summary: "Fix a stint's dates or fields (overlap rules still apply)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: opErrors,
			Examples: []op.Example{{Title: "fix a typed date", CLI: "lore actor period edit --period apd_01jd9a00c001 --ended-on 2026-09-30", Want: "period"}}}, o.periodEdit),
	}
}
