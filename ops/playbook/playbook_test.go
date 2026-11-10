package playbook_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's playbook facts (read from its real output): add takes
// --title, the row and --json call it name, edit renames with --name.
var spec = contenttest.Spec{
	Noun: "playbook", Table: schema.TablePlaybooks, Prefix: "pbk",
	KindList: "playbook.list", KindShow: "playbook.show", KindSearch: "playbook.search",
	Empty: "(no playbooks)\n", Badge: "PL", TitleKey: "name",
	ListLine: contenttest.TitleLine("PL", "name"),
	Add: func(title, body string) string {
		return `{"title":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "trust_score", "source_kind", "project_id", "name", "description", "body", "created_by_actor_id"},
	HitKeys:    []string{"body", "name"},
	EditWord:   "playbook",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","name":` + contenttest.Quote(v) + `}`
	},
	Archive: true,
}

func TestPlaybookConformance(t *testing.T) {
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
		id := h.ID(h.Must("playbook.add", in+"}"))
		show := h.JSON(h.Must("playbook.show", `{"id":"`+id+`"}`))
		if got := contenttest.Str(t, show, "data", "description"); got != c.want {
			t.Errorf("%q/%q: description %q, want %q", c.title, c.desc, got, c.want)
		}
		h.Must("playbook.edit", `{"id":"`+id+`","description":"new desc"}`)
		show = h.JSON(h.Must("playbook.show", `{"id":"`+id+`"}`))
		if contenttest.Str(t, show, "data", "description") != "new desc" || contenttest.Str(t, show, "data", "name") != c.title {
			t.Errorf("edit --description: %s", show)
		}
	}
	if _, err := h.Run("playbook.add", `{"title":"x","body":"b","description":"  "}`); err != nil && !strings.Contains(err.Error(), "description") {
		t.Errorf("blank description: %v", err)
	}
}
