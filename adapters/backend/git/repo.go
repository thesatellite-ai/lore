package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// OpenRepo opens the built-in store `repo`: the code repository that holds
// dir, in repo mode. Lore writes row files under the lore project's folder
// (projectRoot: the nearest folder holding .lore from dir up to the
// repository's top level, else the top; <root>/<folder>/.lore/data) and
// never commits; the Read cursor is rowfs's stat cursor, so a row changed
// but not committed is seen as readily as one pulled by `git pull`. The
// store lock is <root>/.lore/lock.
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
	root, err := rowfs.Open(projectRoot(dir, top, o.Home), rowfs.WithLockTimeout(cfg.lockTimeout), rowfs.WithLockTimeout(o.LockTimeout), rowfs.WithClock(cfg.now), rowfs.WithAllowNetworkFS(o.AllowNetworkFS))
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

// projectRoot is the repo store's root: the nearest folder from dir up to
// the work tree top that holds a .lore folder, else top. Why: in a
// monorepo the lore project may live in a subfolder (mono/svc/.lore), and
// its rows belong under that folder's .lore/data, as v0.1.17 wrote them,
// not under the top's (which may hold another project or none). Git still
// works from there: repo mode only reads and writes files. The lore home
// (~/.lore, when the work tree top is above it, a dotfiles repo) is never
// taken for a project.
func projectRoot(dir, top, home string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return top
	}
	if real, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = real
	}
	homeLore := ""
	if home != "" {
		homeLore = filepath.Clean(home)
		if real, rerr := filepath.EvalSymlinks(homeLore); rerr == nil {
			homeLore = real
		}
	}
	for d := abs; ; d = filepath.Dir(d) {
		rel, rerr := filepath.Rel(top, d)
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return top // dir is outside the work tree top (a symlinked path): keep top
		}
		lore := filepath.Join(d, rowfs.LoreDir)
		if fi, serr := os.Stat(lore); serr == nil && fi.IsDir() && lore != homeLore {
			return d
		}
		if rel == "." || filepath.Dir(d) == d {
			return top
		}
	}
}
