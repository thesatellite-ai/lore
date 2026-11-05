package validate

// P3-TEXT-1: every text value is cleaned on the write path (logic/textnorm).

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestFieldsCleanText: a text field and each text item of a list lose bidi
// controls, zero-width characters and CR, and are NFC; other kinds are
// untouched; a NUL byte is refused (E_RESOURCE_INVALID) without the value
// in the message; clean input comes back unchanged.
func TestFieldsCleanText(t *testing.T) {
	t.Parallel()
	text := func(s string) value.Value {
		v, err := value.TextOf(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tags, err := value.ListOf(value.KindText, text("a\u202eb"), text("plain"), text("c\r\nd"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{Whole, Patch} {
		out, err := Fields(hostDef, map[string]value.Value{
			"name": text("safe\u202eevil\r\nline\u200b cafe\u0301"),
			"tags": tags,
			"port": text("8080"),
		}, mode)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if got := out["name"].String(); got != "safeevil\nline caf\u00e9" {
			t.Errorf("%s: name = %q", mode, got)
		}
		l, ok := out["tags"].(value.List)
		if !ok {
			t.Fatalf("%s: tags = %T", mode, out["tags"])
		}
		var items []string
		for _, it := range l.Items() {
			items = append(items, it.String())
		}
		if strings.Join(items, "|") != "ab|plain|c\nd" {
			t.Errorf("%s: tags = %q", mode, items)
		}
		if out["port"].Kind() != value.KindNumber {
			t.Errorf("%s: port kind = %s", mode, out["port"].Kind())
		}
	}
	clean := text("already clean\n  indented")
	out, err := Fields(hostDef, map[string]value.Value{"name": clean}, Whole)
	if err != nil || out["name"].String() != clean.String() {
		t.Fatalf("clean text changed: %v %v", out["name"], err)
	}
	_, err = Fields(hostDef, map[string]value.Value{"name": text("secretvalue\x00x")}, Whole)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.ResourceInvalid || !strings.Contains(e.Message, "NUL") || strings.Contains(err.Error(), "secretvalue") {
		t.Fatalf("NUL byte: %v", err)
	}
}
