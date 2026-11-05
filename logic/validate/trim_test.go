package validate

// OWNERQ-3 (P3-TEXT-1): a built-in table's title, body, description and
// note are trimmed of edge whitespace on every write; other fields and a
// resource kind's own fields keep theirs.

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// trimDef is a built-in-shaped table: every trimmed name (required and
// optional) beside fields that must keep their whitespace.
var trimDef = schema.Table("trim_rows", ids.Prefix("trr"),
	schema.Text("title", schema.Required()),
	schema.Text("body"),
	schema.Text("description"),
	schema.Text("note"),
	schema.Text("name"),
	schema.Text("value"),
	schema.List("tags", value.KindText),
)

func trimText(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestFieldsTrimEdges: field name x edge shape x mode. Trimmed names lose
// leading/trailing spaces, tabs and line breaks (after cleaning, so a CRLF
// edge goes too) and keep inner whitespace; other fields come back as
// given.
func TestFieldsTrimEdges(t *testing.T) {
	t.Parallel()
	edges := []struct {
		name, in, trimmed string
	}{
		{"plain", "fix the build", "fix the build"},
		{"spaces", "  fix the build  ", "fix the build"},
		{"tabs and newlines", "\t\nfix the build\n\n", "fix the build"},
		{"crlf edge", "\r\nfix the build\r\n", "fix the build"},
		{"inner kept", " line one\n\n  indented two ", "line one\n\n  indented two"},
		{"unicode space", " fix ", "fix"},
	}
	trimmed := []string{"title", "body", "description", "note"}
	kept := []string{"name", "value"}
	for _, mode := range []Mode{Whole, Patch} {
		for _, c := range edges {
			in := map[string]value.Value{}
			for _, f := range append(append([]string{}, trimmed...), kept...) {
				in[f] = trimText(t, c.in)
			}
			out, err := Fields(trimDef, in, mode)
			if err != nil {
				t.Fatalf("%s/%s: %v", mode, c.name, err)
			}
			for _, f := range trimmed {
				if got := out[f].String(); got != c.trimmed {
					t.Errorf("%s/%s: %s = %q, want %q", mode, c.name, f, got, c.trimmed)
				}
			}
			for _, f := range kept {
				// cleaning still runs (CRLF -> LF); only trimming is skipped
				want := strings.ReplaceAll(c.in, "\r\n", "\n")
				if got := out[f].String(); got != want {
					t.Errorf("%s/%s: %s = %q, want untrimmed %q", mode, c.name, f, got, want)
				}
			}
		}
	}
}

// TestFieldsTrimListItemsKept: text list items (tags) are not trimmed.
func TestFieldsTrimListItemsKept(t *testing.T) {
	t.Parallel()
	tags, err := value.ListOf(value.KindText, trimText(t, " a "))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Fields(trimDef, map[string]value.Value{"title": trimText(t, "x"), "tags": tags}, Whole)
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := out["tags"].(value.List); !ok || l.Items()[0].String() != " a " {
		t.Fatalf("tags = %v", out["tags"])
	}
}

// TestFieldsTrimBlank: a required trimmed field that is blank once trimmed
// is refused (E_RESOURCE_INVALID, field named, value never repeated); an
// optional one is stored empty; null still clears an optional field.
func TestFieldsTrimBlank(t *testing.T) {
	t.Parallel()
	for _, mode := range []Mode{Whole, Patch} {
		for _, blank := range []string{"", " ", "\n\t \r\n", " "} {
			_, err := Fields(trimDef, map[string]value.Value{"title": trimText(t, blank)}, mode)
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != errs.ResourceInvalid || !strings.Contains(e.Message, "field title is required and cannot be blank") {
				t.Errorf("%s: blank title %q: %v", mode, blank, err)
			}
			out, err := Fields(trimDef, map[string]value.Value{"title": trimText(t, "t"), "body": trimText(t, blank)}, mode)
			if err != nil || out["body"].String() != "" {
				t.Errorf("%s: blank optional body %q: %v %v", mode, blank, out["body"], err)
			}
		}
		out, err := Fields(trimDef, map[string]value.Value{"title": trimText(t, "t"), "body": value.Null{}}, mode)
		if err != nil || out["body"].Kind() != value.KindNull {
			t.Errorf("%s: null body: %v %v", mode, out["body"], err)
		}
	}
}

// TestFieldsTrimSkipsResourceKinds: a resource kind's definition (Kind set)
// keeps the whitespace of its own fields, even ones named title or body.
func TestFieldsTrimSkipsResourceKinds(t *testing.T) {
	t.Parallel()
	kind, err := ids.New[ids.Kind]()
	if err != nil {
		t.Fatal(err)
	}
	def := trimDef
	def.Kind = kind
	out, err := Fields(def, map[string]value.Value{"title": trimText(t, "  keep  "), "body": trimText(t, " b ")}, Whole)
	if err != nil {
		t.Fatal(err)
	}
	if out["title"].String() != "  keep  " || out["body"].String() != " b " {
		t.Fatalf("resource fields trimmed: %q %q", out["title"], out["body"])
	}
}
