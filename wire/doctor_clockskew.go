package wire

// doctor_clockskew.go — `lore doctor` names a large clock skew (devco:U-3).
// A machine whose clock runs ahead stamps its rows and commits in the
// future: its edits outrank everyone's concurrent ones until real time
// catches up. Every machine that reads the store sees those commits, so
// doctor compares the newest commit of each store clone (its branch and
// the remote's tracking copy) with this machine's clock; one stamped more
// than clockSkewLimit ahead is a warning naming the store, the gap and
// the author. Before, docs/team/troubleshooting.md promised the check and
// no doctor had it.

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckClockSkew names the check.
const DoctorCheckClockSkew DoctorCheck = "clock_skew"

// clockSkewLimit is how far ahead of this clock a commit may be stamped
// before doctor names it: past ordinary drift and a commit made a moment
// ago on a machine a few seconds off.
const clockSkewLimit = 5 * time.Minute

// DoctorClockSkew warns for each store clone whose newest commit is
// stamped more than clockSkewLimit ahead of this machine's clock.
func (a *App) DoctorClockSkew(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	now := a.Now()
	seen := map[string]bool{}
	var out []DoctorFinding
	for _, src := range a.Stores() {
		gb := gitOf(st.byName[src])
		if gb == nil || seen[gb.Dir()] {
			continue
		}
		seen[gb.Dir()] = true
		at, author, ok := gb.NewestCommit(ctx)
		if !ok {
			continue
		}
		if f, skewed := clockSkewFinding(src, now, at, author); skewed {
			out = append(out, f)
		}
	}
	return out, nil
}

// clockSkewFinding is the warning of a store whose newest commit (at, by
// author) is stamped more than clockSkewLimit ahead of now.
func clockSkewFinding(src store.Source, now, at time.Time, author string) (DoctorFinding, bool) {
	ahead := at.Sub(now)
	if ahead <= clockSkewLimit {
		return DoctorFinding{}, false
	}
	return DoctorFinding{Check: DoctorCheckClockSkew, Level: DoctorWarning, Store: src,
		Message: "store " + string(src) + ": its newest commit (by " + author + ") is stamped " + ahead.Round(time.Minute).String() + " ahead of this machine's clock; one of the two clocks is off, and rows stamped ahead outrank concurrent edits until real time catches up",
		Fix:     "compare `date -u` here and on " + author + "'s machine, and turn on network time sync on the one that is off"}, true
}
