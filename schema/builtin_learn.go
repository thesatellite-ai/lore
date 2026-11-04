package schema

import "github.com/thesatellite-ai/lore/model/ids"

// Learn-group tables (phase 5 group 7) that are synced team data in
// v0.1.17 (lsync syncedTables): reminders, knowledge refs and rule
// verifier refs. Column sets are v0.1.17's dbent/schema/reminder.go,
// knowledge_ref.go and rule_verifier_ref.go, so a v0.1.17 row file reads
// unchanged; id, created_at and updated_at are envelope fields.
//
// The group's machine-local tables (drafts, learn_candidates, learn_runs,
// knowledge_revisions, memory_code_refs, code_files, code_symbols) never
// travel (v0.1.17 lsync localTables) and stay on lore.db: they have no
// definition here on purpose.
const (
	TableReminders        = "reminders"
	TableKnowledgeRefs    = "knowledge_refs"
	TableRuleVerifierRefs = "rule_verifier_refs"
)

// Reminder fields shared by the reminder operations.
const (
	FieldReminderTargetTable = "target_table"
	FieldReminderTargetID    = "target_id"
	FieldReminderDueAt       = "due_at"
	FieldReminderMessage     = "message"
	FieldReminderRecurrence  = "recurrence"
	FieldReminderDoneAt      = "done_at"
)

// Reminder recurrences (dbent/schema/enums.go ReminderRecurrence), in
// v0.1.17 order.
const (
	Recurrence7d  = "7d"
	Recurrence30d = "30d"
	Recurrence1m  = "1m"
	Recurrence3m  = "3m"
	Recurrence6m  = "6m"
	Recurrence1y  = "1y"
)

// ReminderRecurrences lists the recurrences in v0.1.17 order (flag help and
// the "bad --recurrence" message print them in this order).
func ReminderRecurrences() []string {
	return []string{Recurrence7d, Recurrence30d, Recurrence1m, Recurrence3m, Recurrence6m, Recurrence1y}
}

// Knowledge-ref confidences (KnowledgeRefConfidence): extracted by a person
// or a regex, inferred by a model, or ambiguous (several targets matched).
const (
	ConfidenceExtracted = "extracted"
	ConfidenceInferred  = "inferred"
	ConfidenceAmbiguous = "ambiguous"
)

// KnowledgeRefConfidences lists the confidences in v0.1.17 order.
func KnowledgeRefConfidences() []string {
	return []string{ConfidenceExtracted, ConfidenceInferred, ConfidenceAmbiguous}
}

// Rule verifier kinds (RuleVerifierKind).
const (
	VerifierRegex  = "regex"
	VerifierShell  = "shell"
	VerifierGoFn   = "go-fn"
	VerifierCustom = "custom"
)

// RuleVerifierKinds lists the verifier kinds in v0.1.17 order.
func RuleVerifierKinds() []string {
	return []string{VerifierRegex, VerifierShell, VerifierGoFn, VerifierCustom}
}

// learnDefs returns the synced learn-group tables.
//
// Why the knowledge-ref relations allow dangling: v0.1.17 never checked
// that a ref's target existed, a target may live in
// another store (RES-OQ2), and nouns not yet on the core have no row the
// engine can see (same reasoning as linkDefs).
func learnDefs() []TableDef {
	return []TableDef{
		Table(TableReminders, ids.PrefixReminder, Synced(), FTS(FieldReminderMessage), Description("a time-based reminder"),
			Ref("project_id", TableProjects, Required()),
			// target_table / target_id are free text, as in v0.1.17: it
			// accepted any --on-table/--on-id (no id check), so a v0.1.17 row
			// may hold a value that is not an id and must still read and update.
			Text(FieldReminderTargetTable),
			Text(FieldReminderTargetID),
			DateTime(FieldReminderDueAt, Required()),
			Text(FieldReminderMessage, Required(), WithWidget(WidgetMarkdown)),
			Text(FieldReminderRecurrence, OneOf(ReminderRecurrences()...)),
			DateTime(FieldReminderDoneAt),
			Ref("created_by_actor_id", TableActors),
			IndexOn("project_id", FieldReminderDueAt),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
			BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
		),
		Table(TableKnowledgeRefs, ids.PrefixKnowledgeRef, Synced(), Description("a typed link between two rows"),
			Text("src_table", Required()),
			Ref("src_id", "", Required()),
			Text("dst_table", Required()),
			Ref("dst_id", "", Required()),
			// dst_project_id: project of the destination when it is not the
			// source's own (a cross-project reference, LORE_TEAM_SPEC F15).
			Ref("dst_project_id", TableProjects),
			// relation is free text in v0.1.17 (mentions, derived_from,
			// caused_by, conflicts_with, supersedes, affects, …: RQ7).
			Text("relation", Required()),
			Text("confidence", Required(), OneOf(KnowledgeRefConfidences()...)),
			IndexOn("src_table", "src_id"),
			IndexOn("dst_table", "dst_id"),
			relation(Relation{
				Name: "src", Kind: Polymorphic, TypeField: "src_table", Field: "src_id",
				OnDelete: TombstoneOnly, AllowDangling: true,
			}),
			relation(Relation{
				Name: "dst", Kind: Polymorphic, TypeField: "dst_table", Field: "dst_id",
				OnDelete: TombstoneOnly, AllowDangling: true,
			}),
			// AllowDangling: the destination's project usually lives in
			// another store this checkout may not attach (v0.1.17 accepted
			// any cross-project ref; F15 shows it "not available here").
			relation(Relation{
				Name: "dst_project", Kind: BelongsTo, Field: "dst_project_id", Target: TableProjects,
				OnDelete: TombstoneOnly, AllowDangling: true,
			}),
		),
		Table(TableRuleVerifierRefs, ids.PrefixRuleVerifierRef, Synced(), Description("a verifier wired to a rule"),
			Ref("rule_id", TableRules, Required()),
			Text("verifier_kind", Required(), OneOf(RuleVerifierKinds()...)),
			Text("verifier_ref", Required()),
			Text("notes"),
			// v0.1.17's unique key, so a ref's natural id equals the v0.1.17 one.
			UniqueOn("rule_id", "verifier_kind", "verifier_ref"),
			BelongsToRel("rule", "rule_id", TableRules, Cascade),
		),
	}
}
