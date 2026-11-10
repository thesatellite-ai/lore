package schema

import "github.com/thesatellite-ai/lore/model/ids"

// Comment and commit-link tables (phase 4 group 4; tags themselves live in
// coreDefs). Column sets are v0.1.17's dbent/schema/comment.go and
// commit_link.go; id, created_at and updated_at are envelope fields.
const (
	TableComments    = "comments"
	TableCommitLinks = "commit_links"
)

// Polymorphic target fields shared by comments and commit links: the
// target's table name and its id (v0.1.17 entity_table / entity_id).
const (
	FieldEntityTable = "entity_table"
	FieldEntityID    = "entity_id"
	relEntity        = "entity"
)

// linkDefs returns the built-in comment and commit-link tables.
//
// Why the entity relation allows dangling: v0.1.17 never checked that a
// comment's or link's target existed, a target may live in another store
// (RES-OQ2), and nouns not yet on the core have no row the engine can see.
// The comment and link operations check a target themselves whenever its
// table is registered (fail closed there); lore lint still lists dangling
// targets.
func linkDefs() []TableDef {
	return []TableDef{
		Table(TableComments, ids.PrefixComment, Synced(), FTS("body"), Description("a comment on any row"),
			Text(FieldEntityTable, Required()),
			Ref(FieldEntityID, "", Required()),
			// entity_project_id: project of the target when it is not the
			// comment's own (a cross-project reference, LORE_TEAM_SPEC F15).
			Ref("entity_project_id", TableProjects),
			Text("body", Required(), WithWidget(WidgetMarkdown)),
			Ref("created_by_actor_id", TableActors),
			IndexOn(FieldEntityTable, FieldEntityID),
			danglingEntity(),
			BelongsToRel("entity_project", "entity_project_id", TableProjects, TombstoneOnly),
			BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
		),
		Table(TableCommitLinks, ids.PrefixCommitLink, Synced(), Description("a git commit linked to a row"), Noun("link"),
			Ref("project_id", TableProjects, Required()),
			Text(FieldEntityTable, Required()),
			Ref(FieldEntityID, "", Required()),
			Text("sha", Required()),
			Text("message"),
			Text("repo_path"),
			Text("author"),
			Text("committed_at"),
			Ref("created_by_actor_id", TableActors),
			// The unique key is v0.1.17's (entity_table, entity_id, sha), so a
			// link's natural id equals the v0.1.17 one.
			UniqueOn(FieldEntityTable, FieldEntityID, "sha"),
			IndexOn("project_id", FieldEntityTable, FieldEntityID),
			IndexOn("project_id", "sha"),
			danglingEntity(),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
			BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
		),
	}
}

// danglingEntity is the polymorphic entity relation (entity_table names
// the table, entity_id holds the id); deleting the target leaves the
// comment or link pointing at its tombstone. A purge of the target also
// purges its comments and commit links (PurgeWithTarget, INTEG-TRASH-6,
// engine/purge_with.go).
func danglingEntity() Item {
	return relation(Relation{
		Name: relEntity, Kind: Polymorphic, TypeField: FieldEntityTable, Field: FieldEntityID,
		OnDelete: TombstoneOnly, AllowDangling: true, PurgeWithTarget: true,
	})
}
