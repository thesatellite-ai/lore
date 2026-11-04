package ids

// Every registered id prefix. The v0.1.17 prefixes are copied unchanged from
// saas/pkg/aicoder/ids/registry.go (ids already written to files must keep
// parsing); the 0.1.18 settings and store prefixes follow (spec 4.3).
//
// Invariant: prefixes are unique (prefix_test.go) and never reused for
// another table: an id names its table forever.
const (
	// PrefixActor is the v0.1.17 prefix "act".
	PrefixActor Prefix = "act"
	// PrefixProject is the v0.1.17 prefix "prj".
	PrefixProject Prefix = "prj"
	// PrefixProjectConfig is the v0.1.17 prefix "pcf".
	PrefixProjectConfig Prefix = "pcf"
	// PrefixRepo is the v0.1.17 prefix "rep".
	PrefixRepo Prefix = "rep"
	// PrefixMountAlias is the v0.1.17 prefix "mal".
	PrefixMountAlias Prefix = "mal"
	// PrefixMemory is the v0.1.17 prefix "mem".
	PrefixMemory Prefix = "mem"
	// PrefixRule is the v0.1.17 prefix "rul".
	PrefixRule Prefix = "rul"
	// PrefixDecision is the v0.1.17 prefix "dec".
	PrefixDecision Prefix = "dec"
	// PrefixHotfix is the v0.1.17 prefix "hfx".
	PrefixHotfix Prefix = "hfx"
	// PrefixPattern is the v0.1.17 prefix "pat".
	PrefixPattern Prefix = "pat"
	// PrefixSnapshot is the v0.1.17 prefix "snp".
	PrefixSnapshot Prefix = "snp"
	// PrefixPlaybook is the v0.1.17 prefix "pbk".
	PrefixPlaybook Prefix = "pbk"
	// PrefixPrompt is the v0.1.17 prefix "prm".
	PrefixPrompt Prefix = "prm"
	// PrefixRun is the v0.1.17 prefix "run".
	PrefixRun Prefix = "run"
	// PrefixThread is the v0.1.17 prefix "thr".
	PrefixThread Prefix = "thr"
	// PrefixTurn is the v0.1.17 prefix "trn".
	PrefixTurn Prefix = "trn"
	// PrefixAuditLog is the v0.1.17 prefix "aud".
	PrefixAuditLog Prefix = "aud"
	// PrefixPlugin is the v0.1.17 prefix "plg".
	PrefixPlugin Prefix = "plg"
	// PrefixSkill is the v0.1.17 prefix "skl".
	PrefixSkill Prefix = "skl"
	// PrefixTag is the v0.1.17 prefix "tag".
	PrefixTag Prefix = "tag"
	// PrefixEntityTag is the v0.1.17 prefix "etg".
	PrefixEntityTag Prefix = "etg"
	// PrefixComment is the v0.1.17 prefix "cmt".
	PrefixComment Prefix = "cmt"
	// PrefixReminder is the v0.1.17 prefix "rmd".
	PrefixReminder Prefix = "rmd"
	// PrefixQueryLog is the v0.1.17 prefix "qry".
	PrefixQueryLog Prefix = "qry"
	// PrefixRenderHistory is the v0.1.17 prefix "rnd".
	PrefixRenderHistory Prefix = "rnd"
	// PrefixLearnCandidate is the v0.1.17 prefix "lcd".
	PrefixLearnCandidate Prefix = "lcd"
	// PrefixDraft is the v0.1.17 prefix "drf".
	PrefixDraft Prefix = "drf"
	// PrefixKnowledgeRef is the v0.1.17 prefix "kfr".
	PrefixKnowledgeRef Prefix = "kfr"
	// PrefixCodeFile is the v0.1.17 prefix "cfl".
	PrefixCodeFile Prefix = "cfl"
	// PrefixInterventionMetric is the v0.1.17 prefix "ivm".
	PrefixInterventionMetric Prefix = "ivm"
	// PrefixRuleVerifierRef is the v0.1.17 prefix "rvr".
	PrefixRuleVerifierRef Prefix = "rvr"
	// PrefixMemoryCodeRef is the v0.1.17 prefix "mcr".
	PrefixMemoryCodeRef Prefix = "mcr"
	// PrefixDBConfig is the v0.1.17 prefix "cfg".
	PrefixDBConfig Prefix = "cfg"
	// PrefixSchemaMigration is the v0.1.17 prefix "smg".
	PrefixSchemaMigration Prefix = "smg"
	// PrefixTask is the v0.1.17 prefix "tsk".
	PrefixTask Prefix = "tsk"
	// PrefixMission is the v0.1.17 prefix "msn".
	PrefixMission Prefix = "msn"
	// PrefixArchitectureNote is the v0.1.17 prefix "anr".
	PrefixArchitectureNote Prefix = "anr"
	// PrefixBehaviour is the v0.1.17 prefix "bhv".
	PrefixBehaviour Prefix = "bhv"
	// PrefixCookbookRecipe is the v0.1.17 prefix "cbr".
	PrefixCookbookRecipe Prefix = "cbr"
	// PrefixIncident is the v0.1.17 prefix "inc".
	PrefixIncident Prefix = "inc"
	// PrefixSuggestion is the v0.1.17 prefix "sug".
	PrefixSuggestion Prefix = "sug"
	// PrefixTastePref is the v0.1.17 prefix "tst".
	PrefixTastePref Prefix = "tst"
	// PrefixPlan is the v0.1.17 prefix "pln".
	PrefixPlan Prefix = "pln"
	// PrefixTaskList is the v0.1.17 prefix "tlt".
	PrefixTaskList Prefix = "tlt"
	// PrefixTaskView is the v0.1.17 prefix "tvw".
	PrefixTaskView Prefix = "tvw"
	// PrefixWorkflow is the v0.1.17 prefix "wfl".
	PrefixWorkflow Prefix = "wfl"
	// PrefixWorkspace is the v0.1.17 prefix "wsp".
	PrefixWorkspace Prefix = "wsp"
	// PrefixTechDoc is the v0.1.17 prefix "tdc".
	PrefixTechDoc Prefix = "tdc"
	// PrefixTechDocPage is the v0.1.17 prefix "tdp".
	PrefixTechDocPage Prefix = "tdp"
	// PrefixRunStep is the v0.1.17 prefix "rst".
	PrefixRunStep Prefix = "rst"
	// PrefixSession is the v0.1.17 prefix "ses".
	PrefixSession Prefix = "ses"
	// PrefixHandoff is the v0.1.17 prefix "hnd".
	PrefixHandoff Prefix = "hnd"
	// PrefixAssembleRun is the v0.1.17 prefix "ars".
	PrefixAssembleRun Prefix = "ars"
	// PrefixAssembleCitation is the v0.1.17 prefix "asc".
	PrefixAssembleCitation Prefix = "asc"
	// PrefixCompressRun is the v0.1.17 prefix "cmp".
	PrefixCompressRun Prefix = "cmp"
	// PrefixLearnRun is the v0.1.17 prefix "lrn".
	PrefixLearnRun Prefix = "lrn"
	// PrefixBenchRun is the v0.1.17 prefix "bnr".
	PrefixBenchRun Prefix = "bnr"
	// PrefixBenchEval is the v0.1.17 prefix "bve".
	PrefixBenchEval Prefix = "bve"
	// PrefixBenchResult is the v0.1.17 prefix "bre".
	PrefixBenchResult Prefix = "bre"
	// PrefixExternalSource is the v0.1.17 prefix "ext".
	PrefixExternalSource Prefix = "ext"
	// PrefixIdentityProfile is the v0.1.17 prefix "ipr".
	PrefixIdentityProfile Prefix = "ipr"
	// PrefixPiiPattern is the v0.1.17 prefix "pip".
	PrefixPiiPattern Prefix = "pip"
	// PrefixTrustedPlugin is the v0.1.17 prefix "tpg".
	PrefixTrustedPlugin Prefix = "tpg"
	// PrefixKnowledgeRevision is the v0.1.17 prefix "krv".
	PrefixKnowledgeRevision Prefix = "krv"
	// PrefixActivityArchive is the v0.1.17 prefix "aac".
	PrefixActivityArchive Prefix = "aac"
	// PrefixCodeSymbol is the v0.1.17 prefix "csy".
	PrefixCodeSymbol Prefix = "csy"
	// PrefixCommitLink is the v0.1.17 prefix "cml".
	PrefixCommitLink Prefix = "cml"
	// PrefixTeam is the v0.1.17 prefix "tem".
	PrefixTeam Prefix = "tem"
	// PrefixStore marks a store (STORE_SERVER_SPEC 4.1 _o.store).
	PrefixStore Prefix = "sto"
	// PrefixMachine marks a machine profile (SECRETS 5.2).
	PrefixMachine Prefix = "mch"
	// PrefixPolicy marks a policy row (spec 4.3).
	PrefixPolicy Prefix = "pol"
	// PrefixMetaKey marks a metadata key row (spec 4.3).
	PrefixMetaKey Prefix = "mky"
	// PrefixMetaValue marks an allowed metadata value row (spec 4.3).
	PrefixMetaValue Prefix = "val"
	// PrefixKind marks a resource kind row (spec 4.3).
	PrefixKind Prefix = "knd"
	// PrefixKindField marks a field of a kind (spec 4.3).
	PrefixKindField Prefix = "fld"
	// PrefixVar marks a variable row (spec 4.3).
	PrefixVar Prefix = "var"
	// PrefixSecretRule marks a secret rule row (spec 4.3).
	PrefixSecretRule Prefix = "srl"
	// PrefixSecretAllow marks a secret allow entry row (spec 4.3).
	PrefixSecretAllow Prefix = "sal"
	// PrefixResource marks a resource row (spec 4.4).
	PrefixResource Prefix = "res"
	// PrefixTimeEntry marks one time entry on a task row's time_entries
	// list (TIME_TRACKING_DESIGN 6.2); entries are not rows, the id is the
	// merge key of the list.
	PrefixTimeEntry Prefix = "tme"
	// PrefixBatch marks one bulk action (a bulk delete, an import). XQ1:
	// every bulk action of every noun uses this one prefix, and `lore
	// batch show` reports the kind (task, meta, resource, delete, import),
	// because per-kind prefixes (mbt_, rbt_, tbt_, dbt_) made one batch
	// list impossible. A batch is not a row: the id lives on the audit
	// events of the rows it wrote.
	PrefixBatch Prefix = "bat"
)

// allPrefixes is the iteration list of every constant above. Read-only.
var allPrefixes = [...]Prefix{
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
	PrefixRun,
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
	PrefixTeam,
	PrefixStore,
	PrefixMachine,
	PrefixPolicy,
	PrefixMetaKey,
	PrefixMetaValue,
	PrefixKind,
	PrefixKindField,
	PrefixVar,
	PrefixSecretRule,
	PrefixSecretAllow,
	PrefixResource,
	PrefixTimeEntry,
	PrefixBatch,
}

// Prefixes returns a fresh copy of every registered prefix, in declaration order.
func Prefixes() []Prefix { return append([]Prefix(nil), allPrefixes[:]...) }

// registered is the lookup form of allPrefixes, built once at init.
var registered = func() map[Prefix]struct{} {
	m := make(map[Prefix]struct{}, len(allPrefixes))
	for _, p := range allPrefixes {
		m[p] = struct{}{}
	}
	return m
}()

// Registered reports whether p is one of Prefixes.
func (p Prefix) Registered() bool {
	_, ok := registered[p]
	return ok
}
