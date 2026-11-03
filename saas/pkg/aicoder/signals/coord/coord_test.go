package coord

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/gitremote/gitremotetest"
	"saas/pkg/aicoder/queue"
	"saas/pkg/aicoder/signals"
)

var (
	t0    = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	lease = signals.Lease{TTL: 20 * time.Minute, RenewEvery: 5 * time.Minute, Grace: 2 * time.Minute, PushRetries: 5}
)

const (
	folder    = "_root"
	liveRef   = "refs/lore/live"
	liveTrack = "refs/lore-remote/origin/live"
)

func rowPath(id string) string { return folder + "/tasks/" + id + ".json" }

// gitRows is a Rows backed by refs/lore/live on the origin: Get fetches,
// Stage builds a fast-forward commit for the atomic push. It is the shape
// the real live destination has, minus hlc and merging.
type gitRows struct{ c *gitremotetest.Clone }

func (g gitRows) fetch(ctx context.Context) error {
	err := g.c.Remote.Fetch(ctx, gitremote.FetchRequest{Refspecs: []string{"+" + liveRef + ":" + liveTrack}})
	if errors.Is(err, gitremote.ErrRemoteRefMissing) {
		return nil
	}
	return err
}

func (g gitRows) Get(ctx context.Context, id string) (Doc, bool, error) {
	if err := g.fetch(ctx); err != nil {
		return nil, false, err
	}
	tip, ok, err := g.c.Repo.ResolveRef(ctx, liveTrack)
	if err != nil || !ok {
		return nil, false, err
	}
	b, ok, err := g.c.Repo.ReadFile(ctx, tip, rowPath(id))
	if err != nil || !ok {
		return nil, false, err
	}
	d, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	return Doc(d), err == nil, err
}

func (g gitRows) List(ctx context.Context) ([]Doc, error) {
	if err := g.fetch(ctx); err != nil {
		return nil, err
	}
	tip, ok, err := g.c.Repo.ResolveRef(ctx, liveTrack)
	if err != nil || !ok {
		return nil, err
	}
	tree, err := g.c.Repo.ReadTree(ctx, tip, folder+"/tasks")
	if err != nil {
		return nil, err
	}
	var out []Doc
	for _, b := range tree {
		d, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
		if err != nil {
			return nil, err
		}
		out = append(out, Doc(d))
	}
	return out, nil
}

type stagedRow struct {
	g    gitRows
	rows []RowWrite
}

func (s stagedRow) Prepare(ctx context.Context) (gitremote.RefUpdate, error) {
	if err := s.g.fetch(ctx); err != nil {
		return gitremote.RefUpdate{}, err
	}
	base, _, err := s.g.c.Repo.ResolveRef(ctx, liveTrack)
	if err != nil {
		return gitremote.RefUpdate{}, err
	}
	put := map[string][]byte{}
	for _, r := range s.rows {
		b, err := canonjson.Encode(r.Doc)
		if err != nil {
			return gitremote.RefUpdate{}, err
		}
		table := r.Table
		if table == "" {
			table = "tasks"
		}
		put[folder+"/"+table+"/"+r.ID+".json"] = b
	}
	c, err := s.g.c.Repo.MakeCommit(ctx, gitref.CommitRequest{Base: base, Changes: gitref.Changes{Put: put},
		Message: "lore: live", Signature: gitremotetest.Sig})
	if err != nil {
		return gitremote.RefUpdate{}, err
	}
	return gitremote.RefUpdate{Src: c.OID, Dst: liveRef, Mode: gitremote.ModeFastForward}, nil
}

func (stagedRow) Finalize(context.Context) error { return nil }

func (g gitRows) Stage(_ context.Context, rows ...RowWrite) ([]signals.Companion, error) {
	return []signals.Companion{stagedRow{g: g, rows: rows}}, nil
}

type world struct {
	t      *testing.T
	w      *gitremotetest.World
	seed   *gitremotetest.Clone
	n      atomic.Int64
	agents Agents
}

func newWorld(t *testing.T) *world {
	w := gitremotetest.NewWorld(t)
	return &world{t: t, w: w, seed: w.Clone("seed"), agents: Agents{MaxClaims: 1, MaxAttempts: 3}}
}

// addTask writes a todo task straight onto the origin's live ref.
func (wd *world) addTask(id string, extra map[string]any) {
	ctx := context.Background()
	d := Doc{keyID: id, keyTitle: "task " + id, keyStatus: statusTodo, keyPriority: "medium", keyCreatedAt: t0.Add(-time.Hour).Format(docTimeLayout)}
	for k, v := range extra {
		d[k] = v
	}
	rows := gitRows{c: wd.seed}
	comps, _ := rows.Stage(ctx, RowWrite{ID: id, Doc: d})
	u, err := comps[0].Prepare(ctx)
	if err != nil {
		wd.t.Fatal(err)
	}
	if res, err := wd.seed.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{u}}); err != nil || !res.OK() {
		wd.t.Fatalf("seed push: %+v %v", res, err)
	}
}

type agent struct {
	env Env
	clk *clock.Fake
	c   *gitremotetest.Clone
}

type seqTokens struct {
	name string
	n    *atomic.Int64
}

func (s seqTokens) New() (string, error) { return fmt.Sprintf("ct_%s_%04d", s.name, s.n.Add(1)), nil }

func (wd *world) agent(label string) *agent {
	c := wd.w.Clone(label)
	clk := clock.NewFake(t0)
	dir := wd.t.TempDir()
	st := &signals.Store{
		Repo: c.Repo, Remote: c.Remote, RemoteName: "origin", Ref: signals.CodeRepoRef,
		Seen: signals.SeenStore{Path: filepath.Join(dir, "seen.json")}, Clock: clk, Lease: lease,
		Signature: func(at time.Time) gitref.Signature {
			return gitref.Signature{Name: label, Email: label + "@t", When: at}
		},
		Autopush: true, Tokens: seqTokens{name: label, n: &wd.n}, Sleep: func(context.Context, time.Duration) error { return nil },
	}
	return &agent{c: c, clk: clk, env: Env{Signals: st, Folder: folder, Rows: gitRows{c: c},
		Caller: signals.Caller{ActorID: "act_" + label, Label: "agent:" + label, Session: "s-" + label},
		Agents: wd.agents, Held: HeldStore{Path: filepath.Join(dir, "held.json")}}}
}

func (a *agent) row(t *testing.T, id string) Doc {
	d, ok, err := a.env.Rows.Get(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("row %s: %v %v", id, ok, err)
	}
	return d
}

func (a *agent) done(id string) error {
	_, err := HolderWrite(context.Background(), a.env, id, true, func(d Doc, now time.Time) ([]RowWrite, error) {
		return nil, MarkDone(a.env.Caller.ActorID, "", 0)(d, now)
	})
	return err
}

func fenceKind(err error) FenceKind {
	var fe *FencedError
	if errors.As(err, &fe) {
		return fe.Kind
	}
	return ""
}

// TestClaimWritesRowAndSignalAtomically: the first claim sets the row
// (in_progress, epoch, attempts 1, one claim_log entry) and the signal.
func TestClaimWritesRowAndSignalAtomically(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a := wd.agent("a")
	res, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{WorkBranch: "agent/a/x"})
	if err != nil {
		t.Fatal(err)
	}
	d := a.row(t, "tsk_1")
	if d.Status() != statusInProgress || d.Epoch() != 1 || d.Int(keyAttempts) != 1 || len(d.ClaimLog()) != 1 || d.Str(keyWorkBranch) != "agent/a/x" {
		t.Fatalf("row after claim: %v", d)
	}
	if res.Plan.Signal.ClaimEpoch != d.Epoch() {
		t.Fatal("the signal carries the row's epoch")
	}
	if rec, ok := a.env.Held.Get(a.env.Caller.Session, "tsk_1"); !ok || rec.Token != res.Plan.Signal.ClaimToken {
		t.Fatalf("held record: %+v", rec)
	}
	again, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{})
	if err != nil || !again.Plan.Resumed {
		t.Fatalf("a second claim by the holder resumes: %+v %v", again, err)
	}
}

// TestZombieIsFencedAfterReclaim is E5's fencing half on real git.
func TestZombieIsFencedAfterReclaim(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	zombie, heir := wd.agent("zombie"), wd.agent("heir")
	if _, err := Claim(ctx, zombie.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	heir.clk.Set(t0.Add(time.Minute))
	if _, err := heir.env.Signals.Read(ctx); err != nil {
		t.Fatal(err)
	}
	heir.clk.Set(t0.Add(lease.TTL + time.Minute))
	var he *signals.HeldError
	if _, err := Claim(ctx, heir.env, "tsk_1", ClaimOptions{}); !errors.As(err, &he) {
		t.Fatalf("inside grace the lease holds: %v", err)
	}
	heir.clk.Set(t0.Add(lease.TTL + lease.Grace + time.Minute))
	res, err := Claim(ctx, heir.env, "tsk_1", ClaimOptions{})
	if err != nil || !res.Plan.Reclaim {
		t.Fatalf("reclaim: %+v %v", res, err)
	}
	d := heir.row(t, "tsk_1")
	if d.Int(keyAttempts) != 2 || d.Epoch() != 2 || d.ClaimLog()[1].PrevActorID != "act_zombie" {
		t.Fatalf("reclaim row: %v", d)
	}
	zombie.clk.Set(t0.Add(lease.TTL + lease.Grace + 2*time.Minute))
	if k := fenceKind(zombie.done("tsk_1")); k != FenceLost {
		t.Fatalf("zombie done must be E_LOST_CLAIM, got %q", k)
	}
	if _, err := Renew(ctx, zombie.env, "tsk_1"); fenceKind(err) != FenceLost {
		t.Fatalf("zombie heartbeat must be E_LOST_CLAIM: %v", err)
	}
	if heir.row(t, "tsk_1").Status() == statusDone {
		t.Fatal("the zombie must not complete the task")
	}
	if err := heir.done("tsk_1"); err != nil {
		t.Fatalf("the heir completes: %v", err)
	}
	if heir.row(t, "tsk_1").Status() != statusDone {
		t.Fatal("done by the holder lands on the row")
	}
}

// TestHumanOverrides: cancel / reassign / revoke while held.
func TestHumanOverrides(t *testing.T) {
	cases := []struct {
		kind   OverrideKind
		mutate func(Doc)
		agent  func(a *agent) error
		want   FenceKind
	}{
		{OverrideCancel, func(d Doc) { d[keyStatus] = statusCancelled }, func(a *agent) error { _, err := Renew(context.Background(), a.env, "tsk_1"); return err }, FenceCancelled},
		{OverrideReassign, func(d Doc) { d.SetStr(keyAssignee, "act_dev1") }, func(a *agent) error { return a.done("tsk_1") }, FenceReassigned},
		{OverrideRevoke, nil, func(a *agent) error { return a.done("tsk_1") }, FenceLost},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			ctx := context.Background()
			wd := newWorld(t)
			wd.addTask("tsk_1", nil)
			a, human := wd.agent("a"), wd.agent("alice")
			if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
				t.Fatal(err)
			}
			removed, err := Override(ctx, human.env, "tsk_1", tc.kind, tc.mutate)
			if err != nil || removed == nil {
				t.Fatalf("override: %v %v", removed, err)
			}
			if got := fenceKind(tc.agent(a)); got != tc.want {
				t.Fatalf("agent sees %q, want %q", got, tc.want)
			}
			if d := human.row(t, "tsk_1"); d.Epoch() != 2 {
				t.Fatalf("an override raises claim_epoch: %v", d)
			}
		})
	}
}

// TestPoisonBlocksAfterMaxAttempts: three holders expire; the fourth
// reclaim blocks the task instead.
func TestPoisonBlocksAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	at := t0
	for i := 1; i <= 4; i++ {
		a := wd.agent(fmt.Sprintf("crasher-%d", i))
		a.clk.Set(at.Add(time.Minute))
		if _, err := a.env.Signals.Read(ctx); err != nil {
			t.Fatal(err)
		}
		if i > 1 {
			at = at.Add(lease.TTL + lease.Grace + 2*time.Minute)
		}
		a.clk.Set(at)
		_, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{})
		if i <= 3 && err != nil {
			t.Fatalf("crasher %d: %v", i, err)
		}
		if i == 4 {
			if !errors.Is(err, ErrPoisoned) {
				t.Fatalf("the fourth reclaim is refused as poison: %v", err)
			}
			if d := a.row(t, "tsk_1"); d.Status() != statusBlocked || d.Int(keyAttempts) != 3 {
				t.Fatalf("poison task blocked: %v", d)
			}
		}
	}
}

// TestNextRaceFourAgents: four agents run Next at once over four tasks;
// each ends up with a different task.
func TestNextRaceFourAgents(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	for i := 0; i < 4; i++ {
		wd.addTask(fmt.Sprintf("tsk_%d", i), nil)
	}
	var agents []*agent
	for i := 0; i < 4; i++ {
		agents = append(agents, wd.agent(fmt.Sprintf("w%d", i)))
	}
	results := make([]NextResult, 4)
	errs := make([]error, 4)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, a := range agents {
		wg.Add(1)
		go func(i int, a *agent) {
			defer wg.Done()
			<-start
			rows := a.env.Rows.(gitRows)
			results[i], errs[i] = Next(ctx, a.env, NextRequest{List: rows.List, Pick: func(n int) int { return i % n }})
		}(i, a)
	}
	close(start)
	wg.Wait()
	got := map[string]int{}
	for i, r := range results {
		if errs[i] != nil {
			if errors.Is(errs[i], ErrContended) {
				continue
			}
			t.Fatalf("agent %d: %v", i, errs[i])
		}
		got[r.Task]++
	}
	for id, n := range got {
		if n != 1 {
			t.Fatalf("%s claimed %d times", id, n)
		}
	}
	// Everyone who lost re-runs; in the end every task is held once.
	for i, a := range agents {
		if errs[i] == nil {
			continue
		}
		rows := a.env.Rows.(gitRows)
		r, err := Next(ctx, a.env, NextRequest{List: rows.List})
		if err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		got[r.Task]++
	}
	if len(got) != 4 {
		t.Fatalf("four tasks, four holders: %v", got)
	}
	r, err := Next(ctx, agents[0].env, NextRequest{List: agents[0].env.Rows.(gitRows).List})
	if err != nil || !r.Resumed {
		t.Fatalf("a holder's next resumes: %+v %v", r, err)
	}
}

// TestNextExplainsWaiting: E_NO_WORK lists why each task waits.
func TestNextExplainsWaiting(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_a", map[string]any{keyStatus: statusInProgress})
	wd.addTask("tsk_b", map[string]any{keyBlockedBy: []any{map[string]any{"task_id": "tsk_a", "until": "merged"}}})
	wd.addTask("tsk_g", map[string]any{keyGate: "required"})
	holder := wd.agent("h")
	if _, err := Claim(ctx, holder.env, "tsk_a", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	a := wd.agent("a")
	_, err := Next(ctx, a.env, NextRequest{List: a.env.Rows.(gitRows).List})
	var nw *NoWorkError
	if !errors.As(err, &nw) || len(nw.Waiting) != 3 {
		t.Fatalf("no work with three reasons: %v", err)
	}
	kinds := map[queue.WaitKind]bool{}
	for _, w := range nw.Waiting {
		kinds[w.Kind] = true
	}
	if !kinds[queue.WaitClaimed] || !kinds[queue.WaitDependency] || !kinds[queue.WaitGate] {
		t.Fatalf("reasons: %+v", nw.Waiting)
	}
}

// TestGateHash: editing the title voids an approval.
func TestGateHash(t *testing.T) {
	d := Doc{keyTitle: "deploy", keyBody: "staging", keyGate: "approved", keyApprovedSum: GateHash("deploy", "staging")}
	if Gated(d) {
		t.Fatal("a matching approval opens the gate")
	}
	d[keyTitle] = "deploy to production"
	if !Gated(d) {
		t.Fatal("an edit voids the approval")
	}
	if !Gated(Doc{keyGate: "requested"}) || Gated(Doc{}) {
		t.Fatal("requested gates; no gate does not")
	}
}
