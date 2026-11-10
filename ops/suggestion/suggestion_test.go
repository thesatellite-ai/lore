package suggestion_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's suggestion facts (read from its real output, 2026-11-04).
var spec = contenttest.Spec{
	Noun: "suggestion", Table: schema.TableSuggestions, Prefix: "sug",
	KindList: "suggestion.list", KindShow: "suggestion.show", KindSearch: "suggestion.search",
	Empty: "(no suggestions)\n", Badge: "SU", TitleKey: "title",
	ListLine: contenttest.TitleLine("SU", "title"),
	Add: func(title, body string) string {
		return `{"title":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "project_id", "title", "body", "status_str", "created_by_actor_id"},
	HitKeys:    []string{"body", "status_str", "title"},
	EditWord:   "suggestion",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","title":` + contenttest.Quote(v) + `}`
	},
}

func TestSuggestionConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}

// TestStatus: a new suggestion is "active" (v0.1.17 status_str default);
// edit --status sets free-form text, which search --column status_str
// then finds.
func TestStatus(t *testing.T) {
	h := opstest.New(t)
	id := h.ID(h.Must("suggestion.add", `{"title":"Use slog","body":"b"}`))
	if got := contenttest.Str(t, h.JSON(h.Must("suggestion.show", `{"id":"`+id+`"}`)), "data", "status_str"); got != "active" {
		t.Errorf("default status %q, want active", got)
	}
	for _, st := range []string{"accepted", "rejected — dup of 12", "wontfix 🚫"} {
		h.Must("suggestion.edit", `{"id":"`+id+`","status":`+opstest.Quote(st)+`}`)
		show := h.JSON(h.Must("suggestion.show", `{"id":"`+id+`"}`))
		if got := contenttest.Str(t, show, "data", "status_str"); got != st {
			t.Errorf("status %q stored as %q", st, got)
		}
		if contenttest.Str(t, show, "data", "title") != "Use slog" {
			t.Error("edit --status changed the title")
		}
	}
	res := h.Must("suggestion.search", `{"query":"wontfix","columns":["status_str"]}`)
	if !strings.Contains(h.JSON(res), id) {
		t.Errorf("search --column status_str misses the status: %s", h.JSON(res))
	}
}
