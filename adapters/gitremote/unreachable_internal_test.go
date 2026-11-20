package gitremote

import (
	"testing"
	"time"
)

// TestUnreachableMarkExpires (devco:H-6): a remote known unreachable gets
// no retry until unreachableFor has passed (a long-running process then
// retries a recovered link) or a call reaches it; other remotes are not
// affected by its mark.
func TestUnreachableMarkExpires(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	old := reach
	reach = &reachMemo{until: map[string]time.Time{}, now: func() time.Time { return now }}
	t.Cleanup(func() { reach = old })
	a, b := remoteKey("/repo", "origin", nil, nil), remoteKey("/repo", "backup", nil, nil)
	if !retryDue(a) {
		t.Fatal("a remote never seen down must get its retry")
	}
	markUnreachable(a)
	if retryDue(a) || !retryDue(b) {
		t.Fatal("only the remote that never connected loses its retry")
	}
	now = now.Add(unreachableFor - time.Second)
	if retryDue(a) {
		t.Fatal("the mark holds for unreachableFor")
	}
	now = now.Add(time.Second)
	if !retryDue(a) {
		t.Fatal("after unreachableFor the remote gets its retry again")
	}
	markUnreachable(a)
	markReached(a)
	if !retryDue(a) {
		t.Fatal("a call that reached the remote clears its mark")
	}
}
