package wire

// fixrepos.go — which repos `lore fix --all` fixes (owner item 26: it
// found 10 repos where the setup folders hold about 85 linked ones):
//
//	known_repos rows ─┐
//	setup folders ────┼─▶ walkFixRepos: every git repo (a folder holding
//	                  │   .git) that has .lore/ OR whose remote a store
//	                  │   link names; not descended into once found
//	store clones ─────┘   (~/.lore/stores): their link rows' remotes
//	                  ─▶ a folder with .lore/ that is no git repo (the
//	                     home holding ~/.lore, a setup folder root):
//	                     noted as skipped, never a failure
//
// The walk is bounded: hidden folders, dependency trees (node_modules,
// vendor, …) and the inside of a found repo are not entered, and it stops
// fixWalkDepth levels below a setup folder. A repo nested inside another
// is fixed when lore knows it (a known_repos row): lore runs in it, so it
// was recorded.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
)

// fixWalkDepth is how many folder levels below a setup folder --all looks
// for repos (a projects tree is rarely deeper; the bound keeps a folder
// such as ~ from being walked whole).
const fixWalkDepth = 8

// fixWalkSkip are folder names --all never enters: dependency and build
// trees, which hold vendored repos, never a person's projects.
var fixWalkSkip = map[string]bool{"node_modules": true, "vendor": true, "bower_components": true, "Pods": true, "__pycache__": true}

// Why lines of the folders --all passes over.
const (
	skipNotRepo = "has a .lore folder but is not a git repo (lore fix works on git repos)"
)

// FixRepos are the repo roots of --all and the folders passed over
// (opsfix.Env).
func (a *App) FixRepos(ctx context.Context) (opsfix.Found, error) {
	known, err := a.KnownRepos(ctx)
	if err != nil {
		return opsfix.Found{}, err
	}
	var w fixRepoWalk
	for _, k := range known {
		w.known = append(w.known, k.Root)
	}
	// B4-8: the repo the command runs in is always one of them (a 0.1.17
	// user has no setup folders and no known repos yet, and "0 repo(s)"
	// while inside an unmigrated repo fixed nothing)
	if top := gitTopLevel(ctx, a.projectDir()); top != "" {
		w.known = append(w.known, top)
	}
	if cfg, cerr := ReadSetupsConfig(a.opts.Home); cerr == nil {
		for _, e := range cfg.Enabled() {
			for _, g := range e.Folders {
				w.bases = append(w.bases, GlobBase(onboard.ExpandHome(g, userHome(SetupQuery{}))))
			}
		}
	}
	// B4-8: every repo this machine's lore opened is recorded with the
	// caches it used (~/.lore/cache/repos); those repos are fixed too, so
	// a machine with no setup folders yet (upgraded from 0.1.17) gets the
	// repos lore has run in, and the run says how it chose them
	if recs, rerr := sqlite.ReadUses(filepath.Join(a.opts.Home, cacheDir)); rerr == nil {
		for _, r := range recs {
			if r.Record.Repo != "" {
				w.known = append(w.known, r.Record.Repo)
			}
		}
	}
	var note string
	if len(w.bases) == 0 {
		// lore 0.1.17 kept no list of its repos on the machine (its
		// caches and renders are named by project id only), so with no
		// setup folders the home folder is looked through, a few levels
		// deep, for repos with .lore/
		note = fixNoSetupFolders
		if h := userHome(SetupQuery{}); h != "" {
			w.bases, w.depth = []string{h}, fixHomeDepth
		}
	}
	w.linked = storeLinkedRemotes(ctx, a.opts.Home)
	w.remotes = func(ctx context.Context, dir string) []string {
		norm, _ := orgcfg.Remotes(ctx, dir)
		return norm
	}
	found, err := w.run(ctx)
	found.Note = note
	return found, err
}

// fixNoSetupFolders is --all's note when no setup has folders.
const fixNoSetupFolders = "no setup has folders yet, so lore fix --all took the repo you run it in, the repos lore has run in on this machine (its known repos and cache records) and the repos with .lore/ up to 4 folders below your home folder; add the folder holding your repos to a setup (`lore setup`) to fix every repo under it"

// fixHomeDepth is how deep --all looks below the home folder when no
// setup has folders (~/work/client/repo is three levels).
const fixHomeDepth = 4

// storeLinkedRemotes are the remotes the link rows of every whole store
// clone under home name (no network: what the clones hold now).
func storeLinkedRemotes(ctx context.Context, home string) map[string]bool {
	found, err := git.ScanClones(home)
	if err != nil {
		return nil
	}
	linked := map[string]bool{}
	for _, f := range found {
		if f.State != git.CloneWhole {
			continue
		}
		remotes, _ := orgcfg.CloneLinkRemotes(ctx, f.Paths.Dir) // an unreadable clone links nothing
		for _, r := range remotes {
			linked[r] = true
		}
	}
	return linked
}

// fixRepoWalk is one discovery of --all's repos.
type fixRepoWalk struct {
	// known are the known_repos roots.
	known []string
	// bases are the setup folders' literal bases.
	bases []string
	// linked are the normalised remotes store links name.
	linked map[string]bool
	// remotes reads a repo's normalised remotes (nil: none read).
	remotes func(ctx context.Context, dir string) []string
	// depth bounds the walk below each base (0: fixWalkDepth).
	depth int
}

// run finds the repos: sorted, each once (symlinks resolved), and the
// folders passed over.
func (w fixRepoWalk) run(ctx context.Context) (opsfix.Found, error) {
	depth := w.depth
	if depth <= 0 {
		depth = fixWalkDepth
	}
	var out opsfix.Found
	seen := map[string]bool{}
	skipped := map[string]bool{}
	consider := func(p string) {
		root := resolvedPath(p)
		if seen[root] || skipped[root] {
			return
		}
		switch {
		case isGitTop(root) && (hasLoreDir(root) || w.isLinked(ctx, root)):
			seen[root] = true
			out.Roots = append(out.Roots, root)
		case !isGitTop(root) && hasLoreDir(root):
			skipped[root] = true
			out.Skipped = append(out.Skipped, opsfix.Skip{Path: root, Why: skipNotRepo})
		}
	}
	for _, k := range w.known {
		if k != "" {
			consider(k)
		}
	}
	for _, base := range w.bases {
		base = filepath.Clean(base)
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, werr error) error {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if werr != nil || !d.IsDir() {
				return nil // unreadable or a file
			}
			if p != base && (strings.HasPrefix(d.Name(), ".") || fixWalkSkip[d.Name()]) {
				return filepath.SkipDir
			}
			consider(p)
			if isGitTop(p) || levels(base, p) >= depth {
				return filepath.SkipDir // a repo's folders belong to it
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return opsfix.Found{}, errs.Wrap(errs.Internal, "look for repos in "+base, err)
		}
	}
	slices.Sort(out.Roots)
	slices.SortFunc(out.Skipped, func(x, y opsfix.Skip) int { return strings.Compare(x.Path, y.Path) })
	return out, nil
}

// isLinked reports whether one of dir's remotes is named by a store link.
func (w fixRepoWalk) isLinked(ctx context.Context, dir string) bool {
	if len(w.linked) == 0 || w.remotes == nil {
		return false
	}
	return slices.ContainsFunc(w.remotes(ctx, dir), func(r string) bool { return w.linked[r] })
}

// isGitTop reports whether dir is the top of a git repo: it holds .git
// (a folder, or the file of a worktree or submodule).
func isGitTop(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// hasLoreDir reports whether dir holds a .lore folder.
func hasLoreDir(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, rowfs.LoreDir))
	return err == nil && fi.IsDir()
}
