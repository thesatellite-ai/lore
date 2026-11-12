package wire

// doctor_storefiles.go — the store_files check of `lore doctor` (plain
// files round, brief 2.5, 2.1): a store clone is a folder a person may
// open and change, so doctor says what in it is not (yet) lore's:
//
//	a file or folder that is not a row (notes.txt, a stray folder)
//	    ─▶ a note naming it; lore never deletes it and never reads it
//	a row file changed by hand and not committed (the pick-up refused it,
//	or it sits in a folder no command opened yet)
//	    ─▶ a warning naming the file and how to settle it
//
// It only reads: the next lore command picks valid hand edits up itself.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
)

// DoctorCheckStoreFiles is the check's JSON name.
const DoctorCheckStoreFiles DoctorCheck = "store_files"

// DoctorStoreFiles reports, per attached store clone, the files that are
// not lore rows and the row files changed by hand but not committed.
func (a *App) DoctorStoreFiles(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []DoctorFinding
	for _, src := range a.Stores() {
		b, ok := concrete(st.byName[src]).(*git.Backend)
		if !ok || !b.CloneMode() || seen[b.Dir()] {
			continue
		}
		seen[b.Dir()] = true
		rep, err := b.HandChanges(ctx)
		if err != nil {
			return nil, err
		}
		if len(rep.Strays) > 0 {
			out = append(out, DoctorFinding{Check: DoctorCheckStoreFiles, Level: DoctorNote, Store: src,
				Message: "store " + string(src) + ": " + b.Dir() + " holds files that are not lore rows: " + strings.Join(rep.Strays, ", ") + " (lore never deletes or reads them)",
				Fix:     "keep them, move them out of the store clone, or commit them yourself (git -C " + b.Dir() + " add <file> && git commit) if the team should have them"})
		}
		for _, c := range rep.Rows {
			out = append(out, DoctorFinding{Check: DoctorCheckStoreFiles, Level: DoctorWarning, Store: src,
				Message: "store " + string(src) + ": " + c.Abs + " was " + c.Kind.String() + " by hand and is not committed yet",
				Fix:     "run any lore command (it picks the change up, or says file:line why not); or put it back: git -C " + b.Dir() + " checkout HEAD -- " + c.Path})
		}
	}
	return out, nil
}
