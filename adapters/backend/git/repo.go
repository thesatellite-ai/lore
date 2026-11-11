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
// notAWorkTreeHint is the next step of E_NOT_A_STORE outside git
// (INTEG-MIGREST-2): a project lives in a git checkout.
const notAWorkTreeHint = "cd into a git checkout, or run git init here first"

func OpenRepo(ctx context.Context, dir, folder string, o store.OpenOptions, opts ...Option) (*Backend, error) {
	cfg := build(opts)
	g, dirRoot, err := repoRoot(ctx, cfg, dir, o.Home)
	if err != nil {
		return nil, err
	}
	root, err := rowfs.Open(dirRoot, rowfs.WithSharedModes(), rowfs.WithLockTimeout(cfg.lockTimeout), rowfs.WithLockTimeout(o.LockTimeout), rowfs.WithClock(cfg.now), rowfs.WithAllowNetworkFS(o.AllowNetworkFS))
	if err != nil {
		return nil, err
	}
	f, err := root.Folder(folder)
	if err != nil {
		return nil, err
	}
	return &Backend{cfg: cfg, git: g, mode: modeRepo, readOnly: o.ReadOnly, root: root, folder: f, work: root.Dir()}, nil
}

// MarkReadOnly makes the handle refuse every write from now on
// (store.ErrReadOnly), as OpenOptions.ReadOnly does at open: for a repo
// that turns out, once open, to be one lore must never write into (a
// client repo linked by its remote, with no lore data of its own). Call
// it right after the open, before the handle is shared: readOnly is read
// without the lock, as the open set it.
func (b *Backend) MarkReadOnly() { b.readOnly = true }

// RepoRoot is the folder OpenRepo would make the repo store's root for dir
// (projectRoot: the nearest folder holding .lore up to the work tree top,
// symlinks resolved), found without opening anything: no rowfs root, no
// lock, nothing created. A caller that only needs the path (which
// .lore/stores.yaml applies here, the org link of this repo) uses it so a
// command that changes a store, not this repo, leaves the repo as it was.
// dir outside a git work tree is E_NOT_A_STORE, as for OpenRepo.
func RepoRoot(ctx context.Context, dir, home string, opts ...Option) (string, error) {
	_, root, err := repoRoot(ctx, build(opts), dir, home)
	return root, err
}

// repoRoot is RepoRoot with the git runner OpenRepo goes on with.
func repoRoot(ctx context.Context, cfg config, dir, home string) (runner, string, error) {
	bin, err := lookGit(cfg.bin)
	if err != nil {
		return runner{}, "", err
	}
	g := runner{bin: bin}
	out, err := g.run(ctx, call{dir: dir, args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		if _, ran := exitCode(err); ran {
			return runner{}, "", notAWorkTree(ctx, g, dir)
		}
		return runner{}, "", errs.Wrap(errs.Internal, "git: run git", err)
	}
	root := projectRoot(dir, filepath.FromSlash(trimmed(out)), home)
	if real, rerr := filepath.EvalSymlinks(root); rerr == nil {
		root = real // as rowfs.Open resolves it: Backend.Dir() and RepoRoot agree
	}
	return g, root, nil
}

// Where git puts a folder that has no work tree (rev-parse answers).
const (
	revParseTrue    = "true"
	gitDirFolder    = ".git"
	bareCloneHint   = "a bare repository has no checked-out files for lore to keep its data in: clone it (git clone <this folder> <new folder>) and run lore in the clone"
	insideGitDirMsg = "git: the folder is inside a repository's .git folder, not its checked-out files"
	insideGitDirFix = "cd to the checkout that holds this .git folder and run lore there"
)

// notAWorkTree is E_NOT_A_STORE for a dir with no work tree, saying which
// case it is: a bare repository and a .git folder are git repositories,
// so "run git init here first" (the plain-folder hint) would tell the
// person to make a second repository inside one.
func notAWorkTree(ctx context.Context, g runner, dir string) error {
	out, err := g.run(ctx, call{dir: dir, args: []string{"rev-parse", "--is-bare-repository", "--is-inside-git-dir"}})
	if err == nil {
		bare, inGitDir, _ := strings.Cut(trimmed(out), "\n")
		switch {
		case strings.TrimSpace(bare) == revParseTrue:
			return errs.Wrap(errs.NotAStore, "git: "+dir+" is a bare repository (no work tree)", store.ErrNotAStore).
				WithHint(bareCloneHint)
		case strings.TrimSpace(inGitDir) == revParseTrue || filepath.Base(dir) == gitDirFolder:
			return errs.Wrap(errs.NotAStore, insideGitDirMsg, store.ErrNotAStore).WithHint(insideGitDirFix)
		}
	}
	return errs.Wrap(errs.NotAStore, "git: the folder is not inside a git work tree", store.ErrNotAStore).
		WithHint(notAWorkTreeHint)
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
	return b.syncClone(ctx, SyncOptions{})
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
