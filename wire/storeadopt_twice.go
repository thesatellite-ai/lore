package wire

// storeadopt_twice.go — what a plain `lore store adopt` leaves held twice
// (devco:D-939, bug hunt H-200, a follow-up of H-195 / D-934).
//
// Without --remove-source an adoption copies the repo's rows into the
// folder and leaves the repo's own copies live. When the repo reads that
// folder (a folder of a store its .lore/stores.yaml attaches whole), each
// such row is live twice: a command by id on it answers E_AMBIGUOUS_ID
// and lore doctor is DEGRADED (copied_id). adopt now counts those rows
// (AdoptOut.HeldTwice) and prints the one rerun that settles them all:
// the same adopt with --remove-source, which takes the repo's copies out
// and keeps the newest copy in the folder.
//
//	adoptChanges: each adopted row the repo keeps live (a row file or a
//	   live-ref row) whose folder copy is live too ─▶ HeldTwice++
//	Adopt: --remove-source, or a repo that does not read the folder ─▶ 0
//
// That rerun must leave doctor HEALTHY, so --remove-source (without
// --move) keeps the repo's actor rows that the rows left in the repo (its
// project and repos rows) were saved by: removed, doctor's actor_roster
// warned that the repo holds rows saved by an actor its roster lacks.

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// adoptRepoReadsFolder reports whether the repo at top reads folder of
// the store src beside its own rows: the folders lists read (lensFolders)
// when top is the repo this command runs in, else a store its
// .lore/stores.yaml attaches whole.
func (a *App) adoptRepoReadsFolder(ctx context.Context, top string, src attach.Store, folder string) bool {
	want, err := attachLocation(src, folder)
	if err != nil {
		return false
	}
	if here, herr := a.storeRepoTop(ctx); herr == nil && here != "" && sameDir(here, top) {
		for _, f := range a.lensFolders(ctx) {
			if loc, lerr := attachLocation(f, f.Folder); lerr == nil && loc == want {
				return true
			}
		}
		return false
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return false
	}
	for _, s := range cfg.Stores {
		if (s.Folder != "" && s.Folder != rowfs.FolderRoot) || s.Name == attach.ReservedMe || s.URL == "" {
			continue // the same stores lensFolders widens
		}
		if loc, lerr := attachLocation(s, folder); lerr == nil && loc == want {
			return true
		}
	}
	return false
}

// adoptKeepWriters is adopted without the actor rows that a row file
// staying in the repo at top (one not in adopted) was saved by
// (created_by or updated_by).
func adoptKeepWriters(top string, adopted []store.Key) ([]store.Key, error) {
	gone := make(map[store.Key]bool, len(adopted))
	for _, k := range adopted {
		gone[k] = true
	}
	data := filepath.Join(top, filepath.FromSlash(rowfs.LayoutRepo.DataPath(rowfs.FolderRoot)))
	writers := map[string]bool{}
	werr := filepath.WalkDir(data, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == rowfs.MetaFileName || rowfs.IsOSJunk(d.Name()) {
			return err
		}
		rel, rerr := filepath.Rel(data, p)
		if rerr != nil {
			return rerr
		}
		table, id, ok := rowfs.ParseRowPath(filepath.ToSlash(rel))
		if !ok || gone[store.Key{Table: table, ID: id}] {
			return nil
		}
		fields, _, ferr := readJSONFields(p)
		if ferr != nil {
			return ferr
		}
		for _, f := range []string{"created_by", "updated_by"} {
			for _, w := range refValues(fields[f]) {
				writers[w] = true
			}
		}
		return nil
	})
	if werr != nil && !errors.Is(werr, fs.ErrNotExist) {
		return nil, errs.Prefix(adoptVerb+": read "+data, werr)
	}
	out := make([]store.Key, 0, len(adopted))
	for _, k := range adopted {
		if k.Table == schema.TableActors && writers[k.ID.String()] {
			continue
		}
		out = append(out, k)
	}
	return out, nil
}
