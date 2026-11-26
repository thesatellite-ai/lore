package git

import (
	"bytes"
	"context"
	"encoding/gob"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// forgetAuthority drops the in-process replay of b's clone, as a new
// process starts without it.
func forgetAuthority(b *Backend) {
	authorityCache.Lock()
	delete(authorityCache.byWork, b.work)
	authorityCache.Unlock()
}

// replayCount is replayedCommits now (tests that read it do not run in parallel).
func replayCount() int64 { return replayedCommits.Load() }

// sameAuthority fails t when got and want differ in anything a read uses:
// the roster, stamps, holds, credits, accepted blobs and the admins after
// each commit (compared through the saved form, which drops only Go's
// nil-versus-empty difference).
func sameAuthority(t *testing.T, got, want *authority) {
	t.Helper()
	norm := func(a *authority) (savedAuthority, map[string]savedAdmins) {
		s, ok := a.saved("test")
		if !ok {
			t.Fatal("the authority has no admin set")
		}
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(&s); err != nil {
			t.Fatal(err)
		}
		var back savedAuthority
		if err := gob.NewDecoder(&buf).Decode(&back); err != nil {
			t.Fatal(err)
		}
		admins := map[string]savedAdmins{"": back.Snaps[back.Snap]}
		for c, i := range back.Admins {
			admins[c] = back.Snaps[i]
		}
		back.Snaps, back.Admins, back.Snap = nil, nil, 0
		return back, admins
	}
	g, gAdmins := norm(got)
	w, wAdmins := norm(want)
	if !reflect.DeepEqual(g, w) {
		t.Errorf("the replay kept on disk differs from a full replay:\n got  %+v\n want %+v", g, w)
	}
	if !reflect.DeepEqual(gAdmins, wAdmins) {
		t.Errorf("the admins per commit differ:\n got  %+v\n want %+v", gAdmins, wAdmins)
	}
}

// fullReplay replays b's whole history at HEAD, without any cache.
func fullReplay(t *testing.T, b *Backend) *authority {
	t.Helper()
	ctx := context.Background()
	h, err := b.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := b.replayCommits(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.replay(ctx, h, commits)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// authorityNow is authorityOf at the clone's HEAD.
func authorityNow(t *testing.T, b *Backend) *authority {
	t.Helper()
	ctx := context.Background()
	h, err := b.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.authorityOf(ctx, h)
	if err != nil || a == nil {
		t.Fatalf("authorityOf: %v (nil %v)", err, a == nil)
	}
	return a
}

// TestAuthorityKeptOnDisk: a new process at a HEAD the replay already
// reached replays no commit (it reads the replay kept in the clone's git
// folder); after a plain commit it replays that commit only; after a
// merge onto the saved HEAD (the merge a sync makes) it replays only the
// merged side and the merge (devco:D-999: it replayed the whole history);
// after a fast-forward to a merge that reaches the saved HEAD only
// through its other side it replays the whole history. Every time the
// result is the full replay's: the same holds, credits and admins
// (devco:D-797: a 5,000-row store paid a full replay, 0.3 s, on every
// command).
func TestAuthorityKeptOnDisk(t *testing.T) {
	o := newOrgTeam(t)
	spoof, roster := mustID[ids.Memory](t), mustID[ids.ActorPeriod](t)
	o.h.commit(o.max.email, map[string]string{orgPath("memories", spoof): stampedRow(spoof, o.boss.actor, "2026-01-02T00:00:01.000Z", "says the admin")})
	o.h.commit(o.max.email, map[string]string{orgPath("actor_periods", roster): o.max.periodRowAt("2026-01-03T00:00:00.000Z", "admin", "")})
	b := orgFolderClone(t, o.h.remote)

	first := authorityNow(t, b)
	if _, err := os.Stat(b.authorityPath()); err != nil {
		t.Fatalf("the replay was not kept on disk: %v", err)
	}
	if heldReason(t, b, roster) == "" {
		t.Fatal("the member's own admin stint is not held: the fixture lost its point")
	}

	forgetAuthority(b)
	before := replayCount()
	again := authorityNow(t, b)
	if n := replayCount() - before; n != 0 {
		t.Errorf("a new process at the same HEAD replayed %d commit(s), want 0 (the replay on disk)", n)
	}
	sameAuthority(t, again, first)

	// a plain commit on top (a write, a fast-forward pull): only it replays
	own := mustID[ids.Memory](t)
	commitIn(t, b.work, o.max.email, orgPath("memories", own), stampedRow(own, o.max.actor, "2026-01-04T00:00:00.000Z", "mine"))
	forgetAuthority(b)
	before = replayCount()
	next := authorityNow(t, b)
	if n := replayCount() - before; n != 1 {
		t.Errorf("after one plain commit %d commit(s) were replayed, want 1", n)
	}
	sameAuthority(t, next, fullReplay(t, b))

	// a merge onto the saved HEAD: the side commit and the merge replay
	side := mustID[ids.Memory](t)
	gitT(t, b.work, "checkout", "--quiet", "-b", "side", "HEAD~1")
	commitIn(t, b.work, o.boss.email, orgPath("memories", side), stampedRow(side, o.boss.actor, "2026-01-05T00:00:00.000Z", "side"))
	gitT(t, b.work, "checkout", "--quiet", testBranch)
	gitT(t, b.work, "-c", "user.name=x", "-c", "user.email="+o.boss.email, "merge", "--quiet", "--no-ff", "--no-edit", "side")
	forgetAuthority(b)
	before = replayCount()
	merged := authorityNow(t, b)
	if n := replayCount() - before; n != 2 {
		t.Errorf("after a merge onto the saved HEAD %d commit(s) were replayed, want 2 (the side commit and the merge)", n)
	}
	sameAuthority(t, merged, fullReplay(t, b))

	// a fast-forward to a teammate's merge whose first parent is not the
	// saved HEAD: the order of the history changed, so all of it replays
	other := mustID[ids.Memory](t)
	gitT(t, b.work, "checkout", "--quiet", "-b", "theirs", "HEAD~1")
	commitIn(t, b.work, o.boss.email, orgPath("memories", other), stampedRow(other, o.boss.actor, "2026-01-06T00:00:00.000Z", "theirs"))
	gitT(t, b.work, "-c", "user.name=x", "-c", "user.email="+o.boss.email, "merge", "--quiet", "--no-ff", "--no-edit", testBranch)
	gitT(t, b.work, "checkout", "--quiet", testBranch)
	gitT(t, b.work, "merge", "--quiet", "--ff-only", "theirs")
	// without the replay kept at the remote tip, which would start it
	// there (devco:D-1000, TestSquashKeepsTheReplayIncremental)
	_ = os.Remove(b.authorityPath() + authorityBaseSuffix)
	forgetAuthority(b)
	before = replayCount()
	again = authorityNow(t, b)
	all, err := b.replayCommits(context.Background(), again.head)
	if err != nil {
		t.Fatal(err)
	}
	if n := replayCount() - before; n != int64(len(all)) {
		t.Errorf("after a fast-forward to a merge off the saved HEAD's line %d commit(s) were replayed, want the whole history (%d)", n, len(all))
	}
	sameAuthority(t, again, fullReplay(t, b))
}

// TestAuthorityOnDiskOfAnotherLoreIsNotUsed: a replay file written by
// another lore binary, or in another layout, is passed over (a new lore
// may judge commits by new rules); a broken one too.
func TestAuthorityOnDiskOfAnotherLoreIsNotUsed(t *testing.T) {
	o := newOrgTeam(t)
	b := orgFolderClone(t, o.h.remote)
	a := authorityNow(t, b)
	write := func(s savedAuthority) {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(&s); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(b.authorityPath(), buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, ok := a.saved(binaryStamp())
	if !ok {
		t.Fatal("no saved form")
	}
	if b.loadAuthority() == nil {
		t.Fatal("this lore's own replay file was not read back")
	}
	other := s
	other.Binary = "another lore"
	write(other)
	if b.loadAuthority() != nil {
		t.Error("a replay file of another lore binary was used")
	}
	old := s
	old.Format = authorityFormat + 1
	write(old)
	if b.loadAuthority() != nil {
		t.Error("a replay file of another layout was used")
	}
	if err := os.WriteFile(b.authorityPath(), []byte("not gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b.loadAuthority() != nil {
		t.Error("a broken replay file was used")
	}
	_ = os.Remove(b.authorityPath() + authorityBaseSuffix) // the replay at the remote tip would answer instead (devco:D-1000)
	forgetAuthority(b)
	before := replayCount()
	authorityNow(t, b)
	if replayCount() == before {
		t.Error("a broken replay file did not lead to a replay")
	}
	if b.loadAuthority() == nil {
		t.Error("the replay after a broken file was not written back")
	}
}

// commitIn writes one file into the clone at dir and commits it as author.
func commitIn(t *testing.T, dir, author, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", rel)
	gitT(t, dir, "-c", "user.name=x", "-c", "user.email="+author, "commit", "--quiet", "--no-verify", "--author", "x <"+author+">", "-m", "edit")
}

// TestAuthorityCarriedInMemory (devco:D-848): a process that holds the
// replay (the read server) carries it on over the plain commits a sync
// adds, without decoding the saved replay again: with the file gone only
// the new commit replays. The result is the full replay's, it is saved
// for the next process, and the replay held before is left as it was for
// any caller still reading it.
func TestAuthorityCarriedInMemory(t *testing.T) {
	o := newOrgTeam(t)
	roster := mustID[ids.ActorPeriod](t)
	o.h.commit(o.max.email, map[string]string{orgPath("actor_periods", roster): o.max.periodRowAt("2026-01-03T00:00:00.000Z", "admin", "")})
	b := orgFolderClone(t, o.h.remote)
	first := authorityNow(t, b)
	firstHead, firstAccepted := first.head, len(first.accepted)

	if err := os.Remove(b.authorityPath()); err != nil {
		t.Fatal(err)
	}
	own := mustID[ids.Memory](t)
	commitIn(t, b.work, o.max.email, orgPath("memories", own), stampedRow(own, o.max.actor, "2026-01-04T00:00:00.000Z", "mine"))
	before := replayCount()
	next := authorityNow(t, b)
	if n := replayCount() - before; n != 1 {
		t.Errorf("after one plain commit %d commit(s) were replayed, want 1 (the replay held in memory, carried on)", n)
	}
	sameAuthority(t, next, fullReplay(t, b))
	if first.head != firstHead || len(first.accepted) != firstAccepted || next == first {
		t.Error("carrying the replay on changed the one held before")
	}
	if b.loadAuthority() == nil {
		t.Error("the carried replay was not saved for the next process")
	}
}
