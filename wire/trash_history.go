package wire

// PurgedHistory: the git side of `lore trash --from-history` (DELETION
// 3.6). Why wire runs git itself: it is a read of the checkouts' own
// history (like the live-ref reads in gitexec.go), not a backend write.
//
// Places walked (P3-DEL-HIST-1: the repo checkout, every attached git
// store's clone and the live refs; nothing is fetched, so the walk works
// offline and reads what this machine has):
//
//	repo checkout (.lore/data)            ─┐
//	each git store clone (its data folder) ─┤ branch walk:
//	  rev-parse --is-shallow-repository ─► shallow: E_UNSUPPORTED, stop (3.6)
//	  log --relative --diff-filter=DM --name-status -- <data>/  (newest first)
//	    ├► D path, file absent now      ─► removed by a commit (a git rm, or an
//	                                        older lore's hard delete): the commit's
//	                                        author, and the git checkout that
//	                                        brings the file back (H-14)
//	    └► M path, file now a tombstone ─► purge (purger from the tombstone)
//	    first (newest) hit per path wins
//	  show <commit>^:./<path>           ─► the last content (none: skipped)
//	live refs (liveSources: refs/lore/live, its tracking copy, each git
//	store's live/<branch> refs)          ─► live walk:
//	  log --diff-filter=DM --name-status <ref> -- <folder>/
//	    a live-kind row (liveTables) gone from its store now (absent, or a
//	    tombstone: purger from it) and not already found by a branch walk
//	  cat-file blob <commit>^:<path>    ─► the last live content, as a row

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/store"
)

// Git output markers of the history walk.
const (
	histCommitMark = "\x00"                    // starts a commit header line (%x00%H %cI %an)
	histFormat     = "--format=%x00%H %cI %an" // commit id, committer date (strict ISO), author name
	histDeleted    = "D"
	histModified   = "M"
	histShallow    = "true"
	histNoCommits  = "does not have any commits"
)

// historyCheckout is a git checkout whose branch history holds a data
// folder: the repo checkout or a git store's clone.
type historyCheckout interface {
	Dir() string
	DataPath() string
	CloneMode() bool
}

// histKey identifies one found row across walks (a branch hit wins over a
// live hit of the same row).
type histKey struct {
	src   store.Source
	table row.Table
	id    string
}

// PurgedHistory implements opstrash.HistoryReader: the repo checkout's
// history, then every attached git store's clone, then the live refs.
func (a *App) PurgedHistory(ctx context.Context) ([]opstrash.HistoryRow, error) {
	dir := a.opts.RepoDir
	if dir == "" {
		return nil, errs.New(errs.Unsupported, "trash --from-history: no repo checkout here").WithHint("run it inside the repository")
	}
	seen := map[histKey]bool{}
	rows, err := branchHistory(ctx, store.SourceRepo, dir, path.Join(rowfs.LoreDir, rowfs.DataDir), "", seen)
	if err != nil {
		return nil, err
	}
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range st.eng.Stores() {
		if name == store.SourceRepo {
			continue
		}
		gc, ok := concrete(st.byName[name]).(historyCheckout)
		if !ok || !gc.CloneMode() {
			continue // a plain folder store keeps no history (nothing to read)
		}
		got, berr := branchHistory(ctx, name, gc.Dir(), gc.DataPath(), "git -C "+gc.Dir()+" fetch --unshallow", seen)
		if berr != nil {
			return nil, berr
		}
		rows = append(rows, got...)
	}
	srcs, serr := liveSources(ctx, st)
	if serr != nil {
		return nil, serr
	}
	for _, src := range srcs {
		if src.dir == "" {
			continue
		}
		got, lerr := liveHistory(ctx, st, src, seen)
		if lerr != nil {
			return nil, lerr
		}
		rows = append(rows, got...)
	}
	return rows, nil
}

// branchHistory walks one checkout's branch history of its data folder
// (data, relative to dir). unshallow is the hint of a shallow clone ("" =
// the repo's plain one).
func branchHistory(ctx context.Context, src store.Source, dir, data, unshallow string, seen map[histKey]bool) ([]opstrash.HistoryRow, error) {
	sh, err := gitRun{dir: dir, args: []string{"rev-parse", "--is-shallow-repository"}}.output(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.Unsupported, "trash --from-history: "+dir+" is not a git checkout", err)
	}
	if strings.TrimSpace(string(sh)) == histShallow {
		if unshallow == "" {
			unshallow = "git fetch --unshallow"
		}
		return nil, errs.New(errs.Unsupported, "trash --from-history: "+dir+" is a shallow clone; its history is incomplete").
			WithHint(unshallow + ", then rerun")
	}
	data = strings.TrimSuffix(data, "/") + "/"
	// The checkout's place in its work tree: the printed `git checkout`
	// names the file from the top (:/<prefix><path>), so it works from any
	// folder of the clone (H-14).
	prefix, _ := gitRun{dir: dir, args: []string{"rev-parse", "--show-prefix"}}.output(ctx)
	w := histWalk{src: src, dir: dir, data: data, prefix: strings.TrimSpace(string(prefix)), store: unshallow != ""}
	// --relative: paths come relative to dir (a repo in a monorepo
	// subfolder, a store folder), which is what readUnder and ./<path> use.
	out, err := gitRun{dir: dir, args: []string{"log", "--relative", "--no-renames", "--diff-filter=DM", "--name-status", histFormat, "--", data}}.output(ctx)
	if err != nil {
		if strings.Contains(err.Error(), histNoCommits) {
			return nil, nil
		}
		return nil, err
	}
	paths := map[string]bool{}
	var rows []opstrash.HistoryRow
	// A purge (or hard delete) not committed yet: the content is still in
	// HEAD, so the regret right after a purge can read it (DELETION 3.6:
	// the history read is the way back to a purged row's text).
	for _, c := range uncommittedChanges(ctx, dir, data) {
		paths[c.path] = true
		hr, keep := w.row(ctx, c)
		if !keep || seen[histKey{src, hr.Table, hr.ID}] {
			continue
		}
		seen[histKey{src, hr.Table, hr.ID}] = true
		rows = append(rows, hr)
	}
	for c := range historyChanges(string(out)) {
		if paths[c.path] {
			continue
		}
		paths[c.path] = true
		hr, keep := w.row(ctx, c)
		if !keep || seen[histKey{src, hr.Table, hr.ID}] {
			continue
		}
		seen[histKey{src, hr.Table, hr.ID}] = true
		rows = append(rows, hr)
	}
	return rows, nil
}

// histWalk is one checkout's branch walk: where it reads and how a
// printed git command names its files.
type histWalk struct {
	src       store.Source
	dir, data string
	// prefix is the checkout folder's path from the top of its work tree
	// (git rev-parse --show-prefix, "" at the top).
	prefix string
	// store: the checkout is a store's clone, not the repo the person is
	// in, so the printed git command runs there (git -C <dir>).
	store bool
}

// restoreCmd is the git command that puts the file p (relative to the
// checkout) back as it was in rev (H-14).
func (w histWalk) restoreCmd(rev, p string) string {
	cmd := "git checkout " + rev + " -- :/" + w.prefix + p
	if w.store {
		cmd = "git -C " + w.dir + " checkout " + rev + " -- :/" + w.prefix + p
	}
	return cmd
}

// histChange is one D or M path of a `git log --name-status` walk.
// uncommitted: the change is in the working tree, not in commit (HEAD),
// so the content is commit's own, not its parent's.
type histChange struct {
	status, path, commit string
	at                   time.Time
	// author is the commit's author name ("" for an uncommitted change).
	author      string
	uncommitted bool
}

// uncommittedChanges is the D and M row paths of the data folder changed
// in the working tree or index since HEAD (a purge or delete not yet
// committed), dated by HEAD. None when HEAD does not exist or git fails.
func uncommittedChanges(ctx context.Context, dir, data string) []histChange {
	head, err := gitRun{dir: dir, args: []string{"log", "-1", histFormat, "HEAD"}}.output(ctx)
	if err != nil {
		return nil
	}
	sha, at, _ := commitHeader(strings.TrimPrefix(strings.TrimSpace(string(head)), histCommitMark))
	out, err := gitRun{dir: dir, args: []string{"diff", "--relative", "--no-renames", "--diff-filter=DM", "--name-status", "HEAD", "--", data}}.output(ctx)
	if err != nil {
		return nil
	}
	var cs []histChange
	for line := range strings.SplitSeq(string(out), "\n") {
		status, p, ok := strings.Cut(line, "\t")
		if !ok || (status != histDeleted && status != histModified) {
			continue
		}
		cs = append(cs, histChange{status: status, path: p, commit: sha, at: at, uncommitted: true})
	}
	return cs
}

// historyChanges yields the D and M entries of a log printed with
// histFormat and --name-status, newest commit first.
func historyChanges(out string) func(yield func(histChange) bool) {
	return func(yield func(histChange) bool) {
		var commit, author string
		var at time.Time
		for line := range strings.SplitSeq(out, "\n") {
			if h, ok := strings.CutPrefix(line, histCommitMark); ok {
				commit, at, author = commitHeader(h)
				continue
			}
			status, p, ok := strings.Cut(line, "\t")
			if !ok || (status != histDeleted && status != histModified) {
				continue
			}
			if !yield(histChange{status: status, path: p, commit: commit, at: at, author: author}) {
				return
			}
		}
	}
}

// commitHeader splits one histFormat header ("<sha> <date> <author>",
// the mark cut off).
func commitHeader(h string) (sha string, at time.Time, author string) {
	sha, rest, _ := strings.Cut(h, " ")
	date, author, _ := strings.Cut(rest, " ")
	at, _ = time.Parse(time.RFC3339, strings.TrimSpace(date))
	return sha, at, strings.TrimSpace(author)
}

// rowFileOf parses <table>/<id>.json; ok=false for side files (_purged.json,
// _meta.json, nested files) and anything that is not a known table's id.
func rowFileOf(rel string) (row.Table, string, bool) {
	table, file := path.Split(rel)
	table = strings.TrimSuffix(table, "/")
	id, isRow := strings.CutSuffix(file, rowfs.RowFileExt)
	if !isRow || strings.Contains(table, "/") || table == "" {
		return "", "", false
	}
	if _, err := ids.ParseAny(id); err != nil {
		return "", "", false
	}
	if !rowfs.IsTableDir(table) { // in a store, any other folder name is a store folder
		return "", "", false
	}
	return row.Table(table), id, true
}

// row builds one row of a branch walk, or keep=false when the path is
// not a purged or removed row file (a live row, a side file, no content).
func (w histWalk) row(ctx context.Context, c histChange) (opstrash.HistoryRow, bool) {
	dir := w.dir
	tb, id, ok := rowFileOf(strings.TrimPrefix(c.path, w.data))
	if !ok {
		return opstrash.HistoryRow{}, false
	}
	hr := opstrash.HistoryRow{Store: w.src, Table: tb, ID: id, Commit: c.commit, CommitAt: c.at, Uncommitted: c.uncommitted}
	cur, rerr := readUnder(dir, c.path) // P2-LINT-5: a git-listed path, read through os.Root (no escape)
	switch {
	case c.status == histDeleted && rerr == nil:
		return hr, false // re-added since: not gone
	case c.status == histDeleted:
		// H-14: the file was removed by a commit (a git rm, or an older
		// lore's hard delete), not purged: its commit's author removed it
		// and git brings it back.
		hr.Removed, hr.RemovedBy = true, c.author
		hr.GitRestore = w.restoreCmd(shortCommit(c.commit)+"^", c.path)
		if c.uncommitted {
			hr.GitRestore = w.restoreCmd("HEAD", c.path)
		}
	case c.status == histModified:
		if rerr != nil {
			return hr, false
		}
		r, derr := row.Decode(cur)
		if derr != nil || !r.Deleted {
			return hr, false // a live (or trashed) row: not purged
		}
		hr.PurgedAt, hr.PurgedBy = r.UpdatedAt, r.UpdatedBy.String()
	}
	from := c.commit + "^"
	if c.uncommitted {
		from = c.commit // the removal is not committed: the content is in HEAD itself
	}
	doc, gerr := gitRun{dir: dir, args: []string{"show", from + ":./" + c.path}}.output(ctx)
	if gerr != nil {
		return hr, false // the parent has no content (a root commit, or created and removed in one commit)
	}
	hr.Doc = doc
	return hr, true
}

// liveHistory walks the live refs of one live source. A file removed from
// a live ref is usually a row that landed on its branch, so a row counts
// only when its store does not hold it now (absent, or a tombstone);
// rows of tables that never ride live are left out (their store could
// never hold them, so "absent" would mean nothing).
func liveHistory(ctx context.Context, st *state, src liveSource, seen map[histKey]bool) ([]opstrash.HistoryRow, error) {
	b, attached := st.byName[src.name]
	if !attached {
		return nil, nil
	}
	want := liveTables()
	var rows []opstrash.HistoryRow
	for _, ref := range src.refs {
		if !liveRefExists(ctx, src.dir, ref) {
			continue
		}
		args := []string{"log", "--no-renames", "--diff-filter=DM", "--name-status", histFormat, ref}
		if src.folder != "" {
			args = append(args, "--", src.folder+"/")
		}
		out, err := liveGit(ctx, src.dir, args...)
		if err != nil {
			return nil, err
		}
		for c := range historyChanges(string(out)) {
			table, ok := liveRowTable(c.path)
			if !ok || !want[table] || (src.folder != "" && liveRowFolder(c.path) != src.folder) {
				continue
			}
			tb, id, ok := rowFileOf(table.String() + "/" + path.Base(c.path))
			if !ok || seen[histKey{src.name, tb, id}] {
				continue
			}
			hr, keep, err := liveHistRow(ctx, b, src, ref, tb, id, c)
			if err != nil {
				return nil, err
			}
			if keep {
				seen[histKey{src.name, tb, id}] = true
				rows = append(rows, hr)
			}
		}
	}
	return rows, nil
}

// liveHistRow builds one row of a live walk: keep=false when the store holds
// the row now, or the parent commit has no readable live copy of it.
func liveHistRow(ctx context.Context, b store.Backend, src liveSource, ref string, tb row.Table, id string, c histChange) (opstrash.HistoryRow, bool, error) {
	aid, err := ids.ParseAny(id)
	if err != nil {
		return opstrash.HistoryRow{}, false, nil
	}
	hr := opstrash.HistoryRow{Store: src.name, Table: tb, ID: id, Commit: c.commit, CommitAt: c.at, Ref: ref}
	sr, gerr := b.Get(ctx, tb, aid)
	switch {
	case errors.Is(gerr, store.ErrNotFound):
	case gerr != nil:
		return hr, false, gerr
	case !sr.Deleted:
		return hr, false, nil // landed (or still there): not gone
	default:
		if t, derr := sr.Decode(); derr == nil {
			hr.PurgedAt, hr.PurgedBy = t.UpdatedAt, t.UpdatedBy.String()
		}
	}
	blob, berr := liveGit(ctx, src.dir, "cat-file", "blob", c.commit+"^:"+c.path)
	if berr != nil {
		return hr, false, nil // created and removed in one commit, or a root commit
	}
	r, _, derr := decodeLiveRow(blob)
	if derr != nil || r.Table != tb || r.ID != aid || r.Deleted {
		return hr, false, nil // never guess a version
	}
	if hr.Doc, err = row.Encode(r); err != nil {
		return hr, false, nil
	}
	return hr, true, nil
}

// readUnder reads the slash path rel inside dir through os.Root, so a
// path git lists can never leave the checkout (P2-LINT-5).
func readUnder(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }() // a read-only handle: a close error changes nothing read
	return root.ReadFile(rel)
}
