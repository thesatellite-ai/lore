package policy

import (
	"cmp"
	"slices"

	"github.com/thesatellite-ai/lore/model/value"
)

// Key is a policy key ("secrets.on_detect").
type Key string

// The closed set of policy keys this version evaluates (DECISIONS_LOG 6).
// Keys of the log whose values have no "stricter" (estimate units, budgets)
// join with their noun and a declared order; an unknown key is refused.
const (
	KeyOnDetect           Key = "secrets.on_detect"
	KeyAgentReveal        Key = "secrets.agent_reveal"
	KeyUseRepoGitleaks    Key = "secrets.use_repo_gitleaks"
	KeyMetaMaxBytes       Key = "meta.max_bytes"
	KeyResourcesMaxBytes  Key = "resources.max_bytes"
	KeyTrashRetentionDays Key = "trash.retention_days"
	KeyAgentsPersonOnly   Key = "agents.person_only"
	KeyTasksMaxDepth      Key = "tasks.max_depth"
)

// Numeric defaults of the keys above.
const (
	defaultMaxBytes      int64 = 16 * 1024 // MQ3, RQ4, CFG10
	defaultTasksMaxDepth int64 = 4         // TSQ1
	ceilingTasksMaxDepth int64 = 8         // TSQ1: "default 4, up to 8"
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
)

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
		{Key: KeyMetaMaxBytes, Shape: ShapeLimit, Default: value.Int(defaultMaxBytes), Doc: "largest metadata value in bytes"},
		{Key: KeyResourcesMaxBytes, Shape: ShapeLimit, Default: value.Int(defaultMaxBytes), Doc: "largest resource in bytes"},
		{Key: KeyTrashRetentionDays, Shape: ShapeLimit, Doc: "maximum days a deleted row stays in the trash; unset keeps forever"},
		{Key: KeyTasksMaxDepth, Shape: ShapeLimit, Ceiling: ceilingTasksMaxDepth, Default: value.Int(defaultTasksMaxDepth),
			Doc: "deepest sub-task nesting"},
		{Key: KeyAgentsPersonOnly, Shape: ShapeSet, Default: mustList(), Doc: "operations only a person may run"},
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
