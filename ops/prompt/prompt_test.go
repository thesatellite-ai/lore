package prompt_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's prompt facts (read from its real output): add takes
// --title, the row and --json call it name, edit renames with --name.
var spec = contenttest.Spec{
	Noun: "prompt", Table: schema.TablePrompts, Prefix: "prm",
	KindList: "prompt.list", KindShow: "prompt.show", KindSearch: "prompt.search",
	Empty: "(no prompts)\n", Badge: "PR", TitleKey: "name",
	ListLine: contenttest.TitleLine("PR", "name"),
	Add: func(title, body string) string {
		return `{"title":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "project_id", "name", "description", "body", "created_by_actor_id"},
	HitKeys:    []string{"body", "name"},
	EditWord:   "prompt",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","name":` + contenttest.Quote(v) + `}`
	},
	Archive: true,
}

func TestPromptConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}

// TestDescription: v0.1.17 defaulted the description to the title; an
// explicit one is kept, and edit --description changes only it.
func TestDescription(t *testing.T) {
	h := opstest.New(t)
	for _, c := range []struct{ title, desc, want string }{
		{"Deploy", "", "Deploy"},
		{"Deploy two", "ship it to prod", "ship it to prod"},
		{"héllo 世界", "", "héllo 世界"},
	} {
		in := `{"title":` + opstest.Quote(c.title) + `,"body":"b"`
		if c.desc != "" {
			in += `,"description":` + opstest.Quote(c.desc)
		}
		id := h.ID(h.Must("prompt.add", in+"}"))
		show := h.JSON(h.Must("prompt.show", `{"id":"`+id+`"}`))
		if got := contenttest.Str(t, show, "data", "description"); got != c.want {
			t.Errorf("%q/%q: description %q, want %q", c.title, c.desc, got, c.want)
		}
		h.Must("prompt.edit", `{"id":"`+id+`","description":"new desc"}`)
		show = h.JSON(h.Must("prompt.show", `{"id":"`+id+`"}`))
		if contenttest.Str(t, show, "data", "description") != "new desc" || contenttest.Str(t, show, "data", "name") != c.title {
			t.Errorf("edit --description: %s", show)
		}
	}
	if _, err := h.Run("prompt.add", `{"title":"x","body":"b","description":"  "}`); err != nil && !strings.Contains(err.Error(), "description") {
		t.Errorf("blank description: %v", err)
	}
}

// TestNameIsUnique: a prompt's name is unique in its project (v0.1.17's
// UNIQUE(project_id, name), now the natural key): a second add with the
// same name is refused and the first prompt is unchanged; another name
// (or case) is a new prompt.
func TestNameIsUnique(t *testing.T) {
	h := opstest.New(t)
	id := h.ID(h.Must("prompt.add", `{"title":"Dup","body":"first"}`))
	msg := h.Fails("prompt.add", `{"title":"Dup","body":"second"}`, errs.NameTaken)
	if !strings.Contains(msg, "already exists") || !strings.Contains(msg, id) {
		t.Errorf("duplicate name message %q should name the existing %s", msg, id)
	}
	if got := contenttest.Str(t, h.JSON(h.Must("prompt.show", `{"id":"`+id+`"}`)), "data", "body"); got != "first" {
		t.Errorf("the refused add changed the first prompt: %q", got)
	}
	other := h.ID(h.Must("prompt.add", `{"title":"dup","body":"lower case"}`))
	if other == id {
		t.Error("a name differing in case got the same id")
	}
	// a deleted prompt's name can be used again (fresh id: the tombstone
	// keeps the old one from coming back).
	h.MustYes("prompt.delete", `{"id":"`+id+`"}`)
	again := h.ID(h.Must("prompt.add", `{"title":"Dup","body":"third"}`))
	if got := contenttest.Str(t, h.JSON(h.Must("prompt.show", `{"id":"`+again+`"}`)), "data", "body"); got != "third" {
		t.Errorf("re-added prompt body %q", got)
	}
}

// TestArgsSchema: --args-schema (new on the CLI) is stored and edited.
func TestArgsSchema(t *testing.T) {
	h := opstest.New(t)
	schemaJSON := `{"type":"object","properties":{"x":{"type":"string"}}}`
	id := h.ID(h.Must("prompt.add", `{"title":"P","body":"hi {{args.x}}","args_schema":`+opstest.Quote(schemaJSON)+`}`))
	show := h.JSON(h.Must("prompt.show", `{"id":"`+id+`"}`))
	if got := contenttest.Str(t, show, "data", "args_schema"); got != schemaJSON {
		t.Errorf("args_schema %q", got)
	}
	h.Must("prompt.edit", `{"id":"`+id+`","args_schema":"{}"}`)
	if got := contenttest.Str(t, h.JSON(h.Must("prompt.show", `{"id":"`+id+`"}`)), "data", "args_schema"); got != "{}" {
		t.Errorf("edited args_schema %q", got)
	}
}
