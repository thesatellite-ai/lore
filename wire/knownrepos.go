package wire

// knownrepos.go — the repos lore has run in on this machine (ONBOARDING_DESIGN
// 6.1, Q11): one known_repos row per clone path in the machine store, so
// `lore doctor --all` checks the repos a person actually works in instead of
// walking a large projects tree. The folder scan to doctor.scan_depth is
// only the fallback for a machine whose list is still empty.
//
//	command in a repo ─▶ App.TouchKnownRepo ─▶ no repo id: nothing
//	                     ─▶ row for this root seen < KnownRepoTouchEvery ago: nothing (no write)
//	                     ─▶ else update last_seen (and repo_id) or create the row
//	doctor --all ─▶ App.KnownReposUnder(globs) ─▶ rows under the globs
//	              └▶ table empty ─▶ ScanLoreRepos(bases, depth) ─▶ folders holding .lore/

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// KnownRepoTouchEvery is how stale a known_repos row may get before a
// command refreshes it: every command touches the repo, so without the
// throttle each one would write a machine row (Q11 "cheaply"). An hour is
// fine-grained enough for doctor, which only needs "lore runs here".
const KnownRepoTouchEvery = time.Hour

// TouchKnownRepo records that lore ran in this checkout: one upsert keyed
// by the root, skipped when the row was refreshed less than
// KnownRepoTouchEvery ago. A checkout with no repo id (not initialised)
// records nothing. The touch is bookkeeping, never part of the command's
// bulk action (lore batch undo must not take it back), as for sessions.
//
// Call it once per process, after the command ran. It writes through the
// state the command already opened (the full one, else the repo-only one
// the store operations use; both attach the machine store) and does
// nothing when the command opened neither (`lore version`, help, a Local
// op), so it never opens a store of its own. A state opened for row reads
// only (App.rowState: `lore whoami`, `lore store list`) is skipped too: its
// format answer is not read yet, and the touch's write would refresh every
// cache the command was spared (B-3); the next command that opens fully
// records the repo. Its error is for the caller to drop or log, never to
// fail the command with.
func (a *App) TouchKnownRepo(ctx context.Context) error {
	a.mu.Lock()
	st := a.st
	if st == nil {
		st = a.cfg
	}
	rowsOnly := st != nil && st.refreshPending
	a.mu.Unlock()
	if st == nil || rowsOnly {
		return nil
	}
	meta, exists, err := rowfs.ReadRepoMeta(st.repoTop)
	if err != nil || !exists || meta.RepoID == "" {
		return err
	}
	return a.touchOn(ctx, st, onboard.KnownRepo{RepoID: meta.RepoID, Root: st.repoTop})
}

// TouchRepo is the throttled upsert behind TouchKnownRepo for an explicit
// repo (setup.Store.TouchRepo), opening the app when needed: keyed by
// r.Root (made absolute, symlinks resolved), at r.LastSeen, or the app's
// clock when that is zero.
func (a *App) TouchRepo(ctx context.Context, r onboard.KnownRepo) error {
	if r.RepoID == "" || r.Root == "" {
		return errs.New(errs.InvalidInput, "known repo: repo id and root are required").WithHint("run lore init in the repo first")
	}
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	return a.touchOn(ctx, st, r)
}

// touchOn is the upsert on st's engine. A row refreshed less than
// KnownRepoTouchEvery before the touch, with the same repo id, is left
// alone. A checkout whose format still waits on `lore migrate` (O6) and
// a state without a machine store write nothing.
func (a *App) touchOn(ctx context.Context, st *state, r onboard.KnownRepo) error {
	if st.format.check() != nil || !st.eng.HasMachineStore() {
		return nil
	}
	repoID, root, now := r.RepoID, resolvedPath(r.Root), r.LastSeen
	if now.IsZero() {
		now = a.opts.Wall()
	}
	// Machine rows write no actor row (P3-LOCAL-1), so the engine is
	// called directly, as App.Create does for the machine store.
	ctx = engine.WithBatch(ctx, engine.Batch{})
	rows, err := st.eng.List(ctx, store.SourceMachine, schema.TableKnownRepos)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(rows, func(r row.Row) bool { return !r.Deleted && fieldText(r, schema.FieldKnownRepoRoot) == root })
	seen := value.DateTimeFrom(now)
	if i < 0 {
		fields := map[string]value.Value{schema.FieldKnownRepoLastSeen: seen}
		if err := setTexts(fields, map[string]string{schema.FieldKnownRepoID: repoID, schema.FieldKnownRepoRoot: root}); err != nil {
			return err
		}
		_, err = st.eng.Create(ctx, store.SourceMachine, schema.TableKnownRepos, fields)
		return err
	}
	cur := rows[i]
	if last := fieldTime(cur, schema.FieldKnownRepoLastSeen); !last.IsZero() && now.Sub(last) < KnownRepoTouchEvery && fieldText(cur, schema.FieldKnownRepoID) == repoID {
		return nil
	}
	patch := map[string]value.Value{schema.FieldKnownRepoLastSeen: seen}
	if err := setTexts(patch, map[string]string{schema.FieldKnownRepoID: repoID}); err != nil {
		return err
	}
	_, err = st.eng.Update(ctx, engine.Ref{Store: store.SourceMachine, Table: schema.TableKnownRepos, ID: cur.ID}, patch)
	return err
}

// KnownRepos lists every known_repos row, by root (setup.Store.KnownRepos).
func (a *App) KnownRepos(ctx context.Context) ([]onboard.KnownRepo, error) {
	m, err := a.machineApp(ctx) // doctor --all from ~ (Q11)
	if err != nil {
		return nil, err
	}
	eng, err := m.Engine(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := eng.List(ctx, store.SourceMachine, schema.TableKnownRepos)
	if err != nil {
		return nil, err
	}
	out := make([]onboard.KnownRepo, 0, len(rows))
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		out = append(out, onboard.KnownRepo{RepoID: fieldText(r, schema.FieldKnownRepoID), Root: fieldText(r, schema.FieldKnownRepoRoot),
			LastSeen: fieldTime(r, schema.FieldKnownRepoLastSeen)})
	}
	slices.SortFunc(out, func(x, y onboard.KnownRepo) int { return strings.Compare(x.Root, y.Root) })
	return out, nil
}

// FolderMatch reports whether a repo root lies in a setup's folder glob.
// The glob rules (absolute, ~ expanded, ** any depth, symlinks resolved)
// belong to logic/onboard (ONB 3.3); the caller passes that matcher so
// doctor and setup resolution can never disagree on a folder.
type FolderMatch func(glob, root string) bool

// KnownReposUnder is the repos doctor --all checks for globs (Q11): the
// known_repos rows whose root match accepts for some glob; only when the
// table is empty, the folders under each glob's literal base holding a
// .lore/ folder, scanDepth levels deep (doctor.scan_depth). scanned is
// true when the list came from the scan.
func (a *App) KnownReposUnder(ctx context.Context, globs []string, match FolderMatch, scanDepth int) (repos []onboard.KnownRepo, scanned bool, err error) {
	all, err := a.KnownRepos(ctx)
	if err != nil {
		return nil, false, err
	}
	if len(all) == 0 {
		bases := make([]string, 0, len(globs))
		for _, g := range globs {
			bases = append(bases, GlobBase(g))
		}
		if all, err = ScanLoreRepos(ctx, bases, scanDepth); err != nil {
			return nil, true, err
		}
		scanned = true
	}
	for _, r := range all {
		if slices.ContainsFunc(globs, func(g string) bool { return match(g, r.Root) }) {
			repos = append(repos, r)
		}
	}
	return repos, scanned, nil
}

// globMeta are the characters that end a glob's literal base.
const globMeta = "*?["

// GlobBase is the literal folder a glob starts from: its path segments
// before the first one holding a wildcard (`/w/agency/**` → `/w/agency`).
// The glob is expected absolute with ~ already expanded.
func GlobBase(glob string) string {
	segs := strings.Split(filepath.ToSlash(filepath.Clean(glob)), "/")
	n := slices.IndexFunc(segs, func(s string) bool { return strings.ContainsAny(s, globMeta) })
	if n < 0 {
		n = len(segs)
	}
	base := strings.Join(segs[:n], "/")
	if base == "" {
		base = "/"
	}
	return filepath.FromSlash(base)
}

// ScanLoreRepos walks each base up to depth folder levels below it (0: the
// base only) and returns every folder holding a .lore/ folder, symlinks
// resolved, each once, by root. Hidden folders and the inside of a found
// project are not descended into (a project's subfolders belong to it).
// A missing base is skipped; an unreadable folder is skipped too, since
// the scan is a best-effort fallback (Q11).
func ScanLoreRepos(ctx context.Context, bases []string, depth int) ([]onboard.KnownRepo, error) {
	seen := map[string]bool{}
	var out []onboard.KnownRepo
	for _, base := range bases {
		base = filepath.Clean(base)
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, werr error) error {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if werr != nil || !d.IsDir() {
				return nil // unreadable or a file: nothing to find here
			}
			if p != base && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if fi, serr := os.Stat(filepath.Join(p, rowfs.LoreDir)); serr == nil && fi.IsDir() {
				root := resolvedPath(p)
				if !seen[root] {
					seen[root] = true
					m, _, _ := rowfs.ReadRepoMeta(p) // no id yet still lists the folder
					out = append(out, onboard.KnownRepo{RepoID: m.RepoID, Root: root})
				}
				return filepath.SkipDir
			}
			if levels(base, p) >= depth {
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, errs.Wrap(errs.Internal, "scan "+base+" for lore repos", err)
		}
	}
	slices.SortFunc(out, func(x, y onboard.KnownRepo) int { return strings.Compare(x.Root, y.Root) })
	return out, nil
}

// levels is how many folders p lies below base.
func levels(base, p string) int {
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(filepath.ToSlash(rel), "/") + 1
}

// resolvedPath is p absolute with symlinks resolved (ONB 3.3 compares
// paths that way). When p, or part of it, does not exist yet (a dangling
// link's end, a folder about to be created), its deepest existing folder
// is resolved and the missing names are kept after it, so the result still
// compares with a resolved root. Before, such a p came back unresolved:
// on macOS, where the temp and many home folders sit under a link
// (/var -> /private/var), a path inside the repository then looked outside
// it. p as given (made absolute) when nothing of it resolves.
func resolvedPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	var tail []string
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{r}, tail...)...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}
