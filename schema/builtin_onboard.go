package schema

import (
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Onboarding tables (ONBOARDING_DESIGN 3.4, 4.5, Q10, Q11).
//
//   - invites: the org's pending, accepted, revoked and expired invites.
//     It lives only in the org roster store (Q10), so it is NeverClient
//     like the team profile tables (TS-3) and Synced so every admin can
//     list and revoke, and the joiner's check reads it.
//   - setup_progress and known_repos: this machine's own bookkeeping
//     (machine.db), never synced (MachineLocal). setup_progress keeps an
//     unfinished setup's answers and last check results, so closing the
//     page loses nothing and a half-finished setup never changes how lore
//     behaves (3.1). known_repos lists every repo lore has run in here, so
//     `lore doctor --all` checks those instead of walking whole folders.
//
// Natural ids (TS-5): the first UniqueOn is the natural key, so a re-run
// (the same step saved again, the same repo seen again) rewrites the same
// row instead of adding a twin.
const (
	TableInvites       = "invites"
	TableSetupProgress = "setup_progress"
	TableKnownRepos    = "known_repos"
)

// invites fields (ONB 4.5).
const (
	// FieldInviteEmail is normalised with teamprofile.Normalize (TEAM_PROFILES 6.1).
	FieldInviteEmail = "email"
	// FieldInviteGitHub is the GitHub user the admin expects (access check).
	FieldInviteGitHub = "github"
	// FieldInviteRoles are role names, applied per project in projects.
	FieldInviteRoles      = "roles"
	FieldInviteProjects   = "projects"
	FieldInvitePeriodType = "period_type"
	FieldInviteStatus     = "status"
	// FieldInviteExpiresOn defaults to 14 days after creation (Q8; set by
	// the op, InviteDefaultDays).
	FieldInviteExpiresOn  = "expires_on"
	FieldInviteAcceptedBy = "accepted_by"
	FieldInviteNote       = "note"
)

// Invite statuses (ONB 4.5). One invite per person, single use (Q8).
const (
	InviteStatusPending  = "pending"
	InviteStatusAccepted = "accepted"
	InviteStatusRevoked  = "revoked"
	InviteStatusExpired  = "expired"
)

// InviteStatuses lists the invite statuses.
func InviteStatuses() []string {
	return []string{InviteStatusPending, InviteStatusAccepted, InviteStatusRevoked, InviteStatusExpired}
}

// InviteDefaultDays is an invite's default lifetime (Q8).
const InviteDefaultDays = 14

// Relation names of invites.
const (
	RelInviteAcceptedBy = "accepted_by"
)

// setup_progress fields (ONB 3.4). updated_at is the row envelope's.
const (
	FieldProgressSetup = "setup"
	// FieldProgressStep is an onboard.StepID.
	FieldProgressStep = "step"
	// FieldProgressAnswers is the JSON of onboard.Answers: validated
	// answers only, never a secret (no question type takes one).
	FieldProgressAnswers = "answers"
	// FieldProgressChecks is the JSON list of the last onboard.CheckResult.
	FieldProgressChecks = "checks"
	// FieldProgressStatus is an onboard.StepStatus.
	FieldProgressStatus = "status"
)

// ProgressStatuses lists the setup_progress.status values; it repeats
// onboard.StepStatuses (schema is below logic), pinned by a test.
func ProgressStatuses() []string { return []string{"todo", "ready", "failed", "done", "skipped"} }

// known_repos fields (Q11).
const (
	FieldKnownRepoID       = "repo_id"
	FieldKnownRepoRoot     = "root"
	FieldKnownRepoLastSeen = "last_seen"
)

// NeverClientTables lists every NeverClient table: the team profile
// tables plus invites (all org roster only).
func NeverClientTables() []string { return append(ProfileTables(), TableInvites) }

// onboardDefs are the onboarding tables.
func onboardDefs() []TableDef {
	return []TableDef{
		Table(TableInvites, ids.PrefixInvite, Synced(), Noun("team.invite"), NeverClient(), Description("an invite to join the org: who, which roles and projects, and its state (onboarding)"),
			Text(FieldInviteEmail, Required(), Describe("normalised email of the person invited")),
			Text(FieldInviteGitHub, Describe("the GitHub user expected to accept")),
			List(FieldInviteRoles, value.KindText, Describe("role names applied on each invite project")),
			// projects holds prj_ ids as text, not a list of refs: the design's
			// AllowDangling (4.5) is a relation flag, and the only relation a
			// list field may carry is many-to-many with a join table. Text
			// ids never refuse a write when the project rows are not attached
			// (store-only roster App), which is what AllowDangling buys.
			List(FieldInviteProjects, value.KindText, Describe("prj_ ids of the projects the person joins on accept")),
			Text(FieldInvitePeriodType, Required(), OneOf(PeriodTypes()...)),
			Text(FieldInviteStatus, Required(), OneOf(InviteStatuses()...)),
			Date(FieldInviteExpiresOn, Required(), Describe("last day the invite can be accepted")),
			Ref(FieldInviteAcceptedBy, TableActors, Describe("set on accept")),
			Text(FieldInviteNote),
			// No natural UniqueOn: one email may hold a revoked and a
			// pending invite (4.5), so ids are random and the index only
			// speeds the lookup.
			IndexOn(FieldInviteEmail),
			BelongsToRel(RelInviteAcceptedBy, FieldInviteAcceptedBy, TableActors, TombstoneOnly),
		),
		Table(TableSetupProgress, ids.PrefixSetupProgress, MachineLocal(), Description("one step of an unfinished setup on this machine: answers, last checks, status"),
			Text(FieldProgressSetup, Required()),
			Text(FieldProgressStep, Required()),
			JSON(FieldProgressAnswers),
			JSON(FieldProgressChecks),
			Text(FieldProgressStatus, Required(), OneOf(ProgressStatuses()...)),
			UniqueOn(FieldProgressSetup, FieldProgressStep), // natural id: first
			IndexOn(FieldProgressSetup),
		),
		Table(TableKnownRepos, ids.PrefixKnownRepo, MachineLocal(), Description("a repo lore has run in on this machine; lore doctor --all checks these"),
			Text(FieldKnownRepoID, Required()),
			Text(FieldKnownRepoRoot, Required(), LoreRecord(), Describe("absolute path of the repo root")),
			DateTime(FieldKnownRepoLastSeen),
			UniqueOn(FieldKnownRepoRoot), // natural id: one row per clone path
			IndexOn(FieldKnownRepoID),
		),
	}
}
