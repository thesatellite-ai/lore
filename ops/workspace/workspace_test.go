package workspace_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's workspace facts (read from its real output, 2026-11-04).
var spec = contenttest.Spec{
	Noun: "workspace", Table: schema.TableWorkspaces, Prefix: "wsp",
	KindList: "workspace.list", KindShow: "workspace.show", KindSearch: "workspace.search",
	Empty: "(no workspaces)\n", Badge: "WO", TitleKey: "name",
	Add: func(title, body string) string {
		return `{"name":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "project_id", "name", "body"},
	HitKeys:    []string{"body", "name"},
	EditWord:   "workspace",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","name":` + contenttest.Quote(v) + `}`
	},
}

func TestWorkspaceConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}
