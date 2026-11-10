package wire

// The safety half of `lore migrate` (FORMAT_EVOLUTION rule 4, sections
// 7.6-7.7; decisions FQ5, FQ6), implementing opsmigrate.Journal:
//
//	Begin    ─► places (repo .lore/data + every attached store's data folder)
//	         ─► Snapshot each into the code repo's objects ─► backup commit
//	            refs/lore-backup/migrate/<date>[-n] (local only) + a backup ref
//	            in each store clone at its tip ─► .lore/state/migrate.json
//	Finish   ─► snapshot again (tree_after) ─► push changed clones (unless
//	            --no-push/--offline) ─► phase done (nothing changed: drop it all)
//	Rollback ─► diff(before, after) per place ─► every migrated file must still
//	            hold the bytes the migration wrote (else E_MIGRATE_ROLLBACK_LOSES,
//	            nothing changes) ─► unpushed clone: reset to its tip; else
//	            restore the files (+ one commit and push for a clone)
//
// Determinism (7.3) makes "untouched since the migration" a byte check:
// the journal keeps the tree the migration produced, so the current blob of
// each migrated path is compared with it. The journal and backup refs never
// reach a pushed file.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsmigrate.Journal = (*App)(nil)

// MigrateJournalFile is the run journal (7.7), relative to the repo top,
// in the git-ignored .lore/state beside the lock: local, never pushed.
const MigrateJournalFile = ".lore/state/migrate.json"

// Journal phases.
const (
	phaseRunning    = "running"
	phaseDone       = "done"
	phaseRolledBack = "rolled_back"
)

// backupDateLayout is the backup ref's date (the run's local date, the only
// clock reading of a migration; it never reaches a pushed file, 7.3).
const backupDateLayout = "2006-01-02"

// repoEntry is the backup tree folder of the repo's .lore/data.
const repoEntry = "branch"

// migrateCommitHint is what a person runs after the branch phase (lore
// never commits the code repo).
const migrateCommitHint = `commit them: git add .lore/data && git commit -m "lore: migrate format"`

// rollbackSubject is the store commit of a rollback after push.
const rollbackSubject = "lore: roll back format migration"

// journalPlace is one folder a migration may change.
type journalPlace struct {
	Name  string `json:"name"`
	Entry string `json:"entry"`
	Dir   string `json:"dir"`
	Data  string `json:"data"`
	Clone bool   `json:"clone,omitempty"`
	// TreeBefore / TreeAfter are the data folder's tree before Begin and
	// after Finish, objects in the code repo.
	TreeBefore string `json:"tree_before"`
	TreeAfter  string `json:"tree_after,omitempty"`
	// TipBefore / TipAfter are a clone's HEAD around the run.
	TipBefore string `json:"tip_before,omitempty"`
	TipAfter  string `json:"tip_after,omitempty"`
	// CloneBackup is the backup ref inside the clone (keeps TipBefore).
	CloneBackup string `json:"clone_backup,omitempty"`
	Pushed      bool   `json:"pushed,omitempty"`
}

// migrateJournal is .lore/state/migrate.json.
type migrateJournal struct {
	Target       int64          `json:"target"`
	Date         string         `json:"date"`
	BackupRef    string         `json:"backup_ref"`
	BackupCommit string         `json:"backup_commit"`
	Phase        string         `json:"phase"`
	Offline      bool           `json:"offline,omitempty"`
	Places       []journalPlace `json:"places"`
	// ConfigKeys are the ~/.lore/config.yaml keys the run set (step 12's
	// personal half); rollback removes those still holding the value.
	ConfigKeys []journalConfigKey `json:"config_keys,omitempty"`
	// ModeBPointer is the v0.1.17 Mode B pointer file (relative to the repo
	// top, e.g. .lore/lore.yaml) this run found and converted. Such a run
	// cannot be rolled back (BUGFIX-07): Rollback refuses it before it
	// changes anything, Status and Finish say so instead of naming the undo.
	ModeBPointer string `json:"mode_b_pointer,omitempty"`
	// Previous is the finished journal this run replaced: a run that
	// changes nothing puts it back (FA-FMT-3), so a no-op second run never
	// takes away the rollback of the first.
	Previous *migrateJournal `json:"previous,omitempty"`
}

// placeBackend is a backend whose data folder lives on disk (git and
// folder backends).
type placeBackend interface {
	Dir() string
	DataPath() string
}

// journalPath is the journal's absolute path.
func (a *App) journalPath(ctx context.Context) (string, string, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return "", "", err
	}
	return top, filepath.Join(top, filepath.FromSlash(MigrateJournalFile)), nil
}

// readJournal returns the journal (ok=false when there is none).
func readJournal(path string) (migrateJournal, bool, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return migrateJournal{}, false, nil
	}
	if err != nil {
		return migrateJournal{}, false, errs.Wrap(errs.Internal, "migrate: read "+MigrateJournalFile, err)
	}
	var j migrateJournal
	if err := json.Unmarshal(b, &j); err != nil {
		return migrateJournal{}, false, errs.Wrap(errs.Internal, "migrate: "+MigrateJournalFile+" is not valid JSON", err)
	}
	return j, true, nil
}

// writeJournal writes j atomically.
func writeJournal(path string, j migrateJournal) error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, "migrate: encode the journal", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return errs.Wrap(errs.Internal, "migrate: create .lore/state", err)
	}
	return writeFileAtomic(path, append(b, '\n'), lockFileMode)
}

// The modes of a file (and a folder) a rollback writes back, as rowfs
// writes them: owner-only in a store, git's usual work-tree modes in the
// code repo's committed .lore/data (read by everyone who clones it; the
// owner-only restore left 0600 files a teammate's checkout could not read).
const (
	restoredFileMode     = 0o600
	restoredRepoFileMode = 0o644
	restoredRepoDirMode  = 0o755
)

// restoreModes returns the file and folder modes of a restore in place p.
func restoreModes(p journalPlace) (fs.FileMode, fs.FileMode) {
	if p.Entry == repoEntry && !p.Clone {
		return restoredRepoFileMode, restoredRepoDirMode
	}
	return restoredFileMode, cacheDirMode
}

// migratePlaces lists the data folders a run may change: the repo's
// .lore/data, then every attached store's (a Mode B repo has only the
// repo: its shared store does not exist before the run).
func (a *App) migratePlaces(ctx context.Context, top string) ([]journalPlace, error) {
	repo := journalPlace{Name: string(store.SourceRepo), Entry: repoEntry, Dir: top, Data: rowfs.LoreDir + "/" + rowfs.DataDir}
	if _, modeB := FindModeBPointer(top); modeB {
		return []journalPlace{repo}, nil
	}
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	names := st.eng.Stores()
	var out []journalPlace
	seen := map[string]bool{}
	for i, b := range st.backends {
		p, ok := b.(placeBackend)
		if !ok {
			continue
		}
		jp := journalPlace{Name: string(names[i]), Entry: "store-" + strconv.Itoa(i), Dir: p.Dir(), Data: p.DataPath()}
		if i == 0 {
			jp.Entry = repoEntry
		}
		if g, ok := b.(*git.Backend); ok && g.CloneMode() {
			jp.Clone = true
		}
		if key := jp.Dir + "\x00" + jp.Data; !seen[key] {
			seen[key] = true
			out = append(out, jp)
		}
	}
	if len(out) == 0 {
		out = append(out, repo)
	}
	return out, nil
}

// Begin implements opsmigrate.Journal (rule 4 step 3, 7.7 resume).
func (a *App) Begin(ctx context.Context, offline bool) (opsmigrate.Begun, error) {
	top, path, err := a.journalPath(ctx)
	if err != nil {
		return opsmigrate.Begun{}, err
	}
	j, ok, err := readJournal(path)
	if err != nil {
		return opsmigrate.Begun{}, err
	}
	if ok && j.Phase == phaseRunning {
		return opsmigrate.Begun{Resumed: true, BackupRef: j.BackupRef,
			Lines: []string{"resuming the interrupted migration (backup " + j.BackupRef + " kept); rows already at _v 2 are skipped"}}, nil
	}
	snaps, err := git.NewSnapshots()
	if err != nil {
		return opsmigrate.Begun{}, err
	}
	places, err := a.migratePlaces(ctx, top)
	if err != nil {
		return opsmigrate.Begun{}, err
	}
	date := a.opts.Wall().Local().Format(backupDateLayout)
	entries := make([]git.TreeEntry, 0, len(places))
	for i := range places {
		p := &places[i]
		if p.TreeBefore, err = snaps.Snapshot(ctx, top, p.Dir, p.Data); err != nil {
			return opsmigrate.Begun{}, err
		}
		entries = append(entries, git.TreeEntry{Name: p.Entry, Tree: p.TreeBefore})
		if p.Clone {
			if p.TipBefore = snaps.Head(ctx, p.Dir); p.TipBefore != "" {
				if p.CloneBackup, err = snaps.BackupAt(ctx, p.Dir, date, p.TipBefore); err != nil {
					return opsmigrate.Begun{}, err
				}
			}
		}
	}
	ref, commit, err := snaps.WriteBackup(ctx, top, date, entries, []string{snaps.Head(ctx, top)}, "lore: backup before format migration")
	if err != nil {
		return opsmigrate.Begun{}, err
	}
	var prev *migrateJournal
	if ok && j.Phase == phaseDone {
		done := j
		done.Previous = nil // keep one level: only the latest finished run can be rolled back
		prev = &done
	}
	j = migrateJournal{Target: row.FormatVersion, Date: date, BackupRef: ref, BackupCommit: commit, Phase: phaseRunning, Offline: offline, Places: places, Previous: prev}
	if pointer, modeB := FindModeBPointer(top); modeB {
		j.ModeBPointer = modeBDir + "/" + filepath.Base(pointer)
	}
	if err := writeJournal(path, j); err != nil {
		return opsmigrate.Begun{}, err
	}
	return opsmigrate.Begun{BackupRef: ref, Lines: []string{"backup: " + ref + " (local only, never pushed)"}}, nil
}

// RewriteRepoV1 implements opsmigrate.Journal: RewriteV1 on the repo store
// only (--offline, 7.3).
func (a *App) RewriteRepoV1(ctx context.Context, table row.Table, dryRun bool) ([]ids.AnyID, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	got, err := rewriteStore(ctx, st.backends[0], table, dryRun)
	if err != nil || dryRun || len(got) == 0 {
		return got, err
	}
	a.wrote(nil)
	_, err = st.eng.Refresh(ctx, store.SourceRepo)
	return got, err
}

// Finish implements opsmigrate.Journal (rule 4 step 7).
func (a *App) Finish(ctx context.Context, push bool, count int) ([]string, error) {
	top, path, err := a.journalPath(ctx)
	if err != nil {
		return nil, err
	}
	j, ok, err := readJournal(path)
	if err != nil || !ok {
		return nil, err
	}
	snaps, err := git.NewSnapshots()
	if err != nil {
		return nil, err
	}
	var lines []string
	changed := false
	pushed := map[string]bool{}
	for i := range j.Places {
		p := &j.Places[i]
		if p.TreeAfter, err = snaps.Snapshot(ctx, top, p.Dir, p.Data); err != nil {
			return nil, err
		}
		if p.Clone {
			p.TipAfter = snaps.Head(ctx, p.Dir)
		}
		diff, err := snaps.DiffTrees(ctx, top, p.TreeBefore, p.TreeAfter)
		if err != nil {
			return nil, err
		}
		if len(diff) == 0 && p.TipAfter == p.TipBefore {
			continue
		}
		changed = true
		line := placeLabel(*p) + ": " + strconv.Itoa(len(diff)) + " files upgraded"
		switch {
		case p.Entry == repoEntry && !p.Clone:
			line += "; " + migrateCommitHint
		case p.Clone && p.TipAfter != p.TipBefore && push:
			if !pushed[p.Dir] {
				if serr := a.syncPlace(ctx, p.Name); serr != nil {
					line += "; push failed (" + serr.Error() + "): run `lore sync`"
					break
				}
				pushed[p.Dir] = true
			}
			p.Pushed = true
			line += ", store commit " + shortOID(p.TipAfter) + ", pushed"
		case p.Clone && p.TipAfter != p.TipBefore:
			line += ", store commit " + shortOID(p.TipAfter) + " (local, not pushed)"
		}
		lines = append(lines, line)
	}
	if len(j.ConfigKeys) > 0 {
		lines = append(lines, "~/.lore/"+ConfigFile+": "+strconv.Itoa(len(j.ConfigKeys))+" personal settings set")
	}
	if !changed && count == 0 && len(j.ConfigKeys) == 0 {
		return nil, a.dropBackup(ctx, snaps, top, path, j)
	}
	j.Phase = phaseDone
	if err := writeJournal(path, j); err != nil {
		return nil, err
	}
	if j.ModeBPointer != "" {
		return append(lines, modeBNoUndoLine), nil
	}
	return append(lines, "undo: lore migrate rollback"), nil
}

// modeBNoUndoLine ends a run that converted Mode B, in place of the undo
// line: that conversion has no rollback (BUGFIX-07).
const modeBNoUndoLine = "undo: none for a shared-database conversion (`lore migrate rollback` refuses it); the shared lore.db was not changed"

// refuseModeBRollback is the E_UNSUPPORTED refusal of rolling back a run
// that converted Mode B (BUGFIX-07). The conversion wrote rows into a
// folder store outside the repo that other repos of the same project may
// share, attached it in .lore/stores.yaml and kept the pointer as
// <file>.migrated; the journal's places hold only the repo's .lore/data,
// so a rollback would restore nothing and report success. The shared
// lore.db was only read, so the way back to v0.1.17 is the hint.
func refuseModeBRollback(j migrateJournal) error {
	return errs.New(errs.Unsupported, "migrate: the last run converted the retired shared-database mode ("+j.ModeBPointer+" → "+j.ModeBPointer+ModeBMigratedSuffix+", rows copied into store "+ModeBStoreName+"); a shared-database conversion cannot be rolled back; nothing changed").
		WithHint("the shared lore.db was not changed: to use it with v0.1.17 again, rename " + j.ModeBPointer + ModeBMigratedSuffix + " back to " + j.ModeBPointer + " and remove the " + ModeBStoreName + " entry from .lore/stores.yaml")
}

// dropBackup removes a run's backup refs and journal (it changed nothing).
func (a *App) dropBackup(ctx context.Context, snaps git.Snapshots, top, path string, j migrateJournal) error {
	all := []error{snaps.DeleteRef(ctx, top, j.BackupRef)}
	for _, p := range j.Places {
		if p.CloneBackup != "" {
			all = append(all, snaps.DeleteRef(ctx, p.Dir, p.CloneBackup))
		}
	}
	if j.Previous != nil { // FA-FMT-3: the earlier run stays rollback-able
		all = append(all, writeJournal(path, *j.Previous))
	} else if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		all = append(all, errs.Wrap(errs.Internal, "migrate: remove the journal", err))
	}
	return errors.Join(all...)
}

// syncPlace syncs (fetch, merge, push) the attached store name.
func (a *App) syncPlace(ctx context.Context, name string) error {
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	b, ok := st.byName[store.Source(name)]
	if !ok {
		return errs.New(errs.NotFound, "store "+name+" is not attached")
	}
	_, err = b.Sync(ctx)
	return err
}

// placeLabel names a place in output lines.
func placeLabel(p journalPlace) string {
	if p.Entry == repoEntry {
		return "branch (" + p.Data + ")"
	}
	return "store " + p.Name
}

// shortOIDLen is how many hex digits output lines show of a commit.
const shortOIDLen = 7

// shortOID abbreviates a commit id for output.
func shortOID(oid string) string {
	if len(oid) > shortOIDLen {
		return oid[:shortOIDLen]
	}
	return oid
}

// Rollback implements opsmigrate.Journal (FQ5, 7.6). The caller holds the
// migration lock.
func (a *App) Rollback(ctx context.Context) (opsmigrate.RollbackOut, error) {
	top, path, err := a.journalPath(ctx)
	if err != nil {
		return opsmigrate.RollbackOut{}, err
	}
	j, ok, err := readJournal(path)
	if err != nil {
		return opsmigrate.RollbackOut{}, err
	}
	if !ok || j.Phase == phaseRolledBack {
		return opsmigrate.RollbackOut{}, errs.New(errs.NotFound, "migrate: no migration to roll back on this machine").
			WithHint("lore migrate status")
	}
	if j.ModeBPointer != "" {
		return opsmigrate.RollbackOut{}, refuseModeBRollback(j)
	}
	snaps, err := git.NewSnapshots()
	if err != nil {
		return opsmigrate.RollbackOut{}, err
	}
	// Check, then check again right before the restore (7.6): an edit that
	// lands between the two (a teammate's pull, a local write) makes the
	// second check refuse, so the restore never runs on stale plans.
	// PointRollbackChecked is the test build's pause between them.
	if _, _, err = a.checkRollback(ctx, snaps, top, &j); err != nil {
		return opsmigrate.RollbackOut{}, err
	}
	if err = opsmigrate.At(ctx, opsmigrate.PointRollbackChecked); err != nil {
		return opsmigrate.RollbackOut{}, err
	}
	out, plans, err := a.checkRollback(ctx, snaps, top, &j)
	if err != nil {
		return opsmigrate.RollbackOut{}, err
	}
	reset := map[string]bool{}
	for _, pl := range plans {
		line, aerr := a.applyRollback(ctx, snaps, top, pl, reset)
		if aerr != nil {
			return opsmigrate.RollbackOut{}, aerr
		}
		if pl.pushed {
			out.Pushed = true
		}
		if line != "" {
			out.Lines = append(out.Lines, line)
		}
	}
	cfgLine, cfgKept, err := a.rollbackConfigKeys(j.ConfigKeys)
	if err != nil {
		return opsmigrate.RollbackOut{}, err
	}
	if cfgLine != "" {
		out.Lines = append(out.Lines, cfgLine)
	}
	out.Kept = append(out.Kept, cfgKept...)
	if st, serr := a.state(ctx); serr == nil {
		for _, name := range st.eng.Stores() {
			if _, err := st.eng.Refresh(ctx, name); err != nil {
				return opsmigrate.RollbackOut{}, err
			}
		}
	}
	j.Phase = phaseRolledBack
	if prev := j.Previous; prev != nil && prev.Phase == phaseDone {
		// A second run (typically after a pull brought more v1 rows)
		// replaced the journal of the first, whose rows are still at
		// _v: 2. Rolling back only the second left them there with no
		// word, and a second rollback answered "no migration to roll
		// back", although v0.1.17 still cannot commit beside them. The
		// earlier run becomes the one to undo next, and the run says so.
		out.Lines = append(out.Lines, fmt.Sprintf(earlierMigrationLine, prev.Date, prev.BackupRef))
		return out, writeJournal(path, *prev)
	}
	return out, writeJournal(path, j)
}

// earlierMigrationLine is the rollback line when an earlier finished
// migration (the journal's Previous) still stands.
const earlierMigrationLine = "the earlier migration of %s (backup %s) still stands: run `lore migrate rollback` again to undo it too"

// checkRollback plans the rollback of every place and refuses with
// E_MIGRATE_ROLLBACK_LOSES when a migrated row changed since the migration.
func (a *App) checkRollback(ctx context.Context, snaps git.Snapshots, top string, j *migrateJournal) (opsmigrate.RollbackOut, []rollbackPlan, error) {
	out := opsmigrate.RollbackOut{BackupRef: j.BackupRef, Lines: []string{}, Kept: []string{}}
	plans, lost, err := a.rollbackPlans(ctx, snaps, top, j, &out)
	if err != nil {
		return opsmigrate.RollbackOut{}, nil, err
	}
	if len(lost) > 0 {
		return opsmigrate.RollbackOut{}, nil, errs.New(errs.MigrateRollbackLoses,
			"migrate: rolling back would lose "+strconv.Itoa(len(lost))+" changes made after the migration: "+strings.Join(lost, ", ")+"; nothing changed").
			WithHint("undo those edits, or keep the migration")
	}
	return out, plans, nil
}

// rollbackPlan is what Rollback does in one place.
type rollbackPlan struct {
	place journalPlace
	diff  []git.TreeChange
	// keepMeta leaves _meta.json raised (rows created after need its floor).
	keepMeta bool
	// localUndo resets an unpushed clone to its old tip.
	localUndo bool
	pushed    bool
}

// rollbackPlans checks every place: a migrated path whose bytes changed
// since the migration is lost (refusal); a row created after it is kept.
func (a *App) rollbackPlans(ctx context.Context, snaps git.Snapshots, top string, j *migrateJournal, out *opsmigrate.RollbackOut) ([]rollbackPlan, []string, error) {
	var plans []rollbackPlan
	var lost []string
	for _, p := range j.Places {
		if p.TreeAfter == "" { // interrupted run: compare with the files as they are now
			after, err := snaps.Snapshot(ctx, top, p.Dir, p.Data)
			if err != nil {
				return nil, nil, err
			}
			p.TreeAfter = after
			if p.Clone {
				p.TipAfter = snaps.Head(ctx, p.Dir)
			}
		}
		diff, err := snaps.DiffTrees(ctx, top, p.TreeBefore, p.TreeAfter)
		if err != nil {
			return nil, nil, err
		}
		if len(diff) == 0 && p.TipAfter == p.TipBefore {
			continue
		}
		now, err := snaps.Snapshot(ctx, top, p.Dir, p.Data)
		if err != nil {
			return nil, nil, err
		}
		since, err := snaps.DiffTrees(ctx, top, p.TreeAfter, now)
		if err != nil {
			return nil, nil, err
		}
		pl := rollbackPlan{place: p, diff: diff}
		migrated := map[string]bool{}
		for _, c := range diff {
			migrated[c.Path] = true
		}
		for _, c := range since {
			switch {
			case migrated[c.Path] && !sameRowSince(ctx, snaps, top, c):
				lost = append(lost, placeLabel(p)+" "+c.Path)
			case migrated[c.Path]:
				// edited and edited back: the rollback loses nothing
			case c.OldBlob == "" && strings.HasSuffix(c.Path, ".json") && c.Path != rowfs.MetaFileName:
				out.Kept = append(out.Kept, placeLabel(p)+" "+c.Path)
				pl.keepMeta = true
			}
		}
		if p.Clone {
			up := snaps.Upstream(ctx, p.Dir)
			unpushed := !p.Pushed && snaps.Head(ctx, p.Dir) == p.TipAfter && (up == "" || !snaps.IsAncestor(ctx, p.Dir, p.TipAfter, up))
			pl.localUndo = unpushed && p.TipBefore != ""
			pl.pushed = !unpushed
		}
		plans = append(plans, pl)
	}
	return plans, lost, nil
}

// applyRollback undoes one place and returns its line.
func (a *App) applyRollback(ctx context.Context, snaps git.Snapshots, top string, pl rollbackPlan, reset map[string]bool) (string, error) {
	p := pl.place
	if pl.localUndo {
		if reset[p.Dir] {
			return "", nil
		}
		reset[p.Dir] = true
		if err := snaps.ResetClone(ctx, p.Dir, p.TipBefore); err != nil {
			return "", err
		}
		return placeLabel(p) + ": local store ref back to " + shortOID(p.TipBefore) + " (" + p.CloneBackup + "); nothing was pushed", nil
	}
	var paths []string
	for _, c := range pl.diff {
		if pl.keepMeta && c.Path == rowfs.MetaFileName {
			continue
		}
		rel := p.Data + "/" + c.Path
		full := filepath.Join(p.Dir, filepath.FromSlash(rel))
		if c.OldBlob == "" {
			if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", errs.Wrap(errs.Internal, "migrate: remove "+rel, err)
			}
		} else {
			b, err := snaps.Blob(ctx, top, c.OldBlob)
			if err != nil {
				return "", err
			}
			fileMode, dirMode := restoreModes(p)
			if err := os.MkdirAll(filepath.Dir(full), dirMode); err != nil {
				return "", errs.Wrap(errs.Internal, "migrate: restore "+rel, err)
			}
			if err := writeFileAtomic(full, b, fileMode); err != nil {
				return "", err
			}
		}
		paths = append(paths, rel)
	}
	line := placeLabel(p) + ": " + plural(len(paths), "file") + " restored"
	if pl.keepMeta {
		line += "; " + rowfs.MetaFileName + " stays raised (rows created after the migration need it)"
	}
	if !p.Clone {
		if p.Entry == repoEntry {
			line += `; commit them: git add .lore/data && git commit -m "lore: roll back format migration"`
		}
		return line, nil
	}
	if err := gitCommitFilesAs(ctx, p.Dir, a.opts.Wall(), storeIdentityFunc(a.opts), rollbackSubject, paths...); err != nil {
		return "", err
	}
	if !pl.pushed {
		return line + ", store commit (local)", nil
	}
	if err := a.syncPlace(ctx, p.Name); err != nil {
		return line + ", store commit; push failed (" + err.Error() + "): run `lore sync`", nil
	}
	return line + ", store commit pushed", nil
}

// Status implements opsmigrate.Journal (rule 4 `lore migrate status`).
func (a *App) Status(ctx context.Context) (opsmigrate.StatusOut, error) {
	top, path, err := a.journalPath(ctx)
	if err != nil {
		return opsmigrate.StatusOut{}, err
	}
	out := opsmigrate.StatusOut{Writes: row.FormatVersion, Places: []opsmigrate.PlaceStatus{}, Features: []string{}, Pending: []string{}, Notes: []string{}}
	if d, ok, ferr := rowfs.ReadRepoFormat(top); ferr != nil {
		out.Notes = append(out.Notes, ferr.Error())
	} else if ok {
		out.Features = append(out.Features, d.Features...)
		out.MinLoreVersion = d.MinLoreVersion
	}
	if p, modeB := FindModeBPointer(top); modeB {
		out.Notes = append(out.Notes, RetiredModeBMessage(p)) // the message names the next step
	} else if err := a.statusPlaces(ctx, &out); err != nil {
		return opsmigrate.StatusOut{}, err
	}
	if j, ok, jerr := readJournal(path); jerr != nil {
		out.Notes = append(out.Notes, jerr.Error())
	} else if ok {
		switch {
		case j.Phase == phaseRunning && j.ModeBPointer != "":
			out.Journal = "interrupted shared-database conversion (" + j.ModeBPointer + "; backup " + j.BackupRef + "): run `lore migrate run` to finish it (it cannot be rolled back)"
		case j.Phase == phaseRunning:
			out.Journal = "interrupted migration to v" + strconv.FormatInt(j.Target, 10) + " (backup " + j.BackupRef + "): run `lore migrate run` to finish it, or `lore migrate rollback` to undo it"
		case j.Phase == phaseDone && j.ModeBPointer != "":
			out.Journal = "last migration " + j.Date + " converted the shared-database mode (" + j.ModeBPointer + "; backup " + j.BackupRef + "); it cannot be rolled back"
		case j.Phase == phaseDone:
			out.Journal = "last migration " + j.Date + " (backup " + j.BackupRef + "); undo: lore migrate rollback"
		default:
			out.Journal = "last migration " + j.Date + " was rolled back (backup " + j.BackupRef + " kept)"
		}
	}
	out.Lock = a.lockState(top)
	return out, nil
}

// statusPlaces counts row versions in every attached store and the v1 rows
// a run would rewrite per core table.
func (a *App) statusPlaces(ctx context.Context, out *opsmigrate.StatusOut) error {
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	core := map[row.Table]bool{}
	for _, t := range CoreTables() {
		core[t] = true
	}
	pending := map[row.Table]int{}
	names := st.eng.Stores()
	for i, b := range st.backends {
		rows, _, err := b.Read(ctx, "")
		if err != nil {
			return err
		}
		ps := opsmigrate.PlaceStatus{Place: string(names[i])}
		for _, sr := range rows {
			ps.Total++
			r, derr := sr.Decode()
			switch {
			case derr != nil && errs.Has(derr, errs.OldLore):
				ps.Newer++
			case derr != nil:
				continue
			case r.SourceVersion < row.FormatVersion:
				ps.V1++
				if core[r.Table] {
					pending[r.Table]++
				}
			default:
				ps.V2++
			}
		}
		// A row written by a newer lore never decodes, so Read leaves its
		// file out (rowfile_notice.go); the files it left out say which.
		if rp, ok := concrete(b).(readProblemer); ok {
			for _, p := range rp.ReadProblems() {
				if p.Code == errs.OldLore {
					ps.Total++
					ps.Newer++
				}
			}
		}
		out.Places = append(out.Places, ps)
	}
	for _, t := range slices.Sorted(maps.Keys(pending)) {
		out.Pending = append(out.Pending, t.String()+": "+plural(pending[t], "row")+" at v1")
	}
	return nil
}

// lockPID reads the pid in the migration lock (ok=false: no lock file).
func lockPID(top string) (int, bool) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(top, filepath.FromSlash(MigrateLockFile))))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, true
	}
	return pid, true
}

// processAlive reports whether pid runs on this machine. On Windows,
// FindProcess opens the process and fails when it is gone; elsewhere
// signal 0 probes it (EPERM: it runs under another user).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// lockState is the status line of the migration lock.
func (a *App) lockState(top string) string {
	pid, ok := lockPID(top)
	switch {
	case !ok:
		return "free"
	case processAlive(pid):
		return "held by a lore migrate running on this machine (pid " + strconv.Itoa(pid) + ")"
	default:
		return "stale: pid " + strconv.Itoa(pid) + " is gone; release it with `lore migrate unlock`"
	}
}

// Unlock implements opsmigrate.Journal (7.2: only a lock whose process is
// gone; the team lock on the signals ref is not built yet).
func (a *App) Unlock(ctx context.Context) (opsmigrate.UnlockOut, error) {
	top, path, err := a.journalPath(ctx)
	if err != nil {
		return opsmigrate.UnlockOut{}, err
	}
	pid, ok := lockPID(top)
	if !ok {
		return opsmigrate.UnlockOut{Line: "no migration lock on this machine"}, nil
	}
	if processAlive(pid) {
		return opsmigrate.UnlockOut{}, errs.New(errs.MigrateLocked, "migrate: the lock is held by a lore migrate that is still running on this machine (pid "+strconv.Itoa(pid)+"); nothing changed")
	}
	if err := os.Remove(filepath.Join(top, filepath.FromSlash(MigrateLockFile))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return opsmigrate.UnlockOut{}, errs.Wrap(errs.Internal, "migrate: remove "+MigrateLockFile, err)
	}
	line := "released the migration lock (pid " + strconv.Itoa(pid) + " is gone)"
	if j, ok, _ := readJournal(path); ok && j.Phase == phaseRunning { // best effort: only adds a hint
		line += "; run `lore migrate run` to finish the interrupted migration"
	}
	return opsmigrate.UnlockOut{Released: true, Line: line}, nil
}

// sameRowSince reports whether a migrated row file changed since the
// migration (c: its blob after the run → now) only in its stamps: the same
// table, id, fields and meta, so rolling it back loses no edit. An edit a
// person undid with another edit writes new bytes (a new HLC and
// updated_at), which refused the rollback with "undo those edits" although
// they were undone (upgrader journey, edit-back-then-rollback). A file that
// is gone, or does not decode, is a change.
func sameRowSince(ctx context.Context, snaps git.Snapshots, top string, c git.TreeChange) bool {
	if c.OldBlob == "" || c.NewBlob == "" {
		return false
	}
	before, err := snaps.Blob(ctx, top, c.OldBlob)
	if err != nil {
		return false
	}
	now, err := snaps.Blob(ctx, top, c.NewBlob)
	if err != nil {
		return false
	}
	a, err := row.Decode(before)
	if err != nil {
		return false
	}
	b, err := row.Decode(now)
	if err != nil {
		return false
	}
	if a.Table != b.Table || a.ID != b.ID || a.Deleted != b.Deleted || len(a.Meta) != len(b.Meta) {
		return false
	}
	// A null field and a missing one are the same row (0.1.17 wrote every
	// null; a 0.1.18 write leaves them out).
	af, bf := setFields(a.Fields), setFields(b.Fields)
	if len(af) != len(bf) {
		return false
	}
	for k, v := range af {
		w, ok := bf[k]
		if !ok || !value.Equal(v, w) {
			return false
		}
	}
	for k, v := range a.Meta {
		if w, ok := b.Meta[k]; !ok || v.String() != w.String() {
			return false
		}
	}
	return true
}

// setFields is fields without the null ones.
func setFields(fields map[string]value.Value) map[string]value.Value {
	out := make(map[string]value.Value, len(fields))
	for k, v := range fields {
		if v != nil && v.Kind() != value.KindNull {
			out[k] = v
		}
	}
	return out
}
