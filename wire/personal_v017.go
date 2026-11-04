package wire

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
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
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	return ""
}

// fileURL is the file:// url of an absolute dir.
func fileURL(dir string) string {
	return (&url.URL{Scheme: folder.Scheme, Path: filepath.ToSlash(dir)}).String()
}

// ensureLocalMarker gives the v0.1.17 local personal store a marker
// (internal, a new sto_ id) unless it has one.
func ensureLocalMarker(dir string, now time.Time, opts ...rowfs.Option) error {
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
		return nil, errs.Wrap(errs.CodeOf(err), "wire: the v0.1.17 personal store ~/.lore/"+personalDirV017, err)
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

// personalFoldersV017 lists the folders of dir holding a .lore/data.
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
		if fi, serr := os.Stat(filepath.Join(dir, e.Name(), rowfs.LoreDir, rowfs.DataDir)); serr == nil && fi.IsDir() {
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
		return nil, 0, nil
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
				return nil, 0, errs.Wrap(errs.CodeOf(perr), "migrate: personal folder "+f, perr)
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
			return nil, 0, errs.Wrap(errs.CodeOf(err), "migrate: personal folder "+f, err)
		}
		steps = append(steps, StepPersonalStoreFlatten+": "+f+"/: "+strconv.Itoa(n)+" row(s) moved to the root")
		moved += n
		// Step 13: declare personal_flat in the old folder's _meta.json, so a
		// v0.1.17 lore turns read-only there instead of writing rows into a
		// folder 0.1.18 no longer reads. A folder without one has nothing to
		// declare (rowfs.DeclareRepo answers E_NOT_FOUND).
		added, derr := rowfs.DeclareRepo(filepath.Join(dir, f), []string{formatgate.PersonalFlat})
		if derr != nil && !errs.Has(derr, errs.NotFound) {
			return nil, 0, errs.Wrap(errs.CodeOf(derr), "migrate: declare "+formatgate.PersonalFlat+" in personal folder "+f, derr)
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
