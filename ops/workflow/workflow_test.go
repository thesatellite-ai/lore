package workflow_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's workflow facts (read from its real output, 2026-11-04).
var spec = contenttest.Spec{
	Noun: "workflow", Table: schema.TableWorkflows, Prefix: "wfl",
	KindList: "workflow.list", KindShow: "workflow.show", KindSearch: "workflow.search",
	Empty: "(no workflows)\n", Badge: "WO", TitleKey: "name",
	Add: func(title, body string) string {
		return `{"name":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	// ListLine: the name follows the id, so workflows are told apart in
	// text (v0.1.17 printed the id alone).
	ListLine: func(t testing.TB, rec string) string {
		return "WO  " + contenttest.Str(t, rec, "id") + "  " + contenttest.Str(t, rec, "name") + "\n"
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "project_id", "name", "body", "created_by_actor_id"},
	HitKeys:    []string{"body", "name"},
	EditWord:   "workflow",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","name":` + contenttest.Quote(v) + `}`
	},
}

func TestWorkflowConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}
