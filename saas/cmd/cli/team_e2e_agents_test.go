//go:build teame2e

package main

// P4 — signals and agent coordination (LORE_TEAM_SPEC.md F7, F9, §7.3,
// §7.4, §7.5 worked example, E5, E7, cookbook 2, 6, 9, 15).

import (
	"strconv"
	"strings"
	"testing"
	"time"

	th "saas/cmd/cli/internal/teamharness"
)

// Lease defaults (§1 config keys): ttl 20m, renew_every 5m, grace 2m.
const (
	leaseTTL   = 20 * time.Minute
	leaseRenew = 5 * time.Minute
	leaseGrace = 2 * time.Minute
)

// crewCaps is what every coordination test needs.
var crewCaps = []th.Cap{th.Cmd("agent", "start"), th.Cmd("task", "claim")}

// crew is the agency plus Northwind's own repo northwind/tools, where a
// single-repo mission uses the repo's own refs/lore/live and
// refs/lore/signals (§7.5 last paragraph).
type crew struct {
	*agency
	toolsC *th.Clone // alice's checkout of the own repo
	tl     string
}

func newCrew(t *testing.T, caps ...th.Cap) *crew {
	t.Helper()
	a := newAgency(t, append(append([]th.Cap{}, crewCaps...), caps...)...)
	cr := &crew{agency: a}
	cr.toolsC = a.alice.Clone(a.tools, "tools")
	cr.toolsC.MustLore("init", "--non-interactive", "--name=tools")
	cr.tl = createTasklist(t, cr.toolsC)
	cr.toolsC.CommitAll("lore")
	cr.toolsC.AdminPush("origin", "main")
	cr.alice.SetNow(t0)
	return cr
}

// task adds a task in the own repo as alice.
func (cr *crew) task(t *testing.T, title string, extra ...string) string {
	t.Helper()
	return mustID(t, cr.toolsC.MustLore(append([]string{"task", "add", title, "--tasklist", cr.tl}, extra...)...), "tsk")
}

// agent starts an agent session on alice's machine and gives it its own
// checkout of the own repo; its clock starts at t0.
func (cr *crew) agent(t *testing.T, label string) (*th.Actor, *th.Clone) {
	t.Helper()
	ag := startAgent(t, cr.alice, cr.toolsC, label, "alice")
	ag.SetNow(t0)
	c := ag.Clone(cr.tools, "tools")
	c.MustLore("task", "list")
	return ag, c
}

// claim reads the claim on a task from the own repo's signals ref.
func (cr *crew) claim(t *testing.T, taskID string) (signal, bool) {
	t.Helper()
	return readSignal(t, cr.tools, refSignals, claimPath(rootFolder, taskID))
}

// liveTask reads a task row of the own repo from its live ref.
func (cr *crew) liveTask(t *testing.T, id string) rowDoc {
	t.Helper()
	return mustRow(t, cr.tools, refLive, liveRowPath(rootFolder, tblTasks, id))
}

// liveOrMainTask reads a task row of the own repo from its live ref, or
// from main once it has landed and left live.
func (cr *crew) liveOrMainTask(t *testing.T, id string) rowDoc {
	t.Helper()
	if r, ok := readRow(t, cr.tools, refLive, liveRowPath(rootFolder, tblTasks, id)); ok {
		return r
	}
	return mustRow(t, cr.tools, refMain, mainRowPath("", tblTasks, id))
}

func intField(t *testing.T, r rowDoc, field string) int {
	t.Helper()
	raw, ok := r.Raw[field]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.Trim(string(raw), `"`))
	if err != nil {
		t.Fatalf("%s=%s is not a number", field, raw)
	}
	return n
}

// ---------------------------------------------------------------- F7 signals

// F7 + rule 10: claim, renew and release are each one parentless commit on
// the signals ref; renew never touches the row; signals never land.
func TestF07_ClaimRenewReleaseNeverMakeHistory(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "heartbeat"), th.Cmd("task", "release"), th.Flag("signals", "task", "show"))
	id := cr.task(t, "invite mutation")
	ag, ac := cr.agent(t, "backend-1")

	ac.MustLore("task", "claim", id).Says(t, "claimed")
	sig, ok := cr.claim(t, id)
	if !ok || sig.Holder == "" || sig.ClaimToken == "" || sig.Session != ag.Env(th.EnvLoreSession) {
		t.Fatalf("claim signal (§3.4): %+v", sig)
	}
	if want := t0.Add(leaseTTL).Format(time.RFC3339); sig.Expires != want {
		t.Fatalf("expires = now + lease.ttl: got %s want %s", sig.Expires, want)
	}
	if n, p := cr.tools.CommitCount(refSignals), cr.tools.Parents(refSignals); n != 1 || p != 0 {
		t.Fatalf("signals ref must be one parentless commit, got %d commits %d parents", n, p)
	}
	if subj := cr.tools.Log(refSignals)[0]; subj != "lore: signals" {
		t.Fatalf("fixed signals message, got %q", subj)
	}
	r := cr.liveTask(t, id)
	if r.Status != statusInProgress || intField(t, r, "claim_epoch") != sig.ClaimEpoch {
		t.Fatalf("first claim sets in_progress and the row's claim_epoch: %+v / %+v", r.Raw, sig)
	}
	if got := intField(t, r, "attempts"); got != 1 {
		t.Fatalf("the first claim sets attempts = 1 (§F7): attempts=%d", got)
	}
	rowBefore, _ := cr.tools.Read(refLive, liveRowPath(rootFolder, tblTasks, id))

	ag.Advance(leaseRenew)
	ac.MustLore("task", "heartbeat", id)
	renewed, _ := cr.claim(t, id)
	if renewed.RenewedSeq <= sig.RenewedSeq || renewed.Expires != t0.Add(leaseRenew+leaseTTL).Format(time.RFC3339) {
		t.Fatalf("renew bumps renewed_seq and expires: %+v → %+v", sig, renewed)
	}
	if n := cr.tools.CommitCount(refSignals); n != 1 {
		t.Fatalf("a renewal replaces the ref, never adds history (%d commits)", n)
	}
	if rowAfter, _ := cr.tools.Read(refLive, liveRowPath(rootFolder, tblTasks, id)); rowAfter != rowBefore {
		t.Fatal("a heartbeat must never touch the task row")
	}
	ac.MustLore("task", "show", id, "--signals").Says(t, sig.ClaimToken[:6])

	ac.MustLore("task", "release", id)
	if _, ok := cr.claim(t, id); ok {
		t.Fatal("release removes the claim")
	}
	if n, p := cr.tools.CommitCount(refSignals), cr.tools.Parents(refSignals); n != 1 || p != 0 {
		t.Fatalf("still one parentless commit after release: %d/%d", n, p)
	}
	if f := cr.tools.FilesMatching(refMain, claimsDir+"/"); len(f) != 0 {
		t.Fatalf("signals never land: %v", f)
	}
	// A claim after a voluntary release is not a reclaim: the count stays.
	ac.MustLore("task", "claim", id).Says(t, "claimed")
	if got := intField(t, cr.liveTask(t, id), "attempts"); got != 1 {
		t.Fatalf("a claim after a voluntary release keeps attempts: attempts=%d", got)
	}
}

// F7 matrix "free × race": three agents claim one task at the same instant;
// exactly one wins and the signal names that session.
func TestF07_Race_ThreeAgentsClaimOneTask(t *testing.T) {
	cr := newCrew(t)
	id := cr.task(t, "only one may hold this")
	var agents []*th.Actor
	var ps []*th.Pending
	for _, l := range []string{"a-1", "a-2", "a-3"} {
		ag, c := cr.agent(t, l)
		agents = append(agents, ag)
		ps = append(ps, c.Prepare("task", "claim", id))
	}
	winner := -1
	for i, r := range cr.w.Simultaneously(ps...) {
		if r.Code == 0 {
			if winner >= 0 {
				t.Fatalf("two agents won the same claim: %d and %d", winner, i)
			}
			winner = i
		}
	}
	if winner < 0 {
		t.Fatal("one agent must win")
	}
	sig, ok := cr.claim(t, id)
	if !ok || sig.Session != agents[winner].Env(th.EnvLoreSession) {
		t.Fatalf("the signal must name the winner: %+v", sig)
	}
}

// E5 / §7.3: four agents race `task next` over four tasks; each task ends with
// exactly one holder, nobody holds two, and pushes stay under 4 × tasks.
func TestE05_Race_FourAgentsRaceTaskNext(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "next"))
	var tasks []string
	for i := 0; i < 4; i++ {
		tasks = append(tasks, cr.task(t, "job "+strconv.Itoa(i)))
	}
	type worker struct {
		ag *th.Actor
		c  *th.Clone
	}
	var ws []worker
	for _, l := range []string{"w-1", "w-2", "w-3", "w-4"} {
		ag, c := cr.agent(t, l)
		ws = append(ws, worker{ag, c})
	}
	for round := 0; round < 6; round++ {
		var ps []*th.Pending
		for _, w := range ws {
			ps = append(ps, w.c.Prepare("task", "next"))
		}
		for i, r := range cr.w.Simultaneously(ps...) {
			if r.Code != th.ExitOK && r.Code != th.ExitNoWork && r.Code != th.ExitContended {
				t.Fatalf("round %d agent %d: unexpected exit: %s", round, i, r)
			}
		}
	}
	holders := map[string]string{}
	for _, id := range tasks {
		sig, ok := cr.claim(t, id)
		if !ok {
			t.Fatalf("%s never claimed after six rounds", id)
		}
		if prev, dup := holders[sig.Session]; dup {
			t.Fatalf("session %s holds two tasks (%s, %s): WIP limit is 1", sig.Session, prev, id)
		}
		holders[sig.Session] = id
	}
	pushes := 0
	for _, w := range ws {
		pushes += len(cr.w.GitPushes(w.ag.Name))
	}
	if limit := 4 * len(tasks); pushes >= limit {
		t.Fatalf("claim races must stay under 4 × tasks pushes: %d ≥ %d", pushes, limit)
	}
}

// E5 / F7 expire + §7.4 fencing: a holder that stops renewing loses the task
// only after ttl + grace with an unchanged renewed_seq; its zombie `done`
// exits 12 and changes nothing.
func TestE05_ZombieHolderLosesAfterLeaseAndGrace(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "next"), th.Cmd("task", "heartbeat"))
	id := cr.task(t, "zombie bait")
	zombie, zc := cr.agent(t, "frontend-1")
	heir, hc := cr.agent(t, "frontend-2")
	zc.MustLore("task", "claim", id)

	heir.SetNow(t0.Add(time.Minute))
	hc.MustLore("task", "list")
	heir.SetNow(t0.Add(leaseTTL + time.Minute)) // expired, but inside grace
	if r := hc.Lore("task", "next"); r.Code == th.ExitOK {
		t.Fatalf("a lease inside its grace must not be reclaimed: %s", r)
	}
	heir.SetNow(t0.Add(leaseTTL + leaseGrace + time.Minute))
	hc.MustLore("task", "next").Says(t, id, "reclaim")
	sig, _ := cr.claim(t, id)
	if sig.Session != heir.Env(th.EnvLoreSession) {
		t.Fatalf("the heir holds the task now: %+v", sig)
	}
	if got := intField(t, cr.liveTask(t, id), "attempts"); got != 2 {
		t.Fatalf("a reclaim counts an attempt: attempts=%d", got)
	}

	zombie.SetNow(t0.Add(leaseTTL + leaseGrace + 2*time.Minute))
	zc.Lore("task", "done", id).Fails(t, th.ExitLostClaim, "E_LOST_CLAIM")
	zc.Lore("task", "heartbeat", id).Fails(t, th.ExitLostClaim, "E_LOST_CLAIM")
	if r := cr.liveTask(t, id); r.Status == statusDone {
		t.Fatal("a zombie must not complete the task")
	}
}

// Rule 11: a claim offline, or with autopush off, fails closed with E_OFFLINE
// and is never queued for a later push.
func TestF07_OfflineClaimFailsClosed(t *testing.T) {
	for _, mode := range []string{"offline", "autopush off"} {
		t.Run(mode, func(t *testing.T) {
			cr := newCrew(t)
			id := cr.task(t, "needs the network")
			_, ac := cr.agent(t, "proxy-1")
			if mode == "offline" {
				cr.alice.Offline()
				ac.Lore("task", "claim", id).Fails(t, th.ExitOffline, "E_OFFLINE")
				cr.alice.Online()
			} else {
				ac.With(autopushOff).Lore("task", "claim", id).Fails(t, th.ExitOffline, "E_OFFLINE")
			}
			ac.MustLore("task", "list")
			cr.toolsC.MustLore("sync")
			if _, ok := cr.claim(t, id); ok {
				t.Fatal("a failed claim must never be pushed later")
			}
		})
	}
}

// §7.4 human overrides: cancel, reassign and revoke while an agent holds the
// task; the agent's next write fails with the distinct code.
func TestF07_HumanOverridesWhileHeld(t *testing.T) {
	cases := []struct {
		name   string
		human  []string
		agent  []string
		code   int
		errStr string
	}{
		{"cancel", []string{"task", "cancel"}, []string{"task", "heartbeat"}, th.ExitCancelled, "E_CANCELLED"},
		{"reassign", []string{"task", "reassign", "--assign", "@dev1"}, []string{"task", "done"}, th.ExitReassigned, "E_REASSIGNED"},
		{"revoke", []string{"task", "revoke"}, []string{"task", "done"}, th.ExitLostClaim, "E_LOST_CLAIM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cr := newCrew(t, th.Cmd(tc.human[:2]...), th.Cmd("task", "heartbeat"))
			id := cr.task(t, "held work")
			_, ac := cr.agent(t, "backend-1")
			ac.MustLore("task", "claim", id)
			cr.toolsC.MustLore(append(append([]string{}, tc.human[:2]...), append([]string{id}, tc.human[2:]...)...)...)
			ac.Lore(append(append([]string{}, tc.agent...), id)...).Fails(t, tc.code, tc.errStr)
			if _, ok := cr.claim(t, id); ok {
				t.Fatal("a human override removes the claim")
			}
		})
	}
}

// F7 edge case: the same actor in a second session cannot take its own
// first session's claim; the message says to set a different LORE_ACTOR.
func TestF07_SameActorSecondSessionRefused(t *testing.T) {
	cr := newCrew(t)
	id := cr.task(t, "one session at a time")
	ag, ac := cr.agent(t, "frontend-1")
	ac.MustLore("task", "claim", id)
	other := ag.Session("s-second")
	ac.As(other).Lore("task", "claim", id).Refused(t, "another session", "LORE_ACTOR")
}

// F7 edge case: a push rejected because of another agent's renewal is
// retried and never reported as a lost claim.
func TestF07_Race_UnrelatedRenewalsNeverLoseClaims(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "heartbeat"))
	t1, t2 := cr.task(t, "first"), cr.task(t, "second")
	a, ac := cr.agent(t, "a-1")
	b, bc := cr.agent(t, "b-1")
	ac.MustLore("task", "claim", t1)
	bc.MustLore("task", "claim", t2)
	for i := 1; i <= 3; i++ {
		a.SetNow(t0.Add(time.Duration(i) * leaseRenew))
		b.SetNow(t0.Add(time.Duration(i) * leaseRenew))
		for _, r := range cr.w.Simultaneously(ac.Prepare("task", "heartbeat", t1), bc.Prepare("task", "heartbeat", t2)) {
			r.OK(t)
		}
	}
}

// F7 poison task: past agents.max_attempts reclaims the task is blocked with
// the reason and lands in the owner's inbox.
func TestF07_PoisonTaskBlockedAfterMaxAttempts(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "next"), th.Cmd("inbox"))
	cfg := aliceConfig()
	cfg.Agents = &th.Agents{MaxAttempts: 3}
	cr.alice.WriteConfig(cfg)
	id := cr.task(t, "always crashes")
	_, first := cr.agent(t, "crasher-1")
	first.MustLore("task", "next").Says(t, id)
	at := t0
	// Each crasher claims, never renews, and the next one reclaims after
	// ttl + grace having watched the same renewed_seq (attempts 2 and 3).
	for i := 2; i <= 3; i++ {
		ag, c := cr.agent(t, "crasher-"+strconv.Itoa(i))
		ag.SetNow(at.Add(time.Minute))
		c.MustLore("task", "list")
		at = at.Add(leaseTTL + leaseGrace + 2*time.Minute)
		ag.SetNow(at)
		c.MustLore("task", "next").Says(t, id, "reclaim")
	}
	last, lc := cr.agent(t, "crasher-4")
	last.SetNow(at.Add(time.Minute))
	lc.MustLore("task", "list")
	last.SetNow(at.Add(leaseTTL + leaseGrace + 2*time.Minute))
	lc.Lore("task", "next").Fails(t, th.ExitNoWork, "E_NO_WORK")
	if r := cr.liveTask(t, id); r.Status != statusBlocked {
		t.Fatalf("a poison task becomes blocked: %q", r.Status)
	}
	cr.toolsC.MustLore("inbox").Says(t, id, "failed 3 times")
}

// F7 clock skew: a reader whose clock is 30 minutes ahead cannot steal a
// lease the holder keeps renewing (expiry needs an unchanged renewed_seq).
func TestF07_ClockSkewCannotStealALiveLease(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "next"), th.Cmd("task", "heartbeat"))
	id := cr.task(t, "skewed")
	holder, hc := cr.agent(t, "steady")
	thief, tc := cr.agent(t, "fast-clock")
	hc.MustLore("task", "claim", id)
	thief.SetNow(t0.Add(30 * time.Minute))
	tc.MustLore("task", "list")
	holder.SetNow(t0.Add(leaseRenew))
	hc.MustLore("task", "heartbeat", id)
	thief.SetNow(t0.Add(31 * time.Minute))
	if r := tc.Lore("task", "next"); r.Code == th.ExitOK {
		t.Fatalf("a skewed reader stole a renewed lease: %s", r)
	}
	if sig, _ := cr.claim(t, id); sig.Session != holder.Env(th.EnvLoreSession) {
		t.Fatalf("the holder keeps the task: %+v", sig)
	}
}

// §7.3 step 2 + WIP limit: `task next` returns the claim the caller already
// holds; a second claim is refused (agents.max_claims_per_agent = 1).
func TestF07_ResumeFirstAndWIPLimit(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "next"))
	t1, t2 := cr.task(t, "first job"), cr.task(t, "second job")
	_, ac := cr.agent(t, "solo-agent")
	first := mustID(t, ac.MustLore("task", "next"), "tsk")
	ac.MustLore("task", "next").Says(t, first, "you hold")
	other := t2
	if first == t2 {
		other = t1
	}
	ac.Lore("task", "claim", other).Refused(t)
	if _, ok := cr.claim(t, other); ok {
		t.Fatal("the WIP limit must stop the second claim")
	}
}

// Rule 10: any writer drops expired signal files.
func TestF07_NextWriterDropsExpiredSignals(t *testing.T) {
	cr := newCrew(t)
	t1, t2 := cr.task(t, "abandoned"), cr.task(t, "fresh")
	_, ac := cr.agent(t, "gone")
	ac.MustLore("task", "claim", t1)
	b, bc := cr.agent(t, "later")
	b.SetNow(t0.Add(3 * leaseTTL))
	bc.MustLore("task", "claim", t2)
	if _, ok := cr.claim(t, t1); ok {
		t.Fatal("an expired claim file must be dropped by the next signals write")
	}
}

// §7.2 resource holds: same lease machinery, exclusive, fail closed offline.
func TestF07_ResourceHoldExclusiveAndFailsClosed(t *testing.T) {
	cr := newCrew(t, th.Cmd("hold", "claim"), th.Cmd("hold", "release"))
	_, ac := cr.agent(t, "tester-1")
	_, bc := cr.agent(t, "tester-2")
	ac.MustLore("hold", "claim", "full-suite", "--ttl", "30m")
	if _, ok := readSignal(t, cr.tools, refSignals, rootFolder+"/"+resourcesDir+"/full-suite.json"); !ok {
		t.Fatalf("resource hold must be a signal file: %v", cr.tools.Files(refSignals))
	}
	bc.Lore("hold", "claim", "full-suite", "--ttl", "30m").Refused(t)
	ac.MustLore("hold", "release", "full-suite")
	bc.MustLore("hold", "claim", "full-suite", "--ttl", "30m")
	cr.alice.Offline()
	bc.Lore("hold", "release", "full-suite").Fails(t, th.ExitOffline, "E_OFFLINE")
}

// ---------------------------------------------------------------- §7.3 ready queue

// §7.3 + --after (same repo waits for done): the dependent is not offered
// until its dependency is done, and the refusal names the reason.
func TestNext_DependencyOrderSameRepo(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "next"), th.Flag("after", "task", "add"))
	first := cr.task(t, "backend first")
	second := cr.task(t, "then frontend", "--after", first)
	_, ac := cr.agent(t, "a-1")
	_, bc := cr.agent(t, "b-1")
	ac.MustLore("task", "next").Says(t, first)
	bc.Lore("task", "next").Fails(t, th.ExitNoWork, "E_NO_WORK", "waits for", first)
	ac.MustLore("task", "done", first)
	bc.MustLore("task", "next").Says(t, second)
}

// §7.2 dependencies: a cycle is refused at add and edit (E_CYCLE).
func TestNext_CycleIsRefused(t *testing.T) {
	cr := newCrew(t, th.Flag("after", "task", "add"), th.Flag("after", "task", "edit"))
	a := cr.task(t, "A")
	b := cr.task(t, "B", "--after", a)
	cr.toolsC.Lore("task", "edit", a, "--after", b).Fails(t, th.ExitGuard, "E_CYCLE")
}

// §7.4 gates (TEAM-028): agents stop at a gate; only a human from the
// identity file approves (never an agent, never LORE_ACTOR=human:…); editing
// the title voids the approval.
func TestNext_GateNeedsAHumanApproval(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "next"), th.Cmd("gate", "request"), th.Cmd("gate", "approve"))
	id := cr.task(t, "deploy to staging")
	cr.toolsC.MustLore("gate", "request", id)
	ag, ac := cr.agent(t, "coord")
	ac.Lore("task", "next").Fails(t, th.ExitNoWork, "gate")
	ac.Lore("gate", "approve", id).Refused(t)
	impostor := ag.Session("s-impostor")
	impostor.SetEnv(th.EnvLoreActor, aliceKey)
	ac.As(impostor).Lore("gate", "approve", id)
	ac.Lore("task", "next").Fails(t, th.ExitNoWork, "gate")

	cr.toolsC.MustLore("gate", "approve", id).Says(t, "approved", aliceName)
	cr.toolsC.MustLore("task", "edit", id, "--title", "deploy to production")
	ac.Lore("task", "next").Fails(t, th.ExitNoWork, "gate")
	cr.toolsC.MustLore("gate", "approve", id)
	ac.MustLore("task", "next").Says(t, id)
}

// §7.3 step 4: a low-priority task blocking a high one is offered first.
func TestNext_EffectivePriorityFollowsWaiters(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "next"), th.Flag("after", "task", "add"))
	blocker := cr.task(t, "low blocker", "--priority", "low")
	cr.task(t, "urgent waiter", "--priority", "urgent", "--after", blocker)
	cr.task(t, "medium other", "--priority", "medium")
	_, ac := cr.agent(t, "picker")
	ac.MustLore("task", "next").Says(t, blocker)
}

// §7.4 idempotent plans: applying a plan file twice creates every row once.
// Ported to `lore mission apply` (TSQ9: lore plan apply removed; the YAML
// import moved to mission apply, same file format, idempotent).
func TestNext_PlanApplyIsIdempotent(t *testing.T) {
	cr := newCrew(t, th.Cmd("mission", "apply"))
	plan := `mission: {key: invite, title: "Invite landing page"}
tasks:
  - {key: backend,  title: "invite mutation + email", timebox: 60m}
  - {key: frontend, title: "invite landing route", after: [backend], timebox: 60m}
  - {key: deploy,   title: "deploy to staging", role: coordinator, after: [frontend], gate: required}
`
	cr.toolsC.WriteFile("invite.yaml", plan)
	first := cr.toolsC.MustLore("mission", "apply", "invite.yaml")
	m := mustID(t, first, "msn")
	again := cr.toolsC.MustLore("mission", "apply", "invite.yaml")
	if mustID(t, again, "msn") != m {
		t.Fatal("a re-run must resolve to the same mission")
	}
	if n := len(listedTitles(t, cr.toolsC, "--mission", m, "--all")); n != 3 {
		t.Fatalf("a re-run must not duplicate tasks: %d", n)
	}
}

// §7.4: a coordinator takes a claim on the mission; a second coordinator is
// told who coordinates it.
func TestNext_SecondCoordinatorIsRefused(t *testing.T) {
	cr := newCrew(t)
	m := mustID(t, cr.toolsC.MustLore("mission", "add", "Invite landing page"), "msn")
	_, c1 := cr.agent(t, "coord-1")
	_, c2 := cr.agent(t, "coord-2")
	c1.MustLore("task", "claim", m)
	c2.Lore("task", "claim", m).Refused(t, "coordinated by", "coord-1")
}

// §7.4 "no prompts": done never prompts for an agent; --lesson saves a memory,
// --no-lesson skips the reminder.
func TestNext_DoneNeverPromptsAndSavesLesson(t *testing.T) {
	cr := newCrew(t, th.Flag("lesson", "task", "done"), th.Flag("no-lesson", "task", "done"))
	t1, t2 := cr.task(t, "with lesson"), cr.task(t, "without")
	_, ac := cr.agent(t, "teacher")
	ac.MustLore("task", "claim", t1)
	r := ac.MustLore("task", "done", t1, "--lesson", "invite tokens expire in 48h")
	mustID(t, r, "mem")
	ac.MustLore("task", "claim", t2)
	ac.MustLore("task", "done", t2, "--no-lesson").Never(t, "lesson")
}

// ---------------------------------------------------------------- F9 merged

// globexGroup links two Globex client repos to the store's globex/ group project.
type globexGroup struct {
	*agency
	backend, frontend *th.Remote
	aliceBE, aliceFE  *th.Clone
	mission, t30, t31 string
	beAgent, feAgent  *th.Actor
	beClone, feClone  *th.Clone
}

func newGlobexGroup(t *testing.T, caps ...th.Cap) *globexGroup {
	t.Helper()
	a := newAgency(t, append(append([]th.Cap{}, crewCaps...), append(caps, th.Cmd("task", "next"), th.Flag("after", "task", "add"), th.Flag("to", "task", "add"), th.Flag("to", "mission", "add"))...)...)
	g := &globexGroup{agency: a, backend: a.w.Remote("github.com/globex/backend"), frontend: a.w.Remote("github.com/globex/frontend")}
	for _, x := range []struct {
		r    *th.Remote
		name string
		dst  **th.Clone
	}{{g.backend, "backend", &g.aliceBE}, {g.frontend, "frontend", &g.aliceFE}} {
		c := a.alice.Clone(x.r, x.name)
		c.MustLore("init", "--non-interactive", "--name="+x.name)
		c.CommitAll("lore")
		c.AdminPush("origin", "main")
		c.MustLore("org", "link", orgNorthwind, "--project", folderGlobex, "--yes")
		*x.dst = c
	}
	to := orgNorthwind + "/" + folderGlobex
	g.mission = mustID(t, g.aliceBE.MustLore("mission", "add", "Invite landing page", "--to", to, "--yes"), "msn")
	g.t30 = mustID(t, g.aliceBE.MustLore("task", "add", "invite mutation", "--to", to, "--mission", g.mission, "--repo", "backend"), "tsk")
	g.t31 = mustID(t, g.aliceBE.MustLore("task", "add", "landing route", "--to", to, "--mission", g.mission, "--repo", "frontend", "--after", g.t30), "tsk")
	g.beAgent = startAgent(t, a.alice, g.aliceBE, "backend-1", "alice")
	g.feAgent = startAgent(t, a.alice, g.aliceFE, "frontend-1", "alice")
	g.beClone = g.beAgent.Clone(g.backend, "backend")
	g.feClone = g.feAgent.Clone(g.frontend, "frontend")
	return g
}

// backendWork claims tsk_30 as backend-1, commits on a work branch, pushes
// it and marks done --pr. tsk_30 is a store-only row and globex/backend is a
// linked client repo, so (Q13, fail closed) the commit carries NO
// Lore-Task trailer and nothing of the store id.
func (g *globexGroup) backendWork(t *testing.T) string {
	t.Helper()
	g.beClone.MustLore("task", "next", "--mission", g.mission, "--repo", "backend").Says(t, g.t30)
	g.beClone.Git("checkout", "-q", "-b", "agent/backend-1/invite")
	g.beClone.WriteFile("api/invite.go", "package api\n")
	g.beClone.CommitAll("invite mutation")
	if msg := g.beClone.Git("log", "-1", "--format=%B"); strings.Contains(msg, "Lore-Task:") || strings.Contains(msg, g.t30) {
		t.Fatalf("a store-only task never puts a trailer or its id in a client repo commit: %q", msg)
	}
	g.beClone.Git("push", "-q", "-u", "origin", "agent/backend-1/invite")
	g.beClone.MustLore("task", "done", g.t30, "--pr", "412")
	return "agent/backend-1/invite"
}

// squashMerge merges a branch on the backend remote the way GitHub's squash
// button does: one new commit on main whose parents do not include the
// branch's commits, so ancestry cannot find it.
func (g *globexGroup) squashMerge(t *testing.T, branch string) {
	t.Helper()
	g.aliceBE.Git("checkout", "-q", "main")
	g.aliceBE.Git("pull", "-q")
	g.aliceBE.Git("fetch", "-q", "origin", branch)
	g.aliceBE.Git("merge", "-q", "--squash", "FETCH_HEAD")
	g.aliceBE.With("LORE_RIDE_ACTIVE=1").Git("commit", "-q", "--no-verify", "-m", "invite mutation (#412)")
	g.aliceBE.AdminPush("origin", "main")
}

// E7 / F9 (Q13): when the held task row lives in the repo being committed to
// (an own repo), the commit-msg hook adds `Lore-Task: <id>`, and a squash
// merge keeping that trailer is found by the next lore command in that repo
// (merged_at set, nothing else needed). Cross-repo dependencies live in store
// missions whose tasks get no trailer (Q13); their merge is found by ancestry
// or marked by hand — TestF09_SquashWithoutTrailerNeedsManualMark and the
// worked example cover that path.
func TestE07_SquashMergeWithTrailerRecordsMerged(t *testing.T) {
	cr := newCrew(t, th.Flag("pr", "task", "done"))
	id := cr.task(t, "retry helper")
	_, c := cr.agent(t, "backend-1")
	c.MustLore("task", "claim", id)
	c.Git("checkout", "-q", "-b", "agent/backend-1/retry")
	c.WriteFile("retry.go", "package tools\n")
	c.CommitAll("retry helper")
	if msg := c.Git("log", "-1", "--format=%B"); !strings.Contains(msg, "Lore-Task: "+id) {
		t.Fatalf("while a claim on a row of this repo is held, commits carry the Lore-Task trailer: %q", msg)
	}
	c.Git("push", "-q", "-u", "origin", "agent/backend-1/retry")
	c.MustLore("task", "done", id, "--pr", "7")

	// GitHub's squash button: one new commit on main, the branch's commits
	// are not ancestors of it; the message keeps the trailer.
	cr.toolsC.Git("checkout", "-q", "main")
	cr.toolsC.Git("pull", "-q")
	cr.toolsC.Git("fetch", "-q", "origin", "agent/backend-1/retry")
	cr.toolsC.Git("merge", "-q", "--squash", "FETCH_HEAD")
	cr.toolsC.With("LORE_RIDE_ACTIVE=1").Git("commit", "-q", "--no-verify", "-m", "retry helper (#7)\n\nLore-Task: "+id)
	cr.toolsC.AdminPush("origin", "main")
	if got := rawString(t, cr.liveOrMainTask(t, id), "merged_at"); got != "" {
		t.Fatalf("control: merged_at must not be set before a lore command looks: %q", got)
	}
	cr.toolsC.MustLore("task", "list")
	if rawString(t, cr.liveOrMainTask(t, id), "merged_at") == "" {
		t.Fatal("merged_at must be recorded once the trailer is on origin/main")
	}
}

// F9 "undetectable → done (merge unknown); `lore task merged` marks it".
func TestF09_SquashWithoutTrailerNeedsManualMark(t *testing.T) {
	g := newGlobexGroup(t, th.Flag("pr", "task", "done"), th.Cmd("task", "merged"))
	branch := g.backendWork(t)
	g.squashMerge(t, branch)
	g.aliceBE.MustLore("task", "list")
	g.feClone.Lore("task", "next", "--mission", g.mission, "--repo", "frontend").Fails(t, th.ExitNoWork, "merged")
	g.aliceBE.MustLore("task", "merged", g.t30)
	g.feClone.MustLore("task", "next", "--mission", g.mission, "--repo", "frontend").Says(t, g.t31)
}

// F9 check 1: --commit <sha> found by ancestry after a merge commit; the
// check writes nothing until it finds the merge (principle 1).
func TestF09_MergeCommitFoundByAncestryAndCheckWritesNothing(t *testing.T) {
	cr := newCrew(t, th.Flag("commit", "task", "done"))
	id := cr.task(t, "merged by ancestry")
	_, ac := cr.agent(t, "merger")
	ac.MustLore("task", "claim", id)
	ac.Git("checkout", "-q", "-b", "agent/merger/work")
	ac.WriteFile("lib/x.go", "package lib\n")
	ac.CommitAll("work")
	sha := ac.Head()
	ac.Git("push", "-q", "-u", "origin", "agent/merger/work")
	ac.MustLore("task", "done", id, "--commit", sha)
	before := cr.tools.Rev(refLive) + cr.tools.Rev(refMain)
	cr.toolsC.MustLore("task", "list")
	if cr.tools.Rev(refLive)+cr.tools.Rev(refMain) != before {
		t.Fatal("a merged check that finds nothing must write nothing")
	}
	cr.toolsC.Git("pull", "-q")
	cr.toolsC.Git("merge", "-q", "--no-ff", "--no-edit", "origin/agent/merger/work")
	cr.toolsC.AdminPush("origin", "main")
	cr.toolsC.MustLore("task", "list")
	r := mustRow(t, cr.tools, refMain, mainRowPath("", tblTasks, id))
	if rawString(t, r, "merged_at") == "" {
		t.Fatal("merged_at must be set once done_commit is an ancestor of origin/main")
	}
}

// ---------------------------------------------------------------- §7.5 worked example

// §7.5 walked end to end: plan → workers → finding → done/merged → crash and
// reclaim with the pushed work branch → gate → mission status. The plan step
// is ported to `lore mission apply` (TSQ9: lore plan apply removed).
func TestWorkedExample75_FourAgentsOneMission(t *testing.T) {
	g := newGlobexGroup(t, th.Cmd("mission", "apply"), th.Cmd("inbox"), th.Cmd("finding", "add"), th.Cmd("task", "note"),
		th.Cmd("gate", "approve"), th.Cmd("mission", "status"), th.Flag("pr", "task", "done"), th.Flag("lesson", "task", "done"))
	proxy := g.w.Remote("github.com/globex/proxy")
	pc := g.alice.Clone(proxy, "proxy")
	pc.MustLore("init", "--non-interactive", "--name=proxy")
	pc.CommitAll("lore")
	pc.AdminPush("origin", "main")
	pc.MustLore("org", "link", orgNorthwind, "--project", folderGlobex, "--yes")

	coord := startAgent(t, g.alice, g.aliceBE, "coord", "alice")
	coordC := g.aliceBE.As(coord)
	g.aliceBE.WriteFile("invite.yaml", `mission: {key: invite, title: "Invite landing page", to: northwind/globex}
approvers: ["@alice"]
tasks:
  - {key: backend,  title: "invite mutation + email",    repo: backend,  timebox: 60m}
  - {key: frontend, title: "invite landing route",       repo: frontend, after: [backend], timebox: 60m}
  - {key: proxy,    title: "allow /invite on the proxy", repo: proxy}
  - {key: deploy,   title: "deploy to staging",          role: coordinator, after: [frontend, proxy], gate: required}
`)
	plan := coordC.MustLore("mission", "apply", "invite.yaml")
	m := mustID(t, plan, "msn")
	coordC.MustLore("mission", "apply", "invite.yaml")
	coordC.MustLore("task", "claim", m)
	if f := g.clientSees(t, "Invite landing page"); len(f) != 0 {
		t.Fatalf("the plan lives in Northwind's store, never in a client repo: %v", f)
	}

	be := g.beClone
	beTask := mustID(t, be.MustLore("task", "next", "--mission", m, "--repo", "backend").Says(t, "invite mutation"), "tsk")
	g.feClone.Lore("task", "next", "--mission", m, "--repo", "frontend").Fails(t, th.ExitNoWork, "merged")
	proxyAgent := startAgent(t, g.alice, pc, "proxy-1", "alice")
	pa := proxyAgent.Clone(proxy, "proxy")
	pa.MustLore("task", "next", "--mission", m, "--repo", "proxy", "--owns", "nginx/sites/invite.conf").Says(t, "allow /invite")

	pa.MustLore("finding", "add", "--mission", m, "the proxy strips X-Forwarded-Host; links must use the public base URL")
	be.MustLore("inbox").Says(t, "X-Forwarded-Host")

	be.MustLore("task", "note", beTask, "mutation inviteUser(email) returns token")
	be.Git("checkout", "-q", "-b", "agent/backend-1/invite")
	be.WriteFile("api/invite.go", "package api\n")
	be.CommitAll("invite mutation")
	// Q13: the task is a store-only row and globex/backend a client repo, so
	// no Lore-Task trailer; the --no-ff merge below is found by ancestry.
	if msg := be.Git("log", "-1", "--format=%B"); strings.Contains(msg, "Lore-Task:") || strings.Contains(msg, beTask) {
		t.Fatalf("a store-only task never puts a trailer or its id in a client repo commit: %q", msg)
	}
	be.Git("push", "-q", "-u", "origin", "agent/backend-1/invite")
	be.MustLore("task", "done", beTask, "--pr", "412", "--lesson", "invite tokens expire in 48h").Says(t, "mem_")
	g.aliceBE.Git("checkout", "-q", "main")
	g.aliceBE.Git("pull", "-q")
	g.aliceBE.Git("merge", "-q", "--no-ff", "--no-edit", "origin/agent/backend-1/invite")
	g.aliceBE.AdminPush("origin", "main")
	g.aliceBE.MustLore("task", "list")

	fe := g.feClone
	g.feAgent.SetNow(t0)
	feTask := mustID(t, fe.MustLore("task", "next", "--mission", m, "--repo", "frontend").Says(t, "invite landing route"), "tsk")
	fe.Git("checkout", "-q", "-b", "agent/frontend-1/invite-route")
	fe.WriteFile("src/routes/invite.tsx", "export {}\n")
	fe.CommitAll("route + loader")
	fe.MustLore("task", "note", feTask, "route + loader done, form next")
	if !g.frontend.Has("refs/heads/agent/frontend-1/invite-route") {
		t.Fatal("task note pushes the claim's work branch so a replacement can fetch it")
	}
	coord.SetNow(t0.Add(leaseTTL + leaseGrace + 4*time.Minute))
	coordC.MustLore("inbox").Says(t, "EXPIRED", feTask)
	fe2 := startAgent(t, g.alice, g.aliceFE, "frontend-2", "alice")
	fe2.SetNow(t0.Add(leaseTTL + leaseGrace + 5*time.Minute))
	fe2c := fe2.Clone(g.frontend, "frontend")
	fe2c.MustLore("task", "next", "--mission", m, "--repo", "frontend").Says(t, feTask, "reclaim")

	coordC.Lore("task", "next", "--mission", m, "--role", "coordinator").Fails(t, th.ExitNoWork, "E_NO_WORK")
	g.aliceBE.MustLore("mission", "status", m).Says(t, "invite mutation", "claimed")
}

// ---------------------------------------------------------------- cookbook 2, 9, 15

// Cookbook 2 (see QUESTION Q4): three developers, tasks reach people at once
// on any branch; done lands on main.
func TestRecipe02_SmallTeamOneRepo(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("assign", "task", "add"), th.Flag("mine", "task", "list"), th.Flag("commit", "task", "done"))
	dev3 := rt.w.Human("dev3", "dev3@acme.example")
	dev3.WriteConfig(th.Config{Live: th.FreshFetch()})
	c3 := dev3.Clone(rt.app, "app")
	for _, c := range []*th.Clone{rt.c1, rt.c2, c3} {
		c.MustLore("actor", "register")
	}
	c3.Git("checkout", "-q", "-b", "feat/retry")
	id := rt.addTask(t, rt.c2, "review the retry logic", "--assign", "@dev3")
	if !hasItem(listedTitles(t, c3, "--mine"), "review the retry logic") {
		t.Fatal("dev3 must see the assignment on the next command, on any branch")
	}
	c3.WriteFile("retry.go", "package app\n")
	c3.CommitAll("retry")
	c3.MustLore("task", "done", id, "--commit", c3.Head())
	if r := mustRow(t, rt.app, refMain, mainRowPath("", tblTasks, id)); rawString(t, r, "done_commit") == "" {
		t.Fatalf("done lands with its outcome: %v", r.Raw)
	}
}

// Cookbook 9: offboarding revokes claims, reassigns open work and
// deactivates the actor without deleting it.
func TestRecipe09_OffboardingADeveloper(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "revoke"), th.Cmd("task", "reassign"), th.Cmd("actor", "deactivate"), th.Flag("claimed-by", "task", "list"), th.Flag("assign", "task", "add"))
	id := cr.task(t, "left behind", "--assign", "@dev1")
	devTools := cr.dev1.Clone(cr.tools, "tools")
	devTools.MustLore("task", "claim", id)
	cr.toolsC.MustLore("task", "list", "--claimed-by", "@dev1", "--all-repos").Says(t, id)
	cr.toolsC.MustLore("task", "revoke", id)
	cr.toolsC.MustLore("task", "reassign", "--from", "@dev1", "--assign", "@alice")
	cr.toolsC.MustLore("actor", "deactivate", "@dev1")
	files := cr.store.FilesMatching(refMain, folderOrg+"/"+dataDir+"/"+tblActors+"/")
	found := false
	for _, f := range files {
		if body, _ := cr.store.Read(refMain, f); strings.Contains(body, dev1Key) {
			found = true
			if !strings.Contains(body, `"active": false`) && !strings.Contains(body, `"active":false`) {
				t.Fatalf("deactivated actor keeps its row with active=false: %s", body)
			}
		}
	}
	if !found {
		t.Fatal("a deactivated actor is never deleted from the roster")
	}
	devTools.Lore("task", "done", id).Refused(t)
}

// Cookbook 15 / §7.6 (D8): an agent on a client task puts its claim on the
// STORE's signals ref and its notes in the store; only `--to repo` reaches
// the client.
//
// Q11 (coordinator, changed — fail closed): with the default
// `agent_details: internal` the client copy never names the agent's label or
// agent actor id; the assignee projection names the agent's owning human.
// An agent label in the client repo is a leak.
func TestRecipe15_AgentOnAClientTask(t *testing.T) {
	a := newAgency(t, append(append([]th.Cap{}, crewCaps...), th.Cmd("task", "note"), th.Flag("to", "task", "note"), th.Flag("pr", "task", "done"))...)
	id := mustID(t, a.acmeApp.MustLore("task", "add", "fix rounding", "--tasklist", a.tl), "tsk")
	a.acmeApp.MustLore("sync")
	ag := startAgent(t, a.alice, a.aliceApp, agentLabelBackend, "alice")
	ac := ag.Clone(a.app, "app")
	ac.MustLore("task", "claim", id)
	if len(a.app.Refs(refSignals)) != 0 {
		t.Fatal("claims for a client task never sit on the client's signals ref")
	}
	if f := a.store.FilesMatching(storeSignals(storeBranch), folderAcme+"/"+claimsDir+"/"); len(f) != 1 {
		t.Fatalf("the claim sits on the store's signals ref, folder acme/: %v", a.store.Files(storeSignals(storeBranch)))
	}
	ac.MustLore("task", "note", id, "reproduced; root cause in rounding")
	ac.MustLore("task", "done", id, "--pr", "88")
	ac.MustLore("task", "note", id, "fixed rounding; see PR #88", "--to", "repo")
	agentKey := ag.Env(th.EnvLoreActor)
	agentID := agentActorID(t, a.store, agentLabelBackend)
	if f := a.clientSees(t, "reproduced; root cause in rounding", "claim_token", agentLabelBackend, agentKey, agentID); len(f) != 0 {
		t.Fatalf("working notes, agent label, agent actor key and id, and claims must stay internal: %v", f)
	}
	// D8 (coordinator 15:05): the client row may carry claim_epoch, nothing
	// else of the claim.
	row := a.clientTaskRow(t, id)
	// Exported rows list every column; null or the schema default carries
	// nothing (integrator 15:58, option a).
	for field, empty := range clientTaskFieldsNeverShared {
		if v, has := row.Raw[field]; has && string(v) != "null" && string(v) != empty {
			t.Errorf("client task row must not carry %s (D8): %s=%s", field, field, v)
		}
	}
	if f := a.clientSees(t, "fixed rounding; see PR #88"); len(f) == 0 {
		t.Fatal("the one shared note must reach the client")
	}
	a.acmeApp.MustLore("task", "show", id).Says(t, aliceName).Never(t, agentLabelBackend, agentKey, agentID)
}

// Cookbook 15 / §7.6 / Q11: `agent_details: client` in the org entry is the
// one deliberate switch that puts agent details (work notes, the agent's
// label) on the client side; claims and tokens still never reach it.
func TestRecipe15_AgentDetailsClientSharesAgentWork(t *testing.T) {
	a := newAgency(t, append(append([]th.Cap{}, crewCaps...), th.Cmd("task", "note"), th.Flag("pr", "task", "done"))...)
	cfg := aliceConfig()
	cfg.Orgs[0].AgentDetails = agentDetailsClient
	a.alice.WriteConfig(cfg)
	id := mustID(t, a.acmeApp.MustLore("task", "add", "fix rounding", "--tasklist", a.tl), "tsk")
	a.acmeApp.MustLore("sync")
	ag := startAgent(t, a.alice, a.aliceApp, agentLabelBackend, "alice")
	ac := ag.Clone(a.app, "app")
	ac.MustLore("task", "claim", id)
	ac.MustLore("task", "note", id, "reproduced; root cause in rounding")
	ac.MustLore("task", "done", id, "--pr", "88")
	if f := a.clientSees(t, "reproduced; root cause in rounding"); len(f) == 0 {
		t.Fatal("agent_details: client puts the agent's work notes in the client repo")
	}
	if f := a.clientSees(t, "claim_token"); len(f) != 0 {
		t.Fatalf("claims stay on the store's signals ref even with agent_details: client: %v", f)
	}
	if f := a.clientSees(t, agentLabelBackend); len(f) == 0 {
		t.Fatal("agent_details: client puts the agent's details (its label) on the client side")
	}
}
