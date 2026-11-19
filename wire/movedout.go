package wire

// movedout.go — row files lore itself removed from a repo because the rows
// now live in a store (devco:D-710). `lore store adopt --remove-source`
// copies a repo's rows into a store folder, then deletes their row files
// from .lore/data as a diff the person commits. lore's own pre-commit
// refuses a commit that deletes row files (host/handdelete.go: "deletes N
// lore rows by removing the file, not with lore"), so adopt's own
// removal was a commit lore refused, with 5,000 rows listed.
//
//	adopt --remove-source ─▶ RecordMovedOut: one entry per removed file
//	  (path from the repo top, the store/folder it went to) in the git dir
//	  (git rev-parse --git-path: never committed, a linked worktree has
//	  its own)
//	pre-commit / doctor ─▶ MovedOut: a deleted row file with an entry is
//	  the move's second half, not a hand delete; the commit takes it
//	pre-commit ─▶ PruneMovedOut: entries whose file is no longer a pending
//	  deletion (committed, or brought back) go, so a later hand delete of
//	  the same path is refused like any other
//
// Why a marker and not a tombstone in the repo (what `lore move` leaves):
// --remove-source promises the files leave the repo; 5,000 tombstones
// would keep every file and the rows' ids in it.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

// movedOutFile is the marker's name in the git dir.
const movedOutFile = "lore-moved-out.json"

// movedOutDoc is the marker: removed row file (slash path from the repo
// top) ─▶ the store/folder that holds its row now.
type movedOutDoc struct {
	Rows map[string]string `json:"rows"`
}

// movedOutPath is the marker of the repo holding dir ("" when not a git
// checkout).
func movedOutPath(ctx context.Context, dir string) string {
	out, err := gitRun{dir: dir, args: []string{"rev-parse", "--path-format=absolute", "--git-path", movedOutFile}}.output(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// readMovedOut reads the marker at p (empty when there is none or it is
// not JSON: a broken marker only means the hook asks as before).
func readMovedOut(p string) movedOutDoc {
	doc := movedOutDoc{Rows: map[string]string{}}
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil || json.Unmarshal(b, &doc) != nil || doc.Rows == nil {
		return movedOutDoc{Rows: map[string]string{}}
	}
	return doc
}

// writeMovedOut writes doc at p, or removes p when doc is empty.
func writeMovedOut(p string, doc movedOutDoc) error {
	if len(doc.Rows) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return errs.Wrap(errs.Internal, "remove "+p, err)
		}
		return nil
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, "encode "+movedOutFile, err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return errs.Wrap(errs.Internal, "write "+tmp, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return errs.Wrap(errs.Internal, "write "+p, errors.Join(err, os.Remove(tmp)))
	}
	return nil
}

// RecordMovedOut notes that lore removed the row files paths (slash paths
// from the git top of the repo at top) because their rows now live in
// place (store/folder as --store names it), so the repo's pre-commit
// commits the removal instead of refusing it as a hand delete.
func RecordMovedOut(ctx context.Context, top, place string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	p := movedOutPath(ctx, top)
	if p == "" {
		return nil // not a git checkout: no hook to tell
	}
	doc := readMovedOut(p)
	for _, rel := range paths {
		doc.Rows[filepath.ToSlash(rel)] = place
	}
	return writeMovedOut(p, doc)
}

// MovedOut is the marker of the repo at root keyed by path relative to
// root (as the hook lists changed files): removed row file ─▶ the
// store/folder its row went to. Empty when there is none.
func MovedOut(ctx context.Context, root string) map[string]string {
	out := map[string]string{}
	p := movedOutPath(ctx, root)
	if p == "" {
		return out
	}
	doc := readMovedOut(p)
	if len(doc.Rows) == 0 {
		return out
	}
	prefix := movedOutPrefix(ctx, root)
	for rel, place := range doc.Rows {
		if r, ok := strings.CutPrefix(rel, prefix); ok {
			out[r] = place
		}
	}
	return out
}

// movedOutPrefix is root's slash path from its git top plus "/" ("" when
// root is the top).
func movedOutPrefix(ctx context.Context, root string) string {
	top := gitTop(ctx, root)
	if top == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real // git prints the top with symlinks resolved
	}
	rel, err := filepath.Rel(top, root)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel) + "/"
}

// PruneMovedOut keeps only the marker entries of the repo at root whose
// file is still a pending deletion (pending: paths relative to root);
// the rest were committed or brought back. Best effort: a marker that
// cannot be rewritten only stays as it is.
func PruneMovedOut(ctx context.Context, root string, pending []string) {
	p := movedOutPath(ctx, root)
	if p == "" {
		return
	}
	doc := readMovedOut(p)
	if len(doc.Rows) == 0 {
		return
	}
	prefix := movedOutPrefix(ctx, root)
	keep := map[string]bool{}
	for _, rel := range pending {
		keep[prefix+rel] = true
	}
	own := prefix + rowfs.LayoutRepo.DataPath(rowfs.FolderRoot) + "/" // another project folder's entries are its own hook's
	changed := false
	for rel := range doc.Rows {
		if strings.HasPrefix(rel, own) && !keep[rel] {
			delete(doc.Rows, rel)
			changed = true
		}
	}
	if changed {
		_ = writeMovedOut(p, doc) // see above
	}
}

// MovedOutPlaces are the distinct store/folders of rows, sorted.
func MovedOutPlaces(rows map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, place := range rows {
		if !seen[place] {
			seen[place] = true
			out = append(out, place)
		}
	}
	sort.Strings(out)
	return out
}
