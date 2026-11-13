package wire

// doctor_clones.go — the store_clone check of `lore doctor`: a store clone
// on this machine that lore cannot use as it is (adapters/backend/git
// clonelayout.go: <home>/stores/<owner>/<repo>, a plain git clone, plus
// any own clone recorded by `lore store use`). A whole clone is never
// reported. Doctor itself never deletes a clone folder, with or without
// --fix: the removal is the person's own `lore store forget` (refused
// while the clone holds unpushed work) or, for a folder whose git data is
// gone, a manual rm after copying out what they need.

import (
	"context"
	"maps"
	"slices"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	"github.com/thesatellite-ai/lore/model/errs"
)

// DoctorCheckStoreClone is the check's JSON name.
const DoctorCheckStoreClone DoctorCheck = "store_clone"

// DoctorStoreClones reports every store clone that is not whole. It only
// reads.
func (a *App) DoctorStoreClones(ctx context.Context) ([]DoctorFinding, error) {
	found, err := gitbackend.ScanClones(a.opts.Home)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "list the store clones", err)
	}
	var out []DoctorFinding
	for _, c := range found {
		if f, ok := storeCloneFinding(c); ok {
			out = append(out, f)
			continue
		}
		// B2-26: a whole clone in the middle of the person's git merge or
		// rebase: lore refuses every write and sync there until it ends
		if op, way := gitbackend.PersonGitOpAt(ctx, c.Paths.Dir); op != "" {
			out = append(out, DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorWarning,
				Message: c.Paths.Dir + " is in the middle of a git " + op + "; lore leaves it alone (no write or sync there) until it is finished",
				Fix:     "in " + c.Paths.Dir + ": " + way})
		}
	}
	return append(out, a.strandedCloneFindings(ctx)...), nil
}

// strandedCloneFindings reports a whole clone left at a store's old
// address that holds work the store has not got (B3-15: a set-url by an
// older lore made a new clone and left the old one): no place on this
// machine names its origin any more, so no sync ever sends those commits.
// The fix is set-url run again, which carries the clone to the store's
// address (storeseturl_clone.go).
func (a *App) strandedCloneFindings(ctx context.Context) []DoctorFinding {
	top, terr := a.repoTop(ctx)
	if terr != nil {
		top = ""
	}
	entries := addressEntries(a.opts.Home, top)
	if len(entries) == 0 {
		return nil
	}
	now := map[string]string{} // store id -> an address a place names it at
	for _, e := range entries {
		if e.StoreID != "" && now[e.StoreID] == "" {
			now[e.StoreID] = e.URL
		}
	}
	var out []DoctorFinding
	for _, id := range slices.Sorted(maps.Keys(now)) {
		addr := now[id]
		for _, s := range strandedClones(ctx, a.opts.Home, id, addr) {
			named := slices.ContainsFunc(entries, func(e storeaddr.Entry) bool { return storeaddr.Same(e.URL, s.found.Origin) })
			if named {
				continue // another place still uses that address: its own sync sends the work
			}
			out = append(out, DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorWarning,
				Message: s.found.Paths.Dir + " is a clone of store " + id + " at its old address " + s.found.Origin + " and holds " + strings.Join(s.pending, ", ") + "; the store is now at " + addr + ", so no sync sends them",
				Fix:     "lore store set-url --name " + id + " --url " + addr})
		}
	}
	return out
}

// storeCloneFinding is the finding for one scanned clone; ok is false for
// a whole clone.
func storeCloneFinding(c gitbackend.CloneFound) (DoctorFinding, bool) {
	p := c.Paths
	switch c.State {
	case gitbackend.CloneNoGitData:
		if c.Own {
			return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorWarning,
				Message: p.Dir + " (your own clone, recorded by `lore store use`) has lost its git data (" + p.GitDir + " has no HEAD), so lore cannot read it",
				Fix:     "lore store use --default " + useArgOf(c)}, true
		}
		return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorWarning,
			Message: p.Dir + " is unused: its git data " + p.GitDir + " is broken (no HEAD), so lore cannot read it and a fresh clone needs the folder; copy anything you still need out of " + p.Dir + ", then remove it",
			Fix:     "rm -rf " + shellQuote(p.Dir)}, true
	case gitbackend.CloneNotCheckedOut:
		fix := "rm -rf " + shellQuote(p.Dir)
		if c.Origin != "" {
			fix = "lore store forget --url " + c.Origin
		}
		return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorNote,
			Message: p.Dir + " is a first clone that was cut short before its files were checked out; lore finishes it the next time it opens the store, or remove it if you no longer use that store",
			Fix:     fix}, true
	case gitbackend.CloneMissing:
		return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorWarning,
			Message: p.Dir + " (your own clone of " + repourl.ShownStore(c.Rel) + ", recorded by `lore store use`) is gone, so lore cannot read that store; clone it there again, or go back to lore's own clone",
			Fix:     "lore store use --default " + useArgOf(c)}, true
	case gitbackend.CloneWhole:
	}
	return DoctorFinding{}, false
}

// useArgOf is the store argument of `lore store use --default` for a
// clone: its origin url, else https:// and the record key's host/path (the
// store is matched by host and path only).
func useArgOf(c gitbackend.CloneFound) string {
	if c.Origin != "" {
		return c.Origin
	}
	norm, _, _ := strings.Cut(c.Rel, branchCloneSep)
	if shown := repourl.ShownStore(norm); shown != norm {
		return shown // a local repository: its path (B3-30)
	}
	return "https://" + norm
}

// shellQuote single-quotes a path for a fix line a person pastes into a
// POSIX shell (a lore home may hold a space).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
