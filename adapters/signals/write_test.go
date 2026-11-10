package signals

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
	"github.com/thesatellite-ai/lore/logic/clock"
)

const liveRef = "refs/lore/live"

// seqTokens hands out ct_<name>_<n>.
type seqTokens struct {
	name string
	n    atomic.Int64
}

func (s *seqTokens) New() (string, error) { return fmt.Sprintf("ct_%s_%d", s.name, s.n.Add(1)), nil }

// noSleep makes retries instant in tests.
func noSleep(context.Context, time.Duration) error { return nil }

func newStore(t *testing.T, c *gitremotetest.Clone, remote gitremote.Remote, clk clock.Clock) *Store {
	t.Helper()
	return &Store{
		Repo: c.Repo, Remote: remote, RemoteName: gitremotetest.RemoteName, Ref: CodeRepoRef,
		Seen: SeenStore{Path: filepath.Join(t.TempDir(), "seen.json")}, Clock: clk, Lease: lease,
		Signature: func(at time.Time) gitref.Signature { return gitref.Signature{Name: "t", Email: "t@t", When: at} },
		Autopush:  true, Tokens: &seqTokens{name: c.Name}, Sleep: noSleep,
	}
}

func claimDecide(s *Store, c Caller, id string, out *ClaimPlan, comps ...Companion) Decide {
	return func(v View) (Change, error) {
		tok, err := s.Tokens.New()
		if err != nil {
			return Change{}, err
		}
		plan, err := DecideClaim(v, ClaimRequest{Caller: c, Folder: folder, ID: id, CountsTowardWIP: true, MaxClaims: 1, MaxAttempts: 3, Token: tok})
		if err != nil {
			return Change{}, err
		}
		*out = plan
		if plan.Resumed {
			return Change{}, nil
		}
		return Change{Put: map[string]Signal{plan.Path: plan.Signal}, Companions: comps}, nil
	}
}

func originSignals(t *testing.T, w *gitremotetest.World) (Board, int, int) {
	t.Helper()
	ctx := context.Background()
	oid := w.OriginRef(CodeRepoRef)
	if oid == "" {
		return Board{}, 0, 0
	}
	tree, err := w.Origin.ReadTree(ctx, oid, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBoard(tree)
	n := gitremotetest.Git(t, w.OriginDir, "rev-list", "--count", CodeRepoRef)
	parents := gitremotetest.Git(t, w.OriginDir, "rev-list", "--parents", "-n1", CodeRepoRef)
	np := len(splitFields(parents)) - 1
	var count int
	_, _ = fmt.Sscan(n, &count)
	return b, count, np
}

func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\n' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// TestWriteClaimRenewReleaseIsOneParentlessCommit pins fixed rule 10 on a
// real remote: every write leaves exactly one parentless commit.
func TestWriteClaimRenewReleaseIsOneParentlessCommit(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	clk := clock.NewFake(t0)
	s := newStore(t, a, a.Remote, clk)

	var plan ClaimPlan
	if _, err := s.Write(ctx, claimDecide(s, me, task, &plan)); err != nil {
		t.Fatal(err)
	}
	b, n, p := originSignals(t, w)
	if n != 1 || p != 0 || b[ClaimPath(folder, task)].ClaimToken != plan.Signal.ClaimToken {
		t.Fatalf("after claim: %d commits %d parents board %v", n, p, b)
	}
	if subj := gitremotetest.Git(t, w.OriginDir, "log", "-1", "--format=%s", CodeRepoRef); subj != CommitMessage {
		t.Fatalf("message %q", subj)
	}
	clk.Advance(lease.RenewEvery)
	if _, err := s.Write(ctx, func(v View) (Change, error) {
		r, err := DecideRenew(v, me, folder, task)
		return Change{Put: map[string]Signal{ClaimPath(folder, task): r}}, err
	}); err != nil {
		t.Fatal(err)
	}
	b, n, p = originSignals(t, w)
	if n != 1 || p != 0 || b[ClaimPath(folder, task)].RenewedSeq != 1 {
		t.Fatalf("after renew: %d commits %d parents %+v", n, p, b)
	}
	if _, err := s.Write(ctx, func(v View) (Change, error) {
		sig, err := DecideRelease(v, me, folder, task)
		return Change{Delete: []string{ClaimPath(folder, task)}}, errors.Join(err, nilIf(sig.ClaimToken != ""))
	}); err != nil {
		t.Fatal(err)
	}
	b, n, p = originSignals(t, w)
	if n != 1 || p != 0 || len(b) != 0 {
		t.Fatalf("after release: %d commits %d parents %v", n, p, b)
	}
}

func nilIf(ok bool) error {
	if ok {
		return nil
	}
	return errors.New("expected a signal")
}

// TestWriteRaceExactlyOneWins: three clones claim the same task at once;
// exactly one wins, the others see HeldError after re-deciding.
func TestWriteRaceExactlyOneWins(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	var wg sync.WaitGroup
	results := make([]error, 3)
	start := make(chan struct{})
	for i := 0; i < 3; i++ {
		c := w.Clone(fmt.Sprintf("c%d", i))
		s := newStore(t, c, c.Remote, clock.NewFake(t0))
		caller := Caller{ActorID: fmt.Sprintf("act_%d", i), Session: "s"}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var plan ClaimPlan
			_, results[i] = s.Write(ctx, claimDecide(s, caller, task, &plan))
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for i, err := range results {
		var he *HeldError
		switch {
		case err == nil:
			wins++
		case errors.As(err, &he):
		default:
			t.Fatalf("clone %d: unexpected %v", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one claim must win, got %d (%v)", wins, results)
	}
	if _, n, p := originSignals(t, w); n != 1 || p != 0 {
		t.Fatalf("still one parentless commit: %d/%d", n, p)
	}
}

// TestWriteUnrelatedRaceRetriesNeverLoses: another writer changes an
// unrelated signal between our fetch and push; we re-decide and both
// signals survive (F7 edge case "rejected by an unrelated signal").
func TestWriteUnrelatedRaceRetriesNeverLoses(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	fa := gitremote.NewFaultRemote(a.Remote)
	sa := newStore(t, a, fa, clock.NewFake(t0))
	sb := newStore(t, b, b.Remote, clock.NewFake(t0))
	fa.BeforePush(1, func(ctx context.Context) error {
		var p ClaimPlan
		_, err := sb.Write(ctx, claimDecide(sb, other, "tsk_99", &p))
		return err
	})
	var plan ClaimPlan
	res, err := sa.Write(ctx, claimDecide(sa, me, task, &plan))
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempts != 2 {
		t.Fatalf("one lost race then success: attempts=%d", res.Attempts)
	}
	board, _, _ := originSignals(t, w)
	if len(board) != 2 {
		t.Fatalf("both claims must be on the ref: %v", board)
	}
}

// TestWriteFailsClosed: offline, autopush off and exhausted retries are
// ErrOffline and leave the remote untouched (fixed rule 11).
func TestWriteFailsClosed(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(f *gitremote.FaultRemote, s *Store)
	}{
		{"offline", func(f *gitremote.FaultRemote, _ *Store) { f.SetOffline(true) }},
		{"autopush off", func(_ *gitremote.FaultRemote, s *Store) { s.Autopush = false }},
		{"retries used up", func(f *gitremote.FaultRemote, _ *Store) {
			for i := 1; i <= 10; i++ {
				f.RejectPush(i, gitremote.StatusStale)
			}
		}},
		{"offline at push", func(_ *gitremote.FaultRemote, s *Store) { s.Remote = unconfirmedPush{s.Remote} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := gitremotetest.NewWorld(t)
			a := w.Clone("a")
			f := gitremote.NewFaultRemote(a.Remote)
			s := newStore(t, a, f, clock.NewFake(t0))
			tc.setup(f, s)
			var plan ClaimPlan
			_, err := s.Write(ctx, claimDecide(s, me, task, &plan))
			if !errors.Is(err, ErrOffline) {
				t.Fatalf("want ErrOffline, got %v", err)
			}
			if w.OriginRef(CodeRepoRef) != "" {
				t.Fatal("a failed coordination write must leave the remote untouched")
			}
			if a.Ref("refs/lore-remote/origin/signals") != "" {
				t.Fatal("no local signals state may be left to push later")
			}
		})
	}
}

// liveRow is a test companion that writes one file on refs/lore/live.
type liveRow struct {
	c        *gitremotetest.Clone
	path     string
	body     string
	prepared string
	finals   int
}

func (l *liveRow) Prepare(ctx context.Context) (gitremote.RefUpdate, error) {
	l.c.Fetch()
	base := l.c.Ref("refs/lore-remote/origin/live")
	commit, err := l.c.Repo.MakeCommit(ctx, gitref.CommitRequest{Base: base, Changes: gitref.Changes{Put: map[string][]byte{l.path: []byte(l.body)}},
		Message: "lore: live", Signature: gitremotetest.Sig})
	if err != nil {
		return gitremote.RefUpdate{}, err
	}
	l.prepared = commit.OID
	return gitremote.RefUpdate{Src: commit.OID, Dst: liveRef, Mode: gitremote.ModeFastForward}, nil
}

func (l *liveRow) Finalize(ctx context.Context) error {
	l.finals++
	return nil
}

// TestCompanionIsFencedByTheSignalsLease: a zombie's done (signals keep +
// live row) must change nothing once the heir reclaimed the task; with
// atomic and with the ordered fallback.
func TestCompanionIsFencedByTheSignalsLease(t *testing.T) {
	for _, noAtomic := range []bool{false, true} {
		t.Run(fmt.Sprintf("noAtomic=%v", noAtomic), func(t *testing.T) {
			ctx := context.Background()
			w := gitremotetest.NewWorld(t)
			z, h := w.Clone("zombie"), w.Clone("heir")
			fz := gitremote.NewFaultRemote(z.Remote)
			fz.SetNoAtomic(noAtomic)
			zclk, hclk := clock.NewFake(t0), clock.NewFake(t0)
			sz, sh := newStore(t, z, fz, zclk), newStore(t, h, h.Remote, hclk)

			var zp ClaimPlan
			if _, err := sz.Write(ctx, claimDecide(sz, me, task, &zp, &liveRow{c: z, path: "_root/tasks/tsk_30.json", body: "in_progress"})); err != nil {
				t.Fatal(err)
			}
			liveAfterClaim := w.OriginRef(liveRef)
			if liveAfterClaim == "" {
				t.Fatal("the claim's companion must reach the live ref")
			}
			// The heir watches the claim, then reclaims after ttl + grace.
			if _, err := sh.Read(ctx); err != nil {
				t.Fatal(err)
			}
			hclk.Set(t0.Add(lease.TTL + lease.Grace + time.Minute))
			var hp ClaimPlan
			if _, err := sh.Write(ctx, func(v View) (Change, error) {
				tok, _ := sh.Tokens.New()
				plan, err := DecideClaim(v, ClaimRequest{Caller: other, Folder: folder, ID: task, CountsTowardWIP: true, MaxClaims: 1, MaxAttempts: 3, Row: RowState{Epoch: 1, Attempts: 1}, Token: tok})
				hp = plan
				return Change{Put: map[string]Signal{plan.Path: plan.Signal}}, err
			}); err != nil || !hp.Reclaim {
				t.Fatalf("heir reclaim: %+v %v", hp, err)
			}
			// The zombie wakes up with its stale view and tries to finish:
			// it decides on a fresh fetch and is fenced out.
			zclk.Set(t0.Add(time.Minute))
			done := &liveRow{c: z, path: "_root/tasks/tsk_30.json", body: "done"}
			_, err := sz.Write(ctx, func(v View) (Change, error) {
				if _, err := CheckHolder(v, me, folder, task, 1); err != nil {
					return Change{}, err
				}
				return Change{Delete: []string{ClaimPath(folder, task)}, Companions: []Companion{done}}, nil
			})
			if !errors.Is(err, ErrNotHeld) {
				t.Fatalf("zombie must be fenced out: %v", err)
			}
			if w.OriginRef(liveRef) != liveAfterClaim || done.finals != 0 {
				t.Fatal("a fenced-out holder must not change the row")
			}
		})
	}
}

// TestCompanionLeaseWhenSignalsUnchanged: a holder write that keeps its
// claim (a note) still carries a lease on the signals ref; a concurrent
// reclaim between decide and push makes the atomic push fail and retry.
func TestCompanionLeaseWhenSignalsUnchanged(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	fa := gitremote.NewFaultRemote(a.Remote)
	sa, sb := newStore(t, a, fa, clock.NewFake(t0)), newStore(t, b, b.Remote, clock.NewFake(t0))
	var p ClaimPlan
	if _, err := sa.Write(ctx, claimDecide(sa, me, task, &p)); err != nil {
		t.Fatal(err)
	}
	// Right before the note's push, a human revokes the claim from b.
	fa.BeforePush(2, func(ctx context.Context) error {
		_, err := sb.Write(ctx, func(v View) (Change, error) {
			return Change{Delete: []string{ClaimPath(folder, task)}}, nil
		})
		return err
	})
	note := &liveRow{c: a, path: "_root/comments/c1.json", body: "note"}
	_, err := sa.Write(ctx, func(v View) (Change, error) {
		if _, err := CheckHolder(v, me, folder, task, 1); err != nil {
			return Change{}, err
		}
		return Change{Companions: []Companion{note}}, nil
	})
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("the revoke must fence the note on the retry: %v", err)
	}
	if w.OriginRef(liveRef) != "" {
		t.Fatal("the fenced note must not reach the live ref")
	}
}

// TestWriterDropsExpiredFiles pins fixed rule 10's cleanup.
func TestWriterDropsExpiredFiles(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	sa := newStore(t, a, a.Remote, clock.NewFake(t0))
	bclk := clock.NewFake(t0)
	sb := newStore(t, b, b.Remote, bclk)
	var p ClaimPlan
	if _, err := sa.Write(ctx, claimDecide(sa, me, task, &p)); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.Read(ctx); err != nil { // b starts watching
		t.Fatal(err)
	}
	bclk.Set(t0.Add(3 * lease.TTL))
	var q ClaimPlan
	if _, err := sb.Write(ctx, claimDecide(sb, other, "tsk_31", &q)); err != nil {
		t.Fatal(err)
	}
	board, _, _ := originSignals(t, w)
	if _, ok := board[ClaimPath(folder, task)]; ok || len(board) != 1 {
		t.Fatalf("expired claim must be dropped by the next writer: %v", board)
	}
}

// unconfirmedPush fetches normally but every push gets no verdict, as when
// the connection drops mid-push.
type unconfirmedPush struct{ gitremote.Remote }

func (unconfirmedPush) Push(context.Context, gitremote.PushRequest) (gitremote.PushResult, error) {
	return gitremote.PushResult{}, &gitremote.TransportError{Op: "push", Err: gitremote.ErrUnconfirmed, Stderr: "connection reset"}
}

// otherRemoteRow is a RemoteCompanion writing a file on another world's
// live ref (a client repo while the claim sits in a store, D8).
type otherRemoteRow struct {
	liveRow
	pushed int
}

func (o *otherRemoteRow) PushSelf(ctx context.Context) error {
	o.pushed++
	res, err := o.c.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: o.prepared, Dst: liveRef}}})
	if err != nil || !res.OK() {
		return fmt.Errorf("push: %+v %w", res, err)
	}
	return nil
}

// TestRemoteCompanionPushedOnlyAfterTheLease: the client-side row is pushed
// after the store's signals push is confirmed, and never when the decision
// is fenced out.
func TestRemoteCompanionPushedOnlyAfterTheLease(t *testing.T) {
	ctx := context.Background()
	store, client := gitremotetest.NewWorld(t), gitremotetest.NewWorld(t)
	s, c := store.Clone("s"), client.Clone("c")
	st := newStore(t, s, s.Remote, clock.NewFake(t0))
	row := &otherRemoteRow{liveRow: liveRow{c: c, path: "_root/tasks/tsk_30.json", body: "in_progress"}}
	var p ClaimPlan
	if _, err := st.Write(ctx, claimDecide(st, me, task, &p, row)); err != nil {
		t.Fatal(err)
	}
	if store.OriginRef(CodeRepoRef) == "" || client.OriginRef(liveRef) == "" || row.pushed != 1 || row.finals != 1 {
		t.Fatalf("claim on the store, row on the client: signals=%q live=%q pushed=%d", store.OriginRef(CodeRepoRef), client.OriginRef(liveRef), row.pushed)
	}
	if store.OriginRef(liveRef) != "" {
		t.Fatal("the client row must never be pushed to the store")
	}
	before := client.OriginRef(liveRef)
	// A caller who does not hold it: the fenced decision pushes nothing.
	fenced := &otherRemoteRow{liveRow: liveRow{c: c, path: "_root/tasks/tsk_30.json", body: "done"}}
	_, err := st.Write(ctx, func(v View) (Change, error) {
		if _, err := CheckHolder(v, other, folder, task, 1); err != nil {
			return Change{}, err
		}
		return Change{Companions: []Companion{fenced}}, nil
	})
	if !errors.Is(err, ErrNotHeld) || fenced.pushed != 0 || client.OriginRef(liveRef) != before {
		t.Fatalf("a fenced write must not reach the other remote: err=%v pushed=%d", err, fenced.pushed)
	}
}
