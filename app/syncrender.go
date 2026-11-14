package app

// syncrender.go — what `lore sync` does after a successful run on the
// render side, as the old binary's SyncRun did (CONFIG_GUIDE, CONTEXT_BUDGET
// 6.2): the private per-store renders are rebuilt (quiet; a failure is a
// warning, never a failed sync; later:312), and one `lore scope move` hint
// per folder scope whose folder git saw renamed is added to the report
// (never run; later:472). An Around hook on sync.run only; the hints
// edit the human text, never the --json data.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/op"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// syncBrought reports whether a `lore sync` result received or merged any
// row (a store or the repo pass) or its live pass moved rows (brought a
// teammate's live writes in, sent this clone's, landed some): only then may
// the sync rewrite a committed LORE.md (renderhost.RenderHeld; B-8). A result of another
// shape answers true, as before.
func syncBrought(res op.Result) bool {
	v := res.Value()
	if !v.IsValid() || !v.CanInterface() {
		return true
	}
	out, ok := v.Interface().(opssync.RunOut)
	if !ok {
		return true
	}
	d := out.Data
	if d.LiveMoved || len(d.Imported) > 0 || len(d.Deleted) > 0 || len(d.Merged) > 0 {
		return true
	}
	for _, s := range d.Stores {
		if s.Received > 0 || s.Merged > 0 {
			return true
		}
	}
	return false
}

// hookSyncRender names the hook in errors and tests.
const hookSyncRender = "sync-render"

// syncHintPrefix starts each scope rename hint line of the sync report.
const syncHintPrefix = "· "

// syncRenderHook runs after a successful `lore sync` (not on --read-only):
// it re-renders .lore/LORE.md when it is stale (render_stamp.go) and the
// private context for the checkout
// at wd and appends the scope rename hints to the report text.
func syncRenderHook(rend *lateRender, wd string) hosthooks.Hook {
	return hosthooks.Hook{Name: hookSyncRender, Around: func(ctx context.Context, run *hosthooks.Run, next hosthooks.Next) (op.Result, error) {
		res, err := next(ctx)
		if err != nil || run.ReadOnly || run.OldRows || run.Entry.Spec().Name != opssync.OpRun || rend.h == nil {
			return res, err
		}
		// SOURCE_OF_TRUTH rows 10, 11: LORE.md when its stamp no longer
		// matches its rows, then the private files and import lines.
		rend.h.RerenderStaleQuiet(ctx, wd, syncBrought(res))
		rend.h.RerenderPrivateQuiet(ctx, wd)
		hints := rend.h.ScopeRenameHints(ctx, wd)
		if len(hints) == 0 {
			return res, nil
		}
		return res.WithTextEdit(func(s string) string {
			var b strings.Builder
			b.WriteString(s)
			if s != "" && !strings.HasSuffix(s, "\n") {
				b.WriteString("\n")
			}
			for _, h := range hints {
				b.WriteString(syncHintPrefix + h + "\n")
			}
			return b.String()
		}), nil
	}}
}
