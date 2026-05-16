// Package ids generates and validates lore opaque entity IDs.
//
// Format: <3-char-prefix>_<32-char-lowercase-hex-UUIDv7>
//
// The prefix tags the entity kind for human readability in logs and CLI output.
// The UUIDv7 portion provides:
//   - Time-sortable storage (high bits = ms-since-epoch)
//   - Collision-resistant cross-install merging (74 random bits per ID)
//   - B-tree append-mostly insert locality
//
// See PLAN.md Round 31 (introduction) and Round 34 (corrections) for design rationale.
package ids

// Prefix constants for every entity in the mini schema. Add new prefixes here
// when introducing a new table; coverage-check enforces uniqueness.
const (
	PrefixActor              = "act"
	PrefixProject            = "prj"
	PrefixProjectConfig      = "pcf"
	PrefixRepo               = "rep"
	PrefixMountAlias         = "mal"
	PrefixMemory             = "mem"
	PrefixRule               = "rul"
	PrefixDecision           = "dec"
	PrefixHotfix             = "hfx"
	PrefixPattern            = "pat"
	PrefixSnapshot           = "snp"
	PrefixPlaybook           = "pbk"
	PrefixPrompt             = "prm"
	PrefixRun                = "run"
	PrefixThread             = "thr"
	PrefixTurn               = "trn"
	PrefixAuditLog           = "aud"
	PrefixPlugin             = "plg"
	PrefixSkill              = "skl"
	PrefixTag                = "tag"
	PrefixEntityTag          = "etg"
	PrefixComment            = "cmt"
	PrefixReminder           = "rmd"
	PrefixQueryLog           = "qry"
	PrefixRenderHistory      = "rnd"
	PrefixLearnCandidate     = "lcd"
	PrefixDraft              = "drf"
	PrefixKnowledgeRef       = "kfr"
	PrefixCodeFile           = "cfl"
	PrefixInterventionMetric = "ivm"
	PrefixRuleVerifierRef    = "rvr"
	PrefixMemoryCodeRef      = "mcr"
	PrefixDBConfig           = "cfg"
	PrefixSchemaMigration    = "smg"
	PrefixTask               = "tsk"
	PrefixMission            = "msn"
	PrefixArchitectureNote   = "anr"
	PrefixBehaviour          = "bhv"
	PrefixCookbookRecipe     = "cbr"
	PrefixIncident           = "inc"
	PrefixSuggestion         = "sug"
	PrefixTastePref          = "tst"
	PrefixPlan               = "pln"
	PrefixTaskList           = "tlt"
	PrefixTaskView           = "tvw"
	PrefixWorkflow           = "wfl"
	PrefixWorkspace          = "wsp"
	PrefixTechDoc            = "tdc"
	PrefixTechDocPage        = "tdp"
	PrefixRunStep            = "rst"
	PrefixSession            = "ses"
	PrefixHandoff            = "hnd"
	PrefixAssembleRun        = "ars"
	PrefixAssembleCitation   = "asc"
	PrefixCompressRun        = "cmp"
	PrefixLearnRun           = "lrn"
	PrefixBenchRun           = "bnr"
	PrefixBenchEval          = "bve"
	PrefixBenchResult        = "bre"
	PrefixExternalSource     = "ext"
	PrefixIdentityProfile    = "ipr"
	PrefixPiiPattern         = "pip"
	PrefixTrustedPlugin      = "tpg"
	PrefixKnowledgeRevision  = "krv"
	PrefixActivityArchive    = "aac"
	PrefixCodeSymbol         = "csy"
	PrefixCommitLink         = "cml"
)

// AllPrefixes lists every registered prefix. Used by ValidateAny and tests.
var AllPrefixes = []string{
	PrefixActor,
	PrefixProject,
	PrefixProjectConfig,
	PrefixRepo,
	PrefixMountAlias,
	PrefixMemory,
	PrefixRule,
	PrefixDecision,
	PrefixHotfix,
	PrefixPattern,
	PrefixSnapshot,
	PrefixPlaybook,
	PrefixPrompt,
	PrefixThread,
	PrefixTurn,
	PrefixAuditLog,
	PrefixPlugin,
	PrefixSkill,
	PrefixTag,
	PrefixEntityTag,
	PrefixComment,
	PrefixReminder,
	PrefixQueryLog,
	PrefixRenderHistory,
	PrefixLearnCandidate,
	PrefixDraft,
	PrefixKnowledgeRef,
	PrefixCodeFile,
	PrefixInterventionMetric,
	PrefixRuleVerifierRef,
	PrefixMemoryCodeRef,
	PrefixDBConfig,
	PrefixSchemaMigration,
	PrefixTask,
	PrefixMission,
	PrefixArchitectureNote,
	PrefixBehaviour,
	PrefixCookbookRecipe,
	PrefixIncident,
	PrefixSuggestion,
	PrefixTastePref,
	PrefixPlan,
	PrefixTaskList,
	PrefixTaskView,
	PrefixWorkflow,
	PrefixWorkspace,
	PrefixTechDoc,
	PrefixTechDocPage,
	PrefixRun,
	PrefixRunStep,
	PrefixSession,
	PrefixHandoff,
	PrefixAssembleRun,
	PrefixAssembleCitation,
	PrefixCompressRun,
	PrefixLearnRun,
	PrefixBenchRun,
	PrefixBenchEval,
	PrefixBenchResult,
	PrefixExternalSource,
	PrefixIdentityProfile,
	PrefixPiiPattern,
	PrefixTrustedPlugin,
	PrefixKnowledgeRevision,
	PrefixActivityArchive,
	PrefixCodeSymbol,
	PrefixCommitLink,
}
