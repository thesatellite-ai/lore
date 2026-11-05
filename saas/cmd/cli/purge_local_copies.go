// purge_local_copies.go — a purge on this machine drops this machine's
// safety copies of the rows it purged (PASS3-A-4, DELETION 3.10).
//
// Why: sync keeps a copy of every row whose file went away in lore.db's
// _lore_sync_trash (`lore trash --local`). A purge removes content for
// good, so leaving that copy behind kept the content on this machine and
// made `lore doctor` warn "local copies of purged rows" right after the
// purge (FB-DOC-3). Copies of rows purged on another machine are not
// touched here: `lore trash purge --local --purged --confirm` drops those
// (OV31), after the person is asked. `lore sync purge`'s own lsync rows
// leave no copy at all (lsync.PurgeArchived).
//
//	engine purge of a repo row ─► Event{Action: purge} ─► coreAuditSink.Record
//	                                  └─► notePurgedThisRun(project root, id)
//	finishSyncSessions (end of every command, after its sync passes,
//	which may still trash the purged row)
//	                                  └─► dropPurgedCopiesThisRun
//	                                        └─► per root: lsync.DropCopies
//
// The end step opens lore.db itself rather than riding a sync session:
// commands outside the core nouns (`lore trash purge`) register none.
// The set lives for one process, like syncSessions: one command runs per
// process.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/model/ids"
)

// purgedThisRun holds, per project root (the folder lore.db resolves
// at, coreAuditSink.projectRoot), the row ids the running command purged.
var purgedThisRun = struct {
	mu     sync.Mutex
	byRoot map[string]map[string]bool
}{byRoot: map[string]map[string]bool{}}

// notePurgedThisRun records ids a purge just wrote in the project at
// root. The engine emits the purge event only after the write succeeded,
// so a failed purge leaves the copies (its content was not removed).
func notePurgedThisRun(root string, purged ...ids.AnyID) {
	purgedThisRun.mu.Lock()
	defer purgedThisRun.mu.Unlock()
	set := purgedThisRun.byRoot[root]
	if set == nil {
		set = map[string]bool{}
		purgedThisRun.byRoot[root] = set
	}
	for _, id := range purged {
		set[id.String()] = true
	}
}

// takePurgedThisRun returns the recorded sets and clears them, so a second
// end-of-command call (doctor runs finishSyncSessions itself) is a no-op.
func takePurgedThisRun() map[string]map[string]bool {
	purgedThisRun.mu.Lock()
	defer purgedThisRun.mu.Unlock()
	out := purgedThisRun.byRoot
	purgedThisRun.byRoot = map[string]map[string]bool{}
	return out
}

// dropPurgedCopiesThisRun drops, in each project's lore.db, the safety
// copies of the ids this command purged there. A failure only warns: the
// purge itself succeeded, and the doctor line plus `lore trash purge
// --local --purged --confirm` still clean up.
func dropPurgedCopiesThisRun(ctx context.Context) {
	for root, set := range takePurgedThisRun() {
		if err := dropCopiesAt(ctx, root, set); err != nil {
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+"drop local copies of purged rows: "+err.Error()+
				" (lore trash purge --local --purged --confirm drops them)"))
		}
	}
}

// dropCopiesAt opens root's lore.db and drops the copies of set.
func dropCopiesAt(ctx context.Context, root string, set map[string]bool) error {
	db, err := openCoreAuditDBAt(ctx, root)
	if err != nil {
		return err
	}
	_, err = lsync.DropCopies(ctx, db, set, false)
	return errors.Join(err, db.Close())
}

// withoutPurgedThisRun is rep minus the "trashed rows" this command purged:
// the end-of-command pass trashes a purged row's lore.db copy and
// dropPurgedCopiesThisRun then drops that copy, so reporting it as kept
// in `lore trash --local` would be wrong. rep.Deleted holds row file
// paths (<table>/<id>.<ext>); the id is the file name without extension.
func withoutPurgedThisRun(rep lsync.Report) lsync.Report {
	purgedThisRun.mu.Lock()
	defer purgedThisRun.mu.Unlock()
	if len(purgedThisRun.byRoot) == 0 || len(rep.Deleted) == 0 {
		return rep
	}
	kept := make([]string, 0, len(rep.Deleted))
	for _, rel := range rep.Deleted {
		id := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		purged := false
		for _, set := range purgedThisRun.byRoot {
			purged = purged || set[id]
		}
		if !purged {
			kept = append(kept, rel)
		}
	}
	rep.Deleted = kept
	return rep
}
