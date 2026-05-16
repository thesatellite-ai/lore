package constants

// EnvelopeKind is the value of the `kind` field in every JSON envelope
// emitted by the lore CLI. Format: "<entity-short>.<verb>" — keeps
// downstream consumers stable when verbs are added.
//
// Most kinds are composable via Entity.EnvelopeKind(VerbX). The constants
// below pin the exact strings for grep-ability and to surface drift at
// compile time.
const (
	KindActorList   = "actor.list"
	KindActorShow   = "actor.show"
	KindProjectList = "project.list"
	KindProjectShow = "project.show"
	KindRepoList    = "repo.list"

	KindMemoryList = "memory.list"
	KindMemoryShow = "memory.show"

	KindRuleList = "rule.list"
	KindRuleShow = "rule.show"

	KindDecisionList = "decision.list"
	KindDecisionShow = "decision.show"

	KindHotfixList = "hotfix.list"
	KindHotfixShow = "hotfix.show"

	KindPatternList = "pattern.list"
	KindPatternShow = "pattern.show"

	KindSnapshotList = "snapshot.list"
	KindSnapshotShow = "snapshot.show"

	KindPlaybookList = "playbook.list"
	KindPlaybookShow = "playbook.show"

	KindPromptList = "prompt.list"
	KindPromptShow = "prompt.show"

	KindRunList   = "run.list"
	KindRunShow   = "run.show"
	KindRunReplay = "run.replay"

	KindMissionList = "mission.list"

	KindTaskList   = "task.list"
	KindTaskShow   = "task.show"
	KindTaskSearch = "task.search"

	KindTaskListList = "taskList.list"
	KindTaskListShow = "taskList.show"

	KindPlanList = "plan.list"
	KindPlanShow = "plan.show"

	KindReminderList = "reminder.list"

	KindTagList     = "tag.list"
	KindCommentList = "comment.list"
	KindCommitShow  = "commit.show"
	KindLinkList    = "link.list"

	KindArchitectureNoteList = "architectureNote.list"
	KindArchitectureNoteShow = "architectureNote.show"

	KindBehaviourList = "behaviour.list"
	KindBehaviourShow = "behaviour.show"

	KindCookbookRecipeList = "cookbookRecipe.list"
	KindCookbookRecipeShow = "cookbookRecipe.show"

	KindIncidentList = "incident.list"
	KindIncidentShow = "incident.show"

	KindSuggestionList = "suggestion.list"
	KindSuggestionShow = "suggestion.show"

	KindTastePrefList = "tastePref.list"
	KindTastePrefShow = "tastePref.show"

	KindWorkflowList = "workflow.list"
	KindWorkflowShow = "workflow.show"

	KindWorkspaceList = "workspace.list"
	KindWorkspaceShow = "workspace.show"

	KindHandoffList = "handoff.list"
	KindHandoffShow = "handoff.show"

	KindTechDocList = "techdoc.list"
	KindTechDocShow = "techdoc.show"

	KindBenchEvalList      = "bench.eval.list"
	KindBenchEvalShow      = "bench.eval.show"
	KindBenchRunList       = "bench.run.list"
	KindBenchRunShow       = "bench.run.show"
	KindBenchRunStart      = "bench.run.start"
	KindBenchResultList    = "bench.result.list"
	KindBenchResultShow    = "bench.result.show"
	KindBenchResultStats   = "bench.result.stats"
	KindBenchReportSummary = "bench.report.summary"
	KindBenchReportByCat   = "bench.report.by-category"
	KindBenchReportCompare = "bench.report.compare"
	KindBenchReportAnalyze = "bench.report.analyze"
	KindBenchReportTrend   = "bench.report.trend"
	KindBenchGraderAudit   = "bench.grader.audit"
	KindBenchGraderTest    = "bench.grader.test"

	KindLearnList    = "learn.list"
	KindSessionList  = "session.list"
	KindQueryLogList = "queryLog.list"
	KindRenderList   = "renderHistory.list"
	KindConfigList   = "config.list"

	KindProjectSharedInit = "project.shared-init"
	KindProjectSharedList = "project.shared-list"

	KindSearchGlobal = "search.global"
	KindSearchStatus = "search.status"

	KindMountAliasList     = "mount-alias.list"
	KindExternalSourceList = "external-source.list"
	KindPiiPatternList     = "pii-pattern.list"
	KindTaskViewList       = "task-view.list"
	KindPluginList         = "plugin.list"
	KindSkillCompile       = "skill.compile"
)
