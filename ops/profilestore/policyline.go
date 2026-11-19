package profilestore

import (
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/value"
)

// Policy verbs of a printed `lore policy <verb>` line (PolicyCommand).
const (
	// PolicyVerbSet is `lore policy set`.
	PolicyVerbSet = "set"
	// PolicyVerbUnset is `lore policy unset`.
	PolicyVerbUnset = "unset"
)

// Policy row fields the guard's own line reads (schema.TablePolicies).
const (
	policyFieldKey   = "key"
	policyFieldValue = "value"
)

// PolicyCommand is the exact `lore policy set|unset` line an admin runs,
// with named flags only and --yes (ops/settings prints the same line in
// its refusals and questions through it, so one place words it). to is
// the full place (PlaceTo: <store>/<folder>), omitted when empty.
func PolicyCommand(verb, key, val, to string) string {
	c := "lore policy " + verb + " --key " + key
	if verb == PolicyVerbSet {
		c += " --value " + shellWord(val)
	}
	if to != "" {
		c += " --to " + to
	}
	return c + " --yes"
}

// policyRowCommand is the line that makes the policy change w makes, for a
// write no command named (a batch undo or an import of a policy row,
// devco D-694): unset when the change removes the row, else set with the
// row's value as a person types it. ok is false when the row names no key.
func policyRowCommand(w engine.GuardedWrite, to string) (string, bool) {
	from := w.After
	if w.After.Deleted && w.Before != nil {
		from = *w.Before // a tombstone may carry no fields
	}
	key := fieldText(from, policyFieldKey)
	if key == "" {
		return "", false
	}
	if w.After.Deleted {
		return PolicyCommand(PolicyVerbUnset, key, "", to), true
	}
	return PolicyCommand(PolicyVerbSet, key, policyValueText(w.After.Fields[policyFieldValue]), to), true
}

// policyValueText is a stored policy value as `lore policy set --value`
// takes it: text as is, a list (a List in memory, a JSON array from a row
// file) as its items joined by ", ", anything else as JSON (the form
// ops/settings display(fromJSON(v)) prints).
func policyValueText(v value.Value) string {
	if v == nil {
		return ""
	}
	b, isJSON := value.JSONForm(v)
	if isJSON {
		if in, err := value.Infer(b); err == nil {
			v = in
		}
	}
	if t, ok := v.(value.Text); ok {
		return t.String()
	}
	if b, ok := value.JSONForm(v); ok && canon.KindOf(b) == canon.KindArray {
		return strings.Join(value.ItemStrings(v), ", ")
	}
	out, err := v.MarshalJSON()
	if err != nil {
		return ""
	}
	return string(out)
}

// shellWord quotes s for a printed command when it holds a space or a
// shell character (a list value "a, b").
func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\;&|<>*?()[]{}!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
