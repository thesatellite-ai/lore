package wire

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// v0.1.17's local personal store (`personal.store: local`, also its
// default when the key is empty): ~/.lore/personal/<folder>/.lore/data,
// one folder per org (the note was written in a repo of that org) plus
// everywhere/ (no org). It has no lore-store.yaml.
//
// Until `lore migrate run`, the core attaches it as `me` at the folder
// v0.1.17 would file a note in (F17: the org this repo resolves to, else
// everywhere), so `--to me` and `--store me` behave as v0.1.17 did; when an
// org applies, everywhere/ is attached too as me/everywhere. A marker is
// added to the folder on first use (the folder backend needs one; v0.1.17
// never reads it). `lore migrate run` then applies personal_store_flatten
// (DECISIONS_LOG 18, FORMAT_EVOLUTION): every row of every folder moves to
// the store root, recording its old folder in _o, and config.yaml gets
// `personal.store: file://<dir>`, the 0.1.18 form (`me` = the root).
const (
	// personalDirV017 is the local personal store under the lore home.
	personalDirV017 = "personal"
	// personalEverywhereV017 is its no-org folder.
	personalEverywhereV017 = "everywhere"
	// StepPersonalStoreFlatten names the migration step.
	StepPersonalStoreFlatten = "personal_store_flatten"
	// originOldFolder is the _o key recording a flattened row's v0.1.17
	// personal folder.
	originOldFolder = "personal_folder_v017"
)

// localPersonalV017 is the dir of the v0.1.17 local personal store when
// config.yaml selects it: `personal.store: local`, or no personal.store
// and the folder exists (v0.1.17's default). "" otherwise.
func (c machineConfig) localPersonalV017(home string) string {
	dir := filepath.Join(home, personalDirV017)
	switch strings.TrimSpace(c.Personal.Store) {
	case PersonalLocalV017:
		return dir
	case "":
		// Looked up by its fixed name inside home (fs.ValidPath holds for
		// it), following a symlinked folder the way os.Stat would.
		if fi, err := fs.Stat(os.DirFS(home), personalDirV017); err == nil && fi.IsDir() {
			return dir
		}
	}
	return ""
}

// fileURL is the file:// url of an absolute dir.
func fileURL(dir string) string {
	return fileuri.URL(dir, "")
}

// ensureLocalMarker gives the v0.1.17 local personal store a marker
// (internal, a new sto_ id) unless it has one.
func ensureLocalMarker(dir string, now time.Time, opts ...rowfs.Option) error {
	if err := privatePersonalDir(dir); err != nil {
		return err
	}
	_, err := rowfs.ReadMarker(dir)
	if err == nil {
		return nil
	}
	if !errs.Has(err, errs.NotAStore) {
		return err
	}
	id, err := ids.New[ids.Store]()
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: new store id", err)
	}
	return folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: id, Visibility: rowfs.VisibilityInternal, Created: now.UTC().Format(markerDateLayout)}, opts...)
}

// personalPrivateMode is the local personal store folder's mode: it holds
// the person's own notes, so no other user of the machine may list or read
// it, whatever the umask or a hand-made ~/.lore allowed.
const personalPrivateMode = 0o700

// privatePersonalDir makes the local personal store folder (dir, inside the
// lore home) owner-only before notes land in it: created 0700 when missing,
// and dir and the lore home above it stripped of group/other access when a
// hand-made ~/.lore (mkdir before the first lore run) or an older lore left
// them open. A symlinked folder is the person's own place: left as it is.
func privatePersonalDir(dir string) error {
	if err := os.MkdirAll(dir, personalPrivateMode); err != nil {
		return errs.FileIO("create", dir, err)
	}
	for _, p := range []string{filepath.Dir(dir), dir} {
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			if err := os.Chmod(p, perm&^0o077); err != nil {
				return errs.FileIO("chmod", p, err)
			}
		}
	}
	return nil
}

// localPersonalStores returns the `me` entries of a v0.1.17 local
// personal store (none when config.yaml does not select one).
func localPersonalStores(ctx context.Context, opts Options) ([]attach.Store, error) {
	mc, err := readMachineConfig(opts.Home)
	if err != nil {
		return nil, err
	}
	dir := mc.localPersonalV017(opts.Home)
	if dir == "" {
		return nil, nil
	}
	wall := opts.Wall
	if wall == nil {
		wall = time.Now
	}
	if err := ensureLocalMarker(dir, wall(), opts.rowfsOptions()...); err != nil {
		return nil, errs.Prefix("wire: the v0.1.17 personal store ~/.lore/"+personalDirV017, err)
	}
	place := personalEverywhereV017
	if opts.Org != nil {
		if org, oerr := opts.Org(ctx); oerr == nil && org != nil {
			if f, ferr := rowfs.CleanFolder(org.Name); ferr == nil && f != rowfs.FolderRoot {
				place = f
			}
		}
	}
	u := fileURL(dir)
	out := []attach.Store{{Name: store.Source(attach.ReservedMe), URL: u, Folder: place}}
	if place != personalEverywhereV017 {
		out = append(out, attach.Store{Name: store.Source(attach.ReservedMe + targetSep + personalEverywhereV017), URL: u, Folder: personalEverywhereV017})
	}
	return out, nil
}

// personalFoldersV017 lists the folders of dir: each holding a .lore/data
// (v0.1.17) or a _meta.json (made since, in the store layout).
func personalFoldersV017(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: read ~/.lore/"+personalDirV017, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		f, ferr := rowfs.CleanFolder(e.Name())
		if ferr != nil || f == rowfs.FolderRoot {
			continue // .lore, .git and other tool folders
		}
		// a folder v0.1.17 made keeps .lore/data; one the new lore made
		// before the migration ran sits flat (both are read where they are)
		if rowfs.HasStoreFolder(dir, f) {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out, nil
}

// migrateLocalPersonal is personal_store_flatten for the v0.1.17 local
// personal store. Rows are copied to the root first (a row already there
// is skipped), then tombstoned in their folder, and only then config.yaml
// is switched (kept as config.yaml.migrated), so an interrupted run
// resumes on the next one and a second run changes nothing. The int is
// the rows moved (to be moved, under dryRun: pendingFlatten).
func (a *App) migrateLocalPersonal(ctx context.Context, dryRun bool) ([]string, int, error) {
	mc, err := readMachineConfig(a.opts.Home)
	if err != nil {
		return nil, 0, err
	}
	dir := mc.localPersonalV017(a.opts.Home)
	if dir == "" {
		// flattened already (personal.store names the folder's url): rows a
		// 0.1.17 folder still holds live (written there after the flatten)
		// are moved the same way, so none is left unread
		if dir = mc.flattenedLocalPersonal(ctx, a.opts.Home, a.opts.rowfsOptions()...); dir == "" {
			return nil, 0, nil
		}
	}
	folders, err := personalFoldersV017(dir)
	if err != nil {
		return nil, 0, err
	}
	u := fileURL(dir)
	steps := []string{StepPersonalStoreFlatten + ": ~/.lore/" + personalDirV017 + " (v0.1.17 local) becomes personal.store " + u +
		" (rows of " + strings.Join(folders, ", ") + " move to its root; " + ConfigFile + " kept as " + ConfigFile + migratedSuffix + ")"}
	if dryRun {
		total := 0
		for _, f := range folders {
			n, perr := pendingFlatten(ctx, dir, f, a.opts.rowfsOptions()...)
			if perr != nil {
				return nil, 0, errs.Prefix("migrate: personal folder "+f, perr)
			}
			total += n
			steps = append(steps, StepPersonalStoreFlatten+": "+f+"/: "+strconv.Itoa(n)+" row(s) to move to the root")
		}
		return steps, total, nil
	}
	moved := 0
	if err = ensureLocalMarker(dir, a.opts.Wall(), a.opts.rowfsOptions()...); err != nil {
		return nil, 0, err
	}
	root, err := folder.Open(dir, rowfs.FolderRoot, a.opts.openOptions())
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = root.Close() }()
	for _, f := range folders {
		n, err := flattenFolder(ctx, dir, f, root, a.opts.openOptions())
		if err != nil {
			return nil, 0, errs.Prefix("migrate: personal folder "+f, err)
		}
		steps = append(steps, StepPersonalStoreFlatten+": "+f+"/: "+strconv.Itoa(n)+" row(s) moved to the root")
		moved += n
		// the tombstones left in the old folder are copies of rows the root
		// now holds: removed, so a person browsing the folder sees no
		// duplicates (its _meta.json stays, declaring personal_flat below)
		if err := dropMovedTombstones(ctx, dir, f, root, a.opts.openOptions()); err != nil {
			return nil, 0, errs.Prefix("migrate: personal folder "+f, err)
		}
		// Step 13: declare personal_flat in the old folder's _meta.json, so a
		// v0.1.17 lore turns read-only there instead of writing rows into a
		// folder 0.1.18 no longer reads. A folder without one has nothing to
		// declare (rowfs.DeclareRepo answers E_NOT_FOUND).
		added, derr := rowfs.DeclareRepo(filepath.Join(dir, f), []string{formatgate.PersonalFlat})
		if derr != nil && !errs.Has(derr, errs.NotFound) {
			return nil, 0, errs.Prefix("migrate: declare "+formatgate.PersonalFlat+" in personal folder "+f, derr)
		}
		if len(added) > 0 {
			steps = append(steps, StepPersonalStoreFlatten+": "+f+"/"+rowfs.RepoMetaPath+": features + "+formatgate.PersonalFlat)
		}
	}
	cfg := configPath(a.opts.Home)
	if b, rerr := os.ReadFile(filepath.Clean(cfg)); rerr == nil {
		if _, serr := os.Stat(cfg + migratedSuffix); errors.Is(serr, fs.ErrNotExist) {
			if err := writeFileAtomic(cfg+migratedSuffix, b, configFileMode); err != nil {
				return nil, 0, err
			}
		}
	}
	if err := setPersonalStore(a.opts.Home, u); err != nil {
		return nil, 0, err
	}
	return steps, moved, nil
}

// flattenedLocalPersonal is the 0.1.17 local personal store's dir when
// personal_store_flatten ran already (personal.store is its file:// url)
// and one of its old folders still holds a live row the root lacks; ""
// otherwise. lore doctor's personal_folders check reads it too.
func (c machineConfig) flattenedLocalPersonal(ctx context.Context, home string, opts ...rowfs.Option) string {
	dir := filepath.Join(home, personalDirV017)
	u := strings.TrimSpace(c.Personal.Store)
	if u == "" || (u != fileURL(dir) && u != fileURL(dir)+"/") {
		return ""
	}
	if n, _ := pendingLocalPersonal(ctx, dir, opts...); n == 0 {
		return ""
	}
	return dir
}

// pendingLocalPersonal counts the live rows of the old folders of the
// local personal store at dir that its root lacks, and names the folders
// holding them.
func pendingLocalPersonal(ctx context.Context, dir string, opts ...rowfs.Option) (int, []string) {
	folders, err := personalFoldersV017(dir)
	if err != nil {
		return 0, nil
	}
	total := 0
	var with []string
	for _, f := range folders {
		if n, perr := pendingFlatten(ctx, dir, f, opts...); perr == nil && n > 0 {
			total += n
			with = append(with, f)
		}
	}
	return total, with
}

// dropMovedTombstones removes the row files of folder f of the store at
// dir that are deleted there and live at root (personal_store_flatten
// moved them): what is left in the old folder is then only what the root
// does not hold.
func dropMovedTombstones(ctx context.Context, dir, f string, root store.Backend, oo store.OpenOptions) error {
	src, err := folder.Open(dir, f, oo)
	if err != nil {
		return err
	}
	rows, _, err := src.Read(ctx, "")
	_ = src.Close() // read only
	if err != nil {
		return err
	}
	for _, sr := range rows {
		if !sr.Deleted {
			continue
		}
		if _, gerr := root.Get(ctx, sr.Table, sr.ID); gerr != nil {
			continue // not at the root: the tombstone is the only record
		}
		name := string(sr.Table) + "/" + sr.ID.String() + ".json"
		for _, p := range []string{filepath.Join(dir, f, rowfs.LoreDir, rowfs.DataDir, filepath.FromSlash(name)), filepath.Join(dir, f, filepath.FromSlash(name))} {
			if rerr := os.Remove(p); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
				return errs.FileIO("remove", p, rerr)
			}
		}
	}
	return nil
}

// pendingFlatten counts the rows flattenFolder would move from folder f of
// the store at dir: its live rows the root does not hold yet. It only
// reads (no marker needed, nothing written), so --dry-run can report the
// same count the run will.
func pendingFlatten(ctx context.Context, dir, f string, opts ...rowfs.Option) (int, error) {
	r, err := rowfs.Open(dir, opts...)
	if err != nil {
		return 0, err
	}
	src, err := r.Folder(f)
	if err != nil {
		return 0, err
	}
	root, err := r.Folder(rowfs.FolderRoot)
	if err != nil {
		return 0, err
	}
	rows, _, err := src.Read(ctx, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sr := range rows {
		if sr.Deleted {
			continue
		}
		_, gerr := root.Get(sr.Table, sr.ID)
		switch {
		case errors.Is(gerr, store.ErrNotFound):
			n++
		case gerr != nil:
			return n, gerr
		}
	}
	return n, nil
}

// flattenFolder moves the live rows of folder f of the store at dir into
// root, returning how many it copied.
func flattenFolder(ctx context.Context, dir, f string, root store.Backend, oo store.OpenOptions) (int, error) {
	src, err := folder.Open(dir, f, oo)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()
	rows, _, err := src.Read(ctx, "")
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, sr := range rows {
		if sr.Deleted {
			continue
		}
		if _, gerr := root.Get(ctx, sr.Table, sr.ID); gerr == nil {
			// already copied (an interrupted run): only the tombstone is left
		} else if !errors.Is(gerr, store.ErrNotFound) {
			return moved, gerr
		} else {
			r, derr := sr.Decode()
			if derr != nil {
				return moved, derr
			}
			if r.Origin, err = withOldFolder(r.Origin, f); err != nil {
				return moved, err
			}
			// audit_fields (FORMAT_EVOLUTION step 9) on the move itself: the
			// audit_fields step skips the me attachments under a flatten, so a
			// v0.1.17 row without *_by actors would otherwise land at the root
			// unfilled and the next run would rewrite it (a second run must
			// change nothing).
			r, _ = auditfill.Defaults(r)
			up, ferr := store.FromRow(r)
			if ferr != nil {
				return moved, ferr
			}
			if err := root.Write(ctx, []store.Change{{Row: up}}); err != nil {
				return moved, err
			}
			moved++
		}
		h := hlc.Next(sr.HLC, time.Now(), sr.HLC.Actor)
		tomb, terr := store.FromRow(row.Row{Table: sr.Table, ID: sr.ID, HLC: h, Deleted: true, UpdatedAt: h.Wall})
		if terr != nil {
			return moved, terr
		}
		if err := src.Write(ctx, []store.Change{{Row: tomb, Expect: sr.HLC}}); err != nil {
			return moved, err
		}
	}
	return moved, nil
}

// withOldFolder adds the old folder to a row's _o (kept when present).
func withOldFolder(origin value.JSON, f string) (value.JSON, error) {
	raw := []byte(emptyJSONObject)
	if !origin.IsZero() {
		raw = origin.Raw()
	}
	ms, err := canon.Members(raw)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "wire: read _o", err)
	}
	for _, m := range ms {
		if m.Key == originOldFolder {
			return origin, nil
		}
	}
	ms = append(ms, canon.Member{Key: originOldFolder, Raw: canon.String(f)})
	obj, err := canon.Object(ms)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "wire: build _o", err)
	}
	return value.JSONOf(obj)
}

// emptyJSONObject is the _o of a row that has none.
const emptyJSONObject = "{}"
