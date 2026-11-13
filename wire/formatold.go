package wire

// formatold.go — decision D13 (round "formatpin"): a project is on the
// format its committed rows and .lore/data/_meta.json say. In a project on
// an older format (lore 0.1.17's) this lore reads normally but never
// writes a newer-format row into it: every write to the project's own
// rows refuses with one line, whatever path it takes (a command, a git
// hook, a sync, a live landing); writes to stores already on the new
// format (the personal store, a migrated org store) still work. Only
// `lore migrate run` raises the format (opsmigrate.InRun lets its own
// writes through). The first write's auto-migrate is gone.
//
//	open ─▶ project on an older format? ─▶ the repo backend is wrapped in
//	        formatGuard: Write refuses with FormatOldErr, Read/Get pass

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

// ProjectFormatOld reports whether the project at top is on an older
// format than this lore writes (ProjectFormatAt + OlderFormat, the one
// reading of a project's format). An unreadable _meta.json counts as old:
// nothing is written into a project whose format lore cannot tell.
func ProjectFormatOld(top string) bool {
	pf, err := ProjectFormatAt(top)
	return err != nil || OlderFormat(pf)
}

// ProjectWriteRefusal is the refusal of a write into the project at top,
// nil when its format allows one. A _meta.json that cannot be read refuses
// with its own reason (formatgate.Unreadable, E_OLD_LORE: fail closed,
// fix the file), never as "on the 0.1.17 format": lore cannot tell the
// format, so telling the person to run lore migrate run would be a guess.
// A project on an older format refuses with FormatOldErr (D13).
func ProjectWriteRefusal(top string) error {
	pf, err := ProjectFormatAt(top)
	if err != nil {
		return err
	}
	if OlderFormat(pf) {
		return FormatOldErr(top)
	}
	return nil
}

// FormatOldErr is the one-line refusal of a write in the project at top
// (D13).
func FormatOldErr(top string) error {
	name := filepath.Base(top)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "this project"
	}
	return errs.New(errs.FormatOld, name+" is on the lore 0.1.17 format; run lore migrate run to upgrade it (teammates need lore 0.1.18)")
}

// formatGuard is the repo backend of a project on an older format: reads
// pass, every write refuses (FormatOldErr) unless the write is lore
// migrate run's own.
type formatGuard struct {
	store.Backend
	top string
}

// Write implements store.Backend.
func (g formatGuard) Write(ctx context.Context, changes []store.Change) error {
	if len(changes) == 0 || opsmigrate.InRun(ctx) {
		return g.Backend.Write(ctx, changes)
	}
	if err := ProjectWriteRefusal(g.top); err != nil {
		return err
	}
	return FormatOldErr(g.top) // wrapped at open as old: stay closed for this run
}

// Unwrap implements Unwrapper.
func (g formatGuard) Unwrap() store.Backend { return g.Backend }

// StoreFormatOldErr is the one-line refusal of a write into a store still
// on lore 0.1.17's layout (decision D16: only lore migrate run raises it).
func StoreFormatOldErr(name string) error {
	return errs.New(errs.FormatOld, "store "+name+" is on the lore 0.1.17 layout; run lore migrate run to upgrade it (teammates need lore 0.1.18)")
}

// storeFormatGuard is an attached store's backend while the store is on
// lore 0.1.17's layout (marker v1): reads pass, writes refuse
// (StoreFormatOldErr) unless they are lore migrate run's own.
type storeFormatGuard struct {
	store.Backend
	name string
}

// Write implements store.Backend.
func (g storeFormatGuard) Write(ctx context.Context, changes []store.Change) error {
	if len(changes) == 0 || opsmigrate.InRun(ctx) {
		return g.Backend.Write(ctx, changes)
	}
	return StoreFormatOldErr(g.name)
}

// Unwrap implements Unwrapper.
func (g storeFormatGuard) Unwrap() store.Backend { return g.Backend }

// storeOnOldLayout reports an attached store whose marker is lore 0.1.17's
// (below rowfs.MarkerFirstStores).
func storeOnOldLayout(b store.Backend) bool {
	m, ok := concrete(b).(markerBackend)
	return ok && m.Marker().Version != 0 && m.Marker().Version < rowfs.MarkerFirstStores
}
