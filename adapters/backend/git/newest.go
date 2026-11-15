package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// NewestCommit is the newest committer stamp among the clone's branch and
// its tracking copy of the remote's (refs/remotes/origin/<branch>), with
// its author's name: what `lore doctor` compares with this machine's
// clock (devco:U-3). ok is false for a code repo (it has no store branch of
// its own) or when neither ref can be read. Read-only: no fetch.
func (b *Backend) NewestCommit(ctx context.Context) (at time.Time, author string, ok bool) {
	if b.mode != modeClone || b.branch == "" {
		return time.Time{}, "", false
	}
	for _, ref := range []string{headsPrefix + b.branch, trackingRefNS + b.branch} {
		out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "-1", "--format=%ct%x09%an", ref, "--"}})
		if err != nil {
			continue // a branch not pushed yet has no tracking copy
		}
		secs, name, found := strings.Cut(trimmed(out), "\t")
		n, perr := strconv.ParseInt(secs, 10, 64)
		if !found || perr != nil {
			continue
		}
		if t := time.Unix(n, 0); t.After(at) {
			at, author, ok = t, name, true
		}
	}
	return at, author, ok
}
