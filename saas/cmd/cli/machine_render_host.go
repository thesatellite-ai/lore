package main

// machine_render_host.go — the old binary's render records go through the
// 0.1.18 core's machine store (P3-LOCAL-5): `lore render` records each
// render with wire.App.RecordRender, and `render --trend`, `why-context`
// and `support-bundle` read wire.App.RenderHistory, so lore.db's
// render_histories table is no longer written or read (its v0.1.17 rows
// reach the machine store through `lore migrate run`, step machine_local).

import (
	"context"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// coreRecordRender keeps rec through the core (E_INTERNAL when the core is
// not mounted, a programmer error).
func coreRecordRender(ctx context.Context, rec wire.RenderRecord) error {
	if coreApp == nil {
		return errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted")
	}
	_, err := coreApp.RecordRender(ctx, rec)
	return err
}

// coreRenderHistory is the project's render records, oldest first.
func coreRenderHistory(ctx context.Context, projectID string) ([]wire.RenderRecord, error) {
	if coreApp == nil {
		return nil, errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted")
	}
	return coreApp.RenderHistory(ctx, projectID)
}

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
