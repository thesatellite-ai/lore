package git

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// OpenRepo opens the built-in store `repo`: the code repository that holds
// dir, in repo mode. Lore writes row files under the repository's top
// level (<top>/<folder>/.lore/data) and never commits; the Read cursor is
// rowfs's stat cursor, so a row changed but not committed is seen as
// readily as one pulled by `git pull`. The store lock is <top>/.lore/lock.
// dir outside a git work tree is store.ErrNotAStore.
func OpenRepo(ctx context.Context, dir, folder string, o store.OpenOptions, opts ...Option) (*Backend, error) {
	cfg := build(opts)
	bin, err := lookGit(cfg.bin)
	if err != nil {
		return nil, err
	}
	g := runner{bin: bin}
	out, err := g.run(ctx, call{dir: dir, args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		if _, ran := exitCode(err); ran {
			return nil, errs.Wrap(errs.NotAStore, "git: the folder is not inside a git work tree", store.ErrNotAStore)
		}
		return nil, errs.Wrap(errs.Internal, "git: run git", err)
	}
	top := filepath.FromSlash(trimmed(out))
	root, err := rowfs.Open(top, rowfs.WithLockTimeout(cfg.lockTimeout), rowfs.WithClock(cfg.now))
	if err != nil {
		return nil, err
	}
	f, err := root.Folder(folder)
	if err != nil {
		return nil, err
	}
	return &Backend{cfg: cfg, git: g, mode: modeRepo, readOnly: o.ReadOnly, root: root, folder: f, work: root.Dir()}, nil
}

// Sync implements store.Backend. Repo mode has nothing to do (the person
// pushes their code); clone mode fetches, merges and pushes (sync.go).
func (b *Backend) Sync(ctx context.Context) (store.SyncReport, error) {
	if err := b.usable(ctx); err != nil {
		return store.SyncReport{}, err
	}
	if b.mode == modeRepo {
		return store.SyncReport{}, nil
	}
	return b.syncClone(ctx)
}
