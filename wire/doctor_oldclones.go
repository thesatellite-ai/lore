package wire

// doctor_oldclones.go — `lore doctor` on the store clones an earlier
// 0.1.18 build left in ~/.lore/clones and ~/.lore/clones-v017 (plain files
// round, brief 1.6). lore moves them itself (oldlayout.go); only lore fix
// deletes them (fixproject.go), the ones this check proves safe; this check says, per old clone, whether everything in it
// is safely in the new clone (carried or pushed) and then names the exact
// command that removes it, or what still keeps it.
//
// Nothing in this lore writes under ~/.lore/clones or ~/.lore/clones-v017
// (the first-run move only reads them; every clone is opened through
// git.CloneDirFor under ~/.lore/stores). So an old clone whose files are
// newer than the move record (state/layout-move.json) was written after
// the move by another lore binary on the machine (v0.1.17 kept one bare
// clone per store at clones/<host>/<owner>/<repo>.git; an earlier 0.1.18
// build kept clones/<host>/<owner>/<repo> + .git). doctor then names that
// writer (owner item 13) instead of only offering rm -rf, which the old
// binary would undo on its next run.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
)

// DoctorCheckOldClones names the doctor check.
const DoctorCheckOldClones DoctorCheck = "old_clones"

// DoctorOldClones reports every old clone and old org link clone.
func (a *App) DoctorOldClones(ctx context.Context) ([]DoctorFinding, error) {
	home := a.opts.Home
	found, err := git.ScanOldClones(home)
	if err != nil {
		return nil, err
	}
	var out []DoctorFinding
	moved, hasMove := moveRecordTime(home)
	var fresh []string
	for _, o := range found {
		if hasMove && newestOf(o.Base, o.Bare, filepath.Join(o.Bare, "FETCH_HEAD"), filepath.Join(o.Bare, "HEAD"), filepath.Join(o.Bare, "refs")).After(moved) {
			fresh = append(fresh, o.Bare)
		}
	}
	for _, o := range found {
		if !o.Whole {
			out = append(out, DoctorFinding{Check: DoctorCheckOldClones, Level: DoctorNote,
				Message: o.Base + " is half of an old store clone (its bare clone or worktree is gone), so lore cannot move it",
				Fix:     "check it holds nothing you need (git -C " + o.Bare + " log --branches --not --remotes), then " + o.RemoveCommand()})
			continue
		}
		e, ok, why := git.MovedSafely(ctx, home, o)
		if !ok {
			out = append(out, DoctorFinding{Check: DoctorCheckOldClones, Level: DoctorWarning,
				Message: "old store clone " + o.Base + " still holds work the new layout does not have: " + why,
				Fix:     "run any lore command so lore moves it (it prints why when it cannot); keep the folder until this check says it may go"})
			continue
		}
		msg := "old store clone " + o.Base + " is left from an earlier build; everything in it is in " + e.To
		if e.To == "" {
			msg = "old store clone " + o.Base + " is left from an earlier build; it held no commit"
		}
		out = append(out, DoctorFinding{Check: DoctorCheckOldClones, Level: DoctorNote, Message: msg, Fix: o.RemoveCommand()})
	}
	v017, err := git.ScanOldOrgClones(home)
	if err != nil {
		return nil, err
	}
	for _, p := range v017 {
		if hasMove && newestOf(p, filepath.Join(p, "FETCH_HEAD"), filepath.Join(p, "HEAD"), filepath.Join(p, "refs")).After(moved) {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) > 0 {
		out = append([]DoctorFinding{freshOldCloneFinding(home, fresh, moved)}, out...)
	}
	for _, p := range v017 {
		out = append(out, DoctorFinding{Check: DoctorCheckOldClones, Level: DoctorNote,
			Message: p + " is an org link clone of an earlier build; this lore no longer uses it (the org link code uses the store clone)",
			Fix:     "rm -rf " + p})
	}
	return out, nil
}

// freshOldCloneFinding is the warning for old-layout clones written after
// the move: this lore never writes there, so another lore binary does.
func freshOldCloneFinding(home string, paths []string, moved time.Time) DoctorFinding {
	self := "this one is unknown"
	if exe, err := os.Executable(); err == nil {
		self = "this one is " + exe
	}
	msg := paths[0]
	if len(paths) > 1 {
		msg += " (and " + strconv.Itoa(len(paths)-1) + " more)"
	}
	return DoctorFinding{Check: DoctorCheckOldClones, Level: DoctorWarning,
		Message: msg + " was written after the move to " + filepath.Join(home, git.StoresDir) + " (" + moved.UTC().Format(time.RFC3339) + "); this lore never writes there, so an older lore binary on this machine (" + self + ") still writes here",
		Fix:     "find it with `which -a lore` (and the lore your agent hooks run), replace or remove the old one, then remove the folder doctor names below"}
}

// moveRecordTime is when the first-run move last wrote its record.
func moveRecordTime(home string) (time.Time, bool) {
	fi, err := os.Stat(git.MoveRecordPath(home))
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// newestOf is the newest modification time of the paths that exist.
func newestOf(paths ...string) time.Time {
	var t time.Time
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}
