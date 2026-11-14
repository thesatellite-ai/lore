package wire

// doctor_cache_forget.go — a store sync that reached its remote is newer
// evidence than any cached doctor result about that store
// (run:journeys:doctor-cache-stale). Without this, `lore doctor` within 60 s
// of a failed probe still showed "cannot read the store" after `lore sync`
// had just pulled and pushed it, and told the person to fix what was fine.
//
//	lore sync ─▶ SyncStores / syncOneStore ─▶ each store whose report is
//	    clean (reached the remote, nothing refused or held)
//	    ─▶ forgetStoreChecks ─▶ doctor_cache rows of "store <name>" (every
//	       setup) and the App's in-process cache dropped
//	lore doctor ─▶ miss ─▶ probes the store again
//
// Bookkeeping only: a cache that cannot be opened or written leaves the
// rows to age out with the TTL, never fails the sync, and a sync never
// creates machine.db just to empty it.

import (
	"context"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// syncedClean reports whether a sync report proves the store works from
// here: its remote answered and nothing was refused, held or failed.
func syncedClean(r opsstores.Report) bool {
	return !r.Offline && !r.Unattached && r.Refused == 0 && len(r.Problems) == 0 && r.HeldBy == ""
}

// forgetStoreChecks drops the cached doctor results of every store in
// reports that synced cleanly.
func (a *App) forgetStoreChecks(ctx context.Context, reports []opsstores.Report) {
	var targets []string
	for _, r := range reports {
		if r.Store != "" && syncedClean(r) {
			targets = append(targets, opsdoctor.TargetStore(r.Store))
		}
	}
	if len(targets) == 0 {
		return
	}
	if a.doctorCache != nil {
		for _, t := range targets {
			a.doctorCache.ForgetTarget(t)
		}
	}
	if a.opts.Home == "" {
		return
	}
	path := filepath.Join(a.opts.Home, filepath.FromSlash(MachineStoreFile))
	if _, err := os.Stat(path); err != nil {
		return // no machine store file: nothing cached there, and none is made
	}
	db, err := sqlite.OpenDoctorCache(ctx, path)
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()
	for _, t := range targets {
		_ = db.ForgetTarget(ctx, t) // a failed delete ages out with the TTL
	}
}
