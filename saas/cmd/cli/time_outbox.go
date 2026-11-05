package main

// The run time outbox (TIME_TRACKING 4.3, 11.9): `lore run end|cancel` are
// core operations (ops/run, P3-LOCAL-3) that queue a span whose task's
// store is unreachable; `lore sync` writes what is queued once it reached
// the stores. The queue and its replay live in ops/run (FlushOutbox).

import (
	"context"
	"fmt"
	"os"

	"saas/pkg/aicoder/style"

	opsrun "github.com/thesatellite-ai/lore/ops/run"
)

// flushTimeOutbox writes every queued span whose task is reachable now and
// returns how many were written; a span that failed for another reason is
// named on stderr and stays queued.
func flushTimeOutbox(ctx context.Context) int {
	if coreApp == nil {
		return 0
	}
	f := opsrun.FlushOutbox(ctx, coreApp)
	if f.Failed != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", style.Warn("!"), f.Failed)
	}
	return f.Written
}
