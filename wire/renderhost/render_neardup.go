package renderhost

// render_neardup.go — a pinned row shown once when two live rows say the
// same thing (devco D-926: an import and its rewrite a month later both
// stayed live, so every pinned rule rendered twice). Among the rows a
// render would pin, logic/neardup.Within pairs the near-duplicates (the
// newer row's source_ref names the older, the same title or summary or
// first line, or bodies sharing most of their words); the NEWER row of
// each pair is pinned and the older is left out:
//
//	committed render ─▶ a skip "near duplicate" (render --report, why-context --cut)
//	private render   ─▶ the kept block names it: hides-duplicate="<older id>",
//	                    and the render prints one line with the lint command
//	lore context     ─▶ (--for-file, --for-prompt, the hooks) the older row
//	                    is never a pick, whatever its kind
//
// Nothing is archived: lore lint --code near_duplicate lists each pair
// with the command that archives the older row.

import (
	"cmp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/logic/neardup"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// renderFieldSourceRef is a row's provenance pointer (a rewrite names
// the row it rewrote there).
const renderFieldSourceRef = "source_ref"

// pinnedNearDups maps the older row of each near-duplicate pair among rs
// (pinned candidates, or every lore context row) to the newer one that is
// shown instead (nil when there is none).
func pinnedNearDups(rs []row.Row) map[string]string {
	if len(rs) < 2 {
		return nil
	}
	sorted := slices.Clone(rs)
	slices.SortFunc(sorted, func(a, b row.Row) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	items := make([]neardup.Item, len(sorted))
	for i, r := range sorted {
		body := renderField(r, renderFieldBody)
		items[i] = neardup.Item{Table: r.Table.String(), ID: r.ID.String(), Body: body, SourceRef: renderField(r, renderFieldSourceRef),
			Title: neardup.Headline(renderField(r, renderFieldTitle), renderField(r, schema.FieldSummary), body)}
	}
	var out map[string]string
	for _, m := range neardup.Within(items) {
		if out == nil {
			out = map[string]string{}
		}
		if _, done := out[m.Existing.ID]; !done {
			out[m.Existing.ID] = m.New.ID
		}
	}
	// a chain a<b<c: b is hidden by c, so a points at what is shown
	for old, nw := range out {
		for seen := 0; seen < len(out); seen++ {
			next, ok := out[nw]
			if !ok {
				break
			}
			nw = next
		}
		out[old] = nw
	}
	return out
}

// nearDups is pinnedNearDups over the render's candidate rows, each older
// row counted and recorded as a "near duplicate" skip.
func (s *pinSkips) nearDups(cands []renderRow) map[string]string {
	rs := make([]row.Row, len(cands))
	for i, c := range cands {
		rs[i] = c.Row
	}
	dups := pinnedNearDups(rs)
	for _, c := range cands {
		if dups[c.ID.String()] != "" {
			s.NearDuplicates++
			s.IDs = append(s.IDs, reportEntry{ID: c.ID.String(), Reason: contextfit.ReasonNearDuplicate})
		}
	}
	return dups
}

// dropReplacedPins leaves out the pinned rows a successor replaced
// (superseded_by_id set): the committed render never pins them (6.12) and
// the private render pinned both (devco D-926). It returns the rows kept
// and how many it left out.
func dropReplacedPins(rs []row.Row) ([]row.Row, int) {
	n := len(rs)
	rs = slices.DeleteFunc(rs, func(r row.Row) bool { return renderField(r, renderFieldSuperseded) != "" })
	return rs, n - len(rs)
}

// hidesAttr is the private block attribute naming the near-duplicates a
// pinned row is shown for, "" when it hides none.
func hidesAttr(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ` hides-duplicate="` + strings.Join(ids, " ") + `"`
}

// nearDupRenderLine is the render's line for n pinned rows it left out
// because a newer row repeats or replaced them.
func nearDupRenderLine(n int) string {
	return plural.Count(n, "pinned row") + " left out, a newer live row says the same thing or replaced it: lore lint --code near_duplicate lists each pair with the command that archives the older one"
}
