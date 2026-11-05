// doctor_local.go — doctor's checks of what only this machine holds,
// called from runDoctor (doctor.go):
//
//	doctorTimeOutbox   → wire LocalState time_outbox (TIME 4.3, 11.9)
//	doctorPurgedCopies → lsync.DropPurgedCopies dry run (DELETION 3.10)
//
// Both only read: doctor never writes the outbox or drops a copy.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"saas/pkg/aicoder/lsync"

	"github.com/thesatellite-ai/lore/store/localstate"
	"github.com/thesatellite-ai/lore/wire"
)

// doctorTimeOutbox warns when run time spans wait in this machine's
// time_outbox (their task's store was unreachable at run end): the spans
// exist nowhere else until a write reaches the store, so a lost machine
// loses them. FB-DOC-2: a warning (degraded), with the count, the oldest
// queue time and the command that writes them (`lore sync` flushes the
// outbox). A machine that never queued a span has no state file; doctor
// does not create one just to look.
func doctorTimeOutbox(ctx context.Context, home string, r *doctorReport) {
	if coreApp == nil || home == "" {
		return
	}
	if _, err := os.Stat(wire.LocalStatePath(home)); errors.Is(err, fs.ErrNotExist) {
		return
	}
	ls, err := coreApp.LocalState(ctx)
	if err != nil {
		r.Warnings = append(r.Warnings, "time_outbox: this machine's local state could not be opened: "+err.Error())
		degrade(r)
		return
	}
	defer func() { _ = ls.Close() }() // read only; a close error loses nothing
	entries, err := ls.List(ctx, localstate.TimeOutbox, "")
	if err != nil {
		r.Warnings = append(r.Warnings, "time_outbox: the queued run time could not be read: "+err.Error())
		degrade(r)
		return
	}
	if len(entries) == 0 {
		return
	}
	oldest := entries[0].UpdatedAt
	for _, e := range entries[1:] {
		if e.UpdatedAt.Before(oldest) {
			oldest = e.UpdatedAt
		}
	}
	r.Warnings = append(r.Warnings, fmt.Sprintf(
		"time_outbox: %d run time span(s) wait on this machine only (oldest queued %s ago): their task's store was unreachable; run lore sync to write them",
		len(entries), agoString(time.Since(oldest))))
	degrade(r)
}

// doctorPurgedCopies warns when this machine's safety copies
// (_lore_sync_trash) still hold rows that were purged elsewhere (the
// folder's _purged.json lists them): the purged content survives here.
// FB-DOC-3: a warning (degraded), wording from DELETION 3.10, naming the
// command that drops them; nothing is dropped here (dry run). An unreadable
// _purged.json is a warning too, never "nothing purged".
func doctorPurgedCopies(ctx context.Context, db *sql.DB, dataDir string, r *doctorReport) {
	if dataDir == "" {
		return
	}
	found, err := lsync.DropPurgedCopies(ctx, db, dataDir, true)
	if err != nil {
		r.Warnings = append(r.Warnings, "local copies of purged rows could not be checked: "+err.Error())
		degrade(r)
		return
	}
	if len(found) == 0 {
		return
	}
	r.Warnings = append(r.Warnings, fmt.Sprintf(
		"%d local cop(ies) of purged rows on this machine; drop them with lore trash purge --local --purged --confirm", len(found)))
	degrade(r)
}
