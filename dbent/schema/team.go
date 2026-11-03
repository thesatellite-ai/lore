package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Team is a named group of actors on an org roster (LORE_TEAM_SPEC F18,
// §10 C15). `--assign @frontend` sets a task's assigned_team to a team id;
// `task next --mine` then offers the task to every member.
//
// Teams live with the roster in the org folder of the org's store
// (`<org_folder>/.lore/data/teams/`). Team names are internal: a linked
// client repo never receives a team row or a team assignment (TEAM-030).
//
// The id is derived from (project_id, handle) — see natural_ids.go — so
// two machines adding the same team produce one file, not two.
type Team struct{ ent.Schema }

func (Team) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixTeam}}
}

func (Team) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		// handle: the @name. Unique per roster across actors AND teams; the
		// (project_id, handle) index below covers teams, the writer checks
		// actors. Immutable because it is the natural key of the id.
		field.String("handle").NotEmpty().Immutable(),
		field.String("display_name").Optional().Nillable(),
		// member_actor_ids: actor ids on the same roster. Order carries no
		// meaning; writers keep it sorted so two equal sets encode equally.
		field.Strings("member_actor_ids").Optional(),
		// active / left_at: teams are deactivated, never deleted, so old
		// assignments still resolve.
		field.Bool("active").Default(true),
		field.Time("left_at").Optional().Nillable(),
	}
}

func (Team) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "handle").Unique(),
	}
}
