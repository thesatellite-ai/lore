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
//	  ─▶ nothing but actors / the project row left in .lore/data (besides
//	     _meta.json, the policies and the repos row the repo keeps): they
//	     go too (the store holds the actors it needs; the folder is its own
//	     project), except a row a kept row still references, which stays
//	     and the move says why (W_REPO_ROW_KEPT)
//
// A row whose store copy cannot be read back keeps its repo copy, and the
// move says so. An unlinked repo keeps today's tombstone: there the repo
// is still a place people write and sync through, and the tombstone is
// what keeps the row from coming back to a place it left (spec 11 rule
// 3); a linked repo's reads and writes go to the folder (D-729), whose
// copy carries the moves entry.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
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
	// LeftKept names each actor or project row that stays in the repo
	// because a row the repo keeps (its repos row, _meta.json) still
	// references it, and who references it.
	LeftKept []string
}

// Warning codes of dropMovedRepoCopies (devco D-731).
const (
	// WarnMovedKept: a moved row's store copy was not found by id, so its
	// repo copy (the tombstone) stays.
	WarnMovedKept engine.WarnCode = "W_MOVED_KEPT"
	// WarnCommentsMoved: the comments on the moved rows moved with them.
	WarnCommentsMoved engine.WarnCode = "W_COMMENTS_MOVED"
	// WarnRepoRowKept: an actor or project row stays in the repo after the
	// move because a row the repo keeps still references it.
	WarnRepoRowKept engine.WarnCode = "W_REPO_ROW_KEPT"
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
	for _, k := range d.LeftKept {
		out = append(out, engine.Warning{Code: WarnRepoRowKept, Message: k})
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
	rest, keptLeft, err := movedDropLeftovers(top)
	if err != nil {
		return out, err
	}
	out.LeftKept = keptLeft
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

// movedDropKeptByRepo are the tables whose rows a linked repo keeps after
// its notes moved out (devco D-731): its policies (read by its own hooks,
// e.g. the branch mode) and its repos row (who the repo is). They do not
// count as "other rows" when deciding whether the actor and project rows
// can go; _meta.json (the format marker) is skipped the same way.
var movedDropKeptByRepo = map[row.Table]bool{
	schema.TablePolicies: true,
	schema.TableRepos:    true,
}

// movedDropLeftovers are the actor and project rows of the repo at top
// that can go once no moved-table row is left in .lore/data: nil while
// any other row is left. Of the rest, a leftover that a row the repo
// keeps still points at through a declared ref field (or that _meta.json
// names as the repo's project) stays, and kept says which and why: the
// repo never loses a row something left in it still references. The
// envelope's created_by / updated_by stamps are attribution, not
// references: the store holds the actors it needs.
func movedDropLeftovers(top string) (remove []store.Key, kept []string, err error) {
	data := filepath.Join(top, filepath.FromSlash(dataDirRel))
	var leftover []store.Key
	var keptRows []string // slash paths under .lore/data
	other := false
	werr := filepath.WalkDir(data, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == rowfs.MetaFileName || rowfs.IsOSJunk(d.Name()) {
			return err
		}
		rel, rerr := filepath.Rel(data, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		table, id, ok := rowfs.ParseRowPath(rel)
		switch {
		case ok && movedDropLeftover[table]:
			leftover = append(leftover, store.Key{Table: table, ID: id})
		case ok && movedDropKeptByRepo[table]:
			keptRows = append(keptRows, rel)
		default:
			other = true
		}
		return nil
	})
	if werr != nil && !errors.Is(werr, fs.ErrNotExist) {
		return nil, nil, errs.Wrap(errs.Internal, "read "+data, werr)
	}
	if other || len(leftover) == 0 {
		return nil, nil, nil
	}
	refs, err := movedDropKeptRefs(data, keptRows)
	if err != nil {
		return nil, nil, err
	}
	for _, k := range leftover {
		if by, ok := refs[k.ID.String()]; ok {
			kept = append(kept, string(k.Table)+"/"+k.ID.String()+" stays: "+by+" references it")
			continue
		}
		remove = append(remove, k)
	}
	return remove, kept, nil
}

// movedDropKeptRefs maps each id the rows the repo keeps reference (the
// declared ref fields of rows, the project_id / repo_id of _meta.json) to
// who references it ("repos/<id> project_id").
func movedDropKeptRefs(data string, rows []string) (map[string]string, error) {
	out := map[string]string{}
	if meta, ok, err := readJSONFields(filepath.Join(data, rowfs.MetaFileName)); err != nil {
		return nil, err
	} else if ok {
		for _, f := range []string{"project_id", "repo_id"} {
			for _, id := range refValues(meta[f]) {
				out[id] = rowfs.MetaFileName + " " + f
			}
		}
	}
	for _, rel := range rows {
		table, _, _ := rowfs.ParseRowPath(rel)
		fields, ok, err := readJSONFields(filepath.Join(data, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, f := range builtinRefFields(table) {
			for _, id := range refValues(fields[f]) {
				out[id] = strings.TrimSuffix(rel, ".json") + " " + f
			}
		}
	}
	return out, nil
}

// builtinRefFields are the names of table's declared ref (or list of ref)
// fields in the built-in schema.
func builtinRefFields(table row.Table) []string {
	var out []string
	for _, t := range schema.Builtin() {
		if t.Name != table {
			continue
		}
		for _, f := range t.Fields {
			if f.Kind == value.KindRef || (f.Kind == value.KindList && f.Elem == value.KindRef) {
				out = append(out, f.Name)
			}
		}
	}
	return out
}

// readJSONFields reads the JSON object in path field by field; ok is false
// when the file does not exist.
func readJSONFields(path string) (map[string]json.RawMessage, bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a row file under the repo's .lore/data
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errs.Wrap(errs.Internal, "read "+path, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false, errs.Wrap(errs.Internal, "parse "+path, err)
	}
	return m, true, nil
}

// refValues are the ids in a ref field's raw value: one string or a list
// of strings (anything else holds no id).
func refValues(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
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
