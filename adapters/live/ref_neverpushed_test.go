package live

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
)

// TestRemoteWithoutRefNeverPushedHere (A-6): a clone saw the live ref on
// one remote url; the remote it now names has no live ref. When the url
// changed (the repo moved to a new remote) nothing was deleted: integrate
// answers IntegrateNeverPushed, keeps the local ref and forgets the old
// marker, so writes go on. On the same url it is still a deletion
// (ErrRemoteRewritten), so a purge on the server is never undone.
func TestRemoteWithoutRefNeverPushedHere(t *testing.T) {
	for _, c := range []struct {
		name, nowURL string
		wantNever    bool
	}{
		{"moved to a new remote", "file:///srv/new.git", true},
		{"same remote, ref deleted", "file:///srv/old.git", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			w := gitremotetest.NewWorld(t)
			a, b := w.Clone("a"), w.Clone("b")
			ra, rb := newRef(t, a, a.Remote), newRef(t, b, b.Remote)
			rb.RemoteURL = "file:///srv/old.git"
			write(t, ra, put("tsk_1", NewVersion(nil, row("tsk_1", "keep", "low"), t0, "act_a")))
			mustPush(t, ra)
			_ = rb.Fetch(ctx)
			if _, err := rb.Integrate(ctx); err != nil {
				t.Fatal(err)
			}
			local, _, _ := b.Repo.ResolveRef(ctx, RefLive)
			gitremotetest.Git(t, w.OriginDir, "update-ref", "-d", RefLive)
			_ = rb.Fetch(ctx)
			rb.RemoteURL = c.nowURL
			res, err := rb.Integrate(ctx)
			if !c.wantNever {
				if !errors.Is(err, ErrRemoteRewritten) {
					t.Fatalf("same url: want ErrRemoteRewritten, got %v %+v", err, res)
				}
				return
			}
			if err != nil || res.Action != IntegrateNeverPushed {
				t.Fatalf("new url: want %s, got %+v %v", IntegrateNeverPushed, res, err)
			}
			if now, _, _ := b.Repo.ResolveRef(ctx, RefLive); now != local {
				t.Fatal("the local ref must not move")
			}
			if s, _ := rb.Snapshot(ctx); s.Seen != "" {
				t.Fatalf("seen marker kept: %q", s.Seen)
			}
			if res, err = rb.Integrate(ctx); err != nil || res.Action != IntegrateNothing {
				t.Fatalf("next run: %+v %v, want nothing (local ahead, never pushed here)", res, err)
			}
		})
	}
}

// TestRemoteWithoutRefURLNotRecorded (G-1): the seen marker was written by
// a lore that did not record the remote url, and the remote now has no
// live ref. A move cannot be told from a deletion, so writes stay stopped,
// but the cause is ErrRemoteGoneURLUnknown (callers name both cases and
// `lore live restore`), still an ErrRemoteRewritten, with a backup kept.
func TestRemoteWithoutRefURLNotRecorded(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	ra, rb := newRef(t, a, a.Remote), newRef(t, b, b.Remote)
	write(t, ra, put("tsk_1", NewVersion(nil, row("tsk_1", "keep", "low"), t0, "act_a")))
	mustPush(t, ra)
	_ = rb.Fetch(ctx)
	if _, err := rb.Integrate(ctx); err != nil { // RemoteURL unset: no url recorded, as an older lore
		t.Fatal(err)
	}
	gitremotetest.Git(t, w.OriginDir, "update-ref", "-d", RefLive)
	_ = rb.Fetch(ctx)
	rb.RemoteURL = "file:///srv/new.git"
	res, err := rb.Integrate(ctx)
	if !errors.Is(err, ErrRemoteGoneURLUnknown) || !errors.Is(err, ErrRemoteRewritten) {
		t.Fatalf("want ErrRemoteGoneURLUnknown, got %v %+v", err, res)
	}
	if res.Backup == "" {
		t.Fatal("no backup of the local live ref")
	}
}

// TestMovedRemoteRunBeforeFetch (G-1): after `git remote set-url` a run
// that does not fetch (a hook, a read inside the fetch interval) still
// integrates the old remote's tracking ref. The seen marker keeps the url
// that tracking ref came from, so the next fetch of the new remote (no
// live ref) is a move (IntegrateNeverPushed), not a deletion.
func TestMovedRemoteRunBeforeFetch(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	ra, rb := newRef(t, a, a.Remote), newRef(t, b, b.Remote)
	rb.RemoteURL = "file:///srv/old.git"
	write(t, ra, put("tsk_1", NewVersion(nil, row("tsk_1", "keep", "low"), t0, "act_a")))
	mustPush(t, ra)
	if err := rb.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := rb.Integrate(ctx); err != nil {
		t.Fatal(err)
	}
	rb.RemoteURL = "file:///srv/new.git" // set-url; this run does not fetch
	if _, err := rb.Integrate(ctx); err != nil {
		t.Fatal(err)
	}
	gitremotetest.Git(t, w.OriginDir, "update-ref", "-d", RefLive) // the new remote has no live ref
	if err := rb.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := rb.Integrate(ctx)
	if err != nil || res.Action != IntegrateNeverPushed {
		t.Fatalf("want %s, got %+v %v", IntegrateNeverPushed, res, err)
	}
}
