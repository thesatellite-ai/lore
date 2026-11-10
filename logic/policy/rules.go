package policy

import (
	"cmp"
	"slices"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// secret_rules row field names (schema.Builtin).
const (
	fieldRuleID = "rule_id"
	fieldState  = "state"
)

// RuleEntry is one secret_rules row's state as seen from a repo.
type RuleEntry struct {
	Level  Level
	RuleID string
	State  string // schema.SecretRuleOn, SecretRuleOff or SecretRuleChanged
	Source ids.AnyID
}

// RuleEntryFromRow reads a live secret_rules row found at level.
func RuleEntryFromRow(level Level, r row.Row) (RuleEntry, error) {
	if r.Table != schema.TableSecretRules || r.Deleted {
		return RuleEntry{}, errs.New(errs.InvalidInput, "policy: not a live secret_rules row")
	}
	id, ok := r.Fields[fieldRuleID].(value.Text)
	if !ok || id.String() == "" {
		return RuleEntry{}, configErr(r.ID, "has no rule_id")
	}
	st, ok := r.Fields[fieldState].(value.Text)
	if !ok || !slices.Contains(schema.SecretRuleStates(), st.String()) {
		return RuleEntry{}, configErr(r.ID, "has no valid state")
	}
	return RuleEntry{Level: level, RuleID: id.String(), State: st.String(), Source: r.ID}, nil
}

// RuleOff is one rule turned off, and where.
type RuleOff struct {
	RuleID string
	Level  Level
	Source ids.AnyID
}

// Rules is the resolved on/off state of secret rules.
//
// The owner exception to stricter-wins (D2/D9 changed): a rule turned off
// at ANY level is off, because users fully control secret rules; a nearer
// "on" does not turn it back on. A rule turned off only in the personal
// store covers only that person's own captures (BATCH 8): the commit and
// push hooks keep it.
type Rules struct {
	off []RuleOff
}

// ResolveRules collects every rule that is off.
func ResolveRules(entries []RuleEntry) Rules {
	var r Rules
	for _, e := range entries {
		if e.State == schema.SecretRuleOff {
			r.off = append(r.off, RuleOff{RuleID: e.RuleID, Level: e.Level, Source: e.Source})
		}
	}
	slices.SortStableFunc(r.off, func(a, b RuleOff) int {
		if c := cmp.Compare(a.RuleID, b.RuleID); c != 0 {
			return c
		}
		return cmp.Compare(a.Level.rank(), b.Level.rank())
	})
	return r
}

// Off lists every off entry (one per rule and level), sorted by rule then
// level: what lore doctor prints.
func (r Rules) Off() []RuleOff { return slices.Clone(r.off) }

// IsOff reports whether ruleID is off for this check. ownCapture is true
// for a write by the person whose personal store turned it off (every
// engine write); the commit and push hooks pass false, so a personal
// turn-off never weakens them.
func (r Rules) IsOff(ruleID string, ownCapture bool) bool {
	for _, o := range r.off {
		if o.RuleID == ruleID && (o.Level != LevelPersonal || ownCapture) {
			return true
		}
	}
	return false
}
