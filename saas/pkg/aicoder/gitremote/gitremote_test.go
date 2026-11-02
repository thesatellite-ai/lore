package gitremote_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	gt "saas/pkg/aicoder/gitremote/gitremotetest"
	"saas/pkg/aicoder/merge3"
)

const (
	live    = "refs/lore/live"
	signals = "refs/lore/signals"
)

func push(t *testing.T, r gitremote.Remote, req gitremote.PushRequest) gitremote.PushResult {
	t.Helper()
	res, err := r.Push(context.Background(), req)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(res.Refs) != len(req.Updates) {
		t.Fatalf("got %d verdicts for %d updates", len(res.Refs), len(req.Updates))
	}
	return res
}

func status(t *testing.T, res gitremote.PushResult, dst string) gitremote.Status {
	t.Helper()
	r, ok := res.Ref(dst)
	if !ok {
		t.Fatalf("no verdict for %s in %+v", dst, res)
	}
	return r.Status
}

// TestLeaseMatrix: what the remote holds × what the update does, for a
// --force-with-lease push. The lease must apply exactly when the remote is
// where the caller expected, whatever the update's shape.
func TestLeaseMatrix(t *testing.T) {
	type remoteState string
	const (
		asExpected remoteState = "as-expected"
		moved      remoteState = "moved"
		missing    remoteState = "deleted-remotely"
		neverSeen  remoteState = "expect-absent"
	)
	type updateKind string
	const (
		forward  updateKind = "fast-forward"
		replace  updateKind = "parentless-replace"
		deletion updateKind = "delete"
	)
	for _, rs := range []remoteState{asExpected, moved, missing, neverSeen} {
		for _, uk := range []updateKind{forward, replace, deletion} {
			t.Run(string(rs)+"/"+string(uk), func(t *testing.T) {
				ctx := context.Background()
				w := gt.NewWorld(t)
				a, b := w.Clone("a"), w.Clone("b")
				ref := signals
				var old string
				if rs != neverSeen {
					old = a.Write(ref, "claims/t1.json", "v1")
					a.MustPush(ref)
				}
				switch rs {
				case moved:
					b.Fetch()
					b.Repo.UpdateRef(ctx, ref, b.Ref("refs/lore-remote/origin/signals"), "")
					b.Write(ref, "claims/t2.json", "other")
					b.MustPush(ref)
				case missing:
					gt.Git(t, w.OriginDir, "update-ref", "-d", ref)
				}
				var src string
				switch uk {
				case forward:
					src = a.Write(ref, "claims/t1.json", "v2")
				case replace:
					res, err := a.Repo.WriteRef(ctx, gitref.WriteRequest{Ref: ref, History: gitref.HistoryReplace, Message: "lore: signals", Signature: gt.Sig,
						Changes: gitref.Changes{Put: map[string][]byte{"claims/t1.json": []byte("v3")}}})
					if err != nil {
						t.Fatal(err)
					}
					src = res.New
				case deletion:
					if rs == neverSeen {
						t.Skip("a delete needs an expected value")
					}
				}
				before := w.OriginRef(ref)
				res := push(t, a.Remote, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: src, Dst: ref, Mode: gitremote.ModeLease, Expect: old}}})
				got := status(t, res, ref)
				wantApplied := rs == asExpected || rs == neverSeen
				if got.Applied() != wantApplied {
					t.Fatalf("status %v, applied want %v", got, wantApplied)
				}
				after := w.OriginRef(ref)
				if wantApplied {
					if after != src {
						t.Fatalf("origin at %q want %q", after, src)
					}
					return
				}
				if got != gitremote.StatusStale {
					t.Fatalf("status %v want stale", got)
				}
				if after != before {
					t.Fatalf("refused lease still changed origin: %q → %q", before, after)
				}
			})
		}
	}
}

func TestFastForwardPushLosesToConcurrentWriter(t *testing.T) {
	w := gt.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	a.Write(live, "_root/tasks/t1.json", "a1")
	a.MustPush(live)
	b.Fetch()
	gt.Git(t, b.Dir, "update-ref", live, b.Ref("refs/lore-remote/origin/live"))
	bNew := b.Write(live, "_root/tasks/t2.json", "b")
	b.MustPush(live)
	aNew := a.Write(live, "_root/tasks/t3.json", "a2")
	res := push(t, a.Remote, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: aNew, Dst: live}}})
	if s := status(t, res, live); s != gitremote.StatusNonFastForward {
		t.Fatalf("status %v want non-fast-forward", s)
	}
	if w.OriginRef(live) != bNew {
		t.Fatal("plain push overwrote the other writer")
	}
	// Pushing the same commit again is up to date, not an error.
	res = push(t, b.Remote, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: bNew, Dst: live}}})
	if s := status(t, res, live); s != gitremote.StatusUpToDate || !res.OK() {
		t.Fatalf("re-push status %v", s)
	}
}

// TestAtomicIsAllOrNothing proves --atomic against a local bare repo: one
// stale lease refuses every ref, and nothing on the remote changes. The
// same push without --atomic applies the healthy ref. (Recorded: git 2.53's
// local transport supports atomic push.)
func TestAtomicIsAllOrNothing(t *testing.T) {
	for _, atomic := range []bool{true, false} {
		name := "atomic"
		if !atomic {
			name = "plain"
		}
		t.Run(name, func(t *testing.T) {
			w := gt.NewWorld(t)
			a, b := w.Clone("a"), w.Clone("b")
			sigOld := a.Write(signals, "claims/t1.json", "a-claim")
			a.MustPush(signals)
			// Another agent replaces the signals ref: a's lease is now stale.
			b.Fetch()
			bRes, err := b.Repo.WriteRef(context.Background(), gitref.WriteRequest{Ref: signals, History: gitref.HistoryReplace, Message: "lore: signals", Signature: gt.Sig,
				Changes: gitref.Changes{Put: map[string][]byte{"claims/t1.json": []byte("b-claim")}}})
			if err != nil {
				t.Fatal(err)
			}
			if r := push(t, b.Remote, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: bRes.New, Dst: signals, Mode: gitremote.ModeLease, Expect: sigOld}}}); !r.OK() {
				t.Fatalf("competing writer failed: %+v", r)
			}
			sigBefore, liveBefore := w.OriginRef(signals), w.OriginRef(live)
			sigNew := a.Write(signals, "claims/t1.json", "a-renew")
			liveNew := a.Write(live, "_root/tasks/t1.json", "done")
			res := push(t, a.Remote, gitremote.PushRequest{Atomic: atomic, Updates: []gitremote.RefUpdate{
				{Src: sigNew, Dst: signals, Mode: gitremote.ModeLease, Expect: sigOld},
				{Src: liveNew, Dst: live},
			}})
			if s := status(t, res, signals); s != gitremote.StatusStale {
				t.Fatalf("signals status %v", s)
			}
			if res.OK() {
				t.Fatal("result claims success")
			}
			if w.OriginRef(signals) != sigBefore {
				t.Fatal("stale lease changed signals")
			}
			if atomic {
				if s := status(t, res, live); s != gitremote.StatusAtomicFailed {
					t.Fatalf("live status %v want atomic-failed", s)
				}
				if w.OriginRef(live) != liveBefore {
					t.Fatal("atomic push applied the live half")
				}
			} else {
				if s := status(t, res, live); s != gitremote.StatusOK {
					t.Fatalf("live status %v want ok", s)
				}
				if w.OriginRef(live) != liveNew {
					t.Fatal("plain push did not apply the healthy ref")
				}
			}
		})
	}
	t.Run("atomic-success", func(t *testing.T) {
		w := gt.NewWorld(t)
		a := w.Clone("a")
		s, l := a.Write(signals, "c", "1"), a.Write(live, "r", "1")
		res := push(t, a.Remote, gitremote.PushRequest{Atomic: true, Updates: []gitremote.RefUpdate{
			{Src: s, Dst: signals, Mode: gitremote.ModeLease}, {Src: l, Dst: live}}})
		if !res.OK() || w.OriginRef(signals) != s || w.OriginRef(live) != l {
			t.Fatalf("atomic success = %+v", res)
		}
	})
}

// installHook makes the origin refuse every push with msg on stderr, like
// a host-side protection rule.
func installHook(t *testing.T, w *gt.World, msg string) {
	t.Helper()
	hook := filepath.Join(w.OriginDir, "hooks", "pre-receive")
	body := "#!/bin/sh\necho \"" + msg + "\" >&2\nexit 1\n"
	if err := os.WriteFile(hook, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestProtectionDetection(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want gitremote.Status
	}{
		{"github", "error: GH006: Protected branch update failed for refs/heads/main.", gitremote.StatusProtected},
		{"gitlab", "GitLab: You are not allowed to push code to protected branches on this project.", gitremote.StatusRemoteRejected},
		{"other hook", "error: file too large", gitremote.StatusRemoteRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := gt.NewWorld(t)
			a := w.Clone("a")
			gt.Git(t, a.Dir, "commit", "-q", "--allow-empty", "-m", "x")
			installHook(t, w, tc.msg)
			head := a.Ref("refs/heads/main")
			res := push(t, a.Remote, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: head, Dst: "refs/heads/main"}}})
			got := status(t, res, "refs/heads/main")
			// GitLab's message does not name the ref, so with a plain
			// "pre-receive hook declined" reason it cannot be told apart
			// from any other hook: fail safe as a generic rejection.
			if got != tc.want {
				t.Fatalf("status %v want %v (%+v)", got, tc.want, res)
			}
		})
	}
}

func TestOfflineIsUnconfirmed(t *testing.T) {
	ctx := context.Background()
	w := gt.NewWorld(t)
	a := w.Clone("a")
	sha := a.Write(live, "x", "x")
	gone := &gitremote.Git{Repo: a.Repo, Name: filepath.Join(w.Root, "no-such-remote.git")}
	if _, err := gone.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: sha, Dst: live}}}); !errors.Is(err, gitremote.ErrUnconfirmed) {
		t.Fatalf("push err = %v", err)
	}
	if err := gone.Fetch(ctx, gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs("origin")}); !errors.Is(err, gitremote.ErrUnconfirmed) {
		t.Fatalf("fetch err = %v", err)
	}
	if _, err := gone.List(ctx); !errors.Is(err, gitremote.ErrUnconfirmed) {
		t.Fatalf("list err = %v", err)
	}
	var te *gitremote.TransportError
	_, err := gone.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: sha, Dst: live}}})
	if !errors.As(err, &te) || te.Stderr == "" {
		t.Fatalf("transport error lacks git's message: %v", err)
	}
}

func TestPushValidation(t *testing.T) {
	w := gt.NewWorld(t)
	a := w.Clone("a")
	f := gitremote.NewFaultRemote(a.Remote)
	bad := []gitremote.PushRequest{
		{},
		{Updates: []gitremote.RefUpdate{{Src: "x", Dst: "main"}}},
		{Updates: []gitremote.RefUpdate{{Src: "x", Dst: live}, {Src: "y", Dst: live}}},
		{Updates: []gitremote.RefUpdate{{Src: "x", Dst: live, Expect: "y"}}},
		{Updates: []gitremote.RefUpdate{{Dst: live}}},
		{Updates: []gitremote.RefUpdate{{Dst: live, Mode: gitremote.ModeLease}}},
	}
	for i, req := range bad {
		for _, r := range []gitremote.Remote{a.Remote, f} {
			if _, err := r.Push(context.Background(), req); !errors.Is(err, gitremote.ErrBadRequest) {
				t.Errorf("case %d (%T): err = %v", i, r, err)
			}
		}
	}
	if f.Pushes() != 0 {
		t.Fatal("invalid requests were counted as pushes")
	}
}

// TestFetchNeverOverwritesLocalLive is F3's core promise: a clone with an
// unpushed live commit fetches a teammate's newer live ref; its own ref is
// untouched, the teammate's commit lands only in the tracking ref — even
// when .git/config carries the old forced refspec into refs/lore/*.
func TestFetchNeverOverwritesLocalLive(t *testing.T) {
	for _, dangerous := range []bool{false, true} {
		name := "clean-config"
		if dangerous {
			name = "forced-refspec-in-config"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			w := gt.NewWorld(t)
			a, b := w.Clone("a"), w.Clone("b")
			if dangerous {
				gt.Git(t, a.Dir, "config", "--add", "remote.origin.fetch", "+refs/lore/*:refs/lore/*")
			}
			// A writes while offline: the push fails, the commit stays local.
			aOffline := gitremote.NewFaultRemote(a.Remote)
			aOffline.SetOffline(true)
			aLocal := a.Write(live, "_root/tasks/a.json", "a")
			if _, err := aOffline.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: aLocal, Dst: live}}}); !errors.Is(err, gitremote.ErrUnconfirmed) {
				t.Fatalf("offline push err = %v", err)
			}
			// B writes and pushes.
			bNew := b.Write(live, "_root/tasks/b.json", "b")
			b.MustPush(live)
			// A comes online and fetches.
			a.Fetch()
			if a.Ref(live) != aLocal {
				t.Fatalf("local live overwritten: %s", a.Ref(live))
			}
			if a.Ref("refs/lore-remote/origin/live") != bNew {
				t.Fatal("tracking ref not updated")
			}
			if a.Ref("refs/remotes/origin/main") == "" {
				t.Fatal("default branch not fetched in the same run")
			}
			// The two lines merge with the primitives (F4 decides content;
			// here a plain union) and push as a fast-forward: nothing lost.
			base, err := a.Repo.ReadTree(ctx, aLocal, "")
			if err != nil {
				t.Fatal(err)
			}
			theirs, err := a.Repo.ReadTree(ctx, bNew, "")
			if err != nil {
				t.Fatal(err)
			}
			for p, c := range theirs {
				base[p] = c
			}
			merged, err := a.Repo.MakeCommit(ctx, gitref.CommitRequest{Base: aLocal, MergeParents: []string{bNew}, Message: "lore: live merge", Signature: gt.Sig, Changes: gitref.Changes{Put: base}})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Repo.UpdateRef(ctx, live, merged.OID, aLocal); err != nil {
				t.Fatal(err)
			}
			a.MustPush(live)
			files, err := w.Origin.ReadTree(ctx, live, "")
			if err != nil || !slices.Equal(sortedKeys(files), []string{"_root/tasks/a.json", "_root/tasks/b.json"}) {
				t.Fatalf("origin live = %v %v", sortedKeys(files), err)
			}
		})
	}
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestFetchPruneAndMissing(t *testing.T) {
	ctx := context.Background()
	w := gt.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	// Nothing under refs/lore/* yet: the glob fetch is fine.
	a.Fetch()
	b.Write(signals, "c", "1")
	b.MustPush(signals)
	a.Fetch()
	if a.Ref("refs/lore-remote/origin/signals") == "" {
		t.Fatal("signals not tracked")
	}
	gt.Git(t, w.OriginDir, "update-ref", "-d", signals)
	a.Fetch()
	if a.Ref("refs/lore-remote/origin/signals") != "" {
		t.Fatal("deleted remote ref not pruned from tracking")
	}
	err := a.Remote.Fetch(ctx, gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs("origin", "no-such-branch")})
	if !errors.Is(err, gitremote.ErrRemoteRefMissing) {
		t.Fatalf("missing branch err = %v", err)
	}
	// Prune never touches local refs/lore/*: only tracking destinations.
	a.Write(live, "x", "x")
	a.Fetch()
	if a.Ref(live) == "" {
		t.Fatal("prune deleted a local lore ref")
	}
}

func TestBareCloneFetchAndPush(t *testing.T) {
	// A store clone is bare; tracking and pushing must work the same.
	w := gt.NewWorld(t)
	s := w.BareClone("store")
	s.Fetch()
	tip := s.Ref("refs/remotes/origin/main")
	if tip == "" {
		t.Fatal("bare clone did not track main")
	}
	c, err := s.Repo.MakeCommit(context.Background(), gitref.CommitRequest{Base: tip, Message: "lore: land 1 row", Signature: gt.Sig,
		Changes: gitref.Changes{Put: map[string][]byte{"acme/.lore/data/tasks/t.json": []byte("{}")}}})
	if err != nil {
		t.Fatal(err)
	}
	res := push(t, s.Remote, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: c.OID, Dst: "refs/heads/main", Mode: gitremote.ModeLease, Expect: tip}}})
	if !res.OK() || w.OriginRef("refs/heads/main") != c.OID {
		t.Fatalf("store landing = %+v", res)
	}
}

func TestList(t *testing.T) {
	w := gt.NewWorld(t)
	a := w.Clone("a")
	l := a.Write(live, "x", "x")
	a.MustPush(live)
	refs, err := a.Remote.List(context.Background(), "refs/lore/*")
	if err != nil || len(refs) != 1 || refs[live] != l {
		t.Fatalf("List = %v %v", refs, err)
	}
}

func TestEnsureTrackingRefspec(t *testing.T) {
	ctx := context.Background()
	w := gt.NewWorld(t)
	a := w.Clone("a")
	st, err := gitremote.EnsureTrackingRefspec(ctx, a.Repo, "origin")
	if err != nil || !st.Added || len(st.Unsafe) != 0 {
		t.Fatalf("first = %+v %v", st, err)
	}
	st, err = gitremote.EnsureTrackingRefspec(ctx, a.Repo, "origin")
	if err != nil || st.Added {
		t.Fatalf("second = %+v %v", st, err)
	}
	specs := gt.Git(t, a.Dir, "config", "--get-all", "remote.origin.fetch")
	if specs != "+refs/heads/*:refs/remotes/origin/*\n+refs/lore/*:refs/lore-remote/origin/*" {
		t.Fatalf("config = %q", specs)
	}
	gt.Git(t, a.Dir, "config", "--add", "remote.origin.fetch", "+refs/lore/*:refs/lore/*")
	gt.Git(t, a.Dir, "config", "--add", "remote.origin.fetch", "refs/lore/live:refs/lore/live")
	st, err = gitremote.EnsureTrackingRefspec(ctx, a.Repo, "origin")
	if err != nil || st.Added || !slices.Equal(st.Unsafe, []string{"+refs/lore/*:refs/lore/*", "refs/lore/live:refs/lore/live"}) {
		t.Fatalf("unsafe = %+v %v", st, err)
	}
	// A plain `git fetch` now fills the tracking namespace.
	b := w.Clone("b")
	b.Write(live, "y", "y")
	b.MustPush(live)
	c := w.Clone("c")
	if _, err := gitremote.EnsureTrackingRefspec(ctx, c.Repo, "origin"); err != nil {
		t.Fatal(err)
	}
	gt.Git(t, c.Dir, "fetch", "-q")
	if c.Ref("refs/lore-remote/origin/live") == "" || c.Ref(live) != "" {
		t.Fatal("configured refspec did not track live, or wrote the local ref")
	}
	// A remote with no fetch config at all (a bare store clone made by hand).
	gt.Git(t, c.Dir, "remote", "add", "up", w.OriginDir)
	gt.Git(t, c.Dir, "config", "--unset-all", "remote.up.fetch")
	if st, err := gitremote.EnsureTrackingRefspec(ctx, c.Repo, "up"); err != nil || !st.Added {
		t.Fatalf("no-config remote = %+v %v", st, err)
	}
}

func TestTrackingRef(t *testing.T) {
	cases := map[string]string{
		"refs/lore/live":         "refs/lore-remote/origin/live",
		"refs/lore/signals/main": "refs/lore-remote/origin/signals/main",
		"refs/heads/main":        "refs/remotes/origin/main",
		"refs/heads/feat/x":      "refs/remotes/origin/feat/x",
	}
	for in, want := range cases {
		if got, ok := gitremote.TrackingRef("origin", in); !ok || got != want {
			t.Errorf("TrackingRef(%s) = %s %v", in, got, ok)
		}
	}
	if _, ok := gitremote.TrackingRef("origin", "refs/tags/v1"); ok {
		t.Error("tags have no tracking ref")
	}
}

func TestFaultRemote(t *testing.T) {
	ctx := context.Background()
	t.Run("reject nth push", func(t *testing.T) {
		w := gt.NewWorld(t)
		a := w.Clone("a")
		f := gitremote.NewFaultRemote(a.Remote)
		f.RejectPush(1, gitremote.StatusNonFastForward)
		sha := a.Write(live, "x", "x")
		req := gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: sha, Dst: live}}}
		if res := push(t, f, req); status(t, res, live) != gitremote.StatusNonFastForward || w.OriginRef(live) != "" {
			t.Fatalf("1st push = %+v", res)
		}
		if res := push(t, f, req); !res.OK() || w.OriginRef(live) != sha {
			t.Fatalf("2nd push = %+v", res)
		}
		if f.Pushes() != 2 {
			t.Fatalf("pushes = %d", f.Pushes())
		}
	})
	t.Run("reject atomic marks the rest", func(t *testing.T) {
		w := gt.NewWorld(t)
		a := w.Clone("a")
		f := gitremote.NewFaultRemote(a.Remote)
		f.RejectPush(1, gitremote.StatusStale)
		res := push(t, f, gitremote.PushRequest{Atomic: true, Updates: []gitremote.RefUpdate{
			{Src: a.Write(signals, "c", "1"), Dst: signals, Mode: gitremote.ModeLease}, {Src: a.Write(live, "r", "1"), Dst: live}}})
		if status(t, res, signals) != gitremote.StatusStale || status(t, res, live) != gitremote.StatusStale {
			t.Fatalf("result = %+v", res)
		}
	})
	t.Run("protected", func(t *testing.T) {
		for _, atomic := range []bool{true, false} {
			w := gt.NewWorld(t)
			a := w.Clone("a")
			gt.Git(t, a.Dir, "commit", "-q", "--allow-empty", "-m", "x")
			f := gitremote.NewFaultRemote(a.Remote)
			f.Protect("refs/heads/main")
			l := a.Write(live, "r", "1")
			before := w.OriginRef("refs/heads/main")
			res := push(t, f, gitremote.PushRequest{Atomic: atomic, Updates: []gitremote.RefUpdate{
				{Src: a.Ref("refs/heads/main"), Dst: "refs/heads/main"}, {Src: l, Dst: live}}})
			m, _ := res.Ref("refs/heads/main")
			if m.Status != gitremote.StatusProtected || w.OriginRef("refs/heads/main") != before {
				t.Fatalf("atomic=%v main = %+v", atomic, m)
			}
			wantLive := gitremote.StatusOK
			if atomic {
				wantLive = gitremote.StatusAtomicFailed
			}
			if s := status(t, res, live); s != wantLive || (w.OriginRef(live) == l) != !atomic {
				t.Fatalf("atomic=%v live = %v", atomic, s)
			}
		}
	})
	t.Run("offline and back", func(t *testing.T) {
		w := gt.NewWorld(t)
		a := w.Clone("a")
		f := gitremote.NewFaultRemote(a.Remote)
		f.SetOffline(true)
		if err := f.Fetch(ctx, gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs("origin")}); !errors.Is(err, gitremote.ErrUnconfirmed) {
			t.Fatalf("fetch = %v", err)
		}
		if _, err := f.List(ctx); !errors.Is(err, gitremote.ErrUnconfirmed) {
			t.Fatalf("list = %v", err)
		}
		f.SetOffline(false)
		if err := f.Fetch(ctx, gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs("origin")}); err != nil || f.Fetches() != 2 {
			t.Fatalf("online fetch = %v (%d)", err, f.Fetches())
		}
	})
	t.Run("no atomic", func(t *testing.T) {
		w := gt.NewWorld(t)
		a := w.Clone("a")
		f := gitremote.NewFaultRemote(a.Remote)
		f.SetNoAtomic(true)
		_, err := f.Push(ctx, gitremote.PushRequest{Atomic: true, Updates: []gitremote.RefUpdate{{Src: a.Write(live, "r", "1"), Dst: live}}})
		if !errors.Is(err, gitremote.ErrAtomicUnsupported) || w.OriginRef(live) != "" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("concurrent writer between fetch and push", func(t *testing.T) {
		w := gt.NewWorld(t)
		a, b := w.Clone("a"), w.Clone("b")
		old := a.Write(signals, "claims/t1.json", "free")
		a.MustPush(signals)
		f := gitremote.NewFaultRemote(a.Remote)
		var bWrote string
		f.BeforePush(1, func(ctx context.Context) error {
			b.Fetch()
			res, err := b.Repo.WriteRef(ctx, gitref.WriteRequest{Ref: signals, History: gitref.HistoryReplace, Message: "lore: signals", Signature: gt.Sig,
				Changes: gitref.Changes{Put: map[string][]byte{"claims/t1.json": []byte("b holds")}}})
			if err != nil {
				return err
			}
			bWrote = res.New
			_, err = b.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: res.New, Dst: signals, Mode: gitremote.ModeLease, Expect: old}}})
			return err
		})
		mine := a.Write(signals, "claims/t1.json", "a holds")
		res := push(t, f, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: mine, Dst: signals, Mode: gitremote.ModeLease, Expect: old}}})
		if status(t, res, signals) != gitremote.StatusStale || w.OriginRef(signals) != bWrote {
			t.Fatalf("racing claim = %+v, origin %s", res, w.OriginRef(signals))
		}
	})
	t.Run("ref deleted remotely before push", func(t *testing.T) {
		w := gt.NewWorld(t)
		a := w.Clone("a")
		old := a.Write(signals, "c", "1")
		a.MustPush(signals)
		f := gitremote.NewFaultRemote(a.Remote)
		f.BeforePush(1, func(context.Context) error {
			gt.Git(t, w.OriginDir, "update-ref", "-d", signals)
			return nil
		})
		res := push(t, f, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: a.Write(signals, "c", "2"), Dst: signals, Mode: gitremote.ModeLease, Expect: old}}})
		if status(t, res, signals) != gitremote.StatusStale || w.OriginRef(signals) != "" {
			t.Fatalf("deleted ref lease = %+v", res)
		}
	})
}

// TestManyLeaseWritersOneWinnerPerRound: N agents race to claim with a
// lease on the same signals ref; in each round exactly one wins and every
// loser sees StatusStale — the property claims are built on.
func TestManyLeaseWritersOneWinnerPerRound(t *testing.T) {
	ctx := context.Background()
	w := gt.NewWorld(t)
	seed := w.Clone("seed")
	old := seed.Write(signals, "claims/t1.json", "free")
	seed.MustPush(signals)
	const agents = 6
	clones := make([]*gt.Clone, agents)
	for i := range clones {
		clones[i] = w.Clone("agent" + string(rune('a'+i)))
	}
	type outcome struct {
		sha string
		st  gitremote.Status
	}
	results := make([]outcome, agents)
	var wg sync.WaitGroup
	for i, c := range clones {
		wg.Go(func() {
			res, err := c.Repo.WriteRef(ctx, gitref.WriteRequest{Ref: signals, History: gitref.HistoryReplace, Message: "lore: signals", Signature: gt.Sig,
				Changes: gitref.Changes{Put: map[string][]byte{"claims/t1.json": []byte(c.Name)}}})
			if err != nil {
				t.Error(err)
				return
			}
			pr, err := c.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: res.New, Dst: signals, Mode: gitremote.ModeLease, Expect: old}}})
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = outcome{res.New, pr.Refs[0].Status}
		})
	}
	wg.Wait()
	winners := 0
	for _, r := range results {
		switch r.st {
		case gitremote.StatusOK:
			winners++
			if w.OriginRef(signals) != r.sha {
				t.Fatal("winner's commit is not on the origin")
			}
		case gitremote.StatusStale:
		default:
			t.Fatalf("unexpected status %v", r.st)
		}
	}
	if winners != 1 {
		t.Fatalf("%d winners, want exactly 1", winners)
	}
}

// TestManyPlainWritersRaceOnLive: N clones push a fast-forward of the same
// live tip at once; exactly one is applied and every loser is reported as
// non-fast-forward (fetch, merge, retry), never as a refusal.
func TestManyPlainWritersRaceOnLive(t *testing.T) {
	ctx := context.Background()
	w := gt.NewWorld(t)
	seed := w.Clone("seed")
	seed.Write(live, "_root/tasks/t0.json", "0")
	seed.MustPush(live)
	const writers = 6
	clones := make([]*gt.Clone, writers)
	for i := range clones {
		clones[i] = w.Clone("w" + string(rune('a'+i)))
		clones[i].SyncRef(live)
	}
	statuses := make([]gitremote.Status, writers)
	var wg sync.WaitGroup
	for i, c := range clones {
		wg.Go(func() {
			sha := c.Write(live, "_root/tasks/"+c.Name+".json", c.Name)
			pr, err := c.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: sha, Dst: live}}})
			if err != nil {
				t.Error(err)
				return
			}
			statuses[i] = pr.Refs[0].Status
		})
	}
	wg.Wait()
	ok := 0
	for _, s := range statuses {
		switch s {
		case gitremote.StatusOK:
			ok++
		case gitremote.StatusNonFastForward:
		default:
			t.Fatalf("unexpected status %v", s)
		}
	}
	if ok != 1 {
		t.Fatalf("%d pushes applied, want 1", ok)
	}
}

// loadFile reads one row file of rev as a merge3.File: absent, a row, or a
// tombstone (`_live.deleted: true`, the marker F2 writes).
func loadFile(t *testing.T, r *gitref.Repo, rev, path string) merge3.File {
	t.Helper()
	b, ok, err := r.ReadFile(context.Background(), rev, path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return merge3.Absent()
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if lv, _ := doc["_live"].(map[string]any); lv != nil && lv["deleted"] == true {
		return merge3.Tombstone(doc)
	}
	return merge3.Row(doc)
}

// TestDeleteVersusEditAcrossClones is the F4 E2E shape on the primitives:
// clone A tombstones a live row and pushes; clone B edits it, loses the
// push race, fetches, merges file-level (modify beats delete) and pushes a
// merge commit. The row survives with B's edit; nothing is half a row.
func TestDeleteVersusEditAcrossClones(t *testing.T) {
	ctx := context.Background()
	const row = "_root/tasks/tsk_02.json"
	w := gt.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	a.Write(live, row, `{"id":"tsk_02","status":"todo","title":"fix login"}`)
	a.MustPush(live)
	b.SyncRef(live)

	a.Write(live, row, `{"_live":{"deleted":true},"id":"tsk_02"}`)
	a.MustPush(live)
	bEdit := b.Write(live, row, `{"id":"tsk_02","status":"todo","title":"fix login timeout"}`)
	res := push(t, b.Remote, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: bEdit, Dst: live}}})
	if status(t, res, live) != gitremote.StatusNonFastForward {
		t.Fatalf("B's push = %+v", res)
	}

	b.Fetch()
	theirs := b.Ref("refs/lore-remote/origin/live")
	baseRev, ok, err := b.Repo.MergeBase(ctx, bEdit, theirs)
	if err != nil || !ok {
		t.Fatalf("merge base = %v %v", ok, err)
	}
	fr, err := merge3.MergeFile(loadFile(t, b.Repo, baseRev, row), loadFile(t, b.Repo, bEdit, row), loadFile(t, b.Repo, theirs, row), merge3.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if fr.Decision != merge3.DecisionModifyBeatsDelete || fr.DeletedBy != merge3.SideTheirs || fr.File.Kind != merge3.FileRow {
		t.Fatalf("merge = %v deleted by %v kind %v", fr.Decision, fr.DeletedBy, fr.File.Kind)
	}
	out, err := json.Marshal(fr.File.Doc)
	if err != nil {
		t.Fatal(err)
	}
	m, err := b.Repo.MakeCommit(ctx, gitref.CommitRequest{Base: bEdit, MergeParents: []string{theirs}, Message: "lore: live merge", Signature: gt.Sig,
		Changes: gitref.Changes{Put: map[string][]byte{row: out}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Repo.UpdateRef(ctx, live, m.OID, bEdit); err != nil {
		t.Fatal(err)
	}
	b.MustPush(live)

	final := loadFile(t, w.Origin, live, row)
	if final.Kind != merge3.FileRow || final.Doc["title"] != "fix login timeout" {
		t.Fatalf("origin row = %v %v", final.Kind, final.Doc)
	}
	if n := gt.Git(t, w.OriginDir, "rev-list", "--count", live); n != "4" {
		t.Fatalf("live history has %s commits, want 4 (seed, tombstone, edit, merge)", n)
	}
}
