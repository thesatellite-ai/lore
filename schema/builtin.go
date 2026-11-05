package schema

import (
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Built-in table names. Other packages refer to these constants, never to
// string literals.
const (
	TableActors       = "actors"
	TableProjects     = "projects"
	TableRepos        = "repos"
	TableMemories     = "memories"
	TablePolicies     = "policies"
	TableMetaKeys     = "meta_keys"
	TableMetaValues   = "meta_values"
	TableKinds        = "kinds"
	TableKindFields   = "kind_fields"
	TableVars         = "vars"
	TableSecretRules  = "secret_rules"
	TableSecretAllows = "secret_allows"
	TableResources    = "resources"
	TableTags         = "tags"
)

// meta_keys.added_by values (MQ7).
const (
	MetaAddedByPerson = "person"
	MetaAddedByAgent  = "agent"
)

// FieldTagIDs is the list-of-ref field that carries a row's tags (decision
// O11: a many-to-many lives in the owning row, replacing v0.1.17's
// entity_tags bridge rows). Every table v0.1.17 let carry tags that the
// core declares has it, with the relation RelTags.
const (
	FieldTagIDs = "tag_ids"
	// FieldIdempotencyKey is the caller's retry key of a create that takes
	// one (`lore memory add --key`, CLEANUP Qs): the same key returns the
	// existing row and writes nothing.
	FieldIdempotencyKey = "idempotency_key"
	// FieldResourceHistory is a resource row's capped field-change history
	// (RESOURCES 3.22; the git log of the row file is the full record).
	FieldResourceHistory = "history"
	RelTags              = "tags"
)

// Cache-only join tables filled from tag_ids (one per owning table, since
// a join table belongs to exactly one relation).
const (
	JoinActorTags   = "actor_tags"
	JoinProjectTags = "project_tags"
	JoinRepoTags    = "repo_tags"
	JoinMemoryTags  = "memory_tags"
)

// Memory kinds (the 5-tier taxonomy of dbent/schema/memory.go).
const (
	MemoryKindCore       = "core"
	MemoryKindRetrieved  = "retrieved"
	MemoryKindEpisodic   = "episodic"
	MemoryKindProcedural = "procedural"
	MemoryKindArchival   = "archival"
)

// MemoryKinds lists the memory kinds in v0.1.17 order.
func MemoryKinds() []string {
	return []string{MemoryKindCore, MemoryKindRetrieved, MemoryKindEpisodic, MemoryKindProcedural, MemoryKindArchival}
}

// Actor kinds (dbent/schema/enums.go ActorKind).
const (
	ActorKindHuman  = "human"
	ActorKindAgent  = "agent"
	ActorKindHook   = "hook"
	ActorKindPlugin = "plugin"
	ActorKindCron   = "cron"
	ActorKindSystem = "system"
)

// ActorKinds lists the actor kinds in v0.1.17 order.
func ActorKinds() []string {
	return []string{ActorKindHuman, ActorKindAgent, ActorKindHook, ActorKindPlugin, ActorKindCron, ActorKindSystem}
}

// Secret rule states (spec 4.3).
const (
	SecretRuleOn      = "on"
	SecretRuleOff     = "off"
	SecretRuleChanged = "changed"
)

// SecretRuleStates lists the states.
func SecretRuleStates() []string { return []string{SecretRuleOn, SecretRuleOff, SecretRuleChanged} }

// Shared field names used across several tables and by KindDefFromRows.
const (
	fieldName = "name"
	fieldKind = "kind"
)

// Builtin returns the compiled-in table definitions. Only the tables the
// 0.1.18 core needs are declared so far; the other v0.1.17 tables join as
// their nouns migrate (strangler, one noun at a time).
//
// Synced v0.1.17 columns are copied from dbent/schema; machine-local
// columns (last_accessed_at, last_seen_at, last_active_at, embedding_*)
// are left out on purpose: they never travel in a row (lsync volatile
// columns). id, created_at and updated_at are envelope fields.
func Builtin() []TableDef {
	defs := coreDefs()
	// Each noun group owns one file (builtin_<group>.go) so the parallel
	// migrations never edit the same file; the order here is stable so the
	// registry and the written docs stay deterministic.
	for _, group := range [][]TableDef{knowledgeDefs(), contentDefs(), workDefs(), linkDefs(), settingsDefs(), content2Defs(), learnDefs(), telemetryDefs(), infraDefs()} {
		defs = append(defs, group...)
	}
	return defs
}

// coreDefs are the tables the core itself needs: actors, projects, repos,
// memories, tags, every settings table and resources.
func coreDefs() []TableDef {
	return []TableDef{
		Table(TableActors, ids.PrefixActor, Synced(), GlobalKey(), Description("people and agents that write rows"),
			Text(fieldKind, Required(), OneOf(ActorKinds()...)),
			Text("display_name", Required()),
			// stable_key is absent from the F18 projection (repo and client
			// stores get id, display_name, kind only: wire fullActorRow).
			Text("stable_key", Internal()),
			Ref("owner_actor_id", TableActors, Internal()),
			Text("label"),
			Text("handle"),
			Bool("active"),
			DateTime("left_at"),
			UniqueOn("stable_key"),
			IndexOn(fieldKind),
			IndexOn("handle"),
			SelfRel("owner", "owner_actor_id", TombstoneOnly),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinActorTags, Cascade),
		),
		Table(TableProjects, ids.PrefixProject, Synced(), Description("a project, or a folder inside a store"),
			Text(fieldName, Required()),
			Text("origin_url"),
			DateTime("archived_at"),
			IndexOn(fieldName),
			HasManyRel("repos", TableRepos, "project_id"),
			HasManyRel("memories", TableMemories, "project_id"),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinProjectTags, Cascade),
		),
		Table(TableRepos, ids.PrefixRepo, Synced(), Description("a code repository of a project"),
			Ref("project_id", TableProjects, Required()),
			Text("mount_name", Required()),
			Text("display_name"),
			Text("origin_url"),
			DateTime("archived_at"),
			// PROV1/PROV2: urls is the repo's full address history, a JSON
			// list of {url, kind (primary|mirror|unconfirmed), how
			// (init|set-url|seen|migrate), from, to, by} entries keyed by
			// the normalised url (PROVENANCE_DESIGN 10.2); an entry is closed
			// with `to`, never deleted. forked_from / split_from name the
			// upstream repo of a repo made by `lore repo fork [--split]`.
			JSON("urls", MergeBy(MergeURLSet)),
			Ref("forked_from", TableRepos),
			Ref("split_from", TableRepos),
			UniqueOn("project_id", "mount_name"),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
			// AllowDangling: the upstream's repos row lives in the upstream
			// repo, which a fork's clone usually never had (PROV1: the id is
			// still the truth about where it came from).
			relation(Relation{Name: "forked_from", Kind: Self, Field: "forked_from", Target: TableRepos, OnDelete: TombstoneOnly, AllowDangling: true}),
			relation(Relation{Name: "split_from", Kind: Self, Field: "split_from", Target: TableRepos, OnDelete: TombstoneOnly, AllowDangling: true}),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinRepoTags, Cascade),
		),
		Table(TableMemories, ids.PrefixMemory, Synced(), FTS("body", "source_ref", fieldKind, "source_kind"), Description("free-form learned notes"),
			Ref("project_id", TableProjects, Required()),
			Ref("repo_id", TableRepos),
			// CQ12: a folder scope ranks the memory in lore context only;
			// memories are never written into a generated file.
			Ref(FieldScope, TableScopes),
			Text(fieldKind, Required(), OneOf(MemoryKinds()...)),
			Text("body", Required(), WithWidget(WidgetMarkdown)),
			DateTime("valid_at"),
			DateTime("valid_until"),
			DateTime("tx_at"),
			Ref("superseded_by_id", TableMemories),
			Ref("created_by_actor_id", TableActors),
			Ref("validated_by_actor_id", TableActors),
			Number("trust_score"),
			Number("confidence"),
			DateTime("last_validated_at"),
			DateTime("archived_at"),
			Text("source_kind", Required()),
			Text("source_ref"),
			// CLEANUP Qs: `memory add --key` stores its idempotency key here;
			// the row's id is natural over it, so a retry finds the row.
			Text(FieldIdempotencyKey),
			IndexOn("project_id", "repo_id"),
			IndexOn("project_id", fieldKind),
			IndexOn("archived_at"),
			IndexOn("superseded_by_id"),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
			BelongsToRel("repo", "repo_id", TableRepos, SetNull),
			BelongsToRel("scope", FieldScope, TableScopes, SetNull),
			BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
			BelongsToRel("validated_by", "validated_by_actor_id", TableActors, TombstoneOnly),
			SelfRel("superseded_by", "superseded_by_id", SetNull),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinMemoryTags, Cascade),
		),
		Table(TablePolicies, ids.PrefixPolicy, Synced(), Description("one policy entry (spec 4.3)"),
			Text("key", Required()),
			JSON("value", Required()),
			Text("scope", Required()),
			UniqueOn("key", "scope"),
		),
		Table(TableMetaKeys, ids.PrefixMetaKey, Synced(), Description("a metadata key definition"),
			Text(fieldName, Required()),
			Text("scope", Required()),
			Text("type", Required(), OneOf(kindWords()...)),
			Bool("strict"),
			Bool("internal"),
			Bool("sensitive"),
			Text("description"),
			// MQ7: who registered the key and whether a person approved it
			// (absent on older rows = reviewed).
			Text("added_by", OneOf(MetaAddedByPerson, MetaAddedByAgent)),
			Bool("reviewed"),
			// rebuilt: recreated by meta rebuild from data rows (METADATA 3.18);
			// a real key row of the same id replaces it whole.
			Bool("rebuilt"),
			// METADATA 3.19 everyday features and the 3.12 fold (OV21):
			// required_on/default_on are JSON lists of table names; min and
			// max are text so one field covers numbers, dates and datetimes.
			Text("label"),
			JSON("required_on"),
			Text("pattern"),
			Text("min"),
			Text("max"),
			Text("default"),
			JSON("default_on"),
			Bool("deprecated"),
			Text("replaced_by"),
			Text("merged_into"),
			// changed_by: one entry per loosening (internal, sensitive or
			// strict turned off), written only by meta edit after its
			// confirmation; the pre-commit check relies on it (RX7, OV58).
			JSON("changed_by", MergeBy(MergeListByID),
				Describe("loosenings [{id (the_…), field, to, by, at}] (METADATA 3.11, RX7)")),
			UniqueOn(fieldName, "scope"),
		),
		Table(TableMetaValues, ids.PrefixMetaValue, Synced(), Description("an allowed value of a metadata key"),
			Ref("key_id", TableMetaKeys, Required()),
			Text(fieldName, Required()),
			Text("label"),
			UniqueOn("key_id", fieldName),
			BelongsToRel("key", "key_id", TableMetaKeys, Cascade),
		),
		Table(TableKinds, ids.PrefixKind, Synced(), Description("a resource kind (a user-defined table shape)"),
			Text(fieldName, Required()),
			Text("description"),
			Text("name_format"),
			Text("render"),
			// secret_manager is the manager a bare secret_ref path means for this kind (RES 3.1 --secret-manager); empty = the policy default.
			Text("secret_manager"),
			// added_by/reviewed: a kind an agent added waits for a person's
			// review (`resource kinds --unreviewed`), as meta keys do (MQ7);
			// absent on older rows = reviewed.
			Text("added_by", OneOf(MetaAddedByPerson, MetaAddedByAgent)),
			Bool("reviewed"),
			JSON("origin"),
			// copies: the client repos' own kinds made from this one, as
			// "<client place>=<knd id>" (MQ15, R2-Q1: the link kept both ways;
			// written by a move into a client repo, engine/move_resource.go).
			List("copies", value.KindText, MergeBy(MergeSetUnion)),
			UniqueOn(fieldName),
			HasManyRel("fields", TableKindFields, "kind_id"),
		),
		Table(TableKindFields, ids.PrefixKindField, Synced(), Description("one field of a resource kind"),
			Ref("kind_id", TableKinds, Required()),
			Text(fieldName, Required()),
			// values is the closed set a field accepts (`field add --values`).
			List("values", value.KindText),
			// value_ids gives each allowed value a permanent val_ id and an
			// optional label: [{id, name, label}] (OQ-AV, FA-RES-OQAV); rows
			// still hold the value name in 0.1.18.
			JSON("value_ids"),
			Text("description"),
			Text("type", Required(), OneOf(kindWords()...)),
			Bool("required"),
			Bool("sensitive"),
			Bool("internal"),
			Ref("ref_kind", TableKinds),
			// extends names the inherited field a lower level tightens (RES 3.6):
			// the row then carries only the tightened options.
			Ref("extends", TableKindFields),
			Text("label"),
			Text("widget", OneOf(widgetWords()...)),
			Bool("in_list"),
			// RES 3.1 field options (OV32): default/min/max are text so one
			// column covers every type (like meta_keys); remind_before is a
			// duration (date/datetime only); replaced_by names the replacing
			// field; type_history is [{type, at}] kept by field retype (3.14).
			Text("default"),
			Text("pattern"),
			Text("min"),
			Text("max"),
			Bool("strict"),
			Bool("deprecated"),
			Text("replaced_by"),
			Text("remind_before"),
			JSON("type_history"),
			// changed_by/changed_at: who loosened the field last and when
			// (RES 3.14: a loosening asks, then records both; OV32).
			Ref("changed_by", TableActors),
			DateTime("changed_at"),
			UniqueOn("kind_id", fieldName),
			BelongsToRel("kind", "kind_id", TableKinds, Cascade),
			BelongsToRel("ref_kind_def", "ref_kind", TableKinds, SetNull),
		),
		Table(TableVars, ids.PrefixVar, Synced(), Description("a variable referenced as ${var:name}"),
			Text(fieldName, Required()),
			Text("description"),
			Text("type", Required(), OneOf(kindWords()...)),
			JSON("value", Required()),
			Bool("shareable"),
			List("aliases", value.KindText),
			UniqueOn(fieldName),
		),
		Table(TableSecretRules, ids.PrefixSecretRule, Synced(), Description("a secret scanner rule"),
			Text("rule_id", Required()),
			Text("description"),
			// reason is why a rule was turned off (`secrets rule off --reason`),
			// listed by lore doctor (D-ITEMS).
			Text("reason"),
			Text("regex"),
			List("keywords", value.KindText),
			Number("entropy"),
			Text("state", Required(), OneOf(SecretRuleStates()...)),
			UniqueOn("rule_id"),
		),
		Table(TableSecretAllows, ids.PrefixSecretAllow, Synced(), Description("a finding marked as not a secret"),
			Ref("row_id", "", Required()),
			Text("field", Required()),
			Text("rule_id", Required()),
			Number("match_hash", Required()),
			Text("reason"),
			IndexOn("row_id"),
		),
		Table(TableTags, ids.PrefixTag, Synced(), Description("a tag; per project, same name allowed across projects"),
			Ref("project_id", TableProjects, Required()),
			Text(fieldName, Required()),
			Text("color"),
			// globs is a topic's glob set (CONTEXT_BUDGET 6.10): lore context
			// --for-file pulls the rows of a topic whose globs cover the path.
			Text("globs"),
			UniqueOn("project_id", fieldName),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
		),
		Table(TableResources, ids.PrefixResource, Synced(), Description("a resource; its fields follow its kind"),
			Ref(fieldKind, TableKinds, Required()),
			Text(fieldName, Required()),
			DateTime("archived_at"),
			// last_checked is when `lore resource check` last verified it
			// (resource stale reads it).
			DateTime("last_checked"),
			// checked_by is who stamped last_checked: manual | edit | verifier:<plugin> (RQ8); the actor is the audit event's.
			Text("checked_by"),
			// check_note is the note of the last `lore resource check --note` (OV32).
			Text("check_note"),
			// replaced_by is the resource that took this one's place: `resource
			// repoint` archives the old row with it (RESOURCES 3.20); show prints it.
			Ref("replaced_by", TableResources),
			// fields: {fld_…: {name, v, strict?, sensitive?}}, merged entry by
			// entry (RESOURCES 3.13, R4, RQ5).
			JSON("fields", MergeBy(MergeEntriesByID)),
			// history: capped field-change list, union by entry id (RES 3.22, OV49).
			JSON(FieldResourceHistory, MergeBy(MergeListByID),
				Describe("field changes [{id (the_…), at, by, batch, field_id, field, from, to}], hidden values masked (RESOURCES 3.22)")),
			IndexOn(fieldKind, fieldName),
			BelongsToRel("kind_def", fieldKind, TableKinds, Restrict),
		),
	}
}

// tagIDs declares the tag_ids field of a table that carries tags.
func tagIDs() Item {
	return RefList(FieldTagIDs, TableTags, Describe("ids of the tags on this row"), Label("Tags"))
}

// kindWords lists the value kind words a settings row may name as a type
// (every kind but null).
func kindWords() []string {
	var out []string
	for _, k := range value.Kinds() {
		if k != value.KindNull {
			out = append(out, string(k))
		}
	}
	return out
}

// widgetWords lists the widget words a kind field row may name.
func widgetWords() []string {
	out := make([]string, 0, len(Widgets()))
	for _, w := range Widgets() {
		out = append(out, string(w))
	}
	return out
}
