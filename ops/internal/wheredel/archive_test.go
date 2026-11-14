package wheredel_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/batch"
	"github.com/thesatellite-ai/lore/ops/decision"
	"github.com/thesatellite-ai/lore/ops/hotfix"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/pattern"
	"github.com/thesatellite-ai/lore/ops/playbook"
	"github.com/thesatellite-ai/lore/ops/prompt"
	"github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/ops/rule"
	"github.com/thesatellite-ai/lore/ops/snapshot"
)

// archiveNoun is one noun with `archive`: add makes a row whose body
// holds word.
type archiveNoun struct {
	name  string
	add   string
	addIn func(word string) string
}

func titled(w string) string { return `{"title":"` + w + `","body":"` + w + `"}` }

var archiveNouns = []archiveNoun{
	{"memory", memory.OpAdd, func(w string) string { return `{"body":"` + w + `"}` }},
	{"rule", rule.OpAdd, func(w string) string { return `{"body":"` + w + `","severity":"should"}` }},
	{"decision", decision.OpAdd, titled},
	{"hotfix", hotfix.OpAdd, titled},
	{"pattern", pattern.OpAdd, titled},
	{"prompt", prompt.OpAdd, titled},
	{"playbook", playbook.OpAdd, titled},
	{"snapshot", snapshot.OpAdd, titled},
}

// archived reports whether row id has archived_at set.
func archived(h *opstest.H, id string) bool {
	return strings.Contains(h.JSON(h.Must(row.OpShow, `{"id":"`+id+`"}`)), `"archived_at":"2`)
}

// TestArchiveWhereMatrix (bug hunt G-7): per noun that archives, --dry-run
// counts and names the matches and writes nothing; the filter refusals are
// E_INVALID_INPUT; the bulk form archives exactly the live matches in one
// batch; a second run matches nothing; `lore batch undo` puts every row
// back. Fails without the fix: archive took only an id.
func TestArchiveWhereMatrix(t *testing.T) {
	for _, n := range archiveNouns {
		t.Run(n.name, func(t *testing.T) {
			h := opstest.New(t)
			op := n.name + ".archive"
			a := h.ID(h.MustYes(n.add, n.addIn("month one alpha")))
			b := h.ID(h.MustYes(n.add, n.addIn("month one beta")))
			keep := h.ID(h.MustYes(n.add, n.addIn("month two gamma")))
			where := `"where":["body~=month one"]`

			dry := opstest.Text(h.Must(op, `{`+where+`,"dry_run":true}`))
			if !strings.HasPrefix(dry, "would archive 2 "+n.name+"(s):") || !strings.Contains(dry, a) || !strings.Contains(dry, b) || strings.Contains(dry, keep) {
				t.Fatalf("dry run: %q", dry)
			}
			for _, in := range []string{
				`{"id":"` + a + `",` + where + `}`, // both forms
				`{"dry_run":true}`,                 // --dry-run without --where
				`{"where":["nosuchkey=1"]}`,        // unknown key
				`{}`,                               // neither
			} {
				h.Fails(op, in, errs.InvalidInput)
			}
			for _, id := range []string{a, b, keep} {
				if archived(h, id) {
					t.Fatalf("a dry run or a refusal archived %s", id)
				}
			}

			res := h.MustYes(op, `{`+where+`}`)
			out := opstest.Text(res)
			bat := batchID.FindString(out)
			if !strings.HasPrefix(out, "archived 2 "+n.name+"(s)") || bat == "" || !strings.Contains(out, "lore batch undo --id "+bat) {
				t.Fatalf("archive --where: %q", out)
			}
			if js := h.JSON(res); !strings.Contains(js, `"rows":[`) || !strings.Contains(js, a) || !strings.Contains(js, b) {
				t.Fatalf("--json names no rows: %s", js)
			}
			if !archived(h, a) || !archived(h, b) || archived(h, keep) {
				t.Fatal("archive --where did not archive exactly the matches")
			}
			if again := opstest.Text(h.MustYes(op, `{`+where+`}`)); !strings.HasPrefix(again, "no live "+n.name+" matches") {
				t.Fatalf("an archived row matched again: %q", again)
			}
			h.MustYes(batch.OpUndo, `{"id":"`+bat+`"}`)
			if archived(h, a) || archived(h, b) {
				t.Fatal("batch undo did not bring the rows back")
			}
			// the one-id form still works
			if one := opstest.Text(h.MustYes(op, `{"id":"`+keep+`"}`)); !strings.Contains(one, "archived") || !archived(h, keep) {
				t.Fatalf("archive --id: %q", one)
			}
		})
	}
}
