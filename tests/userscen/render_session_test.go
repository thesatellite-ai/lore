//go:build userscen

package userscen

// render_session_test.go — the two render features a person reads over
// days rather than in one run, driven with a moving clock (LORE_NOW):
//   - `lore render --trend`: the bytes of every render in the last 14
//     days, the growth per day and the date LORE.md reaches its budget
//     (CONTEXT_BUDGET 6.11), checked against the sizes the renders wrote;
//   - `lore context --hook <tool> --hook-stdin --session-start`: the
//     SessionStart answer (CONTEXT_BUDGET 6.9) — the person's in-flight
//     tasks and the rows changed since their last session, nothing on a
//     first session with nothing in flight, nothing changed twice.

import (
	"encoding/json"
	"math"
	"os/exec"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// renderDay0 is the pinned start of the clock-driven render scenarios.
var renderDay0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

// renderTrendJSON is `lore render --trend --json` as a script reads it.
type renderTrendJSON struct {
	Target  string `json:"target"`
	Budget  int    `json:"budget"`
	Renders []struct {
		At    time.Time `json:"at"`
		Bytes int       `json:"bytes"`
	} `json:"renders"`
	Growth float64 `json:"growth_per_day"`
	Reach  string  `json:"reaches_budget"`
	Over   bool    `json:"over_budget"`
}

func renderTrend(t *testing.T, c *th.Clone) renderTrendJSON {
	t.Helper()
	r := c.MustLore("render", "--trend", "--json")
	var tr renderTrendJSON
	if err := json.Unmarshal([]byte(r.Stdout), &tr); err != nil {
		t.Fatalf("render --trend --json is not JSON: %v\n%s", err, r)
	}
	return tr
}

func TestRenderTrendOverDays(t *testing.T) {
	c := renderSolo(t)
	c.A.SetNow(renderDay0)
	// Day 0: a render that falls out of the 14-day window later.
	c.MustLore("rule", "add", "--body", "Run the tests before you push")
	c.MustLore("render")
	// Day 15 and day 17: two renders the trend reads, LORE.md growing.
	c.A.Advance(15 * 24 * time.Hour)
	c.MustLore("rule", "add", "--body", "Never log a customer's card number")
	c.MustLore("render")
	size1 := len(renderRead(t, c, renderLoreMD))
	at1 := c.A.Now()
	c.A.Advance(2 * 24 * time.Hour)
	c.MustLore("rule", "add", "--body", "Every migration ships with its rollback, reviewed by a second person before it runs")
	c.MustLore("render")
	size2 := len(renderRead(t, c, renderLoreMD))
	at2 := c.A.Now()
	if size2 <= size1 {
		t.Fatalf("fixture: LORE.md should grow (%d then %d)", size1, size2)
	}
	c.A.Advance(24 * time.Hour)
	now := c.A.Now()

	tr := renderTrend(t, c)
	if tr.Target != renderLoreMD || tr.Budget <= 0 {
		t.Fatalf("trend names LORE.md and its budget: %+v", tr)
	}
	if len(tr.Renders) != 2 {
		t.Fatalf("only the renders of the last 14 days count (day 0 is out): %+v", tr.Renders)
	}
	if !tr.Renders[0].At.Equal(at1) || !tr.Renders[1].At.Equal(at2) {
		t.Errorf("renders oldest first at their own times (%s, %s): %+v", at1, at2, tr.Renders)
	}
	if tr.Renders[0].Bytes != size1 || tr.Renders[1].Bytes != size2 {
		t.Errorf("each render records the bytes LORE.md had (%d, %d): %+v", size1, size2, tr.Renders)
	}
	wantGrowth := float64(size2-size1) / 2
	if math.Abs(tr.Growth-wantGrowth) > 0.01 {
		t.Errorf("growth per day = (last − first) / days = %.2f, got %.2f", wantGrowth, tr.Growth)
	}
	left := float64(tr.Budget-size2) / wantGrowth
	wantReach := now.Add(time.Duration(math.Ceil(left*24)) * time.Hour).Format(time.DateOnly)
	if tr.Over || tr.Reach != wantReach {
		t.Errorf("reaches the budget around %s (not over), got reach=%q over=%v", wantReach, tr.Reach, tr.Over)
	}
	// The person's view says the same in words.
	c.MustLore("render", "--trend").Says(t,
		renderLoreMD+": renders in the last 14 days",
		"reaches the budget around "+wantReach,
	)

	// Renders minutes apart give no rate: a growth from them would be
	// meaningless, so the trend says what it needs instead.
	c.A.Advance(15 * 24 * time.Hour)
	c.MustLore("render")
	c.A.Advance(5 * time.Minute)
	c.MustLore("render")
	tr = renderTrend(t, c)
	if len(tr.Renders) != 2 || tr.Growth != 0 || tr.Reach != "" {
		t.Errorf("two renders minutes apart: no growth, no date: %+v", tr)
	}
	c.MustLore("render", "--trend").Says(t, "growth: needs renders at least a day apart")

	// A machine with no render in the window says so, exit 0.
	c.A.Advance(30 * 24 * time.Hour)
	c.MustLore("render", "--trend").Says(t, "no render recorded on this machine in 14 days")
}

// renderHookAnswer is the hook JSON a tool reads.
type renderHookAnswer struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// renderSessionStart runs the SessionStart hook the way the tool does
// (JSON on stdin) and returns stdout; a hook always exits 0.
func renderSessionStart(t *testing.T, c *th.Clone, flag bool, stdin string) string {
	t.Helper()
	renderHookNoDeadline(c)
	args := []string{"context", "--hook", "claude_code", "--hook-stdin"}
	if flag {
		args = append(args, "--session-start")
	}
	cmd := c.Command(args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("the SessionStart hook never fails the tool: %v\n%s%s", err, out, stderr)
	}
	return strings.TrimSpace(string(out))
}

// renderHookDeadline is the hook deadline these tests give the binary
// (LORE_HOOK_TIMEOUT, loretest builds): far above any run, so a gate that
// saturates the machine cannot turn a hook's answer into the 2 s
// deadline's silent nothing; what a hook says is the thing under test.
const renderHookDeadline = "10m"

// renderHookNoDeadline lifts the hook deadline for c's actor.
func renderHookNoDeadline(c *th.Clone) { c.A.SetEnv("LORE_HOOK_TIMEOUT", renderHookDeadline) }

func TestRenderSessionStartHook(t *testing.T) {
	c := renderSolo(t)
	c.A.SetNow(renderDay0)
	old := renderID(t, c.MustLore("rule", "add", "--body", "Run the tests before you push"), "rul")

	// First session, nothing in flight: nothing to say, nothing printed.
	if out := renderSessionStart(t, c, true, `{"session_id":"s1"}`); out != "" {
		t.Fatalf("a first session with nothing in flight prints nothing: %s", out)
	}

	// Between sessions: a new rule, and a task the person started.
	c.A.Advance(24 * time.Hour)
	changed := renderID(t, c.MustLore("rule", "add", "--body", "Deploy only on weekdays"), "rul")
	var who struct {
		Data struct {
			Identity struct {
				ActorID string `json:"actor_id"`
			} `json:"identity"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(c.MustLore("whoami", "--json").Stdout), &who); err != nil || who.Data.Identity.ActorID == "" {
		t.Fatalf("whoami --json names the person's actor: %v", err)
	}
	task := renderID(t, c.MustLore("task", "add", "Fix the invoice crash", "--priority", "high", "--assigned-to", who.Data.Identity.ActorID), "tsk")
	c.MustLore("task", "start", task)
	other := renderID(t, c.MustLore("task", "add", "Someone else's later work"), "tsk")

	c.A.Advance(time.Hour)
	out := renderSessionStart(t, c, true, `{"session_id":"s2"}`)
	var ans renderHookAnswer
	if err := json.Unmarshal([]byte(out), &ans); err != nil {
		t.Fatalf("SessionStart answers in Claude Code's hook JSON: %v\n%s", err, out)
	}
	ctx := ans.HookSpecificOutput.AdditionalContext
	if ans.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("event is SessionStart: %s", out)
	}
	inflight, rest, ok := strings.Cut(ctx, "## Changed since your last session")
	if !ok || !strings.Contains(inflight, "## In-flight tasks") {
		t.Fatalf("two groups, in-flight tasks first, then the changed rows:\n%s", ctx)
	}
	if !strings.Contains(inflight, task) || !strings.Contains(inflight, "Fix the invoice crash (high)") {
		t.Errorf("the person's in-progress task, with its priority:\n%s", ctx)
	}
	if strings.Contains(ctx, other) {
		t.Errorf("a task not in progress for this person is left out:\n%s", ctx)
	}
	if !strings.Contains(rest, changed) || strings.Contains(ctx, old) {
		t.Errorf("only the rule changed since the last session (%s), not the older one (%s):\n%s", changed, old, ctx)
	}
	if !strings.Contains(ctx, "lore show "+changed) {
		t.Errorf("each changed row names the command for its full text:\n%s", ctx)
	}
	if len(ctx) > 2000 {
		t.Errorf("the SessionStart answer stays within its 2000-byte cap: %d bytes", len(ctx))
	}

	// The next session, minutes later, with the event named in the input
	// instead of the flag: the task is still in flight, nothing changed.
	c.A.Advance(5 * time.Minute)
	out = renderSessionStart(t, c, false, `{"session_id":"s3","hook_event_name":"SessionStart"}`)
	ans = renderHookAnswer{}
	if err := json.Unmarshal([]byte(out), &ans); err != nil {
		t.Fatalf("hook_event_name SessionStart answers like --session-start: %v\n%s", err, out)
	}
	ctx = ans.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, task) || strings.Contains(ctx, "Changed since your last session") || strings.Contains(ctx, changed) {
		t.Errorf("a second session: the in-flight task again, no row changed twice:\n%s", ctx)
	}

	// Once the task is done and nothing changed, the hook is silent again.
	c.MustLore("task", "done", task)
	c.A.Advance(5 * time.Minute)
	c.MustLore("task", "list") // a read changes nothing
	c.A.Advance(5 * time.Minute)
	if out := renderSessionStart(t, c, true, `{"session_id":"s4"}`); strings.Contains(out, task) {
		t.Errorf("a done task is no longer in flight: %s", out)
	}
}
