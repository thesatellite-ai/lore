package git

// settle.go — a row both sides changed, seen and settled by a person.
//
// A store sync whose merge meets a row it cannot merge (a free-text field
// both sides rewrote) aborts the merge and keeps both sides: the local
// commit waits, the remote's copy sits on the tracking ref (sync.go,
// resolveRow). Before this file nothing showed the other side's copy and
// the only way out was editing the row until both sides agreed. Now:
//
//	RowSides(id)        ─▶ the merge base's, this clone's and the remote's
//	                       copy of the row file (lore sync conflicts)
//	SettleRow(id, take) ─▶ records the person's choice in the clone's git
//	                       dir (lore-settled.json), keyed by path with the
//	                       blob ids of both sides they saw
//	next sync's merge   ─▶ resolveRow takes the chosen side for that path
//	                       while both blobs are still the ones the person
//	                       saw; a newer edit on either side is a new
//	                       conflict, never silently overwritten
//	refused sync        ─▶ records each refused row as seen (markSeen):
//	                       a local edit made after the refusal was
//	                       reported is the person's settlement, whatever
//	                       its value (bug hunt B2-10: before, only copying
//	                       the remote's exact value ended the conflict),
//	                       as long as the remote's copy is still the one
//	                       the refusal named
//
// The record is cleared once a merge commits.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// settleFile is the settlement record's name in the clone's git dir (never
// in the worktree, so it is never committed or pushed).
const settleFile = "lore-settled.json"

// RowSides is one row both this clone and the remote changed since they
// last met: its path in the store, its table and the three copies of its
// row file (nil when absent on that side).
type RowSides struct {
	Path   string
	Table  row.Table
	Base   []byte
	Mine   []byte
	Theirs []byte
	// mineBlob and theirsBlob are the blob ids of Mine and Theirs, what a
	// settlement is keyed by.
	mineBlob, theirsBlob string
}

// settlement is one settled row: the blob ids of both sides the person
// saw and the side they took.
type settlement struct {
	Mine   string `json:"mine"`
	Theirs string `json:"theirs"`
	// TakeTheirs is true for the remote's copy, false for this clone's.
	TakeTheirs bool `json:"take_theirs"`
	// Seen marks a refusal the sync reported, not a choice: it settles
	// the row (this clone's copy) only once this clone's copy changed.
	Seen bool `json:"seen,omitempty"`
}

// RowSides returns the copies of row id when this clone and the remote's
// last fetched tip both changed it since their merge base; ok is false
// when the clone has not diverged from the remote or the row is not
// changed on both sides (nothing to settle). Repo mode has no remote of
// its own: always false.
func (b *Backend) RowSides(ctx context.Context, id ids.AnyID) (RowSides, bool, error) {
	if b.mode == modeRepo || id.IsZero() {
		return RowSides{}, false, nil
	}
	h, err := b.head(ctx)
	if err != nil {
		return RowSides{}, false, err
	}
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return RowSides{}, false, err
	}
	if h == "" || remote == "" || h == remote {
		return RowSides{}, false, nil
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"merge-base", h, remote}})
	if err != nil {
		return RowSides{}, false, nil // no common history (a compacted remote): adoptEpoch's case, not a row conflict
	}
	base := trimmed(out)
	if base == h || base == remote {
		return RowSides{}, false, nil // one side holds the other: a fast-forward, no conflict
	}
	p, err := b.changedPath(ctx, base, remote, id)
	if err != nil || p == "" {
		return RowSides{}, false, err
	}
	_, rel, ok := rowPlace(p)
	if !ok {
		return RowSides{}, false, nil
	}
	table, _, _ := rowfs.ParseRowPath(rel)
	s := RowSides{Path: p, Table: table, mineBlob: b.blobAt(ctx, h, p), theirsBlob: b.blobAt(ctx, remote, p)}
	if s.mineBlob == s.theirsBlob || s.mineBlob == b.blobAt(ctx, base, p) {
		return RowSides{}, false, nil // same on both sides, or only the remote changed it
	}
	blobs, err := b.readBlobs(ctx, []string{base + ":" + p, h + ":" + p, remote + ":" + p})
	if err != nil {
		return RowSides{}, false, err
	}
	s.Base, s.Mine, s.Theirs = blobs[base+":"+p], blobs[h+":"+p], blobs[remote+":"+p]
	return s, true, nil
}

// changedPath is the store path of row id among the files the remote
// changed since base, in this Backend's folder ("" when it did not).
func (b *Backend) changedPath(ctx context.Context, base, remote string, id ids.AnyID) (string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--name-only", "--no-renames", "-z", base, remote, "--", b.folder.DataPath()}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: list the remote's changed rows", err)
	}
	suffix := "/" + id.String() + ".json"
	for _, p := range splitZ(out) {
		if strings.HasSuffix(p, suffix) {
			return p, nil
		}
	}
	return "", nil
}

// SettleRow records the person's choice for row id, which both sides
// changed (RowSides): the next sync's merge takes the remote's copy
// (takeTheirs) or this clone's, as long as neither side changed again.
// A row that is not in conflict any more is E_NOT_FOUND.
func (b *Backend) SettleRow(ctx context.Context, id ids.AnyID, takeTheirs bool) (err error) {
	if err := b.usable(ctx); err != nil {
		return err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return err
	}
	defer func() { err = releaseKeep(err, lock) }()
	s, ok, err := b.RowSides(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return errs.New(errs.NotFound, "settle: "+id.String()+" is not changed on both sides any more").
			WithHint("run `lore sync`; `lore sync conflicts` lists what is still open")
	}
	all, err := b.settlements(ctx)
	if err != nil {
		return err
	}
	all[s.Path] = settlement{Mine: s.mineBlob, Theirs: s.theirsBlob, TakeTheirs: takeTheirs}
	return b.writeSettlements(ctx, all)
}

// settled is the side to take for the conflicted path: ok only when a
// settlement names path and both index stages are still the blobs the
// person saw.
func (b *Backend) settled(ctx context.Context, path string) (takeTheirs, ok bool) {
	all, err := b.settlements(ctx)
	if err != nil {
		return false, false // an unreadable record settles nothing: the row stays a refusal
	}
	s, found := all[path]
	if !found {
		return false, false
	}
	ours := b.stageBlob(ctx, stageOurs+path)
	theirs := b.stageBlob(ctx, stageTheirs+path)
	if theirs != s.Theirs {
		return false, false // the remote changed again: a new conflict
	}
	if s.Seen {
		return false, ours != s.Mine // edited here after the refusal: keep that edit
	}
	if ours != s.Mine {
		return false, false
	}
	return s.TakeTheirs, true
}

// markSeen records the refused paths of an aborted merge (MERGE_HEAD
// still set, so the index holds both sides) as seen. A person's explicit
// choice for the same two copies is kept. Recording is best effort: a
// failure only means a later edit does not settle the row by itself.
func (b *Backend) markSeen(ctx context.Context, paths []string) {
	all, err := b.settlements(ctx)
	if err != nil {
		return
	}
	for _, p := range paths {
		s := settlement{Mine: b.stageBlob(ctx, stageOurs+p), Theirs: b.stageBlob(ctx, stageTheirs+p), Seen: true}
		if s.Mine == "" || s.Theirs == "" {
			continue
		}
		if old, ok := all[p]; ok && !old.Seen && old.Mine == s.Mine && old.Theirs == s.Theirs {
			continue
		}
		all[p] = s
	}
	_ = b.writeSettlements(ctx, all)
}

// stageBlob is the blob id of an index stage name (":2:<path>"), "" when
// absent.
func (b *Backend) stageBlob(ctx context.Context, name string) string {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "-q", "--verify", name}})
	if err != nil {
		return ""
	}
	return trimmed(out)
}

// settledSide sets *result to the side a settlement takes for path and
// reports whether one applies (settled).
func (b *Backend) settledSide(ctx context.Context, path string, ours, theirs []byte, result *[]byte) bool {
	takeTheirs, ok := b.settled(ctx, path)
	if !ok {
		return false
	}
	*result = ours
	if takeTheirs {
		*result = theirs
	}
	return true
}

// clearSettlements removes the record once a merge committed (every
// settled row is in it).
func (b *Backend) clearSettlements(ctx context.Context) {
	p, err := b.settlePath(ctx)
	if err != nil {
		return
	}
	_ = os.Remove(p) // a leftover only names blobs no conflict will have again
}

// settlePath is the record's absolute path in the clone's git dir.
func (b *Backend) settlePath(ctx context.Context) (string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: locate the clone's git dir", err)
	}
	return filepath.Join(trimmed(out), settleFile), nil
}

// settlements reads the record (empty when there is none).
func (b *Backend) settlements(ctx context.Context) (map[string]settlement, error) {
	p, err := b.settlePath(ctx)
	if err != nil {
		return nil, err
	}
	all := map[string]settlement{}
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return all, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read "+settleFile, err)
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, errs.Wrap(errs.Internal, "git: decode "+settleFile, err)
	}
	return all, nil
}

// writeSettlements replaces the record.
func (b *Backend) writeSettlements(ctx context.Context, all map[string]settlement) error {
	p, err := b.settlePath(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(all)
	if err != nil {
		return errs.Wrap(errs.Internal, "git: encode "+settleFile, err)
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		return errs.Wrap(errs.Internal, "git: write "+settleFile, err)
	}
	return nil
}
