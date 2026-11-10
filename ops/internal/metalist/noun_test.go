package metalist_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/decision"
	"github.com/thesatellite-ai/lore/ops/hotfix"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/ops/rule"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// supersedingNoun is one noun with --supersedes, --no-meta and delete
// --reason (the knowledge nouns and memory).
type supersedingNoun struct {
	add, del string
	body     string // the add input's own fields, without braces
}

var supersedingNouns = []supersedingNoun{
	{decision.OpAdd, decision.OpDelete, `"title":"T","body":"b"`},
	{memory.OpAdd, memory.OpDelete, `"body":"b"`},
	{rule.OpAdd, rule.OpDelete, `"body":"b","severity":"should"`},
	{hotfix.OpAdd, hotfix.OpDelete, `"title":"T","body":"b"`},
}

// TestNounNoMetaAndReason (later 481, METADATA 3.19, DELETION 2.1): on
// every noun with --supersedes, the replacement carries the replaced row's
// metadata unless --no-meta (then none), and delete --reason is saved on
// the trashed row.
func TestNounNoMetaAndReason(t *testing.T) {
	for _, n := range supersedingNouns {
		t.Run(n.add, func(t *testing.T) {
			h := opstest.New(t)
			h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
			show := func(id string) string {
				t.Helper()
				return h.JSON(h.Must(row.OpShow, `{"id":"`+id+`"}`))
			}
			old := h.ID(h.MustYes(n.add, `{`+n.body+`,"meta":["client=acme-carried"]}`))
			if !strings.Contains(show(old), "acme-carried") {
				t.Fatalf("the replaced row has no metadata: %s", show(old))
			}
			carried := h.ID(h.MustYes(n.add, `{`+n.body+`,"supersedes":"`+old+`"}`))
			if !strings.Contains(show(carried), "acme-carried") {
				t.Fatalf("--supersedes did not carry the metadata: %s", show(carried))
			}
			bare := h.ID(h.MustYes(n.add, `{`+n.body+`,"supersedes":"`+carried+`","no_meta":true}`))
			if got := show(bare); strings.Contains(got, "acme-carried") {
				t.Fatalf("--no-meta carried the metadata: %s", got)
			}

			h.MustYes(n.del, `{"id":"`+bare+`","reason":"replaced by the vendor doc"}`)
			if got := h.JSON(h.Must(trash.OpShow, `{"id":"`+bare+`"}`)); !strings.Contains(got, "replaced by the vendor doc") {
				t.Fatalf("delete --reason not saved: %s", got)
			}
		})
	}
}
