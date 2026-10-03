package lsync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// bootstrap is the first pass on a DB whose project has no .lore/data yet
// (§12.3): back up lore.db, export every synced row, and pin the project
// identity in _meta.json. It never modifies DB rows.
func (e *engine) bootstrap(ctx context.Context) error {
	if err := e.backup(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(e.o.DataDir, dataDirMode); err != nil {
		return fmt.Errorf("lsync: create data dir: %w", err)
	}
	e.bases = map[string]baseEntry{}
	for _, name := range e.reg.Names() {
		if e.present[name] == nil {
			continue
		}
		t, _ := e.reg.Table(name)
		idsList, err := listRowIDs(ctx, e.tx, t)
		if err != nil {
			return err
		}
		cols := liveColumns(t, e.present)
		for _, id := range idsList {
			doc, err := readRowDoc(ctx, e.tx, t, cols, id)
			if err != nil {
				return err
			}
			if err := e.exportDoc(ctx, rowKey{name, id}, t, doc); err != nil {
				return err
			}
		}
	}
	pid, err := e.primaryProjectID(ctx)
	if err != nil {
		return err
	}
	if err := writeProjectMeta(e.o.DataDir, ProjectMeta{ProjectID: pid}); err != nil {
		return err
	}
	if err := e.drain(ctx, nil); err != nil {
		return err
	}
	if err := setMeta(ctx, e.tx, metaBaselined, metaFlagSet); err != nil {
		return err
	}
	return e.countPending(ctx)
}

// primaryProjectID picks the project _meta.json pins. Mode A DBs hold one
// project; if several exist the oldest (smallest UUIDv7 id) is chosen and a
// warning names the others.
func (e *engine) primaryProjectID(ctx context.Context) (string, error) {
	t, ok := e.reg.Table(projectsTable)
	if !ok || e.present[projectsTable] == nil {
		return "", nil
	}
	idsList, err := listRowIDs(ctx, e.tx, t)
	if err != nil {
		return "", err
	}
	if len(idsList) == 0 {
		return "", nil
	}
	if len(idsList) > 1 {
		e.rep.Warnings = append(e.rep.Warnings, fmt.Sprintf(
			"lore.db holds %d projects; _meta.json pins %s (others: %s)", len(idsList), idsList[0], strings.Join(idsList[1:], ", ")))
	}
	return idsList[0], nil
}

func (e *engine) backup(ctx context.Context) error {
	if e.o.Backup == nil {
		return nil
	}
	p, err := e.o.Backup(ctx)
	if err != nil {
		return fmt.Errorf("lsync: backup lore.db before %s: %w", e.rep.Pass, err)
	}
	e.rep.BackupPath = p
	return nil
}

// adopt is the first pass on a DB that has never synced, in a checkout
// that already has .lore/data (§12.3): a teammate bootstrapped and this
// clone pulled it, or `lore restore` reset the sync state (E43).
//
// Per row (PreferNewest, the default for clones and pulls): only in files →
// import; only in the DB → export (this clone's unshared knowledge; purged
// ids are trashed instead, E47); in both and different → newest updated_at
// wins and the losing version is kept as a conflict copy.
//
// After `lore restore` the preference is explicit and is a ROLLBACK to one
// side: PreferDB removes files whose rows are not in the restored DB;
// PreferFiles trashes DB rows that have no file. Clashing rows follow the
// preference too, with the other version kept as a conflict copy.
func (e *engine) adopt(ctx context.Context) error {
	if err := e.backup(ctx); err != nil {
		return err
	}
	meta, err := ReadProjectMeta(e.o.DataDir)
	if err != nil {
		return fmt.Errorf("lsync: cannot adopt: %w", err)
	}
	if err := e.adoptProjectID(ctx, meta.ProjectID); err != nil {
		return err
	}
	if err := e.loadScanAndBases(ctx); err != nil {
		return err
	}
	purged, err := readPurged(e.o.DataDir)
	if err != nil {
		return err
	}
	preferStr, _, err := getMeta(ctx, e.tx, metaAdoptPrefer)
	if err != nil {
		return err
	}
	prefer := PreferMode(preferStr)

	keys := map[rowKey]bool{}
	for rel := range e.scan.files {
		table, id, _ := parseRelPath(rel)
		keys[rowKey{table, id}] = true
	}
	for _, name := range e.reg.Names() {
		if e.present[name] == nil {
			continue
		}
		t, _ := e.reg.Table(name)
		idsList, err := listRowIDs(ctx, e.tx, t)
		if err != nil {
			return err
		}
		for _, id := range idsList {
			keys[rowKey{name, id}] = true
		}
	}
	for _, k := range sortedKeys(keys) {
		if err := e.adoptOne(ctx, k, purged, prefer); err != nil {
			return err
		}
	}
	if err := e.drain(ctx, nil); err != nil {
		return err
	}
	if err := setMeta(ctx, e.tx, metaBaselined, metaFlagSet); err != nil {
		return err
	}
	if err := deleteMeta(ctx, e.tx, metaAdoptPrefer); err != nil {
		return err
	}
	e.warnMultipleProjects()
	return e.countPending(ctx)
}

// adoptProjectID rewrites every local project id to the one _meta.json pins
// (E1), so this clone's rows hang off the shared project.
func (e *engine) adoptProjectID(ctx context.Context, shared string) error {
	if shared == "" || e.present[projectsTable] == nil {
		return nil
	}
	t, _ := e.reg.Table(projectsTable)
	local, err := listRowIDs(ctx, e.tx, t)
	if err != nil {
		return err
	}
	for _, pid := range local {
		if pid == shared {
			continue
		}
		if err := rewriteIDEverywhere(ctx, e.tx, pid, shared); err != nil {
			return err
		}
		e.rep.Warnings = append(e.rep.Warnings, fmt.Sprintf("local project %s adopted as shared project %s", pid, shared))
	}
	return nil
}

func (e *engine) adoptOne(ctx context.Context, k rowKey, purged map[string]bool, prefer PreferMode) error {
	t, ok := e.reg.Table(k.table)
	if !ok {
		return nil
	}
	rel := k.rel()
	var fileDoc map[string]any
	var fc fileChange
	if st, onDisk := e.scan.files[rel]; onDisk {
		b, err := readRowFile(filepath.Join(e.o.DataDir, filepath.FromSlash(rel)))
		if err != nil {
			e.recordError(ctx, rel, ErrKindInvalid, err.Error())
			return nil
		}
		doc, err := e.parseRowFile(ctx, rel, k, b)
		if err != nil {
			return nil
		}
		fileDoc, fc = doc, fileChange{bytes: b, stat: st}
	}
	dbDoc, err := readRowDoc(ctx, e.tx, t, liveColumns(t, e.present), k.id)
	if err != nil {
		return err
	}
	switch {
	case fileDoc == nil && dbDoc == nil:
		return e.clearDirty(ctx, k)
	case fileDoc != nil && dbDoc == nil:
		if prefer == PreferDB {
			// A restore that prefers the DB is a rollback: rows added after
			// the backup must go, or the restore would be silently undone.
			e.rep.Conflicts = append(e.rep.Conflicts, ConflictNote{Path: rel, Kept: KeptDB, Why: "not in the restored DB; file removed"})
			return e.exportDoc(ctx, k, t, nil)
		}
		return e.applyFile(ctx, k, t, fileDoc, fc)
	case fileDoc == nil:
		if purged[k.id] {
			return e.deleteRowToTrash(ctx, k, t, "purged on main")
		}
		if prefer == PreferFiles {
			e.rep.Conflicts = append(e.rep.Conflicts, ConflictNote{Path: rel, Kept: KeptFile, Why: "not in the files; row moved to the trash"})
			return e.deleteRowToTrash(ctx, k, t, "restore preferred the files")
		}
		return e.exportDoc(ctx, k, t, dbDoc)
	}
	same, err := sameDoc(fileDoc, dbDoc)
	if err != nil {
		return err
	}
	if same {
		return e.applyFile(ctx, k, t, fileDoc, fc)
	}
	keepDB := false
	why := ""
	switch prefer {
	case PreferDB:
		keepDB, why = true, "restore preferred the DB"
	case PreferFiles:
		keepDB, why = false, "restore preferred the files"
	default:
		keepDB = firstIsNewer(dbDoc, fileDoc)
		why = "newer updated_at won during adopt"
	}
	if keepDB {
		if err := e.saveConflictCopy(ctx, k, KeptDB, fileDoc); err != nil {
			return err
		}
		e.rep.Conflicts = append(e.rep.Conflicts, ConflictNote{Path: rel, Kept: KeptDB, Why: why})
		return e.exportDoc(ctx, k, t, dbDoc)
	}
	if err := e.saveConflictCopy(ctx, k, KeptFile, dbDoc); err != nil {
		return err
	}
	e.rep.Conflicts = append(e.rep.Conflicts, ConflictNote{Path: rel, Kept: KeptFile, Why: why})
	return e.applyFile(ctx, k, t, fileDoc, fc)
}
