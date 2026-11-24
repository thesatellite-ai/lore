package renderhost

// context_stale.go — lore context marks a row stating a fact about code
// whose evidence changed in git after it was verified (devco:D-953, bug
// hunt H-212). Why: lore ask prints `lore context --for-file`'s picks for
// a question that names a file, and they came without the mark search and
// ask's own rows carry, so the stale fact read as current (and the fresh
// row that ranks above it in search was left out as "already printed").
//
//	pickOf ─► contextPick.fact (fact_evidence + fact_verified_at)
//	contextAnswer, runContextHook ─► markStalePicks: one Checker per command
//	  (the hook's git questions run under its own deadline)
//	  └─ stale pick: `stale="evidence changed since …"` on its tag, "[stale: …]" on its line,
//	     Stale + Reverify in --json (lore ask's applies)

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/factstale"
	"github.com/thesatellite-ai/lore/store"
)

// markStalePicks marks every pick whose fact evidence changed after it
// was verified; nothing outside a git repository.
func (h *Host) markStalePicks(ctx context.Context, picks []contextPick) {
	markStale(ctx, picks, h.app.FactChecker, h.now())
}

// markStale marks picks against the checker checker returns (asked once,
// only when a pick names evidence; nil marks nothing), dating the printed
// fix from now.
func markStale(ctx context.Context, picks []contextPick, checker func(context.Context) *factstale.Checker, now time.Time) {
	var chk *factstale.Checker
	for i := range picks {
		p := &picks[i]
		if p.fact == nil {
			continue
		}
		if chk == nil {
			if chk = checker(ctx); chk == nil {
				return
			}
		}
		v := chk.Check(ctx, *p.fact)
		if !v.Stale {
			continue
		}
		st := ""
		if p.source != store.SourceRepo {
			st = string(p.source)
		}
		p.Stale, p.Reverify = v.Note(), factstale.Reverify(p.ID, st, v, now)
		p.full = strings.Replace(p.full, ">\n", fmt.Sprintf(" stale=%q>\n", strings.TrimPrefix(p.Stale, "stale: ")), 1)
		p.line = strings.TrimSuffix(p.line, "\n") + "  [" + p.Stale + "]\n"
		p.Bytes = len(p.full)
	}
}
