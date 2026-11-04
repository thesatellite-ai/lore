package cookbookrecipe_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's cookbookrecipe facts (read from its real output, 2026-11-04).
var spec = contenttest.Spec{
	Noun: "cookbookrecipe", Table: schema.TableCookbookRecipes, Prefix: "cbr",
	KindList: "cookbookRecipe.list", KindShow: "cookbookRecipe.show", KindSearch: "cookbookrecipe.search",
	Empty: "(no cookbookrecipes)\n", Badge: "CO", TitleKey: "title",
	Add: func(title, body string) string {
		return `{"title":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "project_id", "title", "body", "created_by_actor_id"},
	HitKeys:    []string{"body", "language", "title"},
	EditWord:   "cookbook-recipe",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","title":` + contenttest.Quote(v) + `}`
	},
}

func TestCookbookrecipeConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}

// TestLanguage: --language is stored, shown in the hit row (v0.1.17
// printed "language": "" when unset), and searchable by --column.
func TestLanguage(t *testing.T) {
	h := opstest.New(t)
	goID := h.ID(h.Must("cookbookrecipe.add", `{"title":"Retry in go","body":"b","language":"golang"}`))
	none := h.ID(h.Must("cookbookrecipe.add", `{"title":"Retry anywhere","body":"b"}`))
	if got := contenttest.Str(t, h.JSON(h.Must("cookbookrecipe.show", `{"id":"`+goID+`"}`)), "data", "language"); got != "golang" {
		t.Errorf("language %q", got)
	}
	doc := h.JSON(h.Must("cookbookrecipe.search", `{"query":"golang","columns":["language"]}`))
	if !strings.Contains(doc, goID) || strings.Contains(doc, none) {
		t.Errorf("search --column language: %s", doc)
	}
	all := h.JSON(h.Must("cookbookrecipe.search", `{"query":"retry"}`))
	if !strings.Contains(all, `"language":""`) {
		t.Errorf("hit row of a recipe without language should carry \"language\":\"\" as v0.1.17: %s", all)
	}
}
