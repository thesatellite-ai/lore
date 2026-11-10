package schema

import (
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Team profile tables (TEAM_PROFILES_DESIGN 2, TP-1..TP-23): contacts,
// contact types, roles, company periods and project memberships. Every row
// lives only in the org roster store (TS-1), so each table is NeverClient
// (TS-3): the engine refuses it in any client store and the commit check
// blocks its row files in a client repo (TP-7).
//
// Natural ids (TS-5): the FIRST UniqueOn of each table is its natural key
// (engine naturalKey reads the first unique index), chosen so the same act
// on two clones (the same contact added, the same period migrated, the same
// member added on the same day) writes the same id and merges instead of
// making a twin. Keep it first when editing these definitions.
const (
	TableActorContacts  = "actor_contacts"
	TableContactTypes   = "contact_types"
	TableRoles          = "roles"
	TableActorPeriods   = "actor_periods"
	TableProjectMembers = "project_members"
)

// ProfileTables lists the team profile tables in declaration order; every
// one is NeverClient (logic/leakcheck repeats the list, pinned by a schema
// test, because it cannot rely on the registry at commit-check time).
func ProfileTables() []string {
	return []string{TableActorContacts, TableContactTypes, TableRoles, TableActorPeriods, TableProjectMembers}
}

// Profile fields added to actors (TEAM_PROFILES 2.1, TP-5, TP-15). Every
// one is Internal (the F18 projection stays {id, display_name, kind}); none
// is Sensitive (TP-6: shown plain in the org store).
const (
	FieldActorPhotoURL   = "photo_url"
	FieldActorTimezone   = "timezone"
	FieldActorLocation   = "location"
	FieldActorTitle      = "title"
	FieldActorBio        = "bio"
	FieldActorBirthday   = "birthday"
	FieldActorStatus     = "status"
	FieldActorLeaveFrom  = "leave_from"
	FieldActorLeaveUntil = "leave_until"
	// FieldActorStatusNote is why the status is what it is ("parental
	// leave"): set by actor status --note, cleared by every write that
	// makes the person active again (TEAM_PROFILES 3.4, 5.3). A row
	// written before it existed has none and reads as "".
	FieldActorStatusNote = "status_note"
	// FieldActorActive and FieldActorLeftAt are the v0.1.17 columns kept in
	// step with status by every profile write (2.1, 2.7): v0.1.17 readers
	// and rosterFind read active.
	FieldActorActive = "active"
	FieldActorLeftAt = "left_at"
)

// ProfileActorFields lists the actor fields of TEAM_PROFILES 2.1 (the
// nine of the design plus status_note).
func ProfileActorFields() []string {
	return []string{FieldActorPhotoURL, FieldActorTimezone, FieldActorLocation, FieldActorTitle, FieldActorBio,
		FieldActorBirthday, FieldActorStatus, FieldActorLeaveFrom, FieldActorLeaveUntil, FieldActorStatusNote}
}

// Actor statuses (TEAM_PROFILES 2.7, TP-15). status is the one source of
// truth; an empty status reads from active (logic/teamprofile.StatusOf).
const (
	ActorStatusActive   = "active"
	ActorStatusOnLeave  = "on_leave"
	ActorStatusLeft     = "left"
	ActorStatusArchived = "archived"
)

// ActorStatuses lists the statuses in TP-15 order.
func ActorStatuses() []string {
	return []string{ActorStatusActive, ActorStatusOnLeave, ActorStatusLeft, ActorStatusArchived}
}

// actor_contacts fields (TEAM_PROFILES 2.2).
const (
	FieldContactActor = "actor_id"
	// FieldContactType is a contact type name from the effective list
	// (built-ins overlaid by contact_types rows, TS-2); not OneOf, because
	// the list is extendable (TP-1).
	FieldContactType  = "type"
	FieldContactValue = "value"
	// FieldContactValueNorm is written by the op, never typed:
	// teamprofile.Normalize(kind, value) (6.1); identity matching reads it.
	FieldContactValueNorm = "value_norm"
	FieldContactLabel     = "label"
	// FieldContactPrimary: at most one live primary per (actor, type) (TP-3).
	FieldContactPrimary = "primary"
	// FieldContactIdentity: "use for identity" (TP-4), only on a type whose
	// identity is true.
	FieldContactIdentity = "identity"
	FieldContactNote     = "note"
)

// contact_types fields (TEAM_PROFILES 2.3).
const (
	FieldContactTypeName        = "name"
	FieldContactTypeLabel       = "label"
	FieldContactTypeKind        = "kind"
	FieldContactTypeIdentity    = "identity"
	FieldContactTypeURLTemplate = "url_template"
	FieldContactTypeRetired     = "retired"
)

// Contact kinds (contact_types.kind, TEAM_PROFILES 2.3, 6.1): each picks
// the normalisation and the value check.
const (
	ContactKindEmail  = "email"
	ContactKindHandle = "handle"
	ContactKindPhone  = "phone"
	ContactKindURL    = "url"
	ContactKindText   = "text"
)

// ContactKinds lists the contact kinds.
func ContactKinds() []string {
	return []string{ContactKindEmail, ContactKindHandle, ContactKindPhone, ContactKindURL, ContactKindText}
}

// roles fields (TEAM_PROFILES 2.4).
const (
	FieldRoleName        = "name"
	FieldRoleLabel       = "label"
	FieldRoleDescription = "description"
	FieldRoleRetired     = "retired"
)

// actor_periods fields (TEAM_PROFILES 2.5).
const (
	FieldPeriodActor     = "actor_id"
	FieldPeriodType      = "type"
	FieldPeriodStartedOn = "started_on"
	// FieldPeriodEndedOn empty = the open period.
	FieldPeriodEndedOn   = "ended_on"
	FieldPeriodOrgRole   = "org_role"
	FieldPeriodEndReason = "end_reason"
	FieldPeriodNote      = "note"
	// FieldPeriodSource is set only on a period built from active/left_at
	// (TEAM_PROFILES 8, TP-22).
	FieldPeriodSource = "source"
)

// Period types (actor_periods.type, TP-14).
const (
	PeriodTypeEmployee      = "employee"
	PeriodTypeContractor    = "contractor"
	PeriodTypeIntern        = "intern"
	PeriodTypeClientContact = "client_contact"
)

// PeriodTypes lists the period types.
func PeriodTypes() []string {
	return []string{PeriodTypeEmployee, PeriodTypeContractor, PeriodTypeIntern, PeriodTypeClientContact}
}

// PeriodSourceMigrated marks a period synthesised from an actor's
// active/left_at (TEAM_PROFILES 8, TP-22).
const PeriodSourceMigrated = "migrated"

// PeriodSources lists the actor_periods.source values.
func PeriodSources() []string { return []string{PeriodSourceMigrated} }

// project_members fields (TEAM_PROFILES 2.6).
const (
	FieldMemberProject = "project_id"
	FieldMemberActor   = "actor_id"
	// FieldMemberRoles: role names, at least one (TP-10); set-union merge so
	// two clones adding a role each keep both.
	FieldMemberRoles     = "roles"
	FieldMemberStartedOn = "started_on"
	// FieldMemberEndedOn empty = current member (TP-12).
	FieldMemberEndedOn   = "ended_on"
	FieldMemberEndReason = "end_reason"
	FieldMemberNote      = "note"
)

// project_members.end_reason values the ops write (TEAM_PROFILES 2.6);
// any other value is the --reason text.
const (
	MemberEndReasonRemoved    = "removed"
	MemberEndReasonOffboarded = "offboarded"
)

// Relation names of the profile tables.
const (
	RelProfileActor  = "actor"
	RelMemberProject = "project"
)

// profileDefs are the team profile tables (TEAM_PROFILES 2.2-2.6).
func profileDefs() []TableDef {
	return []TableDef{
		Table(TableActorContacts, ids.PrefixActorContact, Synced(), Noun("actor.contact"), NeverClient(), Description("one contact of a person: email, GitHub login, phone, … (team profiles)"),
			Ref(FieldContactActor, TableActors, Required()),
			Text(FieldContactType, Required(), Describe("a contact type name (lore contact-type list)")),
			Text(FieldContactValue, Required(), Describe("as typed")),
			Text(FieldContactValueNorm, Required(), Describe("the normalised value identity matching compares; written by lore")),
			Text(FieldContactLabel, Describe("free text: personal, work, client-<name> (TP-2)")),
			Bool(FieldContactPrimary, Describe("the primary contact of its type (TP-3)")),
			Bool(FieldContactIdentity, Describe("use for identity: git emails and logins resolve to this actor (TP-4)")),
			Text(FieldContactNote),
			UniqueOn(FieldContactActor, FieldContactType, FieldContactValueNorm), // natural id (TS-5): first
			IndexOn(FieldContactType, FieldContactValueNorm),                     // identity lookup
			IndexOn(FieldContactActor),
			// Actors are never deleted (BATCH2 #8), so TombstoneOnly.
			BelongsToRel(RelProfileActor, FieldContactActor, TableActors, TombstoneOnly),
		),
		Table(TableContactTypes, ids.PrefixContactType, Synced(), NeverClient(), Description("a contact type; rows add to or change the built-in list (TP-1, TS-2)"),
			Text(FieldContactTypeName, Required(), Describe("snake_case key; never renamed (contacts store it)")),
			Text(FieldContactTypeLabel),
			Text(FieldContactTypeKind, Required(), OneOf(ContactKinds()...), Describe("picks the normalisation and the value check")),
			Bool(FieldContactTypeIdentity, Describe("contacts of this type may be flagged for identity")),
			Text(FieldContactTypeURLTemplate, Describe("how lore actor show links a value: https://github.com/{value}")),
			Bool(FieldContactTypeRetired, Describe("out of contact add; existing contacts keep it")),
			UniqueOn(FieldContactTypeName),
		),
		Table(TableRoles, ids.PrefixRole, Synced(), NeverClient(), Description("a project role; rows add to or change the built-in list (TP-8, TS-2)"),
			Text(FieldRoleName, Required(), Describe("snake_case key; never renamed (memberships store it)")),
			Text(FieldRoleLabel),
			Text(FieldRoleDescription),
			Bool(FieldRoleRetired, Describe("out of member add --role; existing memberships keep it")),
			UniqueOn(FieldRoleName),
		),
		Table(TableActorPeriods, ids.PrefixActorPeriod, Synced(), Noun("actor.period"), NeverClient(), Description("one stint of a person at the company (TP-14)"),
			Ref(FieldPeriodActor, TableActors, Required()),
			Text(FieldPeriodType, Required(), OneOf(PeriodTypes()...)),
			Date(FieldPeriodStartedOn, Required(), Describe("first day")),
			Date(FieldPeriodEndedOn, Describe("last day; empty = the open period")),
			Text(FieldPeriodOrgRole, Describe("the org-wide title for this stint (TP-11)")),
			Text(FieldPeriodEndReason, Describe("plain text (TP-6, TP-19)")),
			Text(FieldPeriodNote),
			Text(FieldPeriodSource, OneOf(PeriodSources()...), Describe("migrated: built from the actor's active/left_at (TP-22)")),
			UniqueOn(FieldPeriodActor, FieldPeriodStartedOn), // natural id (TS-5): first
			IndexOn(FieldPeriodActor),
			BelongsToRel(RelProfileActor, FieldPeriodActor, TableActors, TombstoneOnly),
		),
		Table(TableProjectMembers, ids.PrefixProjectMember, Synced(), Noun("project.member"), NeverClient(), Description("one stint of a person on a project, with roles (TP-10, TP-12)"),
			Ref(FieldMemberProject, TableProjects, Required()),
			Ref(FieldMemberActor, TableActors, Required()),
			List(FieldMemberRoles, value.KindText, Required(), MergeBy(MergeSetUnion), Describe("role names (lore role list), at least one")),
			Date(FieldMemberStartedOn, Required()),
			Date(FieldMemberEndedOn, Describe("empty = current member")),
			Text(FieldMemberEndReason, Describe("removed, offboarded, or the --reason text")),
			Text(FieldMemberNote),
			UniqueOn(FieldMemberProject, FieldMemberActor, FieldMemberStartedOn), // natural id (TS-5): first
			IndexOn(FieldMemberActor),
			IndexOn(FieldMemberProject),
			// AllowDangling: the project row lives in the project's own folder
			// or repo, which a store-only roster App may not have attached
			// (same reason as repos.forked_from).
			relation(Relation{Name: RelMemberProject, Kind: BelongsTo, Field: FieldMemberProject, Target: TableProjects, OnDelete: TombstoneOnly, AllowDangling: true}),
			BelongsToRel(RelProfileActor, FieldMemberActor, TableActors, TombstoneOnly),
		),
	}
}
