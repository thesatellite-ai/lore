package schema

// Typed string enums for every field.Enum value across the schema package.
// Schemas reference the *Values slices in field.Enum(...).Values(...) and the
// typed constants for .Default(...). Keeps the value list and the Go-side
// constants in one place so renames / additions can't drift.

// --- Memory.kind -----------------------------------------------------------

type MemoryKind string

const (
	MemoryKindCore       MemoryKind = "core"
	MemoryKindRetrieved  MemoryKind = "retrieved"
	MemoryKindEpisodic   MemoryKind = "episodic"
	MemoryKindProcedural MemoryKind = "procedural"
	MemoryKindArchival   MemoryKind = "archival"
)

var memoryKindValues = []string{
	string(MemoryKindCore),
	string(MemoryKindRetrieved),
	string(MemoryKindEpisodic),
	string(MemoryKindProcedural),
	string(MemoryKindArchival),
}

// --- MemoryCodeRef.relation ------------------------------------------------

type MemoryCodeRefRelation string

const (
	MemoryCodeRefRelationReferences  MemoryCodeRefRelation = "references"
	MemoryCodeRefRelationAppliesTo   MemoryCodeRefRelation = "applies_to"
	MemoryCodeRefRelationDerivedFrom MemoryCodeRefRelation = "derived_from"
	MemoryCodeRefRelationCausedBy    MemoryCodeRefRelation = "caused_by"
)

var memoryCodeRefRelationValues = []string{
	string(MemoryCodeRefRelationReferences),
	string(MemoryCodeRefRelationAppliesTo),
	string(MemoryCodeRefRelationDerivedFrom),
	string(MemoryCodeRefRelationCausedBy),
}

// --- Task.status -----------------------------------------------------------

type TaskStatus string

const (
	TaskStatusTodo       TaskStatus = "todo"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusDone       TaskStatus = "done"
	TaskStatusCancelled  TaskStatus = "cancelled"
	TaskStatusBlocked    TaskStatus = "blocked"
)

var taskStatusValues = []string{
	string(TaskStatusTodo),
	string(TaskStatusInProgress),
	string(TaskStatusDone),
	string(TaskStatusCancelled),
	string(TaskStatusBlocked),
}

// --- Task.priority ---------------------------------------------------------

type TaskPriority string

const (
	TaskPriorityLow    TaskPriority = "low"
	TaskPriorityMedium TaskPriority = "medium"
	TaskPriorityHigh   TaskPriority = "high"
	TaskPriorityUrgent TaskPriority = "urgent"
)

var taskPriorityValues = []string{
	string(TaskPriorityLow),
	string(TaskPriorityMedium),
	string(TaskPriorityHigh),
	string(TaskPriorityUrgent),
}

// --- KnowledgeRef.confidence ----------------------------------------------

type KnowledgeRefConfidence string

const (
	KnowledgeRefConfidenceExtracted KnowledgeRefConfidence = "extracted"
	KnowledgeRefConfidenceInferred  KnowledgeRefConfidence = "inferred"
	KnowledgeRefConfidenceAmbiguous KnowledgeRefConfidence = "ambiguous"
)

var knowledgeRefConfidenceValues = []string{
	string(KnowledgeRefConfidenceExtracted),
	string(KnowledgeRefConfidenceInferred),
	string(KnowledgeRefConfidenceAmbiguous),
}

// --- Decision.status -------------------------------------------------------

type DecisionStatus string

const (
	DecisionStatusProposed   DecisionStatus = "proposed"
	DecisionStatusAccepted   DecisionStatus = "accepted"
	DecisionStatusSuperseded DecisionStatus = "superseded"
	DecisionStatusDeprecated DecisionStatus = "deprecated"
)

var decisionStatusValues = []string{
	string(DecisionStatusProposed),
	string(DecisionStatusAccepted),
	string(DecisionStatusSuperseded),
	string(DecisionStatusDeprecated),
}

// --- BenchResult.arm -------------------------------------------------------

type BenchArm string

const (
	BenchArmBaseline  BenchArm = "baseline"
	BenchArmWithSkill BenchArm = "with_skill"
	BenchArmAblationA BenchArm = "ablation_a"
	BenchArmAblationB BenchArm = "ablation_b"
	BenchArmAblationC BenchArm = "ablation_c"
	BenchArmAblationD BenchArm = "ablation_d"
)

var benchArmValues = []string{
	string(BenchArmBaseline),
	string(BenchArmWithSkill),
	string(BenchArmAblationA),
	string(BenchArmAblationB),
	string(BenchArmAblationC),
	string(BenchArmAblationD),
}

// --- BenchResult.grade -----------------------------------------------------

type BenchGrade string

const (
	BenchGradePass    BenchGrade = "pass"
	BenchGradeFail    BenchGrade = "fail"
	BenchGradeError   BenchGrade = "error"
	BenchGradeSkipped BenchGrade = "skipped"
)

var benchGradeValues = []string{
	string(BenchGradePass),
	string(BenchGradeFail),
	string(BenchGradeError),
	string(BenchGradeSkipped),
}

// --- BenchRun.status -------------------------------------------------------

type BenchRunStatus string

const (
	BenchRunStatusRunning  BenchRunStatus = "running"
	BenchRunStatusComplete BenchRunStatus = "complete"
	BenchRunStatusAborted  BenchRunStatus = "aborted"
	BenchRunStatusFailed   BenchRunStatus = "failed"
)

var benchRunStatusValues = []string{
	string(BenchRunStatusRunning),
	string(BenchRunStatusComplete),
	string(BenchRunStatusAborted),
	string(BenchRunStatusFailed),
}

// --- LearnCandidate.source_kind -------------------------------------------

type LearnCandidateSourceKind string

const (
	LearnCandidateSourceAgentProposal LearnCandidateSourceKind = "agent-proposal"
	LearnCandidateSourceLearnFrom     LearnCandidateSourceKind = "learn-from"
	LearnCandidateSourcePlugin        LearnCandidateSourceKind = "plugin"
)

var learnCandidateSourceKindValues = []string{
	string(LearnCandidateSourceAgentProposal),
	string(LearnCandidateSourceLearnFrom),
	string(LearnCandidateSourcePlugin),
}

// --- LearnCandidate.status ------------------------------------------------

type LearnCandidateStatus string

const (
	LearnCandidateStatusPending  LearnCandidateStatus = "pending"
	LearnCandidateStatusAccepted LearnCandidateStatus = "accepted"
	LearnCandidateStatusRejected LearnCandidateStatus = "rejected"
	LearnCandidateStatusExpired  LearnCandidateStatus = "expired"
)

var learnCandidateStatusValues = []string{
	string(LearnCandidateStatusPending),
	string(LearnCandidateStatusAccepted),
	string(LearnCandidateStatusRejected),
	string(LearnCandidateStatusExpired),
}

// --- Mission.status -------------------------------------------------------

type MissionStatus string

const (
	MissionStatusActive    MissionStatus = "active"
	MissionStatusPaused    MissionStatus = "paused"
	MissionStatusDone      MissionStatus = "done"
	MissionStatusCancelled MissionStatus = "cancelled"
)

var missionStatusValues = []string{
	string(MissionStatusActive),
	string(MissionStatusPaused),
	string(MissionStatusDone),
	string(MissionStatusCancelled),
}

// --- Rule.activation ------------------------------------------------------

type RuleActivation string

const (
	RuleActivationAlways   RuleActivation = "always"
	RuleActivationGlob     RuleActivation = "glob"
	RuleActivationSemantic RuleActivation = "semantic"
	RuleActivationManual   RuleActivation = "manual"
)

var ruleActivationValues = []string{
	string(RuleActivationAlways),
	string(RuleActivationGlob),
	string(RuleActivationSemantic),
	string(RuleActivationManual),
}

// --- Rule.severity --------------------------------------------------------

type RuleSeverity string

const (
	RuleSeverityMust   RuleSeverity = "must"
	RuleSeverityShould RuleSeverity = "should"
	RuleSeverityMay    RuleSeverity = "may"
)

var ruleSeverityValues = []string{
	string(RuleSeverityMust),
	string(RuleSeverityShould),
	string(RuleSeverityMay),
}

// --- BenchEval.category ---------------------------------------------------

type BenchEvalCategory string

const (
	BenchEvalCategoryRuleTrigger     BenchEvalCategory = "rule-trigger"
	BenchEvalCategoryHotfixAvoid     BenchEvalCategory = "hotfix-avoid"
	BenchEvalCategoryDecisionRespect BenchEvalCategory = "decision-respect"
	BenchEvalCategoryConvention      BenchEvalCategory = "convention"
	BenchEvalCategoryCaptureBack     BenchEvalCategory = "capture-back"
	BenchEvalCategoryCustom          BenchEvalCategory = "custom"
)

var benchEvalCategoryValues = []string{
	string(BenchEvalCategoryRuleTrigger),
	string(BenchEvalCategoryHotfixAvoid),
	string(BenchEvalCategoryDecisionRespect),
	string(BenchEvalCategoryConvention),
	string(BenchEvalCategoryCaptureBack),
	string(BenchEvalCategoryCustom),
}

// --- BenchEval.linked_kind ------------------------------------------------

type BenchEvalLinkedKind string

const (
	BenchEvalLinkedKindRule     BenchEvalLinkedKind = "rule"
	BenchEvalLinkedKindHotfix   BenchEvalLinkedKind = "hotfix"
	BenchEvalLinkedKindDecision BenchEvalLinkedKind = "decision"
	BenchEvalLinkedKindMemory   BenchEvalLinkedKind = "memory"
	BenchEvalLinkedKindPattern  BenchEvalLinkedKind = "pattern"
	BenchEvalLinkedKindNone     BenchEvalLinkedKind = "none"
)

var benchEvalLinkedKindValues = []string{
	string(BenchEvalLinkedKindRule),
	string(BenchEvalLinkedKindHotfix),
	string(BenchEvalLinkedKindDecision),
	string(BenchEvalLinkedKindMemory),
	string(BenchEvalLinkedKindPattern),
	string(BenchEvalLinkedKindNone),
}

// --- BenchEval.grader_kind ------------------------------------------------

type BenchEvalGraderKind string

const (
	BenchEvalGraderProgrammatic BenchEvalGraderKind = "programmatic"
	BenchEvalGraderLLMJudge     BenchEvalGraderKind = "llm-judge"
	BenchEvalGraderGoldenDiff   BenchEvalGraderKind = "golden-diff"
	BenchEvalGraderComposite    BenchEvalGraderKind = "composite"
)

var benchEvalGraderKindValues = []string{
	string(BenchEvalGraderProgrammatic),
	string(BenchEvalGraderLLMJudge),
	string(BenchEvalGraderGoldenDiff),
	string(BenchEvalGraderComposite),
}

// --- SchemaMigration.status -----------------------------------------------

type SchemaMigrationStatus string

const (
	SchemaMigrationStatusApplied    SchemaMigrationStatus = "applied"
	SchemaMigrationStatusInProgress SchemaMigrationStatus = "in_progress"
)

var schemaMigrationStatusValues = []string{
	string(SchemaMigrationStatusApplied),
	string(SchemaMigrationStatusInProgress),
}

// --- Reminder.recurrence --------------------------------------------------

type ReminderRecurrence string

const (
	ReminderRecurrence7d  ReminderRecurrence = "7d"
	ReminderRecurrence30d ReminderRecurrence = "30d"
	ReminderRecurrence1m  ReminderRecurrence = "1m"
	ReminderRecurrence3m  ReminderRecurrence = "3m"
	ReminderRecurrence6m  ReminderRecurrence = "6m"
	ReminderRecurrence1y  ReminderRecurrence = "1y"
)

var reminderRecurrenceValues = []string{
	string(ReminderRecurrence7d),
	string(ReminderRecurrence30d),
	string(ReminderRecurrence1m),
	string(ReminderRecurrence3m),
	string(ReminderRecurrence6m),
	string(ReminderRecurrence1y),
}

// --- Hotfix.severity ------------------------------------------------------

type HotfixSeverity string

const (
	HotfixSeverityLow      HotfixSeverity = "low"
	HotfixSeverityMedium   HotfixSeverity = "medium"
	HotfixSeverityHigh     HotfixSeverity = "high"
	HotfixSeverityCritical HotfixSeverity = "critical"
)

var hotfixSeverityValues = []string{
	string(HotfixSeverityLow),
	string(HotfixSeverityMedium),
	string(HotfixSeverityHigh),
	string(HotfixSeverityCritical),
}

// --- Actor.kind -----------------------------------------------------------

type ActorKind string

const (
	ActorKindHuman  ActorKind = "human"
	ActorKindAgent  ActorKind = "agent"
	ActorKindHook   ActorKind = "hook"
	ActorKindPlugin ActorKind = "plugin"
	ActorKindCron   ActorKind = "cron"
	ActorKindSystem ActorKind = "system"
)

var actorKindValues = []string{
	string(ActorKindHuman),
	string(ActorKindAgent),
	string(ActorKindHook),
	string(ActorKindPlugin),
	string(ActorKindCron),
	string(ActorKindSystem),
}

// --- RuleVerifierRef.verifier_kind ----------------------------------------

type RuleVerifierKind string

const (
	RuleVerifierKindRegex  RuleVerifierKind = "regex"
	RuleVerifierKindShell  RuleVerifierKind = "shell"
	RuleVerifierKindGoFn   RuleVerifierKind = "go-fn"
	RuleVerifierKindCustom RuleVerifierKind = "custom"
)

var ruleVerifierKindValues = []string{
	string(RuleVerifierKindRegex),
	string(RuleVerifierKindShell),
	string(RuleVerifierKindGoFn),
	string(RuleVerifierKindCustom),
}

// --- InterventionMetric.kind ----------------------------------------------

type InterventionMetricKind string

const (
	InterventionMetricKindCorrection InterventionMetricKind = "correction"
	InterventionMetricKindManualEdit InterventionMetricKind = "manual_edit"
	InterventionMetricKindRetry      InterventionMetricKind = "retry"
	InterventionMetricKindRollback   InterventionMetricKind = "rollback"
	InterventionMetricKindUndo       InterventionMetricKind = "undo"
)

var interventionMetricKindValues = []string{
	string(InterventionMetricKindCorrection),
	string(InterventionMetricKindManualEdit),
	string(InterventionMetricKindRetry),
	string(InterventionMetricKindRollback),
	string(InterventionMetricKindUndo),
}
