package git

// seed.go — a folder store copied into an empty git store (devco N-13).
// A personal folder store (~/.lore/stores/_me) holds its rows in the same
// layout a git store does. Moving it to a private git repo used to mean
// "push the folder", but the folder is no git repository, and a set-url
// to the still-empty repo was refused. SeedFrom does the push for the
// person instead:
//
//	SeedFrom(dir)   (the clone of an EMPTY remote: no commit, no file)
//	  ├─ copy every file of dir except its state folder (.lore-state:
//	  │  lock and pending audit journal) and any .git
//	  ├─ one commit, signed like any other write of this clone
//	  └─ left in the clone: the person's next `lore sync` pushes it

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

// seedFileMode is the mode of a file SeedFrom copies (as a write makes).
const seedFileMode = 0o644

// SeedFrom copies the folder store at dir into this clone and commits it.
// Why: it is how `lore store set-url` moves a folder store to an empty
// git repo in one step. The clone must have no commit and no file of its
// own (an empty remote's clone): anything else is E_REFUSED, nothing
// copied. subject is the commit's subject. The commit is local; the
// caller pushes it.
func (b *Backend) SeedFrom(ctx context.Context, dir, subject string) error {
	if err := b.usable(ctx); err != nil {
		return err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return err
	}
	err = b.seedLocked(ctx, dir, subject)
	return releaseKeep(err, lock)
}

// seedLocked is SeedFrom under the clone lock.
func (b *Backend) seedLocked(ctx context.Context, dir, subject string) error {
	if h, _ := b.head(ctx); h != "" {
		return errs.New(errs.Refused, "git: the clone "+b.work+" already has commits; only an empty store is filled from a folder")
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"status", "--porcelain", "--untracked-files=all"}})
	if err != nil {
		return errs.Wrap(errs.Internal, "git: read the clone's status", err)
	}
	if trimmed(out) != "" {
		return errs.New(errs.Refused, "git: the clone "+b.work+" holds files that are not committed; only an empty clone is filled from a folder").
			WithHint("move those files away (or remove the clone folder), then run the command again")
	}
	if err = copyStoreFolder(dir, b.work); err != nil {
		return err
	}
	if _, err = b.git.run(ctx, call{dir: b.work, args: []string{"add", "-A"}}); err != nil {
		return errs.Wrap(errs.Internal, "git: stage the copied store", err)
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return err
	}
	if _, err = b.git.run(ctx, call{dir: b.work, args: []string{"commit", "--quiet", "--no-verify", "-m", b.cfg.withTrailers(subject)}, env: env}); err != nil {
		return errs.Wrap(errs.Internal, "git: commit the copied store", err)
	}
	m, err := rowfs.ReadMarker(b.work)
	if err != nil {
		return err
	}
	b.marker = m
	return nil
}

// copyStoreFolder copies the store files under src into dst: every
// regular file except the store's state folder and any .git (a folder a
// person turned into a repo by hand). Symlinks and other special files
// are skipped: a store holds plain files only.
func copyStoreFolder(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return errs.FileIO("read", p, werr)
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return errs.FileIO("read", p, err)
		}
		if d.IsDir() {
			if rel == rowfs.StoreStateDir || d.Name() == gitDirName {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		to := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(to), cloneDirMode); err != nil {
			return errs.FileIO("create", filepath.Dir(to), err)
		}
		return copyFile(p, to)
	})
}

// copyFile copies one regular file.
func copyFile(from, to string) error {
	in, err := os.Open(filepath.Clean(from))
	if err != nil {
		return errs.FileIO("read", from, err)
	}
	defer func() { _ = in.Close() }() // read-only handle
	out, err := os.OpenFile(filepath.Clean(to), os.O_WRONLY|os.O_CREATE|os.O_EXCL, seedFileMode)
	if err != nil {
		return errs.FileIO("write", to, err)
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return errs.FileIO("write", to, err)
	}
	if err = out.Close(); err != nil {
		return errs.FileIO("write", to, err)
	}
	return nil
}
