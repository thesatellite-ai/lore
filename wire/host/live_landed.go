package host

// live_landed.go — the default-branch read-through (LORE_TEAM_SPEC F1,
// coordinator 2026-11-02; ported from saas/cmd/cli live_core.go
// landedOnDefault / landedOver, P3-MODEB-1): on the default branch only, a
// row that landed on the fetched origin/<default> and left live is read
// from there until this checkout pulls it, so `task edit <id> --live` of a
// reopened row finds the copy it read (no E_STALE) and goes live again.
//
//	no live copy, not in the work tree ─▶ landedOnDefault: origin/<default>'s
//	   file, unless HEAD holds it (deleted here on purpose: not read through)
//	no live copy, in the work tree     ─▶ landedOver: origin's copy when the
//	   work tree equals HEAD (nothing local pending) and origin's is newer
//
// Other branches keep "not found". Best effort: a file that cannot be
// read or decoded leaves the row unseen, as before the read-through. A full
// Read (lists) gets the same through landedRows / landedOverRows: two
// ls-trees, then one read per landed row.

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// rowFileExt ends a row file's name on the branch.
const rowFileExt = ".json"

// landedCopy is the read-through copy of k for a reader holding branchRow
// (found reports whether the work tree has the row); false when the
// branch copy (or "not found") stands.
func (b *liveBackend) landedCopy(ctx context.Context, s *liveSession, k live.RowKey, branchRow store.Row, found bool) (store.Row, bool) {
	branch, ok := s.onDefaultBranch(ctx)
	if !ok {
		return store.Row{}, false
	}
	file := mainDataPath(b.folder, k.Table+"/"+k.ID+rowFileExt)
	remote := "refs/remotes/" + s.ref.RemoteName + "/" + branch
	headRaw, inHead, err := s.repo.ReadFile(ctx, "HEAD", file)
	if err != nil {
		return store.Row{}, false
	}
	if !found {
		if inHead {
			return store.Row{}, false // removed here on purpose: E_NOT_FOUND, not a read-through
		}
		raw, inRemote, rerr := s.repo.ReadFile(ctx, remote, file)
		if rerr != nil || !inRemote {
			return store.Row{}, false
		}
		r, decoded := decodeLanded(raw)
		if !decoded || r.ID.String() != k.ID {
			return store.Row{}, false
		}
		return r, true
	}
	if !inHead {
		return store.Row{}, false
	}
	head, ok := decodeLanded(headRaw)
	if !ok || head.HLC.Compare(branchRow.HLC) != 0 {
		return store.Row{}, false // the work tree differs from HEAD: local wins
	}
	raw, ok, err := s.repo.ReadFile(ctx, remote, file)
	if err != nil || !ok {
		return store.Row{}, false
	}
	r, ok := decodeLanded(raw)
	if !ok || r.Key() != branchRow.Key() || r.HLC.Compare(branchRow.HLC) <= 0 {
		return store.Row{}, false
	}
	return r, true
}

// onDefaultBranch is the default branch's name and whether HEAD is on it.
func (s *liveSession) onDefaultBranch(ctx context.Context) (string, bool) {
	branch := s.defaultBranch(ctx)
	head, off := s.checkoutBranch(ctx, branch)
	return branch, !off && head != ""
}

// decodeLanded decodes a row file read from a commit; false when it is not
// a row.
func decodeLanded(raw []byte) (store.Row, bool) {
	r, err := row.Decode(raw)
	if err != nil {
		return store.Row{}, false
	}
	sr, err := store.FromRow(r)
	return sr, err == nil
}

// dataPaths maps every row file under the folder's .lore/data at rev to its
// blob id (none when rev does not exist, e.g. no fetched default branch).
func (b *liveBackend) dataPaths(ctx context.Context, s *liveSession, rev string) map[string]string {
	raw, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"ls-tree", "-r", rev, "--", mainDataPath(b.folder, "")}})
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		meta, p, ok := strings.Cut(line, "\t") // "<mode> <type> <oid>\t<path>"
		if f := strings.Fields(meta); ok && len(f) == 3 {
			out[p] = f[2]
		}
	}
	return out
}

// landedRows is every row of a live-readable table that landed on the
// fetched origin/<default>, is not in HEAD (not pulled, and not deleted here
// on purpose) and is not in have (neither on the branch nor live), so a done
// task that left live stays in lists until the checkout pulls.
func (b *liveBackend) landedRows(ctx context.Context, s *liveSession, have map[store.Key]bool) []store.Row {
	branch, ok := s.onDefaultBranch(ctx)
	if !ok {
		return nil
	}
	remote := "refs/remotes/" + s.ref.RemoteName + "/" + branch
	inHead := b.dataPaths(ctx, s, "HEAD")
	landed := b.dataPaths(ctx, s, remote)
	paths := make([]string, 0, len(landed))
	for p := range landed {
		if _, held := inHead[p]; !held {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths) // stable output
	var out []store.Row
	for _, p := range paths {
		table := path.Base(path.Dir(p))
		id, isRow := strings.CutSuffix(path.Base(p), rowFileExt)
		// readsLive, not liveReadable: rows a live row carried (the project,
		// its repo) land too, and Get reads them through the same way.
		if !isRow || !readsLive(s, row.Table(table)) {
			continue
		}
		raw, found, err := s.repo.ReadFile(ctx, remote, p)
		if err != nil || !found {
			continue
		}
		r, decoded := decodeLanded(raw)
		if !decoded || r.ID.String() != id || have[r.Key()] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// landedOverRows swaps, in place, each branch row of out (index by key in
// branchOnly) whose copy on origin/<default> is newer while the work tree
// equals HEAD (landedCopy's rule); only paths whose blobs differ are read.
func (b *liveBackend) landedOverRows(ctx context.Context, s *liveSession, out []store.Row, branchOnly map[live.RowKey]int) {
	if len(branchOnly) == 0 {
		return
	}
	branch, ok := s.onDefaultBranch(ctx)
	if !ok {
		return
	}
	inHead := b.dataPaths(ctx, s, "HEAD")
	landed := b.dataPaths(ctx, s, "refs/remotes/"+s.ref.RemoteName+"/"+branch)
	for k, i := range branchOnly {
		p := mainDataPath(b.folder, k.Table+"/"+k.ID+rowFileExt)
		lo, onRemote := landed[p]
		if ho, inH := inHead[p]; !onRemote || !inH || ho == lo || !readsLive(s, row.Table(k.Table)) {
			continue
		}
		if r, newer := b.landedCopy(ctx, s, k, out[i], true); newer {
			out[i] = r
		}
	}
}

// pullWouldAdd reports whether k's row file is one the next `git pull` of
// origin/<default> adds to this checkout: the fetched default branch holds
// it and the merge base of HEAD and that branch does not (it landed after
// this checkout's last pull). A branch write of such a row would leave an
// untracked file that the pull then refuses to overwrite ("untracked
// working tree files would be overwritten by merge", campaign knowledge
// 2026-11-08). A row deleted here on purpose after it landed (t-old2: the
// merge base holds it) is not one. False whenever git cannot answer, which
// keeps the write as it was before this check.
func (b *liveBackend) pullWouldAdd(ctx context.Context, s *liveSession, k live.RowKey) bool {
	branch, ok := s.onDefaultBranch(ctx)
	if !ok {
		return false
	}
	remote := "refs/remotes/" + s.ref.RemoteName + "/" + branch
	file := mainDataPath(b.folder, k.Table+"/"+k.ID+rowFileExt)
	if _, inRemote, err := s.repo.ReadFile(ctx, remote, file); err != nil || !inRemote {
		return false
	}
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"merge-base", "HEAD", remote}})
	if err != nil {
		return false
	}
	base := strings.TrimSpace(string(out))
	if base == "" {
		return false
	}
	_, inBase, err := s.repo.ReadFile(ctx, base, file)
	return err == nil && !inBase
}

// notPulledError is the refusal of a write that would land k in the work
// tree although it is only on the fetched default branch (pullWouldAdd)
// and cannot go live (--no-live, or the kind is not read from live).
// E_REFUSED, not E_STALE: errs matches by code, so an E_STALE is
// store.ErrStale to the engine, which re-reads, merges and retries and
// then answers "kept changing while it was merged" without this hint;
// only a pull can fix it.
func notPulledError(k live.RowKey, branch string) error {
	return errs.New(errs.Refused, "row "+k.String()+" landed on "+branch+" after your last pull; writing it here would leave an untracked file that git pull refuses to overwrite").
		WithHint("run git pull first: this row landed on " + branch + " after your last pull; then run the command again")
}

// landedAhead reports whether ch edits a row this checkout has only through
// the read-through (no live copy, no work-tree copy) and that the next pull
// adds (pullWouldAdd): the write must go live or be refused, never land in
// the work tree.
func (b *liveBackend) landedAhead(ctx context.Context, s *liveSession, files map[live.RowKey]live.File, ch store.Change) (bool, error) {
	k := keyOf(ch.Row)
	if ch.Expect.IsZero() || !liveReadable(s, ch.Row.Table) {
		return false, nil
	}
	if _, inLive := files[k]; inLive {
		return false, nil
	}
	if _, err := b.inner.Get(ctx, ch.Row.Table, ch.Row.ID); err == nil {
		return false, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if _, ok := b.landedCopy(ctx, s, k, store.Row{}, false); !ok {
		return false, nil
	}
	return b.pullWouldAdd(ctx, s, k), nil
}
