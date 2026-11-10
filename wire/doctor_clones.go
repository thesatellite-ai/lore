package wire

// doctor_clones.go — the store_clone check of `lore doctor`: a store clone
// on this machine that lost one of its two halves (adapters/backend/git
// clonelayout.go). <repo>/ and <repo>.git/ side by side are one clone and
// are never reported; only a half that cannot be used as it is, or that
// lore would reuse, is named, with the command that removes it. Doctor
// itself never deletes a clone folder, with or without --fix: the removal
// is the person's own `lore store forget` (refused while the clone holds
// unpushed work) or, for a worktree whose git data is gone, a manual rm
// after copying out what they need.

import (
	"context"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
)

// DoctorCheckStoreClone is the check's JSON name.
const DoctorCheckStoreClone DoctorCheck = "store_clone"

// cloneForgetScheme turns a clone's host/owner/repo back into a url `lore
// store forget` accepts: forget finds the clone from the url's host and
// path only, so the scheme the store was added with does not matter.
const cloneForgetScheme = "https://"

// DoctorStoreClones reports every clone under <home>/clones that is not
// whole. It only reads.
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
	forget := "lore store forget " + cloneForgetScheme + cloneStoreNorm(c.Norm)
	p := c.Paths
	switch c.State {
	case gitbackend.CloneNoBare:
		return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorWarning,
			Message: p.Base + " is unused: its git data " + p.Bare + " is missing, so lore cannot read it and makes a fresh clone instead; copy anything you still need out of " + p.Work + ", then remove the folder",
			Fix:     "rm -rf " + shellQuote(p.Base)}, true
	case gitbackend.CloneNoWork:
		return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorNote,
			Message: p.Bare + " has no worktree beside it (" + p.Work + "); lore reuses it the next time it opens the store, or remove it if you no longer use that store",
			Fix:     forget}, true
	case gitbackend.CloneOrphanWork:
		return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorWarning,
			Message: p.Base + " is unused: its worktree names git data that is gone (" + linkedOrNone(c.LinkedTo) + "), so lore cannot read it; copy anything you still need out of " + p.Work + ", then remove the folder (lore adds a fresh worktree on the next open)",
			Fix:     "rm -rf " + shellQuote(p.Base)}, true
	case gitbackend.CloneMislinked:
		return DoctorFinding{Check: DoctorCheckStoreClone, Level: DoctorWarning,
			Message: p.Work + " belongs to " + linkedOrNone(c.LinkedTo) + ", not to " + p.Bare + " beside it",
			Fix:     "git -C " + shellQuote(p.Bare) + " worktree repair " + shellQuote(p.Work)}, true
	case gitbackend.CloneWhole:
	}
	return DoctorFinding{}, false
}

// cloneStoreNorm is a clone stem's store: a pinned-branch clone
// (<repo>@<branch>) belongs to <repo>, and forget removes those too.
func cloneStoreNorm(norm string) string {
	store, _, _ := strings.Cut(norm, branchCloneSep)
	return store
}

// linkedOrNone names an unreadable worktree link.
func linkedOrNone(s string) string {
	if s == "" {
		return "no readable git data"
	}
	return s
}

// shellQuote single-quotes a path for a fix line a person pastes into a
// POSIX shell (a lore home may hold a space).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
