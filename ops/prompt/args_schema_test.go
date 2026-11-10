package prompt_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
)

// TestArgsSchemaIsAJSONObject (G-CONTENT-2): prompt add|edit --args-schema
// takes a JSON object (the schema of the {{args.x}} placeholders); bad
// JSON, an array, a string, a number, null or a bare word is refused with
// E_INVALID_INPUT and nothing written (no new prompt; an edit leaves the
// stored schema and the other fields as they were); an object, also with
// surrounding whitespace, is kept; an edit without --args-schema keeps it.
func TestArgsSchemaIsAJSONObject(t *testing.T) {
	h := opstest.New(t)
	bad := []string{`nope`, `{"type":`, `[1,2]`, `"object"`, `42`, `null`, `true`}
	for _, b := range bad {
		t.Run("add "+b, func(t *testing.T) {
			before := h.JSON(h.Must("prompt.list", `{}`))
			msg := h.Fails("prompt.add", `{"title":`+opstest.Quote("t-"+b)+`,"body":"b","args_schema":`+opstest.Quote(b)+`}`, errs.InvalidInput)
			if !strings.Contains(msg, "--args-schema is not a JSON object") {
				t.Fatalf("message %q", msg)
			}
			if after := h.JSON(h.Must("prompt.list", `{}`)); after != before {
				t.Fatal("a refused add wrote a prompt")
			}
		})
	}
	obj := `{"type":"object","properties":{"name":{"type":"string"}}}`
	id := h.ID(h.Must("prompt.add", `{"title":"greet","body":"hi {{args.name}}","args_schema":`+opstest.Quote("  "+obj+"\n")+`}`))
	show := func() string { return h.JSON(h.Must("prompt.show", `{"id":"`+id+`"}`)) }
	stored := contenttest.Str(t, show(), "data", "args_schema")
	if !strings.Contains(stored, `"properties"`) {
		t.Fatalf("args_schema not kept: %q", stored)
	}
	for _, b := range bad {
		t.Run("edit "+b, func(t *testing.T) {
			msg := h.Fails("prompt.edit", `{"id":"`+id+`","body":"changed","args_schema":`+opstest.Quote(b)+`}`, errs.InvalidInput)
			if !strings.Contains(msg, "prompt edit") {
				t.Fatalf("message %q", msg)
			}
			s := show()
			if contenttest.Str(t, s, "data", "args_schema") != stored || contenttest.Str(t, s, "data", "body") != "hi {{args.name}}" {
				t.Fatalf("a refused edit changed the prompt: %s", s)
			}
		})
	}
	h.Must("prompt.edit", `{"id":"`+id+`","body":"hello {{args.name}}"}`)
	if got := contenttest.Str(t, show(), "data", "args_schema"); got != stored {
		t.Fatalf("an edit without --args-schema changed it: %q", got)
	}
	h.Must("prompt.edit", `{"id":"`+id+`","args_schema":"{}"}`)
	if got := contenttest.Str(t, show(), "data", "args_schema"); got != "{}" {
		t.Fatalf("edit --args-schema {}: %q", got)
	}
}
