package schema

import "github.com/thesatellite-ai/lore/model/ids"

// Content-2 table names (phase 5 group 6): the rest of v0.1.17's knowledge
// rows. Every one is synced team data in v0.1.17
// (saas/pkg/aicoder/lsync/registry.go syncedTables); tech_doc_pages is
// machine-local there ("fetched page bodies; large and re-fetchable") and is
// deliberately not declared here: it stays on lore.db.
const (
	TableArchitectureNotes = "architecture_notes"
	TableBehaviours        = "behaviours"
	TableCookbookRecipes   = "cookbook_recipes"
	TableIncidents         = "incidents"
	TableSuggestions       = "suggestions"
	TableTastePrefs        = "taste_prefs"
	TableWorkflows         = "workflows"
	TableWorkspaces        = "workspaces"
	TableHandoffs          = "handoffs"
	TableSnapshots         = "snapshots"
	TableTechDocs          = "tech_docs"
	TableExternalSources   = "external_sources"
)

// Cache-only join tables filled from the content-2 tables' tag_ids
// (v0.1.17 `lore tag attach` could tag any row).
const (
	JoinArchitectureNoteTags = "architecture_note_tags"
	JoinBehaviourTags        = "behaviour_tags"
	JoinCookbookRecipeTags   = "cookbook_recipe_tags"
	JoinIncidentTags         = "incident_tags"
	JoinSuggestionTags       = "suggestion_tags"
	JoinTastePrefTags        = "taste_pref_tags"
	JoinWorkflowTags         = "workflow_tags"
	JoinWorkspaceTags        = "workspace_tags"
	JoinHandoffTags          = "handoff_tags"
	JoinSnapshotTags         = "snapshot_tags"
	JoinTechDocTags          = "tech_doc_tags"
	JoinExternalSourceTags   = "external_source_tags"
)

// content2Defs returns the built-in architecture-note, behaviour,
// cookbook-recipe, incident, suggestion, taste-pref, workflow, workspace,
// handoff, snapshot, tech-doc and external-source tables. Columns are
// v0.1.17's (dbent/schema/<table>.go; snapshots add the LifecycleMixin's
// synced columns); project_id is required where v0.1.17 made it NotEmpty.
func content2Defs() []TableDef {
	return []TableDef{
		Table(TableArchitectureNotes, ids.PrefixArchitectureNote, Synced(), FTS("title", contentBody), Description("a note on how the system is built"),
			Ref(contentProjectID, TableProjects, Required()),
			Ref("repo_id", TableRepos),
			Text("title", Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			Ref(contentCreatedBy, TableActors),
			IndexOn(contentProjectID, "repo_id"),
			content2Common(JoinArchitectureNoteTags, true),
		),
		Table(TableBehaviours, ids.PrefixBehaviour, Synced(), FTS(fieldName, contentBody), Description("how the agent should behave in a situation"),
			Ref(contentProjectID, TableProjects, Required()),
			Text(fieldName, Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			Ref(contentCreatedBy, TableActors),
			content2Common(JoinBehaviourTags, true),
		),
		Table(TableCookbookRecipes, ids.PrefixCookbookRecipe, Synced(), FTS("title", contentBody, "language"), Description("a worked recipe for a recurring task"),
			Ref(contentProjectID, TableProjects, Required()),
			Text("title", Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			Text("language"),
			Ref(contentCreatedBy, TableActors),
			content2Common(JoinCookbookRecipeTags, true),
		),
		Table(TableIncidents, ids.PrefixIncident, Synced(), FTS("title", contentBody), Description("something that went wrong and what was learned"),
			Ref(contentProjectID, TableProjects, Required()),
			Text("title", Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			Text("severity_str"),
			DateTime("resolved_at"),
			Ref(contentCreatedBy, TableActors),
			content2Common(JoinIncidentTags, true),
		),
		Table(TableSuggestions, ids.PrefixSuggestion, Synced(), FTS("title", contentBody, "status_str"), Description("a pending proposal the team reviews"),
			Ref(contentProjectID, TableProjects, Required()),
			Text("title", Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			// status_str is free-form in v0.1.17 ("active" on add).
			Text("status_str", Required()),
			Ref(contentCreatedBy, TableActors),
			content2Common(JoinSuggestionTags, true),
		),
		Table(TableTastePrefs, ids.PrefixTastePref, Synced(), FTS(contentBody, "scope"), Description("a subjective preference (\"I prefer X\")"),
			Ref(contentProjectID, TableProjects, Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			Text("scope"),
			Ref(contentCreatedBy, TableActors),
			content2Common(JoinTastePrefTags, true),
		),
		Table(TableWorkflows, ids.PrefixWorkflow, Synced(), FTS(fieldName, contentBody), Description("a named sequence of steps"),
			Ref(contentProjectID, TableProjects, Required()),
			Text(fieldName, Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			content2Common(JoinWorkflowTags, false),
		),
		Table(TableWorkspaces, ids.PrefixWorkspace, Synced(), FTS(fieldName, contentBody), Description("a named working context"),
			Ref(contentProjectID, TableProjects, Required()),
			Text(fieldName, Required()),
			// body is optional on workspaces in v0.1.17.
			Text(contentBody, WithWidget(WidgetMarkdown)),
			content2Common(JoinWorkspaceTags, false),
		),
		Table(TableHandoffs, ids.PrefixHandoff, Synced(), FTS(contentBody, "status_str"), Description("work handed to another actor (shows in their inbox)"),
			Ref(contentProjectID, TableProjects, Required()),
			Ref("from_actor_id", TableActors),
			Ref("to_actor_id", TableActors),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			// status_str: open until accepted or declined (v0.1.17 also
			// wrote "active" and "acked").
			Text("status_str", Required()),
			Ref("task_id", TableTasks),
			Ref("mission_id", TableMissions),
			IndexOn("task_id"),
			IndexOn("mission_id"),
			BelongsToRel("project", contentProjectID, TableProjects, Restrict),
			BelongsToRel("from", "from_actor_id", TableActors, TombstoneOnly),
			BelongsToRel("to", "to_actor_id", TableActors, TombstoneOnly),
			BelongsToRel("task", "task_id", TableTasks, SetNull),
			BelongsToRel("mission", "mission_id", TableMissions, SetNull),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinHandoffTags, Cascade),
		),
		Table(TableSnapshots, ids.PrefixSnapshot, Synced(), FTS("title", contentBody), Description("a point-in-time knowledge capture"),
			Ref(contentProjectID, TableProjects, Required()),
			Ref("repo_id", TableRepos),
			Text("title", Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			DateTime("taken_at", Required()),
			Ref(contentCreatedBy, TableActors),
			contentLifecycle(),
			IndexOn(contentProjectID, "repo_id"),
			IndexOn("taken_at"),
			content2Common(JoinSnapshotTags, true),
		),
		Table(TableTechDocs, ids.PrefixTechDoc, Synced(), FTS(fieldName, "description", "base_url"), Description("a registered external documentation source (pages are fetched per machine)"),
			Ref(contentProjectID, TableProjects, Required()),
			Text(fieldName, Required()),
			Text("base_url"),
			Text("description"),
			content2Common(JoinTechDocTags, false),
		),
		Table(TableExternalSources, ids.PrefixExternalSource, Synced(), Description("a registered source learn-from reads (disabled until enabled)"),
			Ref(contentProjectID, TableProjects, Required()),
			Text("kind", Required()),
			Text(fieldName, Required()),
			// config_json is v0.1.17's JSON config blob, kept as its text.
			Text("config_json"),
			Bool("enabled"),
			content2Common(JoinExternalSourceTags, false),
		),
	}
}

// content2Common is the project relation, the optional created_by and
// repo relations (by which columns the table has) and the O11 tags of a
// content-2 table. createdBy says whether the table has
// created_by_actor_id; the repo relation is added when repo_id exists.
func content2Common(join string, createdBy bool) Item {
	return itemFunc(func(t *TableDef) {
		items := []Item{BelongsToRel("project", contentProjectID, TableProjects, Restrict)}
		for _, f := range t.Fields {
			if f.Name == "repo_id" {
				items = append(items, BelongsToRel("repo", "repo_id", TableRepos, SetNull))
			}
		}
		if createdBy {
			items = append(items, BelongsToRel("created_by", contentCreatedBy, TableActors, TombstoneOnly))
		}
		items = append(items, tagIDs(), ManyToManyRel(RelTags, FieldTagIDs, TableTags, join, Cascade))
		for _, it := range items {
			it.apply(t)
		}
	})
}
