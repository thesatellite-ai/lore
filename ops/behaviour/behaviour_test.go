package behaviour_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's behaviour facts (read from its real output, 2026-11-04).
var spec = contenttest.Spec{
	Noun: "behaviour", Table: schema.TableBehaviours, Prefix: "bhv",
	KindList: "behaviour.list", KindShow: "behaviour.show", KindSearch: "behaviour.search",
	Empty: "(no behaviours)\n", Badge: "BE", TitleKey: "name",
	ListLine: contenttest.TitleLine("BE", "name"),
	Add: func(title, body string) string {
		return `{"name":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "project_id", "name", "body", "created_by_actor_id"},
	HitKeys:    []string{"body", "name"},
	EditWord:   "behaviour",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","name":` + contenttest.Quote(v) + `}`
	},
}

func TestBehaviourConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}
