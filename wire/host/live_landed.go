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
// read or decoded leaves the row unseen, as before the read-through. Not
// ported: the full Read (lists) read-through, landedRows.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
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
		raw, ok, rerr := s.repo.ReadFile(ctx, remote, file)
		if rerr != nil || !ok {
			return store.Row{}, false
		}
		r, ok := decodeLanded(raw)
		if !ok || r.ID.String() != k.ID {
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
	head, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	return branch, err == nil && strings.TrimSpace(string(head)) == branch
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
