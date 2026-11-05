package main

// machine_render_host.go — `support-bundle` counts machine-local rows
// (render_history_rows) in the 0.1.18 core's machine store (P3-LOCAL-5);
// the render records themselves are written and read by wire/renderhost.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// coreMachineRowCount counts the live rows of a machine-local table (every
// repo's on this machine); false when the core is not mounted or the
// machine store cannot be read.
func coreMachineRowCount(ctx context.Context, table string) (int, bool) {
	if coreApp == nil {
		return 0, false
	}
	rows, err := coreApp.List(ctx, store.SourceMachine, row.Table(table))
	if err != nil {
		return 0, false
	}
	n := 0
	for _, r := range rows {
		if !r.Deleted {
			n++
		}
	}
	return n, true
}
