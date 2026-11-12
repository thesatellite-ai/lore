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
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
)

// DoctorCheckStoreClone is the check's JSON name.
const DoctorCheckStoreClone DoctorCheck = "store_clone"

// DoctorStoreClones reports every store clone that is not whole. It only
// reads.
func (a *App) DoctorStoreClones(context.Context) ([]DoctorFinding, error) {
	found, err := gitbackend.ScanClones(a.opts.Home)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "list the store clones", err)
	}
	var out []DoctorFinding
	for _, c := range found {
		if f, ok := storeCloneFinding(c); ok {
			out = append(out, f)
		}
	}
	return out, nil
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
			fix = "lore store forget " + c.Origin
		}
		return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorNote,
			Message: p.Dir + " is a first clone that was cut short before its files were checked out; lore finishes it the next time it opens the store, or remove it if you no longer use that store",
			Fix:     fix}, true
	case gitbackend.CloneMissing:
		return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorWarning,
			Message: p.Dir + " (your own clone of " + c.Rel + ", recorded by `lore store use`) is gone, so lore cannot read that store; clone it there again, or go back to lore's own clone",
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
	return "https://" + norm
}

// shellQuote single-quotes a path for a fix line a person pastes into a
// POSIX shell (a lore home may hold a space).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
