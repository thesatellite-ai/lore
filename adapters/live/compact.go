package live

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
)

// compactedAtPath records when the live ref was last compacted (F8), so the
// automatic compaction waits at least CompactMinInterval between runs.
const compactedAtPath = metaFolder + "/compacted_at"

// CompactMinInterval is the shortest time between two automatic
// compactions (F8: "the last compaction in _live/epoch is older than a
// day"), so a busy ref is not rewritten on every write.
const CompactMinInterval = 24 * time.Hour

// CompactedAt reads when the tree was last compacted (zero = never).
func (t Tree) CompactedAt() time.Time {
	b, ok := t[compactedAtPath]
	if !ok {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return at
}

// CompactInputs are what ShouldCompact decides from.
type CompactInputs struct {
	Commits     int
	Threshold   int
	LastCompact time.Time
	Now         time.Time
}

// ShouldCompact is the automatic trigger of F8: more commits than
// live.compact_after, and no compaction within CompactMinInterval. A
// threshold of 0 or less turns automatic compaction off.
func ShouldCompact(in CompactInputs) bool {
	if in.Threshold <= 0 || in.Commits <= in.Threshold {
		return false
	}
	return in.LastCompact.IsZero() || in.Now.Sub(in.LastCompact) >= CompactMinInterval
}

// CommitCount counts the commits of the local live ref.
func (r *Ref) CommitCount(ctx context.Context) (int, error) {
	out, err := r.Repo.Run(ctx, gitref.Invocation{Args: []string{"rev-list", "--count", r.Name}})
	if err != nil {
		if _, isExit := gitref.ExitCode(err); isExit {
			return 0, nil // no ref yet
		}
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// CompactPlan is what a compaction would do (`lore live compact --dry-run`).
type CompactPlan struct {
	Commits   int
	Files     int
	FromEpoch int
	ToEpoch   int
	Purged    int
}

// CompactResult reports Compact.
type CompactResult struct {
	Plan   CompactPlan
	Status PushStatus
	Backup string
	Commit string
}

// compactRetries bounds the restarts when a writer pushes between our
// fetch and our lease push.
const compactRetries = 5

// Compact rewrites the live ref to ONE parentless commit with the remote
// tip's tree (purged rows dropped), the epoch raised by one and
// compacted_at set, after keeping a local backup; the push is a lease on
// the tip it read, so a concurrent writer makes it start again instead of
// being overwritten (F8). Local writes not pushed yet are pushed first, so
// they are part of what is compacted. With dryRun nothing changes.
func (r *Ref) Compact(ctx context.Context, dryRun bool) (CompactResult, error) {
	for attempt := 0; attempt < compactRetries; attempt++ {
		res, err := r.compactOnce(ctx, dryRun)
		if err == nil && res.Status == PushRetriesOut {
			continue // lost the lease: someone wrote meanwhile, start again
		}
		return res, err
	}
	return CompactResult{Status: PushRetriesOut}, nil
}

func (r *Ref) compactOnce(ctx context.Context, dryRun bool) (CompactResult, error) {
	var res CompactResult
	if r.Remote != nil {
		if err := r.Fetch(ctx); err != nil {
			if errors.Is(err, gitremote.ErrUnconfirmed) {
				res.Status = PushOffline
				return res, nil
			}
			return res, err
		}
		if _, err := r.Integrate(ctx); err != nil {
			return res, err
		}
		if !dryRun {
			out, err := r.Push(ctx)
			if err != nil {
				return res, err
			}
			if !out.Pushed() {
				res.Status = out.Status
				return res, nil
			}
		}
	}
	s, err := r.Snapshot(ctx)
	if err != nil {
		return res, err
	}
	if s.Local == "" {
		res.Status = PushNothing
		return res, nil
	}
	tree, err := r.ReadTree(ctx, s.Local)
	if err != nil {
		return res, err
	}
	n, err := r.CommitCount(ctx)
	if err != nil {
		return res, err
	}
	purged, err := tree.Purged()
	if err != nil {
		return res, err
	}
	ch := gitref.Changes{Put: map[string][]byte{
		EpochPath:       encodeEpoch(tree.Epoch() + 1),
		compactedAtPath: []byte(r.now().Format(time.RFC3339) + "\n"),
	}}
	dropped := 0
	for p, b := range tree {
		if dropsPurged(p, b, purged) {
			ch.Delete = append(ch.Delete, p)
			dropped++
		}
	}
	res.Plan = CompactPlan{Commits: n, Files: len(tree) - dropped, FromEpoch: tree.Epoch(), ToEpoch: tree.Epoch() + 1, Purged: dropped}
	if dryRun {
		return res, nil
	}
	c, err := r.Repo.MakeCommit(ctx, gitref.CommitRequest{
		Base: s.Local, History: gitref.HistoryReplace, Changes: ch,
		Message: fmt.Sprintf("lore: live compact (epoch %d)", res.Plan.ToEpoch), Signature: r.sig(),
	})
	if err != nil {
		return res, err
	}
	if res.Backup, err = r.Backup(ctx, s.Local); err != nil {
		return res, err
	}
	if err = r.Repo.UpdateRef(ctx, r.Name, c.OID, s.Local); err != nil {
		return res, err
	}
	res.Commit = c.OID
	if r.Remote == nil {
		res.Status = PushNoRemote
		return res, r.markSeen(c.OID)
	}
	out, err := r.ForcePush(ctx, s.Tracking)
	if err != nil {
		return res, err
	}
	if out.Status == PushRetriesOut {
		// Lost the lease: put the local ref back so the next round integrates
		// the newcomer's write on top of the history it belongs to.
		if err := r.Repo.UpdateRef(ctx, r.Name, s.Local, c.OID); err != nil {
			return res, err
		}
	}
	res.Status = out.Status
	return res, nil
}

// ListPurged adds ids to _live/purged.json and leaves their files alone:
// the path of `lore trash purge` on a repo with a live ref, whose engine
// write already put a tombstone (no content) in live. The tombstone stays
// until it lands as a file delete plus a _purged.json id on main
// (LORE_TEAM_SPEC side effect 11, DQ4), and the list reaches every clone on
// its next fetch (merge unions it), so each answers E_PURGED for the ids
// (BUG-LIVE-PURGE-LIST, F-TOMB-1). Purge is the §6.6 path that also drops
// the files. It writes the local ref only; the caller pushes.
func (r *Ref) ListPurged(ctx context.Context, ids []string, version string) (WriteResult, error) {
	for attempt := 0; ; attempt++ {
		_, tree, err := r.LocalTree(ctx)
		if err != nil {
			return WriteResult{}, err
		}
		set, err := tree.Purged()
		if err != nil {
			return WriteResult{}, err
		}
		for _, id := range ids {
			set[id] = true
		}
		b, err := encodePurged(set)
		if err != nil {
			return WriteResult{}, err
		}
		res, err := r.writeOnce(ctx, WriteRequest{Folder: RootFolder, Raw: map[string][]byte{PurgedPath: b}, BinaryVersion: version})
		if err == nil || !errors.Is(err, gitref.ErrRefMoved) || attempt >= casRetries {
			return res, err
		}
	}
}

// Purge removes rows from live for good (§6.6 leak cleanup): their files
// go and their ids join _live/purged.json, so no stale clone can push them
// back; a compaction then drops them from history. It writes the local ref
// only; the caller pushes.
func (r *Ref) Purge(ctx context.Context, ids []string, version string) (WriteResult, error) {
	_, tree, err := r.LocalTree(ctx)
	if err != nil {
		return WriteResult{}, err
	}
	set, err := tree.Purged()
	if err != nil {
		return WriteResult{}, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		set[id] = true
		want[id] = true
	}
	b, err := encodePurged(set)
	if err != nil {
		return WriteResult{}, err
	}
	var del []string
	for p := range tree {
		if _, k, ok := ParseRowPath(p); ok && want[k.ID] {
			del = append(del, p)
		}
	}
	for attempt := 0; ; attempt++ {
		res, err := r.writeOnce(ctx, WriteRequest{Folder: RootFolder, Raw: map[string][]byte{PurgedPath: b}, RawDelete: del, BinaryVersion: version})
		if err == nil || !errors.Is(err, gitref.ErrRefMoved) || attempt >= casRetries {
			return res, err
		}
	}
}
