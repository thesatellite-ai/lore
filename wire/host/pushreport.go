package host

// pushreport.go — PushReportHook: every org write's push (org link,
// unlink, org file; adapters/orgcfg commitStore) prints one line naming
// the store, the commit and the branch on standard error (no hidden
// network writes, plain files sweep 3.3). Store row pushes print from
// wire's storePush (wire/pushreport.go) with the same wording.

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire"
)

// HookPushReport is PushReportHook's name.
const HookPushReport = "push-report"

// PushReportHook reports org store pushes of every operation to
// standard error.
func PushReportHook() Hook { return pushReportHook(os.Stderr) }

// pushReportHook reports to w (tests pass a buffer).
func pushReportHook(w io.Writer) Hook {
	return Hook{Name: HookPushReport, Around: func(ctx context.Context, _ *Run, next Next) (op.Result, error) {
		return next(orgcfg.WithPushReporter(ctx, func(p orgcfg.StorePush) {
			_, _ = fmt.Fprintln(w, wire.StorePushLine(p.Store, p.Commit, p.Branch))
		}))
	}}
}
