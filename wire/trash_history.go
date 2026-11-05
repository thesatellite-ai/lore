package wire

// PurgedHistory: the git side of `lore trash --from-history` (DELETION
// 3.6). Why wire runs git itself: it is a read of the repo checkout's own
// history (like the live-ref reads in gitexec.go), not a backend write.
//
//	rev-parse --is-shallow-repository ─► shallow: E_UNSUPPORTED, stop (3.6)
//	log --diff-filter=DM --name-status -- .lore/data/   (newest first)
//	  ├► D path, file absent now      ─► old hard delete (no purger, "?")
//	  └► M path, file now a tombstone ─► purge (purger from the tombstone)
//	  first (newest) hit per path wins
//	show <commit>^:<path>             ─► the last content (none: skipped)

import (
	"context"
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
	histCommitMark = "\x00"                // starts a commit header line (%x00%H %cI)
	histFormat     = "--format=%x00%H %cI" // commit id, committer date (strict ISO)
	histDeleted    = "D"
	histModified   = "M"
	histShallow    = "true"
)

// PurgedHistory implements opstrash.HistoryReader for the repo checkout.
// Only the place you are in is read (3.6: history is per place); a store's
// purged rows are found from the store's own checkout.
func (a *App) PurgedHistory(ctx context.Context) ([]opstrash.HistoryRow, error) {
	dir := a.opts.RepoDir
	if dir == "" {
		return nil, errs.New(errs.Unsupported, "trash --from-history: no repo checkout here").WithHint("run it inside the repository")
	}
	sh, err := gitRun{dir: dir, args: []string{"rev-parse", "--is-shallow-repository"}}.output(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.Unsupported, "trash --from-history: "+dir+" is not a git checkout", err)
	}
	if strings.TrimSpace(string(sh)) == histShallow {
		return nil, errs.New(errs.Unsupported, "trash --from-history: this is a shallow clone; its history is incomplete").
			WithHint("git fetch --unshallow, then rerun")
	}
	data := path.Join(rowfs.LoreDir, rowfs.DataDir) + "/"
	out, err := gitRun{dir: dir, args: []string{"log", "--no-renames", "--diff-filter=DM", "--name-status", histFormat, "--", data}}.output(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{}
	var rows []opstrash.HistoryRow
	var commit string
	var at time.Time
	for _, line := range strings.Split(string(out), "\n") {
		if h, ok := strings.CutPrefix(line, histCommitMark); ok {
			sha, date, _ := strings.Cut(h, " ")
			commit = sha
			at, _ = time.Parse(time.RFC3339, strings.TrimSpace(date))
			continue
		}
		status, p, ok := strings.Cut(line, "\t")
		if !ok || seen[p] || (status != histDeleted && status != histModified) {
			continue
		}
		seen[p] = true
		if hr, keep := a.historyRow(ctx, dir, data, status, p, commit, at); keep {
			rows = append(rows, hr)
		}
	}
	return rows, nil
}

// historyRow builds one row of the walk, or keep=false when p is not a
// purged or hard-deleted row file (a live row, a side file, no content).
func (a *App) historyRow(ctx context.Context, dir, data, status, p, commit string, at time.Time) (opstrash.HistoryRow, bool) {
	rel := strings.TrimPrefix(p, data)
	table, file := path.Split(rel)
	table = strings.TrimSuffix(table, "/")
	id, isRow := strings.CutSuffix(file, rowfs.RowFileExt)
	if !isRow || strings.Contains(table, "/") || table == "" {
		return opstrash.HistoryRow{}, false // _purged.json, _meta.json, nested side files
	}
	if _, err := ids.ParseAny(id); err != nil {
		return opstrash.HistoryRow{}, false
	}
	tb, err := row.ParseTable(table)
	if err != nil {
		return opstrash.HistoryRow{}, false
	}
	hr := opstrash.HistoryRow{Store: store.SourceRepo, Table: tb, ID: id, Commit: commit, CommitAt: at}
	cur, rerr := readUnder(dir, p) // P2-LINT-5: a git-listed path, read through os.Root (no escape)
	switch {
	case status == histDeleted && rerr == nil:
		return hr, false // re-added since: not gone
	case status == histModified:
		if rerr != nil {
			return hr, false
		}
		r, derr := row.Decode(cur)
		if derr != nil || !r.Deleted {
			return hr, false // a live (or trashed) row: not purged
		}
		hr.PurgedAt, hr.PurgedBy = r.UpdatedAt, r.UpdatedBy.String()
	}
	doc, gerr := gitRun{dir: dir, args: []string{"show", commit + "^:" + p}}.output(ctx)
	if gerr != nil {
		return hr, false // the parent has no content (a root commit, or created and removed in one commit)
	}
	hr.Doc = doc
	return hr, true
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
