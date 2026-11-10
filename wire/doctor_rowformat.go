package wire

// doctor_rowformat.go — `lore doctor` names row files lore 0.1.17 wrote
// (_v: 1) that `lore migrate run` has not rewritten yet.
//
//	no v1 row of a migrated table           ──▶ nothing
//	v1 rows, repo not migrated (no floor)   ──▶ note: run the migration once
//	                                            everyone runs 0.1.18
//	v1 rows, repo migrated (min_lore_version ──▶ warning: a lore older than
//	set by a migration)                         0.1.18 still writes here
//
// Why: after a migration a _v: 1 file can only come from a machine still on
// 0.1.17 (it commits past its hook, or from before the pull). That machine
// cannot read the migrated files and cannot commit beside them, and the
// rows it writes stay in the old format until someone migrates again.
// migrate status counted them, but nothing a person runs every day said so:
// doctor was healthy (upgrader journey, desk-sees-the-0117-note step).

import (
	"context"
	"fmt"
	"strings"
)

// DoctorCheckRowFormat names the v1-row-files check.
const DoctorCheckRowFormat DoctorCheck = "row_format"

const (
	rowFormatBeforeNote = "row files written by lore 0.1.17 (_v: 1) are not migrated yet (%s)"
	rowFormatBeforeFix  = "lore migrate run, once everyone who writes here runs lore 0.1.18 or later"
	rowFormatAfterWarn  = "row files at _v: 1 after this repo's migration (min_lore_version %s): a lore older than 0.1.18 still writes here, cannot read the migrated rows and cannot commit beside them (%s)"
	rowFormatAfterFix   = "upgrade lore on the machine that wrote them, then lore migrate run"
)

// DoctorRowFormat reports the v1 row files a migration would rewrite.
func (a *App) DoctorRowFormat(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.Status(ctx)
	if err != nil || len(st.Pending) == 0 {
		return nil, err
	}
	pending := strings.Join(st.Pending, ", ")
	if st.MinLoreVersion == "" {
		return []DoctorFinding{{Check: DoctorCheckRowFormat, Level: DoctorNote,
			Message: fmt.Sprintf(rowFormatBeforeNote, pending), Fix: rowFormatBeforeFix}}, nil
	}
	return []DoctorFinding{{Check: DoctorCheckRowFormat, Level: DoctorWarning,
		Message: fmt.Sprintf(rowFormatAfterWarn, st.MinLoreVersion, pending), Fix: rowFormatAfterFix}}, nil
}
