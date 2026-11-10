package schema

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/value"
)

// Widget is the closed set of input widgets a generic UI may draw
// (spec 9.8).
type Widget string

// Widgets.
const (
	WidgetText     Widget = "text"
	WidgetMarkdown Widget = "markdown"
	WidgetCode     Widget = "code"
	WidgetSecret   Widget = "secret"
	WidgetSelect   Widget = "select"
	WidgetDate     Widget = "date"
	WidgetNumber   Widget = "number"
	WidgetBool     Widget = "bool"
	WidgetJSON     Widget = "json"
)

// Widgets lists every widget.
func Widgets() []Widget {
	return []Widget{WidgetText, WidgetMarkdown, WidgetCode, WidgetSecret, WidgetSelect, WidgetDate, WidgetNumber, WidgetBool, WidgetJSON}
}

// Valid reports whether w is one of Widgets.
func (w Widget) Valid() bool {
	for _, x := range Widgets() {
		if x == w {
			return true
		}
	}
	return false
}

// InList is the tri-state "show in list views" hint: unset means "use the
// default rule".
type InList int

// InList states.
const (
	InListDefault InList = iota
	InListYes
	InListNo
)

// UI holds optional display hints. Zero fields mean "use the default"; the
// Resolve* functions apply spec 9.8's defaults so every surface agrees.
type UI struct {
	Label  string
	Widget Widget
	InList InList
}

// DefaultLabel is the field name with "_" as spaces and the first letter
// upper-cased ("due_at" → "Due at").
func DefaultLabel(name string) string {
	s := strings.ReplaceAll(name, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// DefaultWidget picks the widget from the field's type: a sensitive field is
// a secret, a closed value set a select, then by kind.
func DefaultWidget(k value.Kind, sensitive, hasValues bool) Widget {
	switch {
	case sensitive:
		return WidgetSecret
	case hasValues:
		return WidgetSelect
	}
	switch k {
	case value.KindDate, value.KindDateTime:
		return WidgetDate
	case value.KindNumber:
		return WidgetNumber
	case value.KindBool:
		return WidgetBool
	case value.KindJSON, value.KindList:
		return WidgetJSON
	case value.KindText, value.KindDuration, value.KindIP, value.KindCIDR, value.KindDNS,
		value.KindURL, value.KindEmail, value.KindRef, value.KindSecretRef, value.KindNull:
	}
	return WidgetText
}

// MaxDefaultInList is how many short fields (after the name) a list view
// shows by default (spec 9.8: "the name and up to 4 short fields").
const MaxDefaultInList = 4

// ResolvedUI is the hint set with every default applied.
type ResolvedUI struct {
	Label  string `json:"label"`
	Widget Widget `json:"widget"`
	InList bool   `json:"in_list"`
}

// ResolveFields applies the defaults to every field of a table, in order: a
// field named "name" is always in the list; up to MaxDefaultInList other
// short fields (not markdown, code, json or secret) are in by default.
func ResolveFields(fields []FieldDef) []ResolvedUI {
	out := make([]ResolvedUI, len(fields))
	short := 0
	for i, f := range fields {
		r := ResolvedUI{Label: f.UI.Label, Widget: f.UI.Widget}
		if r.Label == "" {
			r.Label = DefaultLabel(f.Name)
		}
		if r.Widget == "" {
			r.Widget = DefaultWidget(f.Kind, f.Sensitive, len(f.Values) > 0)
		}
		switch f.UI.InList {
		case InListYes:
			r.InList = true
		case InListNo:
			r.InList = false
		case InListDefault:
			r.InList = defaultInList(f.Name, r.Widget, &short)
		}
		out[i] = r
	}
	return out
}

// nameField is the field always shown in lists.
const nameField = "name"

// defaultInList applies the spec 9.8 default and counts short fields used.
func defaultInList(name string, w Widget, short *int) bool {
	if name == nameField {
		return true
	}
	switch w {
	case WidgetMarkdown, WidgetCode, WidgetJSON, WidgetSecret:
		return false
	case WidgetText, WidgetSelect, WidgetDate, WidgetNumber, WidgetBool:
	}
	if *short >= MaxDefaultInList {
		return false
	}
	*short++
	return true
}
