package wire

// migrate_repodb.go — a v0.1.17 checkout whose rows live only in its own
// git-ignored .lore/lore.db (owner report 2026-11-11: v0.1.17 with live
// sync off never exported them to .lore/data, so `lore migrate status`
// said "repo 0 rows" and `lore migrate run` imported nothing). migrate
// reads that database as it reads a shared one (Mode B, modeb_v017.go):
//
//	.lore/lore.db (no Mode B pointer) ─▶ the project: .lore/data/_meta.json,
//	   else the database's only project
//	─▶ its rows, the project row and the actors they name, tags folded
//	   into tag_ids (ExportRepoDB, modeBChanges)
//	─▶ every row .lore/data does not hold yet ─▶ written as `_v: 2` into
//	   .lore/data (one batch, journaled with the run), counted by status
//
// A row the live ref also holds is left to MigrateLive (live rows are in
// lore.db too). Once 0.1.18 has written a row here (any `_v: 2` row file)
// the database is not read at all (it is a copy from before,
// and a row deleted or purged since must not come back). The database is opened read-only and never changed or removed: v0.1.17
// may still use it, and a rollback leaves it as the run found it. A row
// .lore/data already holds (any version, a tombstone too) is never
// overwritten: the row file is the newer copy (v0.1.17 exported from the
// database, never back).

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// RepoDBExporter reads a checkout's own v0.1.17 lore.db (V017ModeB does).
type RepoDBExporter interface {
	// ExportRepoDB returns projectID's rows of the lore.db at dbPath
	// ("" = its only project).
	ExportRepoDB(ctx context.Context, dbPath, projectID string) (ModeBExport, error)
}

// repoDBStepPrefix starts each step line of the import.
const repoDBStepPrefix = v017LoreDB + ": "

// repoDBRows lists the rows of the checkout's own lore.db the repo store
// lacks, as `_v: 2` changes (nil when there is no such database, it is a
// Mode B repo, or this build cannot read it).
func (a *App) repoDBRows(ctx context.Context) (repoDB, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return repoDB{}, err
	}
	if _, modeB := FindModeBPointer(top); modeB {
		return repoDB{}, nil // MigrateModeB reads the shared database instead
	}
	path := filepath.Join(top, filepath.FromSlash(v017LoreDB))
	if fi, serr := os.Lstat(path); errors.Is(serr, fs.ErrNotExist) || (serr == nil && !fi.Mode().IsRegular()) {
		return repoDB{}, nil // none, or a link (never followed)
	} else if serr != nil {
		return repoDB{}, errs.Wrap(errs.Internal, "migrate: stat "+v017LoreDB, serr)
	}
	exp, ok := a.opts.ModeB.(RepoDBExporter)
	if !ok {
		return repoDB{}, nil
	}
	st, err := a.state(ctx)
	if err != nil {
		return repoDB{}, err
	}
	b := st.byName[store.SourceRepo]
	if b == nil {
		return repoDB{}, nil
	}
	if moved, merr := holdsCurrentRows(ctx, b); merr != nil || moved {
		return repoDB{}, merr // 0.1.18 wrote here: the old database is stale (see the file comment)
	}
	pid, err := repoMetaProject(top)
	if err != nil {
		return repoDB{}, err
	}
	out := repoDB{top: top, needsMeta: pid == ""}
	got, err := exp.ExportRepoDB(ctx, path, pid)
	if err != nil {
		return repoDB{}, err
	}
	out.project = got.ProjectID
	changes, err := modeBChanges(got)
	if err != nil {
		return repoDB{}, errs.Prefix("migrate: "+v017LoreDB, err)
	}
	// A row v0.1.17 also keeps on the live ref comes from there
	// (MigrateLive, with its live stamp): lore.db holds the same row.
	onLive := map[store.Key]bool{}
	if folder, ferr := repoLiveFolder(ctx, top); ferr == nil {
		if live, lerr := readLiveRows(ctx, top, []string{liveRefLocal, liveRefTracking}, folder, liveTables()); lerr == nil {
			for _, lr := range live {
				onLive[store.Key{Table: lr.row.Table, ID: lr.row.ID}] = true
			}
		}
	}
	for _, c := range changes {
		if onLive[c.Row.Key()] {
			continue
		}
		switch _, gerr := b.Get(ctx, c.Row.Table, c.Row.ID); {
		case errors.Is(gerr, store.ErrNotFound):
			if own, ok := movedOwnerOf(ctx, st, c.Row.Table, c.Row.ID); ok {
				// B1-5: a newer lore moved it away; bringing lore.db's
				// copy back would make a second live copy (migrate_moved.go)
				out.moved = append(out.moved, movedStepPrefix+c.Row.Table.String()+"/"+c.Row.ID.String()+" ("+v017LoreDB+") is in "+string(own.src)+" (moved there by a newer lore); lore.db's copy is not brought back")
				continue
			}
			out.rows = append(out.rows, c)
		case gerr != nil:
			return repoDB{}, gerr
		}
	}
	return out, nil
}

// repoDB is what the checkout's lore.db adds to .lore/data.
type repoDB struct {
	// moved are the step lines of lore.db rows left out because a newer
	// lore moved them to another store (migrate_moved.go).
	moved   []string
	top     string
	project string // the database's project the rows belong to
	// needsMeta: .lore/data/_meta.json is missing, so the import writes
	// it (SeedRepoMeta, as lore init does) to keep the repo on that project.
	needsMeta bool
	rows      []store.Change
}

// holdsCurrentRows reports whether b holds a row in the current format
// (`_v: 2`): 0.1.18 then wrote or migrated this checkout, every row the
// v0.1.17 lore.db had reached .lore/data then, and the database is a stale
// copy that must never bring back a row deleted or purged since
// (the file comment). A v0.1.17 checkout holds only `_v: 1` rows, or none.
func holdsCurrentRows(ctx context.Context, b store.Backend) (bool, error) {
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		return false, err
	}
	return anyCurrent(rows), nil
}

// anyCurrent reports whether one of rows is in the current format.
func anyCurrent(rows []store.Row) bool {
	for _, sr := range rows {
		if r, err := sr.Decode(); err == nil && r.SourceVersion >= row.FormatVersion {
			return true
		}
	}
	return false
}

// repoMetaProject is the project id of root's .lore/data/_meta.json (""
// when there is none: the database's only project is used).
func repoMetaProject(top string) (string, error) {
	p := filepath.Join(top, filepath.FromSlash(rowfs.RepoMetaPath))
	b, err := os.ReadFile(filepath.Clean(p))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errs.Wrap(errs.Internal, "migrate: read .lore/data/"+rowfs.MetaFileName, err)
	}
	var m struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return "", errs.Wrap(errs.InvalidInput, "migrate: .lore/data/"+rowfs.MetaFileName+" is not JSON", err)
	}
	return m.ProjectID, nil
}

// migrateRepoDB is the import step of `lore migrate run` (see the file
// comment): one step line per row, the rows counted; dryRun writes nothing.
func (a *App) migrateRepoDB(ctx context.Context, dryRun bool) ([]string, int, error) {
	got, err := a.repoDBRows(ctx)
	todo := got.rows
	if err != nil || len(todo) == 0 {
		return got.moved, 0, err
	}
	steps := make([]string, 0, len(todo)+1+len(got.moved))
	steps = append(steps, got.moved...)
	meta := filepath.Join(got.top, filepath.FromSlash(rowfs.RepoMetaPath))
	if got.needsMeta {
		steps = append(steps, repoDBStepPrefix+"project "+got.project+" → .lore/data/"+rowfs.MetaFileName)
	}
	for _, c := range todo {
		steps = append(steps, repoDBStepPrefix+c.Row.Table.String()+"/"+c.Row.ID.String()+" → .lore/data")
	}
	if dryRun {
		return steps, len(todo), nil
	}
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	if got.needsMeta {
		pid, perr := ids.Parse[ids.Project](got.project)
		if perr != nil {
			return nil, 0, errs.Wrap(errs.InvalidInput, "migrate: "+v017LoreDB+" names the project "+got.project+", which is not a project id", perr)
		}
		if err := os.MkdirAll(filepath.Dir(meta), cacheDirMode); err != nil {
			return nil, 0, errs.FileIO("create .lore/data", filepath.Dir(meta), err)
		}
		if err := a.SeedRepoMeta(ctx, pid); err != nil { // as lore init writes it
			return nil, 0, err
		}
	}
	if err := st.byName[store.SourceRepo].Write(ctx, todo); err != nil {
		return nil, 0, errs.Prefix("migrate: write the rows of "+v017LoreDB+" into .lore/data", err)
	}
	a.wrote(nil)
	if _, err := st.eng.Refresh(ctx, store.SourceRepo); err != nil {
		return nil, 0, err
	}
	return steps, len(todo), nil
}
