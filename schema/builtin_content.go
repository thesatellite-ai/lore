package schema

import "github.com/thesatellite-ai/lore/model/ids"

// Content table names (phase 4 group 2): reusable how-to knowledge.
const (
	TablePatterns  = "patterns"
	TablePlaybooks = "playbooks"
	TablePrompts   = "prompts"
)

// Cache-only join tables filled from the content tables' tag_ids (v0.1.17
// let `lore tag attach` tag any row, so these tables carry tags too).
const (
	JoinPatternTags  = "pattern_tags"
	JoinPlaybookTags = "playbook_tags"
	JoinPromptTags   = "prompt_tags"
)

// Shared content field names.
const (
	contentProjectID  = "project_id"
	contentBody       = "body"
	contentCreatedBy  = "created_by_actor_id"
	contentArchivedAt = "archived_at"
)

// contentDefs returns the built-in pattern, playbook and prompt tables.
// Columns are v0.1.17's (dbent/schema/{pattern,playbook,prompt}.go plus
// LifecycleMixin on patterns and playbooks); last_accessed_at is
// machine-local and left out, like on memories.
func contentDefs() []TableDef {
	return []TableDef{
		Table(TablePatterns, ids.PrefixPattern, Synced(), FTS("title", contentBody), Description("how code should look: an exemplar, idiom or convention"),
			Ref(contentProjectID, TableProjects, Required()),
			Ref("repo_id", TableRepos),
			Text("title", Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			Text("language"),
			Ref("superseded_by_id", TablePatterns),
			// CQ12: a folder scope ranks the pattern in lore context only.
			Ref(FieldScope, TableScopes),
			Ref(contentCreatedBy, TableActors),
			contentLifecycle(),
			IndexOn(contentProjectID, "repo_id"),
			IndexOn("language"),
			BelongsToRel("project", contentProjectID, TableProjects, Restrict),
			BelongsToRel("repo", "repo_id", TableRepos, SetNull),
			BelongsToRel("scope", FieldScope, TableScopes, SetNull),
			BelongsToRel("created_by", contentCreatedBy, TableActors, TombstoneOnly),
			SelfRel("superseded_by", "superseded_by_id", SetNull),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinPatternTags, Cascade),
		),
		Table(TablePlaybooks, ids.PrefixPlaybook, Synced(), FTS(fieldName, contentBody), Description("a reusable workflow: steps to follow"),
			// project_id empty = a global playbook (v0.1.17).
			Ref(contentProjectID, TableProjects),
			Text(fieldName, Required()),
			Text("description", Required()),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			// composes is v0.1.17's JSON array of playbook ids, kept as
			// its text so a v0.1.17 row reads back unchanged.
			Text("composes"),
			Ref(contentCreatedBy, TableActors),
			contentLifecycle(),
			IndexOn(contentProjectID),
			BelongsToRel("project", contentProjectID, TableProjects, Restrict),
			BelongsToRel("created_by", contentCreatedBy, TableActors, TombstoneOnly),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinPlaybookTags, Cascade),
		),
		Table(TablePrompts, ids.PrefixPrompt, Synced(), FTS(fieldName, contentBody), Description("a slash-command template with {{args.x}} placeholders"),
			Ref(contentProjectID, TableProjects, Required()),
			Text(fieldName, Required()),
			Text("description", Required()),
			// args_schema is v0.1.17's JSON schema of the args, as text.
			Text("args_schema"),
			Text(contentBody, Required(), WithWidget(WidgetMarkdown)),
			DateTime(contentArchivedAt),
			Ref(contentCreatedBy, TableActors),
			UniqueOn(contentProjectID, fieldName),
			BelongsToRel("project", contentProjectID, TableProjects, Restrict),
			BelongsToRel("created_by", contentCreatedBy, TableActors, TombstoneOnly),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinPromptTags, Cascade),
		),
	}
}

// contentLifecycle is v0.1.17's LifecycleMixin columns as one item (the synced
// ones: trust_score, confidence, last_validated_at, archived_at,
// source_kind, source_ref).
func contentLifecycle() Item {
	items := []Item{
		Number("trust_score"),
		Number("confidence"),
		DateTime("last_validated_at"),
		DateTime(contentArchivedAt),
		Text("source_kind", Required()),
		Text("source_ref"),
	}
	return itemFunc(func(t *TableDef) {
		for _, it := range items {
			it.apply(t)
		}
	})
}
