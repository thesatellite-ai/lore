package git

// authoritycarry.go — the roster replay carried over a compaction by its
// old ─▶ new id map (STORE_GROWTH_SPEC 9, devco:D-1004).
//
//	clone's replay at the old tip ─► overMap(idMap) ─► replay at the
//	    rewritten tip (every commit id the rewrite re-made renamed) ─►
//	    saved; the next read carries it on over the epoch commit and
//	    the writes after it (replayFromDisk)
//
// Without it a clone that took a compaction replayed the new short
// history from the snapshot, which vouches for the writer every row
// states: a row version written before the cut by someone other than the
// writer it names lost the credit the replay had given its real author
// (C35), so the same row read with another writer after the compaction.

import (
	"context"
	"slices"
)

// overMap is a, the replay at an old tip, as the replay at that tip's
// rewrite: commit ids idMap renames are renamed; a hold by a commit the
// rewrite squashed is released at snap (the snapshot and every kept commit
// carry that file's trusted version, compactv2.go overlaid, so nothing is
// held back any more: the same as a clone that replays the new history).
// Admin sets are kept for the re-made commits and for the parents of the
// org-file changes that still name an old commit (orgcfg judges each one
// by its author's role at that parent), and dropped for the rest of the
// squashed history. nil when idMap does not rename a's head.
func (a *authority) overMap(idMap map[string]string, snap string) *authority {
	if a == nil {
		return nil
	}
	head, ok := idMap[a.head]
	if !ok {
		return nil
	}
	ren := func(id string) string {
		if n, ok := idMap[id]; ok {
			return n
		}
		return id
	}
	c := a.clone()
	c.head = head
	c.admins = make(map[string]*adminSet, len(idMap))
	c.refused = map[string]bool{}
	for old, n := range idMap {
		if set, ok := a.admins[old]; ok {
			c.admins[n] = set
		}
		if a.refused[old] {
			c.refused[n] = true
		}
	}
	c.orgFile = make([]orgFileChange, len(a.orgFile))
	for i, o := range a.orgFile {
		if _, kept := idMap[o.parent]; !kept && o.parent != "" {
			if set, ok := a.admins[o.parent]; ok {
				c.admins[o.parent] = set
			}
		}
		o.commit, o.parent = ren(o.commit), ren(o.parent)
		c.orgFile[i] = o
	}
	c.held = make(map[string]HeldChange, len(a.held))
	for _, p := range slices.Sorted(func(yield func(string) bool) {
		for p := range a.held {
			if !yield(p) {
				return
			}
		}
	}) {
		h := a.held[p]
		if n, ok := idMap[h.Commit]; ok {
			h.Commit = n
			c.held[p] = h
			continue
		}
		delete(c.trusted, p)
		c.holds = append(c.holds, holdEvent{commit: snap, path: p})
	}
	for p, cr := range c.credited {
		cr.commit = ren(cr.commit)
		c.credited[p] = cr
	}
	holds := make([]holdEvent, len(c.holds))
	for i, h := range c.holds {
		h.commit = ren(h.commit)
		holds[i] = h
	}
	c.holds = holds
	return c
}

// carryReplay saves the clone's replay at oldTip carried over idMap
// (overMap) as the replay of the rewritten history, so the next read goes
// on from it instead of replaying the new history from the snapshot.
// a is the replay at oldTip when the caller holds it (nil: read from the
// saved replays, else replayed). Best effort: when it cannot be made, the
// next read replays the new history, as before.
func (b *Backend) carryReplay(ctx context.Context, a *authority, oldTip, snap string, idMap map[string]string) {
	if b.mode != modeClone {
		return
	}
	if a == nil || a.head != oldTip {
		var err error
		if a, err = b.replayAt(ctx, oldTip); err != nil || a == nil || a.head != oldTip {
			return
		}
	}
	c := a.overMap(idMap, snap)
	if c == nil {
		return
	}
	b.saveAuthority(c)
	authorityCache.Lock()
	authorityCache.byWork[b.work] = c
	authorityCache.Unlock()
}
