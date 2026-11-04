package schema

import "github.com/thesatellite-ai/lore/model/ids"

// Infra tables (phase 5 group 9) beyond coreDefs (actors, projects and
// repos live there). Only synced v0.1.17 tables are declared here: the
// machine-local ones of v0.1.17's lsync registry (identity_profiles,
// mount_alias, pii_patterns, trusted_plugins, task_views, config,
// schema_migrations, users) never travel in a row and stay in lore.db,
// and project_configs is not a 0.1.18 table at all (CFG12: its settings
// become policies rows or ~/.lore/config.yaml).
const (
	// TableTeams is the roster's teams (LORE_TEAM_SPEC F18): an @handle
	// that resolves to a group of actors, per project (the org folder's
	// project for an org roster, the repo's project without one).
	TableTeams = "teams"
	// FieldTeamMembers is the list-of-ref field holding a team's actors
	// (v0.1.17 member_actor_ids, a JSON string list of act_ ids).
	FieldTeamMembers = "member_actor_ids"
	// RelTeamMembers is the many-to-many relation over FieldTeamMembers.
	RelTeamMembers = "members"
	// JoinTeamMembers is the cache-only join table filled from
	// FieldTeamMembers (O11).
	JoinTeamMembers = "team_members"
)

// infraDefs returns the built-in infra tables beyond coreDefs. The columns
// are v0.1.17's (dbent/schema/team.go), so a v0.1.17 row file reads
// unchanged; id, created_at and updated_at are envelope fields.
func infraDefs() []TableDef {
	return []TableDef{
		Table(TableTeams, ids.PrefixTeam, Synced(), Description("roster teams: an @handle naming a group of actors"),
			Ref("project_id", TableProjects, Required()),
			Text("handle", Required()),
			Text("display_name"),
			RefList(FieldTeamMembers, TableActors, Describe("ids of the team's actors"), Label("Members")),
			Bool("active"),
			DateTime("left_at"),
			UniqueOn("project_id", "handle"),
			IndexOn("handle"),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
			ManyToManyRel(RelTeamMembers, FieldTeamMembers, TableActors, JoinTeamMembers, Cascade),
		),
	}
}
