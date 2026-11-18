package git

// pushlog.go — every push this Backend made, for the command's output
// (no hidden magic, 2026-11-12: "every push lore makes is visible in the
// command's output: store, commit"). The backend never prints; the
// caller that knows the store's name (wire's storePush, the batch flush)
// takes the pushes after a sync and prints one line each.
//
//	push / forcePush ─▶ the remote took tip ─▶ recordPush(tip, branch, forced)
//	caller ─▶ TakePushes ─▶ "pushed store <s>: <sha> to <branch>"

import "sync"

// Pushed is one push the remote accepted.
type Pushed struct {
	// Commit is the full commit id the store branch points at now.
	Commit string `json:"commit"`
	// Branch is the store branch that moved.
	Branch string `json:"branch"`
	// Remote is the clone's remote name (always RemoteName).
	Remote string `json:"remote"`
	// Forced marks a history rewrite (compaction, a batch squash onto an
	// adopted epoch) pushed with --force-with-lease.
	Forced bool `json:"forced,omitempty"`
	// Folders are the data folders whose row files the push carried
	// (rowfs folder names, "." for the root), sorted; empty when unknown.
	// One clone pushes every folder, so the caller names the push by
	// these, not by the folder that happened to push (bug hunt W-14).
	Folders []string `json:"folders,omitempty"`
	// Commits is how many commits the push sent (git rev-list --count
	// <remote tip before>..<tip>) and Rows how many row files they changed;
	// 0 when git cannot say (a forced compaction). The push line prints
	// them, so it agrees with git (devco D-601: a sync printed "sent 7"
	// while 18 commits went out).
	Commits int `json:"commits,omitempty"`
	Rows    int `json:"rows,omitempty"`
}

// ShortCommitLen is how many hex digits of a commit id output shows.
const ShortCommitLen = 7

// Short is the commit's short id.
func (p Pushed) Short() string {
	if len(p.Commit) > ShortCommitLen {
		return p.Commit[:ShortCommitLen]
	}
	return p.Commit
}

// pushLog holds the pushes not yet taken.
type pushLog struct {
	mu     sync.Mutex
	pushes []Pushed
}

// recordPush notes that the remote took tip on the store branch.
func (b *Backend) recordPush(tip string, forced bool, st pushStats) {
	l := &b.pushes
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pushes = append(l.pushes, Pushed{Commit: tip, Branch: b.branch, Remote: RemoteName, Forced: forced, Folders: st.folders, Commits: st.commits, Rows: st.rows})
}

// pushStats is what one push carried: the folders whose row files
// changed, how many row files, and how many commits.
type pushStats struct {
	folders       []string
	commits, rows int
}

// TakePushes returns the pushes made since the last call and forgets
// them, oldest first.
func (b *Backend) TakePushes() []Pushed {
	l := &b.pushes
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.pushes
	l.pushes = nil
	return out
}
