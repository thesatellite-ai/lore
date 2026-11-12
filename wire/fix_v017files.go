package wire

// fix_v017files.go — the "0.1.17 files" step of lore fix (B4-7): lore
// 0.1.17 kept its database of the checkout in .lore/lore.db (plus -wal,
// -shm) and copies of it in .lore/backups/. This lore reads lore.db only
// while migrating (the rows and edits only it held go into .lore/data);
// after that nothing reads either, and they sat in the repo unexplained.
//
//	files there? ─no─▶ current
//	migrate not current (v1 rows, pending, a journal, a Mode B pointer),
//	a file tracked by git, this build cannot read lore.db, or a row of
//	lore.db that no store here holds (not even as a tombstone or a purge)
//	                  ─▶ kept, the step says why
//	else              ─▶ removed (dry: would remove), each file named

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// v017BackupsDir is lore 0.1.17's backups folder of lore.db.
const v017BackupsDir = ".lore/backups"

// v017RepoFiles are the 0.1.17 files of a checkout the step removes.
var v017RepoFiles = []string{v017LoreDB, v017LoreDB + "-wal", v017LoreDB + "-shm", v017BackupsDir}

// oldFilesKeptPrefix starts the step's result when the files stay.
const oldFilesKeptPrefix = "kept: "

// fixOldFiles is the 0.1.17 files step (see the file comment).
func (a *App) fixOldFiles(ctx context.Context, top string, dry bool) opsdoctor.FixStep {
	st := opsdoctor.FixStep{Step: opsdoctor.StepOldFiles, Where: filepath.Join(top, rowfs.LoreDir), Result: opsdoctor.ResultCurrent}
	var there []string
	for _, f := range v017RepoFiles {
		if _, err := os.Lstat(filepath.Join(top, filepath.FromSlash(f))); err == nil {
			there = append(there, f)
		}
	}
	if len(there) == 0 {
		return st
	}
	if why := a.oldFilesNeeded(ctx, top); why != "" {
		st.Result = oldFilesKeptPrefix + strings.Join(there, ", ") + ": " + why
		return st
	}
	st.Changed, st.Detail = true, there
	if dry {
		st.Result = "would remove lore 0.1.17's database and backups: every row they hold is in this repo's stores"
		return st
	}
	for _, f := range there {
		if err := os.RemoveAll(filepath.Join(top, filepath.FromSlash(f))); err != nil {
			st.Failed, st.Result = true, "remove "+f+": "+err.Error()
			return st
		}
	}
	st.Result = "removed lore 0.1.17's database and backups: every row they held is in this repo's stores; nothing reads them"
	return st
}

// oldFilesNeeded is why the 0.1.17 files must stay ("" when they may go).
func (a *App) oldFilesNeeded(ctx context.Context, top string) string {
	if _, modeB := FindModeBPointer(top); modeB {
		return "this repo still points at a shared lore 0.1.17 database"
	}
	if hasV017Rows(top) {
		return "rows here are still in the lore 0.1.17 format; the migrate step brings them over first"
	}
	status, err := a.Status(ctx)
	if err != nil {
		return "the migration status cannot be read (" + err.Error() + ")"
	}
	if len(status.Pending) > 0 || strings.HasPrefix(status.Journal, "interrupted") {
		return "the migration is not finished"
	}
	for _, f := range v017RepoFiles {
		out, gerr := gitRun{dir: top, args: []string{"ls-files", "--", f}}.output(ctx)
		if gerr != nil {
			return "git cannot tell whether " + f + " is committed"
		}
		if strings.TrimSpace(string(out)) != "" {
			return f + " is committed in git; remove it with git rm if the team no longer needs it"
		}
	}
	missing, err := a.v017RowsUnaccounted(ctx, top)
	if err != nil {
		return "lore 0.1.17's database cannot be checked (" + err.Error() + ")"
	}
	if missing > 0 {
		return strconv.Itoa(missing) + " row(s) of lore 0.1.17's database are in no store here"
	}
	edits, err := a.v017Edits(ctx)
	if err != nil {
		return "lore 0.1.17's database cannot be checked (" + err.Error() + ")"
	}
	if len(edits) > 0 {
		return "it holds edits not in .lore/data yet (" + v017EditNames(edits) + "); the migrate step copies them first"
	}
	return ""
}

// v017RowsUnaccounted counts the rows of the checkout's lore.db that no
// attached store holds: a row file, a tombstone or a purge record all
// count (a row deleted or purged since the migration was dealt with).
// An error when this build cannot read the database.
func (a *App) v017RowsUnaccounted(ctx context.Context, top string) (int, error) {
	path := filepath.Join(top, filepath.FromSlash(v017LoreDB))
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return 0, nil // only backups or WAL files are left
	}
	exp, ok := a.opts.ModeB.(RepoDBExporter)
	if !ok {
		return 0, errs.New(errs.Unsupported, "this build has no lore 0.1.17 database reader")
	}
	pid, err := repoMetaProject(top)
	if err != nil {
		return 0, err
	}
	got, err := exp.ExportRepoDB(ctx, path, pid)
	if err != nil {
		return 0, err
	}
	changes, err := modeBChanges(got)
	if err != nil {
		return 0, err
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return 0, err
	}
	missing := 0
	for _, c := range changes {
		_, _, gerr := eng.Get(ctx, engine.Ref{Table: c.Row.Table, ID: c.Row.ID})
		switch {
		case gerr == nil, errs.Has(gerr, errs.Purged), errs.Has(gerr, errs.AmbiguousID):
		case errs.Has(gerr, errs.NotFound):
			missing++
		default:
			return 0, gerr
		}
	}
	return missing, nil
}
