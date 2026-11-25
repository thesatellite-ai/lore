package git

// owncommits.go — the commits this process made in a clone, as the roster
// replay lists them (devco:D-945, bug hunt H-203).
//
// Why: a write commits to the store clone and lists the commit's files
// (noteCommitted); the read right after it (the write's render refresh,
// D-921) finds the clone's HEAD moved and carries the remembered replay
// over the new commit (replayOverHeld), which asked git for that same
// commit again: one more git process per write to a store. noteCommitted
// now lists the commit in the replay's own form and keeps it here, so
//
//	replayRangeAfter(from, to)
//	  every commit from to back to from is one this process listed ─▶ them
//	  anything else (a pull, another process's commit, a merge)     ─▶ git log
//
// The commits kept are git's own answer about each commit (log --raw of
// that commit), so the replay decides them exactly as it would from a
// `git log from..to`.

import (
	"slices"
	"sync"
)

// ownCommitsMax is the most commits kept per clone; past it the clone's
// list starts again (the replay then asks git, as before).
const ownCommitsMax = 256

// ownCommits are the commits this process listed after making them, by
// clone work tree and commit id.
var ownCommits = struct {
	sync.Mutex
	byWork map[string]map[string]replayCommit
}{byWork: map[string]map[string]replayCommit{}}

// noteOwnCommit keeps c, a commit this process just made in the clone at
// work.
func noteOwnCommit(work string, c replayCommit) {
	if c.id == "" || c.merge {
		return
	}
	ownCommits.Lock()
	defer ownCommits.Unlock()
	m := ownCommits.byWork[work]
	if m == nil || len(m) >= ownCommitsMax {
		m = map[string]replayCommit{}
		ownCommits.byWork[work] = m
	}
	m[c.id] = c
}

// ownCommitsAfter is replayRangeAfter answered from the commits kept:
// the line of single-parent commits from (exclusive) to to, oldest first,
// when every one of them is kept; ok is false otherwise.
func ownCommitsAfter(work, from, to string) ([]replayCommit, bool) {
	if from == "" || to == "" || from == to {
		return nil, false
	}
	ownCommits.Lock()
	defer ownCommits.Unlock()
	m := ownCommits.byWork[work]
	var line []replayCommit
	for id := to; id != from; {
		c, ok := m[id]
		if !ok || len(c.parents) != 1 || len(line) >= ownCommitsMax {
			return nil, false
		}
		line = append(line, c)
		id = c.parents[0]
	}
	slices.Reverse(line)
	return line, true
}
