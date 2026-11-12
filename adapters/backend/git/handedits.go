package git

// handedits.go — what a person changed by hand in a checkout lore writes
// (plain files round, brief 2.1–2.3, 2.7): the row files that differ from
// the last commit, read with one `git status`, so the pick-up in wire can
// take each through the write path (validate, re-stamp, commit).
//
//	HandChanges ─▶ git status --porcelain -z --no-renames (row paths only
//	   in a code repo; the whole clone in a store: every folder shares it)
//	   ─▶ each changed row file: folder, table, id, kind, HEAD's bytes
//	   ─▶ any other changed file: listed as a stray (lore doctor names it)
//	RestoreHead / PutBack ─▶ the pick-up's undo: a file is put back exactly
//	   as the person left it when its write is refused
//	setAsideHandEdits (sync) ─▶ files still not picked up (refused) are
//	   stashed for the merge and put back after it, so a sync never
//	   clobbers them and never stops on them
//
// A code repo's rows are never committed by lore (the person commits them
// with the code); there the pick-up re-stamps the file and leaves it.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// HandKind is what happened to a row file outside lore.
type HandKind uint8

const (
	// HandModified: the file differs from the last commit.
	HandModified HandKind = iota + 1
	// HandAdded: a new file (untracked or staged, not in the last commit).
	HandAdded
	// HandDeleted: the file of a committed row is gone.
	HandDeleted
)

// String names the kind in messages.
func (k HandKind) String() string {
	switch k {
	case HandAdded:
		return "added"
	case HandDeleted:
		return "deleted"
	default:
		return "edited"
	}
}

// HandChange is one row file changed outside lore.
type HandChange struct {
	// Path is the file's slash path relative to the checkout top.
	Path string
	// Abs is the file's absolute path.
	Abs string
	// Folder is the store folder holding it (rowfs.FolderRoot for the root).
	Folder string
	Table  row.Table
	ID     ids.AnyID
	Kind   HandKind
	// Base is the file as the last commit holds it (nil: not committed).
	Base []byte
}

// HandReport is what HandChanges found.
type HandReport struct {
	// Rows are the changed row files, sorted by path.
	Rows []HandChange
	// Strays are the other changed or new files (slash paths relative to
	// the checkout top) that are not lore's: notes, folders a person made.
	// lore never deletes them; lore doctor names them.
	Strays []string
}

// Folder is the clean store folder this Backend reads and writes.
func (b *Backend) Folder() string { return b.folder.Name() }

// Layout is where this checkout keeps its folders' rows.
func (b *Backend) Layout() rowfs.Layout { return b.root.Layout() }

// GitBusy reports whether a git command holds the checkout's index right
// now (index.lock exists): a person's `git commit` running lore's hook, or
// a git run another process makes. The pick-up waits for the next command
// then: it could not stage anything anyway.
func (b *Backend) GitBusy(ctx context.Context) bool {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--path-format=absolute", "--git-path", "index.lock"}})
	if err != nil {
		return false
	}
	return exists(trimmed(out))
}

// HandChanges lists the row files changed outside lore (see the file
// comment). A store clone is read whole (every folder shares the clone);
// a code repo only under this folder's .lore/data, never its code.
func (b *Backend) HandChanges(ctx context.Context) (HandReport, error) {
	if err := b.usable(ctx); err != nil {
		return HandReport{}, err
	}
	args := []string{"-c", "core.quotePath=false", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames"}
	if b.mode == modeRepo {
		args = append(append(args, "--"), b.root.Layout().Pathspecs(b.folder.Name())...)
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		return HandReport{}, errs.Wrap(errs.Internal, "git: read the changed files of "+b.work, err)
	}
	head, err := b.head(ctx)
	if err != nil {
		return HandReport{}, err
	}
	var rep HandReport
	var changed []HandChange
	layout := b.root.Layout()
	for _, f := range splitZ(out) {
		if len(f) < len("XY ")+1 {
			continue
		}
		status, p := f[:2], f[3:]
		if status == "!!" || strings.HasSuffix(p, "/") && b.mode == modeRepo {
			continue
		}
		if rowfs.IsOSJunk(p) || lorePath(p) {
			continue
		}
		folder, rel, ok := layout.SplitRowPath(p)
		if !ok {
			if _, isFlatRoot := StoreDirPath(p); isFlatRoot && b.mode == modeClone {
				// a root folder file at the store's top (the first plain
				// layout's place, or written there by an older lore): not
				// junk, the layout commit moves it into _store/ (D-ad)
				continue
			}
			if _, _, isFolderFile := layout.SplitFolderFile(p); !isFolderFile {
				rep.Strays = append(rep.Strays, p)
			}
			continue
		}
		table, id, _ := rowfs.ParseRowPath(rel)
		changed = append(changed, HandChange{Path: p, Abs: filepath.Join(b.work, filepath.FromSlash(p)), Folder: folder, Table: table, ID: id})
	}
	if err := b.headBases(ctx, head, changed); err != nil {
		return HandReport{}, err
	}
	for _, c := range changed {
		_, statErr := os.Lstat(c.Abs)
		switch {
		case errors.Is(statErr, fs.ErrNotExist):
			if c.Base == nil {
				continue // staged by hand, then removed: nothing of lore's changed
			}
			c.Kind = HandDeleted
		case statErr != nil:
			return HandReport{}, errs.FileIO("read", c.Abs, statErr)
		case c.Base == nil:
			c.Kind = HandAdded
		default:
			c.Kind = HandModified
		}
		rep.Rows = append(rep.Rows, c)
	}
	sort.Slice(rep.Rows, func(i, j int) bool { return rep.Rows[i].Path < rep.Rows[j].Path })
	sort.Strings(rep.Strays)
	return rep, nil
}

// headBases fills each change's Base with the file as commit head holds
// it: one `git ls-tree` of the changed files' folders for their blob ids,
// then one `git cat-file --batch` of those ids (owner item 27: after a
// migration of 5,000 rows every command read each changed row with its own
// git process until the files were committed; a batch of "HEAD:<path>"
// names is no cure either, git walks the 5,000-entry tree once per name).
func (b *Backend) headBases(ctx context.Context, head string, changed []HandChange) error {
	if head == "" || len(changed) == 0 {
		return nil
	}
	var dirs []string
	for _, c := range changed {
		if d := path.Dir(c.Path); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	args := append([]string{"--literal-pathspecs", "ls-tree", "-r", "-z", "--full-tree", head, "--"}, dirs...)
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		return errs.Wrap(errs.Internal, "git: list the committed row files of "+b.work, err)
	}
	oids := map[string]string{} // path -> blob id
	for _, e := range splitZ(out) {
		meta, p, ok := strings.Cut(e, "\t")
		if f := strings.Fields(meta); ok && len(f) == lsTreeFields && f[1] == "blob" {
			oids[p] = f[2]
		}
	}
	var names []string
	for _, c := range changed {
		if oid, ok := oids[c.Path]; ok {
			names = append(names, oid)
		}
	}
	blobs, err := b.readBlobs(ctx, names)
	if err != nil {
		return err
	}
	for i := range changed {
		if blob, ok := blobs[oids[changed[i].Path]]; ok {
			changed[i].Base = append([]byte{}, blob...) // an empty committed file is still committed (non-nil)
		}
	}
	return nil
}

// lsTreeFields is the field count before the tab of a `git ls-tree` line:
// "<mode> <type> <oid>".
const lsTreeFields = 3

// lorePath reports whether a checkout path is one lore keeps for itself
// (its marker and README are store files, never strays; a folder store's
// state folder; a repo's .lore files outside .lore/data).
func lorePath(p string) bool {
	switch {
	case p == rowfs.MarkerFile, p == rowfs.ReadmeFile, p == ".gitignore", p == ".gitattributes":
		return true
	case strings.HasPrefix(p, rowfs.StoreStateDir+"/"):
		return true
	}
	return false
}

// HeadCommit is the commit HEAD names ("" before the first commit or when
// it cannot be read): the pick-up prints the commit it made.
func (b *Backend) HeadCommit(ctx context.Context) string {
	h, err := b.head(ctx)
	if err != nil {
		return ""
	}
	return h
}

// Unstage empties the index of everything a person staged but did not
// commit, keeping every file as it is, so lore's next commit holds only
// the files it stages itself (a person's `git add` of a refused file
// must never ride along).
func (b *Backend) Unstage(ctx context.Context) error {
	if h, err := b.head(ctx); err != nil || h == "" {
		return err
	}
	if b.mode == modeClone {
		if err := b.refusePersonGitOp(ctx, b.work); err != nil {
			return err // B2-9: git reset ends a merge in progress
		}
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"reset", "--quiet"}}); err != nil {
		return errs.Wrap(errs.Internal, "git: unstage the files staged by hand in "+b.work, err)
	}
	return nil
}

// RestoreHead puts a row file back as the last commit holds it (the
// pick-up of a hand delete or move: lore's own delete or move then runs
// on the committed row). PutBack undoes it.
func (b *Backend) RestoreHead(ctx context.Context, c HandChange) error {
	if c.Base == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.Abs), handDirMode); err != nil {
		return errs.FileIO("create", filepath.Dir(c.Abs), err)
	}
	if err := os.WriteFile(c.Abs, c.Base, handFileMode); err != nil {
		return errs.FileIO("write", c.Abs, err)
	}
	return nil
}

// PutBack leaves the file as the person left it: hand (nil = no file).
func PutBack(c HandChange, hand []byte) error {
	if hand == nil {
		if err := os.Remove(c.Abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return errs.FileIO("remove", c.Abs, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.Abs), handDirMode); err != nil {
		return errs.FileIO("create", filepath.Dir(c.Abs), err)
	}
	if err := os.WriteFile(c.Abs, hand, handFileMode); err != nil {
		return errs.FileIO("write", c.Abs, err)
	}
	return nil
}

// setAsideHandEdits stashes the tracked files that still differ from the
// last commit (hand edits the pick-up refused, or one made while lore
// ran) before a sync merges, and returns the step that puts them back.
// Untracked files are left where they are: a merge never touches them
// unless it would create the same path, and git refuses that itself. A
// clean checkout stashes nothing.
func (b *Backend) setAsideHandEdits(ctx context.Context) (putBack func() error, err error) {
	putBack = func() error { return nil }
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"status", "--porcelain", "-z", "--untracked-files=no"}})
	if err != nil {
		return putBack, errs.Wrap(errs.Internal, "git: read the worktree status", err)
	}
	if len(splitZ(out)) == 0 {
		return putBack, nil
	}
	var files []string
	for _, f := range splitZ(out) {
		if len(f) > len("XY ") {
			files = append(files, f[3:])
		}
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return putBack, err
	}
	if _, err = b.git.run(ctx, call{dir: b.work, env: env, args: []string{"stash", "push", "--quiet", "-m", handStashMessage}}); err != nil {
		return putBack, errs.Wrap(errs.Internal, "git: set the files changed by hand aside before the merge", err).
			WithHint("commit or restore them in " + b.work + " (`git -C " + b.work + " status`), then run `lore sync`")
	}
	return func() error {
		if _, perr := b.git.run(ctx, call{dir: b.work, env: env, args: []string{"stash", "pop", "--quiet"}}); perr != nil {
			return errs.New(errs.Stale, "git: the store changed under files you edited by hand ("+strings.Join(files, ", ")+"); they are kept in the clone's git stash").
				WithHint("settle them: git -C " + b.work + " stash pop, fix each file, then run any lore command")
		}
		return nil
	}, nil
}

// Modes of a row file the pick-up puts back: a committed tree's.
const (
	handDirMode  = 0o755
	handFileMode = 0o644
)

// handStashMessage names the stash a sync makes of refused hand edits.
const handStashMessage = "lore: hand edits set aside while lore merged the store"
