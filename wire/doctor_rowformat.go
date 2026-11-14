package wire

// doctor_rowformat.go — `lore doctor` names row files lore 0.1.17 wrote
// (_v: 1) that `lore migrate run` has not rewritten yet.
//
//	no v1 row of a migrated table           ──▶ nothing
//	v1 rows, repo not migrated (no floor)   ──▶ note: run the migration once
//	                                            everyone runs 0.1.18
//	v1 rows, min_lore_version set, this     ──▶ note: the migration did not
//	checkout's migration journal holds one      finish; run lore fix
//	that did not finish (interrupted, rolled
//	back)
//	v1 rows only of tables a run before T-2  ──▶ note: an earlier migration left
//	left (actors, projects, repos), repo         them; run lore fix --migrate
//	migrated
//	format 1 (_meta.json) but _v: 2 files    ──▶ warning: a reverted migration
//	or the signpost (E-7)                        (formats disagree); lore migrate run
//	v1 rows, repo migrated, every v1 file    ──▶ note: they came from a branch
//	last updated before _meta.json's            made before the migration
//	migrated_at                                 (rebase, merge, cherry-pick);
//	                                            lore fix --migrate
//	v1 rows, repo migrated (min_lore_version ──▶ warning: a lore older than
//	set; finished here, or no journal: a        0.1.18 still writes here
//	teammate's checkout)
//
// The rows are counted in the working tree (migrate status reads the row
// files on disk), so the files a migration upgraded and the person has not
// committed yet count as migrated: lore never commits a code repo, and the
// upgraded files waiting for that commit are not a finding (owner item C2;
// lore fix ends with the commit line instead).
//
// Owner item C2: a migrate that failed part way (after a 0.1.18 repair
// had written its rows and raised min_lore_version) was reported as "a
// lore older than 0.1.18 still writes here", a warning naming the wrong
// cause and the wrong fix; a migration this checkout knows did not finish
// is named as such.
//
// devco K-3: a branch made before the migration and rebased, merged or
// cherry-picked onto it brings its v1 files with it, written before the
// migration by a lore that has since been upgraded; blaming "a lore older
// than 0.1.18" sent the person to upgrade a machine that already runs the
// new lore. Such files are told apart by time: each was last updated
// before the migration (_meta.json migrated_at), while a lore 0.1.17 still
// writing here stamps rows after it.
//
// Why: after a migration a _v: 1 file can only come from a machine still on
// 0.1.17 (it commits past its hook, or from before the pull). That machine
// cannot read the migrated files and cannot commit beside them, and the
// rows it writes stay in the old format until someone migrates again.
// migrate status counted them, but nothing a person runs every day said so:
// doctor was healthy (upgrader journey, desk-sees-the-0117-note step).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckRowFormat names the v1-row-files check.
const DoctorCheckRowFormat DoctorCheck = "row_format"

const (
	rowFormatBeforeNote = "row files written by lore 0.1.17 (_v: 1) are not migrated yet (%s)"
	rowFormatBeforeFix  = "lore migrate run" // fmt1: the one next step doctor names in a 0.1.17-format project
	rowFormatAfterWarn  = "row files at _v: 1 after this repo's migration (min_lore_version %s): a lore older than 0.1.18 still writes here, cannot read the migrated rows and cannot commit beside them (%s)"
	rowFormatAfterFix   = "upgrade lore on the machine that wrote them (lore upgrade), then run lore fix --migrate"
	rowFormatLeftBefore = "row files an earlier lore migrate run left at _v: 1 (%s): this lore's migration rewrites them too"
	rowFormatFromBranch = "row files at _v: 1 (%s) were last written before this repo's migration (%s), so they came from a branch made before it (a rebase, merge or cherry-pick of that branch), not from an older lore"
	rowFormatBranchFix  = "lore fix --migrate" // upgrades exactly those files; commit them after
	rowFormatUnfinished = "row files written by lore 0.1.17 (_v: 1) are not migrated yet (%s); the last migration of this repo did not finish or was rolled back (min_lore_version %s)"
)

// rowFormatMixedOld is the warning of a repo on the lore 0.1.17 format
// that holds `_v: 2` row files (E-7): the usual cause is a `git revert` of
// the migration commit, which puts the v1 files back but keeps the rows
// written after the migration (and a signpost a later commit added).
const rowFormatMixedOld = "this repo is on the lore 0.1.17 format but %d row file(s) are at _v: 2%s, as after a git revert of the migration commit: lore 0.1.17 cannot read them and this lore does not write here until the formats agree (lore migrate rollback, on the machine that ran the migration, is the supported undo)"

// rowFormatMixedFix is the one next step of rowFormatMixedOld: migrating
// again brings every row to one format; going back to 0.1.17 instead
// means reverting the commits that added the _v: 2 files too.
const rowFormatMixedFix = "lore migrate run"

// DoctorRowFormat reports the v1 row files a migration would rewrite.
func (a *App) DoctorRowFormat(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.Status(ctx)
	if err != nil {
		return nil, err
	}
	if f, ok := a.mixedOldFormat(ctx, st); ok {
		return []DoctorFinding{f}, nil
	}
	if len(st.Pending) == 0 {
		return nil, nil
	}
	pending := strings.Join(st.Pending, ", ")
	if st.MinLoreVersion != "" && st.Unfinished {
		return []DoctorFinding{{Check: DoctorCheckRowFormat, Level: DoctorNote,
			Message: fmt.Sprintf(rowFormatUnfinished, pending, st.MinLoreVersion), Fix: rowFormatBeforeFix}}, nil
	}
	if st.MinLoreVersion == "" {
		return []DoctorFinding{{Check: DoctorCheckRowFormat, Level: DoctorNote,
			Message: fmt.Sprintf(rowFormatBeforeNote, pending), Fix: rowFormatBeforeFix}}, nil
	}
	if onlyLeftBefore(st.Pending) {
		return []DoctorFinding{{Check: DoctorCheckRowFormat, Level: DoctorNote,
			Message: fmt.Sprintf(rowFormatLeftBefore, pending), Fix: rowFormatBeforeFix}}, nil
	}
	if at, ok := a.v1BeforeMigration(ctx); ok {
		return []DoctorFinding{{Check: DoctorCheckRowFormat, Level: DoctorWarning,
			Message: fmt.Sprintf(rowFormatFromBranch, pending, at), Fix: rowFormatBranchFix}}, nil
	}
	return []DoctorFinding{{Check: DoctorCheckRowFormat, Level: DoctorWarning,
		Message: fmt.Sprintf(rowFormatAfterWarn, st.MinLoreVersion, pending), Fix: rowFormatAfterFix}}, nil
}

// v1BeforeMigration reports whether every `_v: 1` row file in the repo's
// .lore/data was last updated (updated_at, else created_at) no later than
// the migration recorded in _meta.json (migrated_at), and returns that
// time. False when the time is not recorded, when no v1 file is found, or
// when any v1 file is newer or carries no time: then a lore older than
// 0.1.18 may still write here and the warning says so.
func (a *App) v1BeforeMigration(ctx context.Context) (string, bool) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return "", false
	}
	pf, err := ProjectFormatAt(top)
	if err != nil || pf.MigratedAt == "" {
		return "", false
	}
	migrated, err := time.Parse(time.RFC3339Nano, pf.MigratedAt)
	if err != nil {
		return "", false
	}
	data := filepath.Join(top, rowfs.LoreDir, rowfs.DataDir)
	tables, err := os.ReadDir(data)
	if err != nil {
		return "", false
	}
	found := 0
	for _, t := range tables {
		if !t.IsDir() || strings.HasPrefix(t.Name(), ".") {
			continue
		}
		files, rerr := os.ReadDir(filepath.Join(data, t.Name()))
		if rerr != nil {
			return "", false
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			b, rerr := os.ReadFile(filepath.Join(data, t.Name(), f.Name()))
			if rerr != nil {
				return "", false
			}
			var stamp struct {
				V         int    `json:"_v"`
				CreatedAt string `json:"created_at"`
				UpdatedAt string `json:"updated_at"`
			}
			if json.Unmarshal(b, &stamp) != nil || stamp.V != 1 {
				continue // not a v1 row file: not this check's
			}
			at := stamp.UpdatedAt
			if at == "" {
				at = stamp.CreatedAt
			}
			when, perr := time.Parse(time.RFC3339Nano, at)
			if perr != nil || when.After(migrated) {
				return "", false
			}
			found++
		}
	}
	return pf.MigratedAt, found > 0
}

// onlyLeftBefore reports whether every pending line names a table that a
// migrate run before T-2 left at _v: 1 (in FormatTables, not CoreTables:
// actors, projects, repos, …). Such files in a migrated repo come from that
// older run, not from a lore older than 0.1.18 writing here.
func onlyLeftBefore(pending []string) bool {
	core := CoreTables()
	formats := FormatTables()
	for _, p := range pending {
		t, _, ok := strings.Cut(p, ": ")
		if !ok || slices.Contains(core, row.Table(t)) || !slices.Contains(formats, row.Table(t)) {
			return false
		}
	}
	return len(pending) > 0
}

// mixedOldFormat is the E-7 finding: the repo's _meta.json says the lore
// 0.1.17 format, yet its .lore/data holds `_v: 2` row files (or the
// signpost only a migration writes).
func (a *App) mixedOldFormat(ctx context.Context, st opsmigrate.StatusOut) (DoctorFinding, bool) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return DoctorFinding{}, false
	}
	pf, err := ProjectFormatAt(top)
	if err != nil || pf.Format != rowfs.ProjectFormatV017 {
		return DoctorFinding{}, false
	}
	v2 := 0
	for _, p := range st.Places {
		if p.Place == string(store.SourceRepo) {
			v2 = p.V2
		}
	}
	signpost := ""
	if _, serr := os.Stat(filepath.Join(top, rowfs.LoreDir, rowfs.DataDir, rowfs.SignpostFile)); serr == nil {
		signpost = " and .lore/data/" + rowfs.SignpostFile + " is present"
	}
	if v2 == 0 && signpost == "" {
		return DoctorFinding{}, false
	}
	return DoctorFinding{Check: DoctorCheckRowFormat, Level: DoctorWarning,
		Message: fmt.Sprintf(rowFormatMixedOld, v2, signpost), Fix: rowFormatMixedFix}, true
}
