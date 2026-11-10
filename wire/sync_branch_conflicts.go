package wire

// sync_branch_conflicts.go — row files of the repo store that a git merge
// left with conflict markers (G-SYNC-2). The core leaves such a file out of
// every read (rowfs decodeImportable, DoctorRowFiles names it), so before
// this `lore sync conflicts` said "no conflicts" and `lore sync resolve 1`
// was E_NOT_FOUND while the row was missing everywhere. Now:
//
//	lore sync conflicts ─▶ live conflicts (#1..L), then these (#L+1..)
//	lore sync status    ─▶ "conflicted row files: N" + each path
//	lore sync resolve n ─▶ n > L: keep one side of every block in the file
//	                       (kept = the first side, git's HEAD; other = the
//	                       second, the merged branch's), re-check, write,
//	                       refresh the cache; the person commits the file
//
// Two shapes are handled: a whole file git wrote markers into (not JSON:
// the marker lines are settled as text), and a JSON row whose text field
// or meta entry holds a marker block (settled per string). A result that
// is not a valid row file, or still holds markers, is refused and nothing
// is written.

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/conflictpick"
	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// BranchConflict is one row file of the repo store holding conflict
// markers.
type BranchConflict struct {
	// Path is the file relative to the data dir ("tasks/tsk_….json").
	Path string
	// Place is the file relative to the project (".lore/data/tasks/…").
	Place string
	Table string
	RowID string
}

// conflictDataDir is a rowfs backend that can name its data dir and list
// the files a read leaves out (git in repo mode, folder).
type conflictDataDir interface {
	Dir() string
	DataPath() string
	Problems(ctx context.Context) ([]rowfs.Problem, error)
}

// repoDataRoot opens the repo store's data dir; ok is false for a repo
// store that is not rowfs-based.
func (a *App) repoDataRoot(ctx context.Context) (*os.Root, conflictDataDir, bool, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	d, ok := concrete(st.byName[store.SourceRepo]).(conflictDataDir)
	if !ok {
		return nil, nil, false, nil
	}
	root, err := os.OpenRoot(filepath.Join(d.Dir(), filepath.FromSlash(d.DataPath())))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, false, nil
		}
		return nil, nil, false, errs.Wrap(errs.Internal, "sync: open the data folder", err)
	}
	return root, d, true, nil
}

// BranchConflicts lists the repo store's row files holding conflict
// markers, by path.
func (a *App) BranchConflicts(ctx context.Context) ([]BranchConflict, error) {
	root, d, ok, err := a.repoDataRoot(ctx)
	if err != nil || !ok {
		return nil, err
	}
	defer func() { _ = root.Close() }() // read-only handle
	probs, err := d.Problems(ctx)
	if err != nil {
		return nil, err
	}
	var out []BranchConflict
	for _, p := range probs {
		b, rerr := root.ReadFile(filepath.FromSlash(p.Path))
		if rerr != nil || !fileHasMarkers(b) {
			continue
		}
		c := BranchConflict{Path: p.Path, Place: d.DataPath() + "/" + p.Path}
		if table, id, ok := rowfs.ParseRowPath(p.Path); ok {
			c.Table, c.RowID = table.String(), id.String()
		}
		out = append(out, c)
	}
	return out, nil
}

// fileHasMarkers: marker lines in the file itself (git's merge), or a
// marker block inside a string of the JSON row.
func fileHasMarkers(b []byte) bool {
	if merge3.ContainsConflictMarkers(string(b)) {
		return true
	}
	obj, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	return err == nil && conflictpick.HasMarkers(obj)
}

// ResolveBranchConflict keeps one side (other: the second) of every
// conflict block of c's file and writes it back when the result is a valid
// row file with no markers left; the cache is refreshed. Nothing is written
// on a refusal.
func (a *App) ResolveBranchConflict(ctx context.Context, c BranchConflict, other bool) error {
	root, _, ok, err := a.repoDataRoot(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errs.New(errs.NotFound, "sync: the repo store has no data folder")
	}
	defer func() { _ = root.Close() }() // the write below is checked on its own
	rel := filepath.FromSlash(c.Path)
	b, err := root.ReadFile(rel)
	if err != nil {
		return errs.Wrap(errs.Internal, "sync: read "+c.Place, err)
	}
	next, err := settleRowFile(b, other)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, c.Place+": "+err.Error(), err).WithHint("fix the file by hand, then commit it")
	}
	if _, derr := rowfs.DecodeRowFile(c.Path, next); derr != nil {
		return errs.Wrap(errs.InvalidInput, c.Place+": keeping one side does not give a valid row file", derr).
			WithHint("fix the file by hand, then commit it")
	}
	if fileHasMarkers(next) {
		return errs.New(errs.InvalidInput, c.Place+" still holds conflict markers after keeping one side (nested or hand-made blocks)").
			WithHint("fix the file by hand, then commit it")
	}
	mode := fs.FileMode(rowFileMode)
	if fi, serr := root.Stat(rel); serr == nil {
		mode = fi.Mode().Perm()
	}
	if err = root.WriteFile(rel, next, mode); err != nil {
		return errs.Wrap(errs.Internal, "sync: write "+c.Place, err)
	}
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	a.wrote(nil)
	_, err = st.eng.Refresh(ctx, store.SourceRepo)
	return err
}

// rowFileMode is the mode of a settled file whose mode cannot be read.
const rowFileMode = 0o644

// settleRowFile settles b: as JSON when it decodes (marker blocks inside
// strings), else as text (git's marker lines around whole lines).
func settleRowFile(b []byte, other bool) ([]byte, error) {
	if obj, err := canonjson.Decode(b, canonjson.DefaultMaxDepth); err == nil {
		return canonjson.Encode(conflictpick.Object(obj, other))
	}
	text := conflictpick.Text(string(b), other)
	if !bytes.HasSuffix([]byte(text), []byte("\n")) {
		text += "\n"
	}
	return []byte(text), nil
}
