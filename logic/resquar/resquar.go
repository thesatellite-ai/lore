package resquar

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/value"
)

// Field is what the check needs of one kind_fields row.
type Field struct {
	Name string
	// Type is the field's type word (kind_fields.type); an unknown word is
	// never a quarantine reason (a newer lore may know it).
	Type string
	// Values is the closed set of allowed values (empty: free).
	Values []string
}

// Finding is one quarantined value: the field and the rule it breaks,
// never the value (3.15: messages never carry a sensitive value).
type Finding struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
}

// RuleUnknownField is the rule of a value whose field the kind does not
// define (ops/settings; the engine and cache treat it on their own).
const RuleUnknownField = "unknown field"

// Check reports whether raw (the "v" of one fields entry) breaks f.
func Check(f Field, raw []byte) (Finding, bool) {
	k, err := value.ParseKind(f.Type)
	if err != nil || k == value.KindRef || k == value.KindSecretRef {
		return Finding{}, false
	}
	if _, perr := value.Parse(k, raw); perr != nil {
		return Finding{Field: f.Name, Rule: "does not fit " + string(k)}, true
	}
	if len(f.Values) == 0 {
		return Finding{}, false
	}
	v, err := value.Infer(raw)
	if err != nil {
		return Finding{Field: f.Name, Rule: "does not fit " + string(k)}, true
	}
	for _, s := range itemsOf(v) {
		if !slices.Contains(f.Values, s) {
			return Finding{Field: f.Name, Rule: "not one of " + strings.Join(f.Values, ", ")}, true
		}
	}
	return Finding{}, false
}

// itemsOf is a value's allowed-value form: a text as itself, a list as its
// items (each must be allowed), a null as nothing, anything else as its
// JSON text.
func itemsOf(v value.Value) []string {
	switch x := v.(type) {
	case value.Null:
		return nil
	case value.Text:
		return []string{x.String()}
	case value.List:
		return value.ItemStrings(x)
	}
	return []string{v.String()}
}
