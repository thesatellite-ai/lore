package policy

import (
	"cmp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/value"
)

// Key is a policy key ("secrets.on_detect").
type Key string

// The closed set of policy keys this version evaluates (DECISIONS_LOG 6).
// Keys of the log whose values have no "stricter" (estimate units, budgets)
// join with their noun and a declared order; an unknown key is refused.
const (
	KeyOnDetect        Key = "secrets.on_detect"
	KeyAgentReveal     Key = "secrets.agent_reveal"
	KeyUseRepoGitleaks Key = "secrets.use_repo_gitleaks"
	// KeyPasswordPairProseFilter: the password_pair prose post-filter (D10,
	// SECRETS 3.4); off flags an all-letters value followed by prose too.
	KeyPasswordPairProseFilter Key = "secrets.password_pair_prose_filter"
	// KeySecretsManagerTimeout: seconds each secret manager call may run
	// (SECRETS_AND_REFERENCES 6: default 5, max 30); a limit, the smaller
	// is stricter (a hung vault CLI is killed sooner).
	KeySecretsManagerTimeout Key = "secrets.manager_timeout"
	KeyMetaMaxBytes          Key = "meta.max_bytes"
	// KeyMetaWarnBytes: row metadata larger than this prints the "looks like content" warning (METADATA 3.13: 4096); a limit, the smaller is stricter.
	KeyMetaWarnBytes Key = "meta.warn_bytes"
	// KeyMetaMaxDepth: most dot segments in a metadata key name (METADATA 3.13: 4, ceiling 6 = the import cap so a written key never quarantines on import); a limit, the smaller is stricter.
	KeyMetaMaxDepth Key = "meta.max_depth"
	// KeyMetaResourceHintValues: past this many distinct values `meta lint` suggests a resource kind (MQ8, D39: 30); a limit, the smaller is stricter (the hint comes sooner).
	KeyMetaResourceHintValues Key = "meta.resource_hint_values"
	KeyResourcesMaxBytes      Key = "resources.max_bytes"
	// KeyResourcesWarnBytes: a resource larger than this prints a warning
	// (RQ4: 4 KB); a limit, the smaller is stricter.
	KeyResourcesWarnBytes Key = "resources.warn_bytes"
	// KeyResourcesAllowedIn: where resources may live, items from
	// AllowedInPlaces (RQ2, OQ24); levels intersect, a lower one only narrows.
	KeyResourcesAllowedIn Key = "resources.allowed_in"
	// KeyResourcesStaleAfter: hours since the last successful fetch of a
	// store after which a resource write checked against that store's kind
	// rows adds "checked against store kinds as of <commit>" (RES 3.6: 24 h;
	// P2-RES-6: whole hours, a limit, the smaller is stricter).
	KeyResourcesStaleAfter Key = "resources.stale_after"
	KeyTrashRetentionDays  Key = "trash.retention_days"
	KeyAgentsPersonOnly    Key = "agents.person_only"
	KeyTasksMaxDepth       Key = "tasks.max_depth"
	// KeyTasksParentAutoclose is what happens when a parent's last open
	// sub-task closes (TSQ2): off | suggest (default) | on.
	KeyTasksParentAutoclose Key = "tasks.parent_autoclose"
	// KeyTasksEstimateUnit is the unit of tasks.estimate (TSQ4):
	// points (default) | hours | days.
	KeyTasksEstimateUnit Key = "tasks.estimate_unit"
	// KeyTasksAutoWatch: creators and assignees watch a task by default
	// (TASK 14.4); false turns that off.
	KeyTasksAutoWatch Key = "tasks.auto_watch"
	// KeyTasksWeekEnd is the day "eow" / "end of week" means in date phrases (TASK 12.1).
	KeyTasksWeekEnd Key = "tasks.week_end"
	// KeyTasksHistoryMax: entries kept on a task's history before the oldest fold into one summary (TSQ10).
	KeyTasksHistoryMax Key = "tasks.history_max"
	// KeyTasksStaleDays: default --days of `lore task stale`; unset = 60 (TASK 18.5).
	KeyTasksStaleDays Key = "tasks.stale_days"
	// KeyTasksSLA*: hours from creation to done per priority; unset = no SLA (TASK 18.4). OV22-SLA: hours, limits are integers.
	KeyTasksSLALow    Key = "tasks.sla.low"
	KeyTasksSLAMedium Key = "tasks.sla.medium"
	KeyTasksSLAHigh   Key = "tasks.sla.high"
	KeyTasksSLAUrgent Key = "tasks.sla.urgent"
	// KeyTasksUrgencyPrefix starts the urgency weight keys (TASK 13.2,
	// OV15B-URG): tasks.urgency.<term> and tasks.urgency.priority.<level>,
	// ShapeWeight values (UrgencyKeys lists them).
	KeyTasksUrgencyPrefix Key = "tasks.urgency."
	// Time tracking keys (TIME_TRACKING_DESIGN 14), in minutes: a limit,
	// the smaller is stricter (flags more, merges less, allows less).
	// OV7-1: integers in minutes, the policy layer has no duration shape.
	KeyTimeLongSpan    Key = "time.long_span"
	KeyTimeCoalesceGap Key = "time.coalesce_gap"
	KeyTimeMaxEntry    Key = "time.max_entry"
	// KeyTimeClientShare: what a task moved into a client repo carries of its time (TQ3): none (default, strictest) | totals.
	KeyTimeClientShare Key = "time.client_share"
	// KeyClientRoutes is v0.1.17's lore-org.yaml `defaults.client_repos`
	// (CFG8: team rules are policies rows of the org folder): where a row
	// of each kind goes in a client repo when the write names no --to.
	// Each item is "<kind>=<target>", target "repo", "<org>" or
	// "<org>/<folder>" (ClientRoutes reads it).
	KeyClientRoutes Key = "routing.client_repos"
	// KeyContextTargets is the tool profiles (logic/toolprofile names) a
	// repo's context is wired into: `lore init` writes the chosen ones and
	// `lore policy set context.targets codex,cursor` changes them (Q-R2-3).
	KeyContextTargets Key = "context.targets"
	// Context budget keys (CONTEXT_BUDGET 6.1): limits in bytes, the smaller
	// is stricter, so a lower level may only shrink a budget. One key per
	// built-in tool profile (CQ1/CQ6); context.budget.default covers a user
	// profile that states none.
	KeyContextBudgetClaudeCode Key = "context.budget.claude_code"
	KeyContextBudgetCodex      Key = "context.budget.codex"
	KeyContextBudgetCursor     Key = "context.budget.cursor"
	KeyContextBudgetGeminiCLI  Key = "context.budget.gemini_cli"
	KeyContextBudgetCopilot    Key = "context.budget.copilot"
	KeyContextBudgetGeneric    Key = "context.budget.generic"
	KeyContextBudgetDefault    Key = "context.budget.default"
	// KeyContextEntryMax: an entry longer than this many bytes always takes its line form.
	KeyContextEntryMax Key = "context.entry_max"
	// KeyContextWarnAt: percent of a budget above which doctor and render --check warn (design 0.8, stored as 80: the policy layer has no fraction shape).
	KeyContextWarnAt Key = "context.warn_at"
	// KeyContextHookCap: bytes of `lore context` output from a hook (under the 10,000-char hook cap).
	KeyContextHookCap Key = "context.hook_cap"
	// KeyContextPromptSearch: lore setup --agent-hooks also installs the per-prompt search hook (CQ5: off).
	KeyContextPromptSearch Key = "context.hooks.prompt_search"
	// KeyContextFullFile: render also writes .lore/LORE.full.md (CQ2).
	KeyContextFullFile Key = "context.full_file"
	// KeyContextDirFiles: render writes per-directory LORE.md files (6.2.7, CQ10): on by default in own repos; a client repo is off until set for that client.
	KeyContextDirFiles Key = "context.dir_files"
	// KeyContextBudgetDir: bytes of each per-directory LORE.md (6.1).
	KeyContextBudgetDir Key = "context.budget.dir"
	// KeyContextDirMinEntries: a directory with fewer scoped entries gets no file; its rows go to the parent file (6.1).
	KeyContextDirMinEntries Key = "context.dir_min_entries"
	// KeyContextChainMaxCodex: bytes the root-to-deepest AGENTS.md chain may hold (Codex adds every level into one cap, 6.2.6).
	KeyContextChainMaxCodex Key = "context.chain_max.codex"
	// KeyBatchPreviewRows is how many rows a bulk action (a --where
	// delete, an import) may change before it shows a preview and asks
	// (XQ2: one key for every bulk action, replacing the per-noun
	// meta/tasks/delete/resources.bulk_preview_rows).
	KeyBatchPreviewRows Key = "batch.preview_rows"
	// KeyDeleteRequireReason refuses a delete without --reason (DELETION 475, E_REASON_REQUIRED).
	KeyDeleteRequireReason Key = "delete.require_reason"
)

// ClientRouteRepo is the KeyClientRoutes target that keeps a kind in the
// client repo itself (v0.1.17 stores.DefaultRepo).
const ClientRouteRepo = "repo"

// clientRouteSep splits one KeyClientRoutes item into kind and target.
const clientRouteSep = "="

// Numeric defaults of the keys above.
const (
	defaultMaxBytes             int64 = 16 * 1024 // MQ3, RQ4, CFG10
	defaultWarnBytes            int64 = 4 * 1024  // RQ4
	defaultStaleAfterHours      int64 = 24        // RES 3.6 resources.stale_after
	defaultTasksMaxDepth        int64 = 4         // TSQ1
	ceilingTasksMaxDepth        int64 = 8         // TSQ1: "default 4, up to 8"
	defaultMetaMaxDepth         int64 = 4         // METADATA 3.13
	defaultMetaResourceHint     int64 = 30        // MQ8, D39
	ceilingMetaMaxDepth         int64 = 6         // metafilter.ImportMaxDepth: deeper keys would quarantine on import
	defaultTasksHistoryMax      int64 = 200       // TSQ10
	ceilingTasksHistoryMax      int64 = 1000      // TSQ10 "up to 1000"
	defaultPreviewRows          int64 = 20        // XQ2
	defaultTimeLongSpanMin      int64 = 600       // TQ2: 10h
	defaultTimeCoalesceGapMin   int64 = 2         // TQ4: 2m
	defaultTimeMaxEntryMin      int64 = 1440      // D13: 24h, also the ceiling
	defaultContextBudgetLarge   int64 = 32000     // CQ1: claude_code, gemini_cli
	defaultContextBudget        int64 = 16000     // CQ1: codex, cursor, copilot, generic, default
	defaultContextEntryMax      int64 = 1500
	defaultContextWarnAtPct     int64 = 80
	ceilingContextWarnAtPct     int64 = 100
	defaultContextHookCap       int64 = 9000
	defaultContextBudgetDir     int64 = 8000
	defaultContextDirMinEntries int64 = 3
	defaultContextChainMaxCodex int64 = 32768
	defaultManagerTimeoutSec    int64 = 5  // SECRETS 6
	ceilingManagerTimeoutSec    int64 = 30 // SECRETS 6: "max 30 s"
)

// OnDetect is the value of secrets.on_detect.
type OnDetect string

// OnDetect choices, strictest first: refuse never stores; ask stops for a
// person (E_NEEDS_CONFIRM for agents); redact stores a reference in place
// of the secret without asking, so it is the loosest.
const (
	OnDetectRefuse OnDetect = "refuse"
	OnDetectAsk    OnDetect = "ask"
	OnDetectRedact OnDetect = "redact"
)

// OnDetects lists the choices strictest first.
func OnDetects() []OnDetect { return []OnDetect{OnDetectRefuse, OnDetectAsk, OnDetectRedact} }

// AgentReveal is the value of secrets.agent_reveal, strictest first.
type AgentReveal string

// AgentReveal choices (SQ10).
const (
	AgentRevealAsk   AgentReveal = "ask"
	AgentRevealAllow AgentReveal = "allow"
)

// Shape says how a key's values compare.
type Shape string

// Shapes.
const (
	// ShapeRank: a text value from Choices, listed strictest first.
	ShapeRank Shape = "rank"
	// ShapeFlag: a bool; Strict is the stricter value.
	ShapeFlag Shape = "flag"
	// ShapeLimit: a non-negative integer, the smaller is stricter (a size
	// limit, a retention maximum, DQ7); Ceiling, when set, caps it.
	ShapeLimit Shape = "limit"
	// ShapeSet: a list of text; the union of every level is the result
	// (agents.person_only: a command any level lists stays person-only).
	ShapeSet Shape = "set"
	// ShapeAllow: a list of text from Choices; the levels INTERSECT, so a
	// lower level may only narrow it (RQ2 resources.allowed_in).
	ShapeAllow Shape = "allow"
	// ShapeWeight: a signed number with at most two decimals, |v| <=
	// Ceiling; the NEAREST level that sets it wins (a weight is a
	// preference, not a safety limit, so there is no stricter value;
	// OV15B-URG). Read as integer hundredths (Set.Weight), so every machine
	// adds the same integers.
	ShapeWeight Shape = "weight"
)

// Urgency weight terms (TASK 13.2; the names of logic/urgency.Term, kept
// as text here because policy sits beside urgency, not above it).
const (
	urgencyPriority = "priority"
	// ceilingUrgencyWeight bounds a weight in points (|v| <= 1000), far
	// above every 13.2 default (the largest is next_tag 15.00).
	ceilingUrgencyWeight = 1000
)

// urgencyTerms are the non-priority weight terms (13.2).
var urgencyTerms = []string{"due", "mission_target", "blocking", "in_progress", "age", "sla_risk", "next_tag", "proposed"}

// urgencyLevels are the priority levels with their own weight.
var urgencyLevels = []string{"low", "medium", "high", "urgent"}

// UrgencyKey is the policy key of an urgency term; level is the priority
// level for the priority term, else "".
func UrgencyKey(term, level string) Key {
	if level != "" {
		return KeyTasksUrgencyPrefix + Key(term+"."+level)
	}
	return KeyTasksUrgencyPrefix + Key(term)
}

// UrgencyKeys lists every urgency weight key with its term and level.
func UrgencyKeys() []UrgencyWeightKey {
	out := make([]UrgencyWeightKey, 0, len(urgencyTerms)+len(urgencyLevels))
	for _, l := range urgencyLevels {
		out = append(out, UrgencyWeightKey{Key: UrgencyKey(urgencyPriority, l), Term: urgencyPriority, Level: l})
	}
	for _, t := range urgencyTerms {
		out = append(out, UrgencyWeightKey{Key: UrgencyKey(t, ""), Term: t})
	}
	return out
}

// UrgencyWeightKey names one urgency weight key.
type UrgencyWeightKey struct {
	Key         Key
	Term, Level string
}

// Places of KeyResourcesAllowedIn (RQ2): a store, our own repo, a client repo.
const (
	PlaceStore  = "store"
	PlaceRepo   = "repo"
	PlaceClient = "client"
)

// AllowedInPlaces lists the KeyResourcesAllowedIn items; all are allowed by default (RQ2).
func AllowedInPlaces() []string { return []string{PlaceStore, PlaceRepo, PlaceClient} }

// KeyDef declares one key: its shape, how strictness is ordered, and the
// default when no level sets it.
type KeyDef struct {
	Key     Key
	Shape   Shape
	Choices []string // ShapeRank: strictest first
	Strict  bool     // ShapeFlag: the stricter value
	Ceiling int64    // ShapeLimit: largest allowed value; 0 = none
	// Default applies when no level sets the key; nil means "unset" (no
	// limit: trash.retention_days keeps forever by default).
	Default value.Value
	Doc     string
}

// Keys returns every key definition, sorted by key.
func Keys() []KeyDef {
	defs := []KeyDef{
		{Key: KeyOnDetect, Shape: ShapeRank, Choices: choices(OnDetects()), Default: mustText(string(OnDetectAsk)),
			Doc: "what a write does on a detected secret"},
		{Key: KeyAgentReveal, Shape: ShapeRank, Choices: []string{string(AgentRevealAsk), string(AgentRevealAllow)},
			Default: mustText(string(AgentRevealAsk)), Doc: "whether an agent may reveal a secret without asking (SQ10)"},
		{Key: KeyUseRepoGitleaks, Shape: ShapeFlag, Strict: false, Default: value.BoolOf(true),
			Doc: "read a repo's .gitleaks.toml allowlists (D8); reading fewer allowlists is stricter"},
		{Key: KeyPasswordPairProseFilter, Shape: ShapeFlag, Strict: false, Default: value.BoolOf(true),
			Doc: "skip password_pair matches whose value is a plain word followed by prose (D10, SECRETS 3.4); off is stricter"},
		{Key: KeySecretsManagerTimeout, Shape: ShapeLimit, Ceiling: ceilingManagerTimeoutSec, Default: value.Int(defaultManagerTimeoutSec),
			Doc: "seconds a secret manager CLI may run per call (reveal, verify) before lore kills it (SECRETS 6, max 30)"},
		{Key: KeyMetaMaxBytes, Shape: ShapeLimit, Default: value.Int(defaultMaxBytes), Doc: "largest metadata value in bytes"},
		{Key: KeyMetaWarnBytes, Shape: ShapeLimit, Default: value.Int(defaultWarnBytes), Doc: "row metadata larger than this many bytes prints a warning (METADATA 3.13)"},
		{Key: KeyMetaMaxDepth, Shape: ShapeLimit, Ceiling: ceilingMetaMaxDepth, Default: value.Int(defaultMetaMaxDepth), Doc: "most dot segments in a metadata key name (METADATA 3.13)"},
		{Key: KeyMetaResourceHintValues, Shape: ShapeLimit, Default: value.Int(defaultMetaResourceHint), Doc: "distinct values past which meta lint suggests a resource kind (METADATA 3.8, D39)"},
		{Key: KeyResourcesMaxBytes, Shape: ShapeLimit, Default: value.Int(defaultMaxBytes), Doc: "largest resource in bytes"},
		{Key: KeyResourcesWarnBytes, Shape: ShapeLimit, Default: value.Int(defaultWarnBytes), Doc: "a resource larger than this many bytes prints a warning (RQ4)"},
		{Key: KeyResourcesStaleAfter, Shape: ShapeLimit, Default: value.Int(defaultStaleAfterHours), Doc: "hours since a store's last fetch after which a resource write notes the store kinds' commit (RES 3.6)"},
		{Key: KeyResourcesAllowedIn, Shape: ShapeAllow, Choices: AllowedInPlaces(), Default: mustTexts(AllowedInPlaces()...),
			Doc: "where resources may live: store, repo, client; a lower level may only narrow (RQ2)"},
		{Key: KeyTrashRetentionDays, Shape: ShapeLimit, Doc: "maximum days a deleted row stays in the trash; unset keeps forever"},
		{Key: KeyTasksMaxDepth, Shape: ShapeLimit, Ceiling: ceilingTasksMaxDepth, Default: value.Int(defaultTasksMaxDepth),
			Doc: "deepest sub-task nesting"},
		// TSQ2: strictest first: off writes and prints nothing, suggest prints the
		// command, on closes the parent in the same write.
		{Key: KeyTasksParentAutoclose, Shape: ShapeRank, Choices: []string{"off", "suggest", "on"}, Default: mustText("suggest"),
			Doc: "when a parent's last open sub-task closes: off, suggest (print the close command) or on (close it in the same write) (TSQ2)"},
		// TSQ4: a unit has no "stricter"; a declared order (points, hours, days) so levels join.
		{Key: KeyTasksEstimateUnit, Shape: ShapeRank, Choices: []string{"points", "hours", "days"}, Default: mustText("points"),
			Doc: "unit of a task's estimate: points, hours or days (TSQ4)"},
		// OV12-2: auto watching is the flag's strict value (more people told).
		{Key: KeyTasksAutoWatch, Shape: ShapeFlag, Strict: true, Default: value.BoolOf(true),
			Doc: "creators and assignees watch a task by default (TASK 14.4)"},
		{Key: KeyTasksWeekEnd, Shape: ShapeRank, Choices: []string{"fri", "sat", "sun", "thu", "mon", "tue", "wed"}, Default: mustText("fri"), Doc: "the day eow / end of week means in date phrases (TASK 12.1)"},
		{Key: KeyTasksHistoryMax, Shape: ShapeLimit, Ceiling: ceilingTasksHistoryMax, Default: value.Int(defaultTasksHistoryMax), Doc: "entries kept in a task's history; older ones fold into one summary entry (TSQ10)"},
		{Key: KeyTasksStaleDays, Shape: ShapeLimit, Doc: "days without a change after which lore task stale lists a task; unset uses 60 (TASK 18.5)"},
		{Key: KeyTasksSLALow, Shape: ShapeLimit, Doc: "SLA in hours for low-priority tasks, creation to done; unset = none (TASK 18.4)"},
		{Key: KeyTasksSLAMedium, Shape: ShapeLimit, Doc: "SLA in hours for medium-priority tasks (TASK 18.4)"},
		{Key: KeyTasksSLAHigh, Shape: ShapeLimit, Doc: "SLA in hours for high-priority tasks (TASK 18.4)"},
		{Key: KeyTasksSLAUrgent, Shape: ShapeLimit, Doc: "SLA in hours for urgent tasks (TASK 18.4)"},
		{Key: KeyTimeLongSpan, Shape: ShapeLimit, Default: value.Int(defaultTimeLongSpanMin), Doc: "minutes after which a closed auto time span is flagged long_span; never capped (TQ2)"},
		{Key: KeyTimeCoalesceGap, Shape: ShapeLimit, Default: value.Int(defaultTimeCoalesceGapMin), Doc: "minutes: auto spans of one worker closer than this are coalesced at write (TQ4)"},
		{Key: KeyTimeMaxEntry, Shape: ShapeLimit, Ceiling: defaultTimeMaxEntryMin, Default: value.Int(defaultTimeMaxEntryMin), Doc: "minutes: the longest manual time entry (D13, max 24h)"},
		// TQ3: none is the strict value (a client gets no time data); a per-move --with-time overrides it.
		{Key: KeyTimeClientShare, Shape: ShapeRank, Choices: []string{"none", "totals"}, Default: mustText("none"), Doc: "what a task moved into a client repo carries of its time: none or totals (per worker, no spans) (TQ3)"},
		{Key: KeyContextBudgetClaudeCode, Shape: ShapeLimit, Default: value.Int(defaultContextBudgetLarge), Doc: "bytes of .lore/LORE.md when the claude_code profile reads it (CQ1)"},
		{Key: KeyContextBudgetCodex, Shape: ShapeLimit, Default: value.Int(defaultContextBudget), Doc: "bytes of .lore/LORE.md when the codex profile reads it (CQ1)"},
		{Key: KeyContextBudgetCursor, Shape: ShapeLimit, Default: value.Int(defaultContextBudget), Doc: "bytes of .lore/LORE.md when the cursor profile reads it (CQ1)"},
		{Key: KeyContextBudgetGeminiCLI, Shape: ShapeLimit, Default: value.Int(defaultContextBudgetLarge), Doc: "bytes of .lore/LORE.md when the gemini_cli profile reads it (CQ1)"},
		{Key: KeyContextBudgetCopilot, Shape: ShapeLimit, Default: value.Int(defaultContextBudget), Doc: "bytes of .lore/LORE.md when the copilot profile reads it (CQ1)"},
		{Key: KeyContextBudgetGeneric, Shape: ShapeLimit, Default: value.Int(defaultContextBudget), Doc: "bytes of .lore/LORE.md for the generic profile (CQ1)"},
		{Key: KeyContextBudgetDefault, Shape: ShapeLimit, Default: value.Int(defaultContextBudget), Doc: "bytes of .lore/LORE.md for a user profile that states no budget (CQ1)"},
		{Key: KeyContextEntryMax, Shape: ShapeLimit, Default: value.Int(defaultContextEntryMax), Doc: "an entry longer than this many bytes uses its one-line form in LORE.md"},
		{Key: KeyContextWarnAt, Shape: ShapeLimit, Ceiling: ceilingContextWarnAtPct, Default: value.Int(defaultContextWarnAtPct), Doc: "percent of a context budget above which doctor and lore render --check warn"},
		{Key: KeyContextHookCap, Shape: ShapeLimit, Default: value.Int(defaultContextHookCap), Doc: "bytes of lore context output answered to a tool hook"},
		// CQ5: off by default; off is the strict value (no search on every prompt).
		{Key: KeyContextPromptSearch, Shape: ShapeFlag, Strict: false, Default: value.BoolOf(false), Doc: "lore setup --agent-hooks also installs the per-prompt search hook (CQ5)"},
		{Key: KeyContextFullFile, Shape: ShapeFlag, Strict: false, Default: value.BoolOf(true), Doc: "lore render also writes .lore/LORE.full.md, every pinned entry in full (CQ2)"},
		// CQ10: off is the strict value (no new files in a repo); client repos read an unset key as off.
		{Key: KeyContextDirFiles, Shape: ShapeFlag, Strict: false, Default: value.BoolOf(true), Doc: "lore render writes per-directory LORE.md files for dir scopes (client repos: off until set for that client, CQ10)"},
		{Key: KeyContextBudgetDir, Shape: ShapeLimit, Default: value.Int(defaultContextBudgetDir), Doc: "bytes of each per-directory LORE.md (6.1)"},
		{Key: KeyContextDirMinEntries, Shape: ShapeLimit, Default: value.Int(defaultContextDirMinEntries), Doc: "a directory scope with fewer entries gets no file; its rows go to the parent file (6.1)"},
		{Key: KeyContextChainMaxCodex, Shape: ShapeLimit, Default: value.Int(defaultContextChainMaxCodex), Doc: "bytes the root-to-deepest AGENTS.md chain may hold for Codex (6.2.6)"},
		// XQ2: a limit, the smaller is stricter: a lower preview count makes
		// more bulk actions stop and ask, so a level may only lower it.
		{Key: KeyBatchPreviewRows, Shape: ShapeLimit, Default: value.Int(defaultPreviewRows),
			Doc: "rows a bulk action (a --where delete, an import) changes before it previews and asks (XQ2)"},
		// true is stricter: a level may require a reason, never drop a requirement above it.
		{Key: KeyDeleteRequireReason, Shape: ShapeFlag, Strict: true, Default: value.BoolOf(false), Doc: "refuse a delete without --reason (E_REASON_REQUIRED)"},
		{Key: KeyAgentsPersonOnly, Shape: ShapeSet, Default: mustList(), Doc: "operations only a person may run"},
		// A set: every level's items join (union), and ClientRoutes then
		// takes, per kind, a store target over "repo" (a store is
		// stricter: the row stays out of the customer's repository), the
		// first in sorted order between two stores.
		{Key: KeyClientRoutes, Shape: ShapeSet, Default: mustList(),
			Doc: "where each kind goes in a client repo without --to: items <kind>=repo|<org>|<org>/<folder>; a store beats repo"},
		// Q-R2-3 (SETTINGS ARE DATA): a repo's tool targets are a settings
		// row, not a fact re-detected from pointer files on every render, so
		// the choice is explicit, synced and changed by one command. A set:
		// the levels' lists join, so a team folder can add a tool to every
		// repo it covers and a repo cannot drop it (the same "lower levels
		// only add" rule as every other key). The empty default means "no
		// row": render then falls back to the pointer-file detection, so a
		// repo wired before 0.1.18 keeps its CLAUDE.md. Profile names are
		// checked against the registry by policy set (ops/settings), not
		// here: user profiles live in ~/.lore/config.yaml, outside L1.
		{Key: KeyContextTargets, Shape: ShapeSet, Default: mustList(),
			Doc: "the tool profiles lore render wires (claude_code, codex, cursor, gemini_cli, copilot, generic, or your own); unset = detect from the repo's tool files"},
	}
	for _, u := range UrgencyKeys() {
		what := "the " + u.Term + " term"
		if u.Level != "" {
			what = "priority " + u.Level
		}
		defs = append(defs, KeyDef{Key: u.Key, Shape: ShapeWeight, Ceiling: ceilingUrgencyWeight,
			Doc: "urgency points of " + what + " (two decimals, may be negative; the nearest level wins; unset = the TASK 13.2 default)"})
	}
	slices.SortFunc(defs, func(a, b KeyDef) int { return cmp.Compare(a.Key, b.Key) })
	return defs
}

// Lookup returns the definition of k.
func Lookup(k Key) (KeyDef, bool) {
	for _, d := range Keys() {
		if d.Key == k {
			return d, true
		}
	}
	return KeyDef{}, false
}

func choices[T ~string](xs []T) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = string(x)
	}
	return out
}

// mustText builds a constant text default; the inputs are ASCII literals.
func mustText(s string) value.Value {
	t, err := value.TextOf(s)
	if err != nil {
		panic("policy: invalid default " + s) // programmer error in Keys
	}
	return t
}

// mustList builds an empty text list default.
func mustList() value.Value {
	l, err := value.ListOf(value.KindText)
	if err != nil {
		panic("policy: invalid empty list default") // programmer error in Keys
	}
	return l
}

// mustTexts builds a constant text list default from ASCII literals.
func mustTexts(xs ...string) value.Value {
	items := make([]value.Value, len(xs))
	for i, x := range xs {
		items[i] = mustText(x)
	}
	l, err := value.ListOf(value.KindText, items...)
	if err != nil {
		panic("policy: invalid list default") // programmer error in Keys
	}
	return l
}

// ClientRoutes reads the effective KeyClientRoutes items into kind →
// target. Per kind the stricter target wins: any store target over
// ClientRouteRepo (v0.1.17 effectiveDefault: "may only make a default
// stricter"); between two store targets the first in sorted order, so the
// answer never depends on row order. Items without "=" or with an empty
// side are skipped (policy set validates the shape it writes).
func (s Set) ClientRoutes() map[string]string {
	out := map[string]string{}
	for _, it := range s.Strings(KeyClientRoutes) { // sorted
		kind, target, ok := strings.Cut(it, clientRouteSep)
		kind, target = strings.TrimSpace(kind), strings.TrimSpace(target)
		if !ok || kind == "" || target == "" {
			continue
		}
		cur, seen := out[kind]
		if !seen || (cur == ClientRouteRepo && target != ClientRouteRepo) {
			out[kind] = target
		}
	}
	return out
}

// ClientRouteItem is one KeyClientRoutes item ("memory=agency").
func ClientRouteItem(kind, target string) string { return kind + clientRouteSep + target }

// ContextTargets returns the effective KeyContextTargets profile names,
// sorted; ok is false when no level sets the key or the joined list is
// empty, which is the caller's cue to fall back to pointer-file detection
// (Q-R2-3: repos without the row keep working unchanged).
func (s Set) ContextTargets() (names []string, ok bool) {
	e, found := s.m[KeyContextTargets]
	if !found || e.IsDefault {
		return nil, false
	}
	names = s.Strings(KeyContextTargets)
	return names, len(names) > 0
}
