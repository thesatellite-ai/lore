package app

// mergedcheck.go — F9's lazy done → merged check (wire App.MergedCheck)
// before the views that show merge state, as the old binary ran
// lazyMergedCheck inside task list, task next, inbox and mission status.
// It is an Around hook placed after the live rows hook, so it runs after
// the command's live fetch and its stamp rides the same live push. A
// read-only run (--read-only, LORE_READ_ONLY=1) and `lore api` (no CLI
// command) check nothing.

import (
	"context"

	"github.com/thesatellite-ai/lore/op"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// hookMergedCheck names the hook in errors and tests.
const hookMergedCheck = "merged-check"

// mergedCheckOps are the views that run the check first.
var mergedCheckOps = map[string]bool{
	opstask.OpList: true, opscoord.OpTaskNext: true, opscoord.OpInbox: true, opscoord.OpMissionStatus: true,
}

// mergedCheckHook runs check before every operation in mergedCheckOps.
// check never fails the command (it prints its own warnings).
func mergedCheckHook(check func(context.Context)) hosthooks.Hook {
	return hosthooks.Hook{Name: hookMergedCheck, Around: func(ctx context.Context, run *hosthooks.Run, next hosthooks.Next) (op.Result, error) {
		if run.Cmd != nil && !run.ReadOnly && mergedCheckOps[run.Entry.Spec().Name] {
			check(ctx)
		}
		return next(ctx)
	}}
}
