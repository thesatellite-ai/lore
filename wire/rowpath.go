package wire

// rowpath.go — where one row's file sits on this machine (`lore path`,
// `lore edit`; D3 of the plain-files round). The one place that turns a
// row id into a file path: it asks the backend that holds the row
// (RowFile on the git and folder backends), never builds the path itself,
// so a change of the on-disk layout (rowfs) moves every answer with it.
//
//	id ─► Engine.Get (any attached store, table from the id prefix)
//	   ─► the store's backend wrapper: a live copy? (LiveCopier)
//	   ─► concrete backend RowFile(table, id) ─► <dir>/<rel>, stat

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/store"
)

// RowFiler is a backend whose rows are files: the git backend (a store
// clone or a code repo) and the folder backend. dir is the checkout or
// store root, rel the row file's slash path under it.
type RowFiler interface {
	RowFile(table row.Table, id ids.AnyID) (dir, rel string)
}

// LiveCopy is where a row read through the live overlay comes from when
// its newest version is not its working-tree file.
type LiveCopy struct {
	// Ref is the git ref holding it (refs/lore/live, or the remote default
	// branch for a landed row that is not pulled yet).
	Ref string
	// Path is the row file's path inside Ref.
	Path string
	// NotPulled: the row landed on the remote default branch and this
	// checkout has not pulled it yet.
	NotPulled bool
}

// LiveCopier is a backend wrapper (the live overlay of a code repo) that
// can answer a row from a git ref instead of its working-tree file.
type LiveCopier interface {
	LiveCopy(ctx context.Context, table row.Table, id ids.AnyID) (LiveCopy, bool, error)
}

// RowLocation is where one row lives on this machine.
type RowLocation struct {
	ID    ids.AnyID
	Table row.Table
	Store store.Source
	// Folder is the store folder ("." the store root, "" a code repo).
	Folder string
	// Path is the row file's absolute path in the working tree or store
	// clone ("" when the store keeps no files, e.g. the machine store).
	Path string
	// Exists: Path is a file now.
	Exists bool
	// Deleted: the row is a tombstone (in the trash); its file still exists.
	Deleted bool
	// Live is set when the newest version is on a git ref, not at Path.
	Live *LiveCopy
}

// RowLocation finds the row id in the attached stores and says where its
// file is. A row that is in no attached store is E_NOT_FOUND naming every
// place looked in and what to run next.
func (a *App) RowLocation(ctx context.Context, id ids.AnyID) (RowLocation, error) {
	st, err := a.state(ctx)
	if err != nil {
		return RowLocation{}, err
	}
	src, r, err := a.Get(ctx, engine.Ref{ID: id})
	if err != nil {
		if errs.Has(err, errs.NotFound) {
			return RowLocation{}, a.rowNotFound(st, id, err)
		}
		return RowLocation{}, err
	}
	loc := RowLocation{ID: id, Table: r.Table, Store: src, Folder: st.folders[src], Deleted: r.Deleted}
	if loc.Folder == "" && src != store.SourceRepo {
		loc.Folder = "."
	}
	b := st.byName[src]
	if b == nil {
		if src == store.SourceMachine {
			return loc, errs.New(errs.NotFound, id.String()+" is a machine-local row ("+string(r.Table)+"): it is kept in this machine's database (~/.lore/state/machine.db), not in a file").
				WithHint("print it with lore show " + id.String())
		}
		return loc, errs.New(errs.Internal, "path: store "+string(src)+" holds "+id.String()+" but has no backend open")
	}
	if lc, ok := b.(LiveCopier); ok {
		c, isLive, lerr := lc.LiveCopy(ctx, r.Table, id)
		if lerr != nil {
			return loc, lerr
		}
		if isLive {
			loc.Live = &c
		}
	}
	rf, ok := concrete(b).(RowFiler)
	if !ok {
		return loc, errs.New(errs.Unsupported, "store "+string(src)+" keeps "+id.String()+" in a backend without row files").
			WithHint("print it with lore show " + id.String())
	}
	dir, rel := rf.RowFile(r.Table, id)
	loc.Path = absPath(dir, rel)
	if _, serr := os.Stat(loc.Path); serr == nil {
		loc.Exists = true
	} else if !errors.Is(serr, fs.ErrNotExist) {
		return loc, errs.Wrap(errs.Internal, "path: read "+loc.Path, serr)
	}
	return loc, nil
}

// absPath joins a backend's dir and slash rel into an absolute path.
func absPath(dir, rel string) string {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// rowNotFound is the not-found answer naming each attached store and the
// data folder looked in (the folder of the row file the store would hold).
func (a *App) rowNotFound(st *state, id ids.AnyID, cause error) error {
	var looked []string
	for _, src := range a.Stores() {
		place := string(src)
		if rf, ok := concrete(st.byName[src]).(RowFiler); ok {
			table := row.Table("tasks")
			if def, found := a.prefixTable(id); found {
				table = def
			}
			dir, rel := rf.RowFile(table, id)
			place += " (" + absPath(dir, path.Dir(path.Dir(rel))) + ")"
		}
		looked = append(looked, place)
	}
	msg := "no row " + id.String() + " in the stores this folder attaches"
	if len(looked) > 0 {
		msg += "; looked in: " + strings.Join(looked, ", ")
	}
	hint := "lore search <words> finds a row by its text; a row of another repo or store folder is found by running the command there; lore store list shows what this folder attaches"
	if h := errs.EnvelopeOf(cause).Error.Hint; h != "" {
		hint = h + "; " + hint
	}
	return errs.New(errs.NotFound, msg).WithHint(hint)
}

// prefixTable is the table an id's prefix names.
func (a *App) prefixTable(id ids.AnyID) (row.Table, bool) {
	reg, err := builtinRegistry()
	if err != nil {
		return "", false
	}
	def, ok := reg.ByPrefix(id.Prefix())
	return def.Name, ok
}

// RowLocate implements opsrow.Locator over RowLocation.
func (a *App) RowLocate(ctx context.Context, id ids.AnyID) (opsrow.Location, error) {
	loc, err := a.RowLocation(ctx, id)
	if err != nil {
		return opsrow.Location{}, err
	}
	out := opsrow.Location{Table: loc.Table, Store: string(loc.Store), Folder: loc.Folder, Path: loc.Path, Exists: loc.Exists, Deleted: loc.Deleted}
	if loc.Live != nil {
		out.LiveRef, out.LivePath, out.NotPulled = loc.Live.Ref, loc.Live.Path, loc.Live.NotPulled
	}
	return out, nil
}
