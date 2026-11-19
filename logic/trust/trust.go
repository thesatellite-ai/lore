// Package trust holds the meaning of a knowledge row's trust_score, layer
// L1 (devco:S-15). The score is a number from 0 to 1 that every rule,
// memory, decision and hotfix row carries and LORE.md prints; it was a
// constant 0.50 until agents wrote rules too. Now a rule an agent adds (or
// whose text an agent changes) starts at Agent, below Default, and stays
// there until a person approves it (`lore rule approve --id`), which sets
// it to Default. Below Default means "agent-written, not approved": render
// marks the rule so in LORE.md, rule list / show say it and a person's
// inbox lists it. The score travels in the row file, so every clone sees
// the same state after a sync; it is an ordinary field (last writer wins
// per field), so an approval and a later agent edit merge like any two
// edits.
package trust

// The trust_score values lore writes.
const (
	// Default is v0.1.17's LifecycleMixin default: a row a person wrote,
	// or an agent's rule a person approved.
	Default = 0.5
	// Agent is the score of a rule an agent wrote or rewrote and no
	// person approved yet.
	Agent = 0.25
)

// ReviewLabel is the words LORE.md and the CLI use for a rule below
// Default, so a reader of any surface sees the same phrase.
const ReviewLabel = "agent-written, not approved"

// Approved reports whether a row's score needs no person's review: true at
// Default or above. A row without the field reads as Default (the caller
// passes Default then), so every row written before this rule stays
// approved.
func Approved(score float64) bool { return score >= Default }
