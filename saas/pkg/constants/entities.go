package constants

// Entity is the canonical name for an entity type in the lore domain.
// It is the value stored in polymorphic `entity_table` columns (comment,
// entity_tag, commit_link, …) and used as the verb prefix in JSON envelope
// `kind` strings. Always plural / snake-case, matching the underlying SQL
// table name.
type Entity string

const (
	EntityActor             Entity = "actors"
	EntityProject           Entity = "projects"
	EntityRepo              Entity = "repos"
	EntityMemory            Entity = "memories"
	EntityRule              Entity = "rules"
	EntityDecision          Entity = "decisions"
	EntityHotfix            Entity = "hotfixes"
	EntityPattern           Entity = "patterns"
	EntitySnapshot          Entity = "snapshots"
	EntityPlaybook          Entity = "playbooks"
	EntityPrompt            Entity = "prompts"
	EntityRun               Entity = "runs"
	EntityMission           Entity = "missions"
	EntityTask              Entity = "tasks"
	EntityTaskList          Entity = "task_lists"
	EntityPlan              Entity = "plans"
	EntityReminder          Entity = "reminders"
	EntityTag               Entity = "tags"
	EntityComment           Entity = "comments"
	EntityCommitLink        Entity = "commit_links"
	EntityArchitectureNote  Entity = "architecture_notes"
	EntityBehaviour         Entity = "behaviours"
	EntityCookbookRecipe    Entity = "cookbook_recipes"
	EntityIncident          Entity = "incidents"
	EntitySuggestion        Entity = "suggestions"
	EntityTastePref         Entity = "taste_prefs"
	EntityWorkflow          Entity = "workflows"
	EntityWorkspace         Entity = "workspaces"
	EntityHandoff           Entity = "handoffs"
	EntityTechDoc           Entity = "tech_docs"
	EntityAssembleRun       Entity = "assemble_runs"
	EntityAssembleCitation  Entity = "assemble_citations"
	EntityBenchEval         Entity = "bench_evals"
	EntityBenchRun          Entity = "bench_runs"
	EntityBenchResult       Entity = "bench_results"
	EntityCompressRun       Entity = "compress_runs"
	EntityLearnRun          Entity = "learn_runs"
	EntityLearnCandidate    Entity = "learn_candidates"
	EntitySession           Entity = "sessions"
	EntityAuditLog          Entity = "audit_logs"
	EntityKnowledgeRevision Entity = "knowledge_revisions"
)

// FTSName is the short, singular name used by the FTS5 registry and as the
// verb prefix in JSON envelope kinds (e.g. "task" in "task.search"). This is
// the form most CLI surfaces use externally; Table is the SQL form.
func (e Entity) FTSName() string {
	if s, ok := entityFTSNames[e]; ok {
		return s
	}
	return string(e)
}

// Table returns the SQL table name (the underlying value).
func (e Entity) Table() string { return string(e) }

// EnvelopeKind builds the canonical JSON envelope `kind` for the given verb,
// e.g. EntityTask.EnvelopeKind(VerbSearch) == "task.search".
func (e Entity) EnvelopeKind(verb string) string {
	return e.FTSName() + "." + verb
}

// SearchKind returns the envelope kind used by per-entity `search` commands,
// which historically use the FTS-registry lowercase form (e.g.
// "tasklist.search", "architecturenote.search") — distinct from the
// camelCase used elsewhere.
func (e Entity) SearchKind() string {
	return e.FTSEntity() + "." + VerbSearch
}

// FTSEntity is the lowercase compressed singular form used by the FTS5
// registry (e.g. "task", "tasklist", "architecturenote") — distinct from
// FTSName which preserves camelCase for envelope `kind` strings.
func (e Entity) FTSEntity() string {
	if s, ok := entityFTSCompressed[e]; ok {
		return s
	}
	return e.FTSName()
}

var entityFTSCompressed = map[Entity]string{
	EntityTaskList:         "tasklist",
	EntityArchitectureNote: "architecturenote",
	EntityCookbookRecipe:   "cookbookrecipe",
	EntityTastePref:        "tastepref",
}

var entityFTSNames = map[Entity]string{
	EntityActor:             "actor",
	EntityProject:           "project",
	EntityRepo:              "repo",
	EntityMemory:            "memory",
	EntityRule:              "rule",
	EntityDecision:          "decision",
	EntityHotfix:            "hotfix",
	EntityPattern:           "pattern",
	EntitySnapshot:          "snapshot",
	EntityPlaybook:          "playbook",
	EntityPrompt:            "prompt",
	EntityRun:               "run",
	EntityMission:           "mission",
	EntityTask:              "task",
	EntityTaskList:          "taskList",
	EntityPlan:              "plan",
	EntityReminder:          "reminder",
	EntityTag:               "tag",
	EntityComment:           "comment",
	EntityCommitLink:        "commit",
	EntityArchitectureNote:  "architectureNote",
	EntityBehaviour:         "behaviour",
	EntityCookbookRecipe:    "cookbookRecipe",
	EntityIncident:          "incident",
	EntitySuggestion:        "suggestion",
	EntityTastePref:         "tastePref",
	EntityWorkflow:          "workflow",
	EntityWorkspace:         "workspace",
	EntityHandoff:           "handoff",
	EntityTechDoc:           "techdoc",
	EntityAssembleRun:       "assembleRun",
	EntityAssembleCitation:  "assembleCitation",
	EntityBenchEval:         "bench.eval",
	EntityBenchRun:          "bench.run",
	EntityBenchResult:       "bench.result",
	EntityCompressRun:       "compressRun",
	EntityLearnRun:          "learnRun",
	EntityLearnCandidate:    "learnCandidate",
	EntitySession:           "session",
	EntityAuditLog:          "audit",
	EntityKnowledgeRevision: "knowledgeRevision",
}

// Verbs used in EnvelopeKind composition. Not exhaustive; add as needed.
const (
	VerbList       = "list"
	VerbShow       = "show"
	VerbSearch     = "search"
	VerbAdd        = "add"
	VerbEdit       = "edit"
	VerbArchive    = "archive"
	VerbUnarchive  = "unarchive"
	VerbStart      = "start"
	VerbDone       = "done"
	VerbCancel     = "cancel"
	VerbReplay     = "replay"
	VerbCompile    = "compile"
	VerbStats      = "stats"
	VerbSummary    = "summary"
	VerbCompare    = "compare"
	VerbAnalyze    = "analyze"
	VerbTrend      = "trend"
	VerbByCategory = "by-category"
	VerbAudit      = "audit"
	VerbTest       = "test"
	VerbSharedInit = "shared-init"
	VerbSharedList = "shared-list"
	VerbStatus     = "status"
	VerbGlobal     = "global"
)
