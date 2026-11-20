package wire

// movedrop.go — a move out of a linked repo leaves nothing behind in it
// (devco D-731). A repo linked to a store folder reads its notes from the
// folder; once `lore move` (one row or --from bulk) took a row out of the
// repo, the tombstone the engine writes at the old place kept a file (or
// a live-ref entry) per moved row in .lore/data, and the row's comments,
// the actors and the project row stayed with it: an own repo whose 4
// notes had moved still held 4 tombstones, 2 comments, 2 actors and 1
// project row.
//
//	move ok ─▶ source is the repo and the repo is linked?
//	  ─▶ comments on the moved rows: moved too (same ids)
//	  ─▶ each moved row (and comment) the target holds live, checked by
//	     id after the store write: its repo file and live-ref entry go,
//	     recorded as moved out (movedout.go, D-710) so the repo's
//	     pre-commit commits the removal instead of refusing a hand delete
//	  ─▶ nothing but actors / the project row left in .lore/data: they go
//	     too (the store holds the actors it needs; the folder is its own
//	     project)
//
// A row whose store copy cannot be read back keeps its repo copy, and the
// move says so. An unlinked repo keeps today's tombstone: there the repo
// is still a place people write and sync through, and the tombstone is
// what keeps the row from coming back to a place it left (spec 11 rule
// 3); a linked repo's reads and writes go to the folder (D-729), whose
// copy carries the moves entry.

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// movedDrop is what dropping the repo copies did.
type movedDrop struct {
	// Removed are the repo files removed (slash paths from the git top).
	Removed []string
	// Kept names each moved row whose store copy was not found by id, so
	// its repo copy stays.
	Kept []string
	// Comments is how many comments on the moved rows moved with them.
	Comments int
}

// Warning codes of dropMovedRepoCopies (devco D-731).
const (
	// WarnMovedKept: a moved row's store copy was not found by id, so its
	// repo copy (the tombstone) stays.
	WarnMovedKept engine.WarnCode = "W_MOVED_KEPT"
	// WarnCommentsMoved: the comments on the moved rows moved with them.
	WarnCommentsMoved engine.WarnCode = "W_COMMENTS_MOVED"
)

// warnings are d's lines for the move's output.
func (d movedDrop) warnings() []engine.Warning {
	var out []engine.Warning
	if d.Comments > 0 {
		out = append(out, engine.Warning{Code: WarnCommentsMoved, Message: plural(d.Comments, "comment") + " moved with the rows"})
	}
	for _, k := range d.Kept {
		out = append(out, engine.Warning{Code: WarnMovedKept, Message: k})
	}
	return out
}

// movedDropLeftover are the tables whose rows a linked repo does not need
// once nothing else is left in .lore/data (devco D-731).
var movedDropLeftover = map[row.Table]bool{
	schema.TableActors:   true,
	schema.TableProjects: true,
}

// repoLinkedTop is the git top of the repo this command runs in when the
// repo is linked to a store folder, else "".
func (a *App) repoLinkedTop(ctx context.Context) string {
	top := gitTop(ctx, a.opts.RepoDir)
	if top == "" || a.linkedFolderTarget(ctx, top) == "" {
		return ""
	}
	return top
}

// dropMovedRepoCopies removes the repo copies of moved (rows that left
// the repo for to) when the repo is linked: see the file comment. moved
// holds only rows whose source was the repo.
func (a *App) dropMovedRepoCopies(ctx context.Context, to store.Source, moved []row.Row) (movedDrop, error) {
	var out movedDrop
	if len(moved) == 0 || to == store.SourceRepo {
		return out, nil
	}
	top := a.repoLinkedTop(ctx)
	if top == "" {
		return out, nil // unlinked: the tombstone stays (file comment)
	}
	st, err := a.writeState(ctx)
	if err != nil {
		return out, err
	}
	moved = leftRepo(ctx, st, moved)
	if len(moved) == 0 {
		return out, nil
	}
	// comments travel with their row, keeping their ids
	movedIDs := map[string]bool{}
	for _, r := range moved {
		movedIDs[r.ID.String()] = true
	}
	comments, err := st.eng.List(ctx, store.SourceRepo, schema.TableComments)
	if err != nil {
		return out, err
	}
	var crefs []engine.Ref
	for _, c := range comments {
		if !c.Deleted && movedIDs[fieldText(c, schema.FieldEntityID)] {
			crefs = append(crefs, engine.Ref{Store: store.SourceRepo, Table: c.Table, ID: c.ID})
		}
	}
	all := append([]row.Row(nil), moved...)
	if len(crefs) > 0 {
		got, merr := a.MoveMany(ctx, crefs, to, engine.MoveOptions{})
		if merr != nil {
			return out, merr
		}
		for _, w := range got.Rows {
			all = append(all, w.Row)
		}
		out.Comments = len(got.Rows)
	}
	// verify by id that the store holds each row before its repo copy goes
	keys := make([]store.Key, 0, len(all))
	for _, r := range all {
		if a.storeHolds(ctx, st, to, r.Table, r.ID) {
			keys = append(keys, store.Key{Table: r.Table, ID: r.ID})
		} else {
			out.Kept = append(out.Kept, r.ID.String()+": its copy in "+string(to)+" was not found, so the repo copy stays")
		}
	}
	if len(keys) == 0 {
		return out, nil
	}
	if _, _, err = a.adoptLiveRemove(ctx, top, keys); err != nil {
		return out, err
	}
	if out.Removed, err = removeAdopted(top, keys); err != nil {
		return out, err
	}
	rest, err := movedDropLeftovers(top)
	if err != nil {
		return out, err
	}
	if len(rest) > 0 {
		gone, rerr := removeAdopted(top, rest)
		out.Removed = append(out.Removed, gone...)
		if rerr != nil {
			return out, rerr
		}
	}
	pruneEmptyDirs(filepath.Join(top, filepath.FromSlash(dataDirRel)))
	return out, RecordMovedOut(ctx, top, string(to), out.Removed)
}

// dataDirRel is the repo's row folder, slash path from the git top.
const dataDirRel = ".lore/data"

// storeHolds reports whether to holds a live row id of table (read back
// from the store itself, after its write).
func (a *App) storeHolds(ctx context.Context, st *state, to store.Source, table row.Table, id ids.AnyID) bool {
	src, r, err := st.eng.Get(ctx, engine.Ref{Store: to, Table: table, ID: id})
	return err == nil && src == to && !r.Deleted
}

// movedDropLeftovers are the actor and project rows of the repo at top
// once nothing else (no other row file) is left in .lore/data; nil while
// any other row is left, so the repo keeps what its rows reference.
func movedDropLeftovers(top string) ([]store.Key, error) {
	data := filepath.Join(top, filepath.FromSlash(dataDirRel))
	var out []store.Key
	other := false
	err := filepath.WalkDir(data, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == rowfs.MetaFileName || rowfs.IsOSJunk(d.Name()) {
			return err
		}
		rel, rerr := filepath.Rel(data, p)
		if rerr != nil {
			return rerr
		}
		table, id, ok := rowfs.ParseRowPath(filepath.ToSlash(rel))
		if !ok || !movedDropLeftover[table] {
			other = true
			return nil
		}
		out = append(out, store.Key{Table: table, ID: id})
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, errs.Wrap(errs.Internal, "read "+data, err)
	}
	if other {
		return nil, nil
	}
	return out, nil
}

// leftRepo keeps the rows of moved whose repo copy is a tombstone now:
// the rows this move took out of the repo (a row moved between two
// stores has no repo copy).
func leftRepo(ctx context.Context, st *state, moved []row.Row) []row.Row {
	out := moved[:0:0]
	for _, r := range moved {
		src, cur, err := st.eng.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: r.Table, ID: r.ID})
		if err == nil && src == store.SourceRepo && cur.Deleted {
			out = append(out, r)
		}
	}
	return out
}
