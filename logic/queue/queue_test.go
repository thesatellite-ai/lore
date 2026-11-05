package queue

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func task(id string, mods ...func(*Task)) Task {
	t := Task{ID: id, Title: id, Status: StatusTodo, Priority: PriorityMedium, CreatedAt: now.Add(-time.Hour)}
	for _, m := range mods {
		m(&t)
	}
	return t
}

func after(id string, u Until) func(*Task) {
	return func(t *Task) { t.After = append(t.After, Dep{TaskID: id, Until: u}) }
}
func status(s Status) func(*Task)    { return func(t *Task) { t.Status = s } }
func merged(t *Task)                 { t.Merged = true }
func gated(t *Task)                  { t.Gated = true }
func repo(r string) func(*Task)      { return func(t *Task) { t.Repo = r } }
func role(r string) func(*Task)      { return func(t *Task) { t.Role = r } }
func prio(p int) func(*Task)         { return func(t *Task) { t.Priority = p } }
func claim(c ClaimState) func(*Task) { return func(t *Task) { t.Claim = c } }
func attempts(n int) func(*Task)     { return func(t *Task) { t.Attempts = n } }
func pr(n int) func(*Task)           { return func(t *Task) { t.DonePR = n } }
func age(d time.Duration) func(*Task) {
	return func(t *Task) { t.CreatedAt = now.Add(-d) }
}

// TestDependencyMatrix: upstream state × edge kind (§9.2 "§7.3 next").
func TestDependencyMatrix(t *testing.T) {
	cases := []struct {
		name     string
		upstream *Task
		until    Until
		ready    bool
		reason   string
	}{
		{"open, :done", ptr(task("up")), UntilDone, false, "waits for up to be done (todo)"},
		{"claimed, :done", ptr(task("up", claim(Claimed), func(t *Task) { t.ClaimedBy = "backend-1" })), UntilDone, false, "claimed by backend-1"},
		{"done, :done", ptr(task("up", status(StatusDone))), UntilDone, true, ""},
		{"done not merged, :merged", ptr(task("up", status(StatusDone), pr(412))), UntilMerged, false, "waits for up to be merged (done, pr #412 not merged yet)"},
		{"done unknown merge, :merged", ptr(task("up", status(StatusDone))), UntilMerged, false, "merge unknown"},
		{"merged, :merged", ptr(task("up", status(StatusDone), merged)), UntilMerged, true, ""},
		{"cancelled", ptr(task("up", status(StatusCancelled))), UntilDone, false, "was cancelled"},
		{"missing", nil, UntilDone, false, "was not found"},
		{"blocked upstream", ptr(task("up", status(StatusBlocked))), UntilDone, false, "to be done (blocked)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tasks := []Task{task("down", after("up", tc.until))}
			if tc.upstream != nil {
				tasks = append(tasks, *tc.upstream)
			}
			res := Next(Input{Tasks: tasks, Now: now})
			isReady := false
			for _, r := range res.Ready {
				if r.ID == "down" {
					isReady = true
				}
			}
			if isReady != tc.ready {
				t.Fatalf("ready=%v want %v (%+v)", isReady, tc.ready, res)
			}
			if !tc.ready {
				w := waitingFor(res, "down")
				if w == nil || !strings.Contains(w.Reason, tc.reason) || w.Kind != WaitDependency {
					t.Fatalf("reason %+v, want %q", w, tc.reason)
				}
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func waitingFor(r Result, id string) *Waiting {
	for i := range r.Waiting {
		if r.Waiting[i].Task.ID == id {
			return &r.Waiting[i]
		}
	}
	return nil
}

func ids(ts []Task) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

// TestFilterMatrix: task repo/role × caller --repo/--role/none.
func TestFilterMatrix(t *testing.T) {
	tasks := []Task{
		task("norepo"),
		task("be", repo("backend")),
		task("fe", repo("frontend")),
		task("coord", role("coordinator")),
		task("coord-be", role("coordinator"), repo("backend")),
		task("other-mission", func(t *Task) { t.MissionID = "msn_2" }),
	}
	for i := range tasks {
		if tasks[i].MissionID == "" {
			tasks[i].MissionID = "msn_1"
		}
	}
	cases := []struct {
		name string
		f    Filter
		want []string
	}{
		{"--repo backend", Filter{Mission: "msn_1", Repo: "backend"}, []string{"be"}},
		{"--repo backend --role coordinator", Filter{Mission: "msn_1", Repo: "backend", Role: "coordinator"}, []string{"be", "coord", "coord-be"}},
		{"--role coordinator, no repo", Filter{Mission: "msn_1", Role: "coordinator"}, []string{"coord", "norepo"}},
		{"nothing, in no repo", Filter{Mission: "msn_1"}, []string{"norepo"}},
		{"nothing, here = frontend", Filter{Mission: "msn_1", Here: "frontend"}, []string{"fe", "norepo"}},
		{"no mission filter", Filter{}, []string{"norepo", "other-mission"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(Next(Input{Tasks: tasks, Filter: tc.f, Now: now}).Ready)
			if strings.Join(sorted(got), ",") != strings.Join(sorted(tc.want), ",") {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// TestGateClaimAndPoison: gate, active claim, expired claim × attempts.
func TestGateClaimAndPoison(t *testing.T) {
	res := Next(Input{Now: now, MaxAttempts: 3, Tasks: []Task{
		task("gate", gated),
		task("held", claim(Claimed)),
		task("expired", claim(ClaimExpired), attempts(1)),
		task("poison", claim(ClaimExpired), attempts(3)),
		task("released-old", attempts(3)),
		task("done", status(StatusDone)),
		task("blocked", status(StatusBlocked)),
	}})
	if got := strings.Join(sorted(ids(res.Ready)), ","); got != "expired,released-old" {
		t.Fatalf("ready %s", got)
	}
	if w := waitingFor(res, "gate"); w == nil || w.Kind != WaitGate || !strings.Contains(w.Reason, "gate") {
		t.Fatalf("gate: %+v", w)
	}
	if w := waitingFor(res, "held"); w == nil || w.Kind != WaitClaimed {
		t.Fatalf("held: %+v", w)
	}
	if len(res.Poisoned) != 1 || res.Poisoned[0].ID != "poison" || !strings.Contains(waitingFor(res, "poison").Reason, "failed 3 times") {
		t.Fatalf("poison: %+v", res.Poisoned)
	}
	if waitingFor(res, "done") != nil || waitingFor(res, "blocked") != nil {
		t.Fatal("closed and blocked tasks are not part of the queue")
	}
}

// TestOrderEffectivePriority: §7.3 step 4 incl. transitive waiters, aging
// and ties.
func TestOrderEffectivePriority(t *testing.T) {
	cases := []struct {
		name  string
		tasks []Task
		first string
	}{
		{"low blocker of an urgent waiter goes first", []Task{task("blocker", prio(PriorityLow)), task("waiter", prio(PriorityUrgent), after("blocker", UntilDone)), task("medium", prio(PriorityMedium))}, "blocker"},
		{"transitive waiter", []Task{task("a", prio(PriorityLow)), task("b", prio(PriorityLow), after("a", UntilDone)), task("c", prio(PriorityUrgent), after("b", UntilDone)), task("m", prio(PriorityHigh))}, "a"},
		{"a done waiter does not lift", []Task{task("x", prio(PriorityLow)), task("w", prio(PriorityUrgent), status(StatusDone), after("x", UntilDone)), task("m", prio(PriorityMedium))}, "m"},
		{"aging: two-day-old low beats fresh low", []Task{task("fresh", prio(PriorityLow), age(time.Hour)), task("old", prio(PriorityLow), age(48*time.Hour))}, "old"},
		{"aging is one step only", []Task{task("ancient", prio(PriorityLow), age(30*24*time.Hour)), task("high", prio(PriorityHigh), age(time.Hour))}, "high"},
		{"tie: older first", []Task{task("newer", age(time.Minute)), task("older", age(2*time.Minute))}, "older"},
		{"tie on age: id order", []Task{task("b"), task("a")}, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Next(Input{Tasks: tc.tasks, Now: now})
			if len(res.Ready) == 0 || res.Ready[0].ID != tc.first {
				t.Fatalf("first %v, want %s", ids(res.Ready), tc.first)
			}
		})
	}
}

// TestCycles: refused at add/edit (CheckCycle) and reported if one slipped
// in through a merge (Next).
func TestCycles(t *testing.T) {
	a, b, c := task("a"), task("b", after("a", UntilDone)), task("c", after("b", UntilDone))
	if cyc := CheckCycle([]Task{a, b, c}, "a", []string{"c"}); strings.Join(cyc, ">") != "a>c>b>a" {
		t.Fatalf("a after c closes a → c → b → a: %v", cyc)
	}
	if cyc := CheckCycle([]Task{a, b, c}, "a", []string{"a"}); cyc == nil {
		t.Fatal("self dependency is a cycle")
	}
	if cyc := CheckCycle([]Task{a, b, c}, "new", []string{"c", "a"}); cyc != nil {
		t.Fatalf("a new task can never close a cycle: %v", cyc)
	}
	if cyc := CheckCycle([]Task{a, b, c}, "c", []string{"a"}); cyc != nil {
		t.Fatalf("re-pointing c to a is fine: %v", cyc)
	}
	merged := []Task{task("a", after("b", UntilDone)), task("b", after("a", UntilDone))}
	res := Next(Input{Tasks: merged, Now: now})
	if w := waitingFor(res, "a"); w == nil || w.Kind != WaitCycle || !strings.Contains(w.Reason, "cycle: a → b → a") {
		t.Fatalf("a merged-in cycle is reported: %+v", res)
	}
}

// TestPickAfterLoss: random among the top three untried.
func TestPickAfterLoss(t *testing.T) {
	ready := []Task{task("1"), task("2"), task("3"), task("4")}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		got, ok := PickAfterLoss(ready, map[string]bool{"1": true}, func(n int) int { return i % n })
		if !ok {
			t.Fatal("candidates left")
		}
		seen[got.ID] = true
	}
	if len(seen) != 3 || seen["1"] {
		t.Fatalf("picks among 2,3,4 only: %v", seen)
	}
	if _, ok := PickAfterLoss(ready, map[string]bool{"1": true, "2": true, "3": true, "4": true}, func(int) int { return 0 }); ok {
		t.Fatal("nothing left to try")
	}
}

// TestAnyRepoRole: views see role and repo tasks of the mission.
func TestAnyRepoRole(t *testing.T) {
	tasks := []Task{task("r", role("coordinator")), task("b", repo("backend")), task("x", func(t *Task) { t.MissionID = "other" })}
	for i := range tasks[:2] {
		tasks[i].MissionID = "m"
	}
	got := ids(Next(Input{Tasks: tasks, Filter: Filter{Mission: "m", AnyRepoRole: true}, Now: now}).Ready)
	if strings.Join(sorted(got), ",") != "b,r" {
		t.Fatalf("views see every task of the mission: %v", got)
	}
}
