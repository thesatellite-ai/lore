// Package profilestore is the host seam every team profile op uses
// (TEAM_PROFILES_DESIGN 3, 11): ops/profile, ops/period and ops/member read
// and write their rows through a Roster, never through the repo engine.
//
// Why a seam of its own: every profile row lives only in the org roster
// store (TS-1), which may be an org store opened store-only outside any
// repo (BUG-T3, FA-AGENT-9). The composition root (wire.App) picks that
// store, opens it, checks the caller and pushes after a write, exactly as
// it does for `lore actor register`; the ops only validate input, apply
// the logic/teamprofile rules and render. The ops run direct (IsDirect,
// wire hostRunOp), so the repo engine is not opened around them.
//
// Flow:
//
//	op ──▶ Store.Roster(ctx, write) ──▶ the org roster store
//	        write: person, or agent with agents.may_manage_people (TS-4)
//	        no org ──▶ E_REFUSED (TS-1)   client store ──▶ E_LEAK_BLOCKED
//	   ──▶ Roster.List / Get / Create / CreateWithID / Update / Delete
//	   ──▶ Roster.Push (write ops, once, at the end)   ──▶ done()
//
// It lives at ops/profilestore, not ops/internal/profilestore as the design
// first named it: the composition root implements Store, and Go's internal
// rule keeps wire from importing anything under ops/internal.
package profilestore

import (
	"context"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/store"
)

// Store opens the org roster store for the profile ops (TS-1).
type Store interface {
	// Roster opens it; write=true checks the caller (person, or an agent
	// with agents.may_manage_people) and refuses a repo without an org
	// (E_REFUSED) and a client store (E_LEAK_BLOCKED). done closes it and
	// is never nil, also on error.
	Roster(ctx context.Context, write bool) (r Roster, done func(), err error)
}

// Roster is the opened org roster store. Every write goes through the
// engine of the roster App (scanned, stamped, audited, natural ids for the
// profile tables, TS-5); nothing is pushed until Push.
type Roster interface {
	// Source is the store the rows live in ("agency/org").
	Source() store.Source
	// Where names it for messages ("agency org/"); hidden in quiet output.
	Where() string
	// Caller is the person running the command: for a coding agent, the
	// person it acts for (owner rule 2026-11-11); the rows still record
	// the agent as the writer.
	Caller() ids.ID[ids.Actor]
	// Today is the App's wall-clock day as midnight UTC (the default of
	// every --on; teamprofile.Day).
	Today() time.Time
	// List returns the live rows of table (no tombstones, nothing in the
	// trash), sorted by id.
	List(ctx context.Context, table row.Table) ([]row.Row, error)
	// Get returns one row of table; E_NOT_FOUND when absent.
	// InTrash is E_IN_TRASH (trash.InTrashErr: who deleted it, `lore trash
	// restore <id>`) when row id of table is in the roster store's trash,
	// else nil (a live, purged or never-written row). An op whose live
	// lookup missed asks it before answering its own E_NOT_FOUND, so a
	// trashed profile row reads like every other trashed row (DELETION 3.2).
	InTrash(ctx context.Context, table row.Table, id ids.AnyID) error
	Get(ctx context.Context, table row.Table, id ids.AnyID) (row.Row, error)
	// Create writes a new row; a profile table's natural id comes from its
	// first UniqueOn (TS-5), and a live row with the same key answers
	// E_NAME_TAKEN "already exists" (INTEG-QUESTIONS-1).
	Create(ctx context.Context, table row.Table, fields map[string]value.Value) (row.Row, error)
	// CreateWithID writes a new row under id (a materialised synthesised
	// period, TEAM_PROFILES 8).
	CreateWithID(ctx context.Context, table row.Table, id ids.AnyID, fields map[string]value.Value) (row.Row, error)
	// Update patches a live row: a value sets a field, value.Null clears it.
	Update(ctx context.Context, table row.Table, id ids.AnyID, patch map[string]value.Value) (row.Row, error)
	// Delete moves a row into the trash with reason (restorable,
	// DELETION_DESIGN). Actors are never deleted (E_REFUSED).
	Delete(ctx context.Context, table row.Table, id ids.AnyID, reason string) error
	// ResolveActor finds the actors row a reference names (TEAM_PROFILES
	// 3): @handle, an act_ id or a stable key (human:mira@acme.com). Left
	// and archived people resolve too. No match is E_NOT_FOUND; two
	// matches are E_AMBIGUOUS_ID listing them. A4 adds the contact forms
	// (email:…, github:…, a bare email).
	ResolveActor(ctx context.Context, ref string) (row.Row, error)
	// Warnings are the engine's warnings of every write through this
	// roster since it was opened (W_URL_CREDENTIALS, W_AUDIT_DEFERRED, a
	// scan warning, ...), one WarningLine each, in write order. A write op
	// copies them into its output, so a warning the engine raised reaches
	// the caller as it does for every other write.
	Warnings() []string
	// Push syncs the roster's store at once (best effort: a failure leaves
	// the change committed locally for `lore sync`).
	Push(ctx context.Context)
}

// FolderProjecter is an optional upgrade of an opened Roster: the project
// row of the roster's own folder (the org folder's), made on the folder's
// first write when it has none. A teams row belongs to it (teams
// project_id is required, LORE_TEAM_SPEC F18), and its id is the folder's
// _meta.json project_id, so two machines that add the first team at once
// write one project row. wire's roster implements it; an op whose Roster
// lacks it refuses with E_UNSUPPORTED.
type FolderProjecter interface {
	FolderProject(ctx context.Context) (ids.ID[ids.Project], error)
}

// ProjectNamer is an optional upgrade of a profile op's Env: the name of
// the project a prj_ id names, looked up in every store the host can read.
//
// Why: project_members.project_id is AllowDangling (TEAM_PROFILES 2.6), so
// the project row usually lives in a project or repo store the roster
// store does not hold, and a preview that only reads the roster would
// print bare prj_ ids (offboard, rejoin, actor show). found is false when
// no store the host reads has the project (or two different projects
// answer), and the caller then shows the id: a label never fails a
// command. wire.App implements it over FindProject.
type ProjectNamer interface {
	ProjectName(ctx context.Context, id string) (name string, found bool)
}

// WarningLine is one engine warning as a profile op prints it: "CODE
// message", the field first when the warning names one ("CODE field:
// message"). The same shape as the member ops' W_TEAM_NOT_MEMBER lines.
func WarningLine(code, field, message string) string {
	if field != "" {
		return code + " " + field + ": " + message
	}
	return code + " " + message
}

// WarningText is ws as output lines, one per warning after the success
// line (as the member ops print W_TEAM_NOT_MEMBER), "" for none.
func WarningText(ws []string) string {
	var b strings.Builder
	for _, w := range ws {
		b.WriteString(w + "\n")
	}
	return b.String()
}

// ActorRowCounter is an optional upgrade of a profile op's Env: how many
// live rows an actor wrote, per table, in every store the host can read
// (TEAM_PROFILES 6.4: `actor contact add --merge` shows the duplicate's
// row counts before the person confirms, so they can see what the merge
// will make reports follow). A row counts once even when two stores hold
// a copy; rows of an older lore without a created_by key are not counted.
// Sorted by table; tables with no row are left out.
type ActorRowCounter interface {
	ActorRowCounts(ctx context.Context, actor ids.AnyID) ([]TableCount, error)
}

// TableCount is one table's count in ActorRowCounter's answer.
type TableCount struct {
	Table string `json:"table"`
	Rows  int    `json:"rows"`
}

// The operation names of the profile ops (TEAM_PROFILES 3). The op
// packages declare their specs with these names, so IsDirect and the
// registry can never disagree.
const (
	// ops/profile (3.1-3.3).
	OpActorEdit       = "actor.edit"
	OpContactAdd      = "actor.contact.add"
	OpContactEdit     = "actor.contact.edit"
	OpContactRemove   = "actor.contact.remove"
	OpContactList     = "actor.contact.list"
	OpContactTypeList = "contact_type.list"
	OpContactTypeAdd  = "contact_type.add"
	OpContactTypeEdit = "contact_type.edit"
	// ops/period (3.4).
	OpActorStatus   = "actor.status"
	OpActorOffboard = "actor.offboard"
	OpActorRejoin   = "actor.rejoin"
	OpPeriodList    = "actor.period.list"
	OpPeriodEdit    = "actor.period.edit"
	OpPeriodAdd     = "actor.period.add"
	// ops/member (3.5, 3.6).
	OpMemberAdd    = "project.member.add"
	OpMemberEdit   = "project.member.edit"
	OpMemberRemove = "project.member.remove"
	OpMemberList   = "project.member.list"
	OpRoleList     = "role.list"
	OpRoleAdd      = "role.add"
	OpRoleEdit     = "role.edit"
	// ops/team teams.go: the org's teams (LORE_TEAM_SPEC F18, v0.1.17
	// `lore team add|list`).
	OpTeamAdd          = "team.add"
	OpTeamList         = "team.list"
	OpTeamShow         = "team.show"
	OpTeamRemove       = "team.remove"
	OpTeamMemberAdd    = "team.member.add"
	OpTeamMemberRemove = "team.member.remove"
)

// directOps is the set IsDirect answers from. Read-only after init.
var directOps = map[string]struct{}{
	OpActorEdit: {}, OpContactAdd: {}, OpContactEdit: {}, OpContactRemove: {}, OpContactList: {},
	OpContactTypeList: {}, OpContactTypeAdd: {}, OpContactTypeEdit: {},
	OpActorStatus: {}, OpActorOffboard: {}, OpActorRejoin: {}, OpPeriodList: {}, OpPeriodEdit: {}, OpPeriodAdd: {},
	OpMemberAdd: {}, OpMemberEdit: {}, OpMemberRemove: {}, OpMemberList: {},
	OpRoleList: {}, OpRoleAdd: {}, OpRoleEdit: {},
	// The team invite ops (ONBOARDING_DESIGN 4.6) read and write the
	// invites table through this same Store on the org roster, so they run
	// direct like the profile ops: outside a repo too, with --yes and the
	// caller carried (revoke and --grant ask first).
	setup.OpTeamInvite: {}, setup.OpTeamInviteList: {}, setup.OpTeamInviteRevoke: {}, setup.OpTeamInviteResend: {},
	// The teams rows live in the org roster store too (owner report
	// 2026-11-11: the team commands were missing), so they run direct.
	OpTeamAdd: {}, OpTeamList: {}, OpTeamShow: {}, OpTeamRemove: {}, OpTeamMemberAdd: {}, OpTeamMemberRemove: {},
}

// IsDirect reports whether op name runs on the roster App (wire hostRunOp):
// every profile op, read or write, because the roster may be a store-only
// org store outside any repo.
func IsDirect(name string) bool {
	_, ok := directOps[name]
	return ok
}

// OpNames lists every direct op name (the profile ops, then the team
// invite ops and the team ops), sorted as declared above.
func OpNames() []string {
	return []string{
		OpActorEdit, OpContactAdd, OpContactEdit, OpContactRemove, OpContactList,
		OpContactTypeList, OpContactTypeAdd, OpContactTypeEdit,
		OpActorStatus, OpActorOffboard, OpActorRejoin, OpPeriodList, OpPeriodEdit, OpPeriodAdd,
		OpMemberAdd, OpMemberEdit, OpMemberRemove, OpMemberList,
		OpRoleList, OpRoleAdd, OpRoleEdit,
		setup.OpTeamInvite, setup.OpTeamInviteList, setup.OpTeamInviteRevoke, setup.OpTeamInviteResend,
		OpTeamAdd, OpTeamList, OpTeamShow, OpTeamRemove, OpTeamMemberAdd, OpTeamMemberRemove,
	}
}
