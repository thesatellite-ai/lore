package incident_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's incident facts (read from its real output, 2026-11-04).
var spec = contenttest.Spec{
	Noun: "incident", Table: schema.TableIncidents, Prefix: "inc",
	KindList: "incident.list", KindShow: "incident.show", KindSearch: "incident.search",
	Empty: "(no incidents)\n", Badge: "IN", TitleKey: "title",
	ListLine: contenttest.TitleLine("IN", "title"),
	Add: func(title, body string) string {
		return `{"title":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "project_id", "title", "body", "created_by_actor_id"},
	HitKeys:    []string{"body", "title"},
}

func TestIncidentConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}
