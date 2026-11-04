package schema

import "github.com/thesatellite-ai/lore/model/ids"

// Knowledge tables (phase 4 group 1): rules, decisions and hotfixes. The
// columns are v0.1.17's (dbent/schema/{rule,decision,hotfix}.go plus the
// LifecycleMixin), so a v0.1.17 row file reads unchanged; machine-local
// columns (last_accessed_at) are left out as for memories.
const (
	TableRules     = "rules"
	TableDecisions = "decisions"
	TableHotfixes  = "hotfixes"
	// TableScopes holds directory scopes (CONTEXT_BUDGET 6.2, D12): one
	// row per repo folder that has scoped knowledge; the row id (scp_) is
	// permanent and the path is only its label, so a folder rename is a
	// label change (lore scope move) and no knowledge row is rewritten.
	TableScopes = "scopes"
)

// Scope fields (6.2.1). FieldScope is on rules, hotfixes, decisions and
// memories; FieldOverrides on rules and hotfixes (6.2.2: a narrower row
// replaces a wider one only through an explicit --overrides).
const (
	FieldScope     = "scope_id"
	FieldOverrides = "overrides_id"
	// FieldScopePath is the folder, relative to the repo root with forward
	// slashes and no leading or trailing slash.
	FieldScopePath = "path"
)

// Cache-only join tables filled from the knowledge tables' tag_ids (O11).
const (
	JoinRuleTags     = "rule_tags"
	JoinDecisionTags = "decision_tags"
	JoinHotfixTags   = "hotfix_tags"
)

// Rule activations (dbent/schema/enums.go RuleActivation): when a rule is
// rendered.
const (
	RuleActivationAlways   = "always"
	RuleActivationGlob     = "glob"
	RuleActivationSemantic = "semantic"
	RuleActivationManual   = "manual"
)

// RuleActivations lists the activations in v0.1.17 order.
func RuleActivations() []string {
	return []string{RuleActivationAlways, RuleActivationGlob, RuleActivationSemantic, RuleActivationManual}
}

// Rule severities (RuleSeverity): must blocks, should warns, may suggests.
const (
	RuleSeverityMust   = "must"
	RuleSeverityShould = "should"
	RuleSeverityMay    = "may"
)

// RuleSeverities lists the rule severities in v0.1.17 order.
func RuleSeverities() []string {
	return []string{RuleSeverityMust, RuleSeverityShould, RuleSeverityMay}
}

// Decision statuses (DecisionStatus).
const (
	DecisionStatusProposed   = "proposed"
	DecisionStatusAccepted   = "accepted"
	DecisionStatusSuperseded = "superseded"
	DecisionStatusDeprecated = "deprecated"
)

// DecisionStatuses lists the decision statuses in v0.1.17 order.
func DecisionStatuses() []string {
	return []string{DecisionStatusProposed, DecisionStatusAccepted, DecisionStatusSuperseded, DecisionStatusDeprecated}
}

// Hotfix severities (HotfixSeverity).
const (
	HotfixSeverityLow      = "low"
	HotfixSeverityMedium   = "medium"
	HotfixSeverityHigh     = "high"
	HotfixSeverityCritical = "critical"
)

// HotfixSeverities lists the hotfix severities in v0.1.17 order.
func HotfixSeverities() []string {
	return []string{HotfixSeverityLow, HotfixSeverityMedium, HotfixSeverityHigh, HotfixSeverityCritical}
}

// lifecycle is v0.1.17's LifecycleMixin minus the machine-local
// last_accessed_at.
func lifecycle() []Item {
	return []Item{
		Number("trust_score"),
		Number("confidence"),
		DateTime("last_validated_at"),
		DateTime("archived_at"),
		Text("source_kind", Required()),
		Text("source_ref"),
	}
}

// FieldSummary is the optional one-line short form of a rule or hotfix
// (CONTEXT_BUDGET 6.4, CQ4): render prints it as the entry's line form when
// the full body does not fit the budget; empty means "use the first
// sentence" (rules) or the title (hotfixes). Plain text, up to 200
// characters (logic/contextfit.MaxSummary, checked by the noun's add/edit).
const FieldSummary = "summary"

func summaryField() Item {
	return Text(FieldSummary, Describe("one-line short form used when the full text does not fit LORE.md (up to 200 characters)"))
}

// knowledgeDefs returns the built-in rule, decision and hotfix tables.
// There is no separate "warning" noun: v0.1.17's hotfix is the loud
// recurring warning.
func knowledgeDefs() []TableDef {
	rule := []Item{
		Synced(), FTS("body", "severity", "activation", "source_kind"), Description("hard constraints: must / should / may"),
		// project_id is optional: v0.1.17's NULL project is a global rule.
		Ref("project_id", TableProjects),
		Ref("repo_id", TableRepos),
		Text("body", Required(), WithWidget(WidgetMarkdown)),
		Text("activation", Required(), OneOf(RuleActivations()...)),
		Text("globs"),
		Text("applies_to_description"),
		Text("severity", Required(), OneOf(RuleSeverities()...)),
		summaryField(),
		Ref("superseded_by_id", TableRules),
		Ref(FieldScope, TableScopes),
		Ref(FieldOverrides, TableRules),
		Ref("created_by_actor_id", TableActors),
		IndexOn("project_id", "repo_id"),
		IndexOn("project_id", "activation"),
		IndexOn("archived_at"),
		BelongsToRel("project", "project_id", TableProjects, Restrict),
		BelongsToRel("repo", "repo_id", TableRepos, SetNull),
		BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
		SelfRel("superseded_by", "superseded_by_id", SetNull),
		BelongsToRel("scope", FieldScope, TableScopes, SetNull),
		SelfRel("overrides", FieldOverrides, SetNull),
		tagIDs(),
		ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinRuleTags, Cascade),
	}
	decision := []Item{
		Synced(), FTS("title", "body", "status", "source_kind"), Description("architectural decision records"),
		Ref("project_id", TableProjects, Required()),
		Ref("repo_id", TableRepos),
		Text("title", Required()),
		Text("body", Required(), WithWidget(WidgetMarkdown)),
		Text("status", Required(), OneOf(DecisionStatuses()...)),
		Ref("superseded_by_id", TableDecisions),
		Ref(FieldScope, TableScopes),
		Ref("created_by_actor_id", TableActors),
		IndexOn("project_id", "repo_id"),
		IndexOn("status"),
		IndexOn("archived_at"),
		BelongsToRel("project", "project_id", TableProjects, Restrict),
		BelongsToRel("repo", "repo_id", TableRepos, SetNull),
		BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
		SelfRel("superseded_by", "superseded_by_id", SetNull),
		BelongsToRel("scope", FieldScope, TableScopes, SetNull),
		tagIDs(),
		ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinDecisionTags, Cascade),
	}
	hotfix := []Item{
		Synced(), FTS("title", "body", "severity"), Description("loud recurring warnings, pinned in render"),
		Ref("project_id", TableProjects, Required()),
		Ref("repo_id", TableRepos),
		Text("title", Required()),
		Text("body", Required(), WithWidget(WidgetMarkdown)),
		Text("severity", Required(), OneOf(HotfixSeverities()...)),
		summaryField(),
		Ref("superseded_by_id", TableHotfixes),
		Ref(FieldScope, TableScopes),
		Ref(FieldOverrides, TableHotfixes),
		Ref("created_by_actor_id", TableActors),
		IndexOn("project_id", "repo_id"),
		IndexOn("severity"),
		IndexOn("archived_at"),
		BelongsToRel("project", "project_id", TableProjects, Restrict),
		BelongsToRel("repo", "repo_id", TableRepos, SetNull),
		BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
		SelfRel("superseded_by", "superseded_by_id", SetNull),
		BelongsToRel("scope", FieldScope, TableScopes, SetNull),
		SelfRel("overrides", FieldOverrides, SetNull),
		tagIDs(),
		ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinHotfixTags, Cascade),
	}
	scope := []Item{
		Synced(), Description("directory scopes: a permanent id per repo folder with scoped rules and hotfixes"),
		Ref("repo_id", TableRepos, Required()),
		Text(FieldScopePath, Required()),
		// Unique: the id is natural (repo, path) so two clones that scope the
		// same folder make the same row; after `lore scope move` the id keeps
		// its old hash, which is fine: ids are never re-derived from a label.
		UniqueOn("repo_id", FieldScopePath),
		BelongsToRel("repo", "repo_id", TableRepos, Restrict),
	}
	return []TableDef{
		Table(TableScopes, ids.PrefixScope, scope...),
		Table(TableRules, ids.PrefixRule, append(rule, lifecycle()...)...),
		Table(TableDecisions, ids.PrefixDecision, append(decision, lifecycle()...)...),
		Table(TableHotfixes, ids.PrefixHotfix, append(hotfix, lifecycle()...)...),
	}
}
