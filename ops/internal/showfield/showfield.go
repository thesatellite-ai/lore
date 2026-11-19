// Package showfield is one stored field as `lore show` and `lore trash
// show` print it: the field's text, or its items when the row file holds
// a list. Before it every field was flattened to one string, so a task's
// `watchers` printed "act_a, act_b" in JSON while the row file held a
// list (devco D-455): a reader of --json had to split text it could not
// tell from a value holding a comma.
//
//	value.List ─▶ Value{List: items}  ─▶ JSON ["a", "b"]   text "a, b"
//	any other  ─▶ Value{Text: s}      ─▶ JSON "s"          text "s"
package showfield

import (
	"encoding/json"
	"strings"

	"github.com/thesatellite-ai/lore/model/value"
)

// listSep joins a list's items in the text form (value.List.String's).
const listSep = ", "

// Value is one field as shown: Text, or List when IsList.
type Value struct {
	Text   string
	List   []string
	IsList bool
}

// Of is v as shown: a list keeps its items, anything else its text.
func Of(v value.Value) Value {
	if l, ok := v.(value.List); ok {
		items := value.ItemStrings(l)
		if items == nil {
			items = []string{}
		}
		return Value{List: items, IsList: true}
	}
	if v == nil {
		return Value{}
	}
	return Value{Text: v.String()}
}

// Text is a field shown as plain text: a hidden (masked) value, a
// resource's masked fields JSON.
func Text(s string) Value { return Value{Text: s} }

// String is the human form: the text, or the items joined.
func (v Value) String() string {
	if v.IsList {
		return strings.Join(v.List, listSep)
	}
	return v.Text
}

// MarshalJSON writes a list as a JSON array of strings, anything else as
// a JSON string.
func (v Value) MarshalJSON() ([]byte, error) {
	if v.IsList {
		items := v.List
		if items == nil {
			items = []string{}
		}
		return json.Marshal(items)
	}
	return json.Marshal(v.Text)
}

// UnmarshalJSON reads either form back (a client of `lore api` decoding
// show's output).
func (v *Value) UnmarshalJSON(b []byte) error {
	var items []string
	if err := json.Unmarshal(b, &items); err == nil {
		*v = Value{List: items, IsList: true}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*v = Value{Text: s}
	return nil
}

// Map is a row's fields as shown. It is a text type with its own JSON
// form for the op layer (op.TypeText, OwnJSON): an output field may be a
// map of strings or a text type, and this map's values are a string or a
// list of strings.
type Map map[string]Value

// MarshalJSON writes the map with each value in its own form.
func (m Map) MarshalJSON() ([]byte, error) { return json.Marshal(map[string]Value(m)) }

// UnmarshalJSON reads the map back.
func (m *Map) UnmarshalJSON(b []byte) error {
	var raw map[string]Value
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*m = raw
	return nil
}

// MarshalText is the JSON form (the op layer's text-type contract; the
// JSON encoder uses MarshalJSON).
func (m Map) MarshalText() ([]byte, error) { return m.MarshalJSON() }

// UnmarshalText reads the JSON form.
func (m *Map) UnmarshalText(b []byte) error { return m.UnmarshalJSON(b) }
