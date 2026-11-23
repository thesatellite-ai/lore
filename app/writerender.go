package app

// writerender.go — after a write, the private render files of the checkout
// the command runs in are brought up to date with their rows
// (devco:D-921). A must rule or a critical/high hotfix edited, archived,
// added, moved or re-severitied in an internal store reaches the file a
// tool's private import (CLAUDE.local.md) loads at once, not at the next
// `lore render`: an agent read an archived rule and an edited rule's old
// text after an import of a few hundred writes. Only files a render
// already wrote are refreshed and nothing is printed (the write's own
// output, --json included, stays as it was); a failure is one stderr
// warning, never a failed write. `lore render` and `lore sync` keep their
// own refresh; a dry run or --read-only writes nothing.

import (
	"context"
	"os"

	"github.com/thesatellite-ai/lore/op"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// hookWriteRender names the hook in errors and tests.
const hookWriteRender = "write-render"

// writeRenderWarnPrefix starts the stderr line of a refresh that failed.
const writeRenderWarnPrefix = "lore: refresh the private render (run `lore render`): "

// writeRenderSkips are the writes that render on their own.
var writeRenderSkips = map[string]bool{opsrender.OpRun: true, opssync.OpRun: true}

// writeRenderHook refreshes the stale private render files of the checkout
// at wd after every successful write (see the file comment).
func writeRenderHook(rend *lateRender, wd string) hosthooks.Hook {
	return hosthooks.Hook{Name: hookWriteRender, Around: func(ctx context.Context, run *hosthooks.Run, next hosthooks.Next) (op.Result, error) {
		res, err := next(ctx)
		spec := run.Entry.Spec()
		if err != nil || run.WritesNothing() || run.OldRows || spec.Access != op.AccessWrite || writeRenderSkips[spec.Name] || rend.h == nil {
			return res, err
		}
		// .lore/LORE.md as a sync refreshes it: only a stale stamp, and a
		// committed render only while this clone has uncommitted row files
		// (render_auto.go; the pre-commit hook carries it into the commit)
		rend.h.RerenderStaleQuiet(ctx, wd)
		if _, rerr := rend.h.RefreshPrivateStaleIn(ctx, wd); rerr != nil {
			_, _ = os.Stderr.WriteString(writeRenderWarnPrefix + rerr.Error() + "\n") // a write never fails over its render
		}
		return res, nil
	}}
}
