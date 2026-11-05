//go:build teame2e

package teame2e

// TIME_TRACKING_DESIGN 11.8, bench gate OQ6: import and `lore task time`
// on a task with 5,000 time entries, and a whole store with 100k entries,
// on real git with the real binary. The rows are written straight into
// .lore/data (one CLI call per entry would take an hour), pushed to main,
// and imported by a teammate's clone. The test logs each measurement; see
// benchLimits for the first numbers and the limits proposed.

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	// benchEntriesPerTask is the 5,000-entry task of OQ6.
	benchEntriesPerTask = 5000
	// benchTasks makes the 100k-entry store of OQ6 (20 × 5,000).
	benchTasks = 20
	// benchEntrySeconds is each generated manual entry's length.
	benchEntrySeconds = 900
	// benchLintWarnAt is the per-task entry count `lore lint` warns at (11.8).
	benchLintWarnAt = 300
)

// benchLimits are the proposed OQ6 limits for one command's wall time.
// The design sets limits "a bit above the measurement"; the first
// measurement (2026-11-05, ex130r, loretest build, other agents running)
// was far above any usable limit (import 48 s, time show 56 s, lint 6 m 30 s,
// one task add 3 m 55 s), so these are the targets proposed to the owner
// and the overrun is pinned as BUG-TIME-BENCH rather than written in as the
// limit.
var benchLimits = struct {
	importStore, showTask, showWarm, lint, write time.Duration
}{
	importStore: 20 * time.Second,
	showTask:    3 * time.Second,
	showWarm:    3 * time.Second,
	lint:        10 * time.Second,
	write:       10 * time.Second,
}

// envBenchOQ6 opts into the bench: it runs about 15 minutes, so the team
// suite leaves it out unless asked (OWNER WALL-TIME RULES).
const envBenchOQ6 = "LORE_BENCH_OQ6"

// bugTimeBench: on a store of 20 tasks × 5,000 time entries, import, time
// show, lint and a plain task add each take from about a minute to over six
// minutes (see benchLimits). Found by a-task-e2e (PASS3 queue #10). Closed
// 2026-11-05 at 5c79f3d (row decode + read memo, list reads its own table,
// parallel rowscan, per-write task_tree P3-PERF-1): import 7.4 s, time show
// 1.4 s, --entries 1.7 s, lint 3.7 s, one task add 2.5 s, all within limits.
var bugTimeBench = th.Bug{ID: "BUG-TIME-BENCH", Owner: "main (row read/write path on big rows)", Open: false}

// benchEntryID is a tme_ id: prefix + 32 hex of a UUIDv7 (version 7,
// variant 10) whose time part is at.
func benchEntryID(t *testing.T, at time.Time) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(at.UnixMilli()))
	copy(b[:6], ms[2:])
	b[6] = 0x70 | b[6]&0x0f
	b[8] = 0x80 | b[8]&0x3f
	return "tme_" + hex.EncodeToString(b[:])
}

// fillTimeEntries rewrites the row file of task id on c with n manual
// entries built from the row's one logged entry (same actor and fields),
// one per 15 minutes going back from the template's start.
func fillTimeEntries(t *testing.T, c *th.Clone, id string, n int) {
	t.Helper()
	rel := ".lore/data/tasks/" + id + ".json"
	body, ok := c.ReadFile(rel)
	if !ok {
		t.Fatalf("no row file %s (task added with --no-live)", rel)
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &row); err != nil {
		t.Fatal(err)
	}
	var tpl []map[string]json.RawMessage
	if err := json.Unmarshal(row["time_entries"], &tpl); err != nil || len(tpl) != 1 {
		t.Fatalf("the seed row holds one entry: %v %s", err, row["time_entries"])
	}
	var start time.Time
	if err := json.Unmarshal(tpl[0]["start"], &start); err != nil {
		t.Fatal(err)
	}
	str := func(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
	out := make([]map[string]json.RawMessage, 0, n)
	for i := 0; i < n; i++ {
		e := map[string]json.RawMessage{}
		for k, v := range tpl[0] {
			e[k] = v
		}
		s := start.Add(-time.Duration(i) * benchEntrySeconds * time.Second).UTC()
		end := s.Add(benchEntrySeconds * time.Second)
		ts := func(x time.Time) json.RawMessage { return str(x.Format("2006-01-02T15:04:05.000Z")) }
		e["id"] = str(benchEntryID(t, end))
		e["start"], e["end"], e["created_at"], e["updated_at"] = ts(s), ts(end), ts(end), ts(end)
		e["day"] = str(s.Format("2006-01-02"))
		e["seconds"] = json.RawMessage(strconv.Itoa(benchEntrySeconds))
		e["note"] = str("bench " + strconv.Itoa(i))
		out = append(out, e)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	row["time_entries"] = raw
	nb, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	c.WriteFile(rel, string(nb)+"\n")
}

// timed runs one lore command on c and returns its result and wall time.
func timed(c *th.Clone, args ...string) (th.Result, time.Duration) {
	start := time.Now()
	r := c.Lore(args...)
	return r, time.Since(start)
}

// OQ6: dev1 builds a store of 20 tasks × 5,000 entries and pushes it;
// dev2's clone imports it, shows one task's time (cold and warm) and runs
// lint, each within its limit, with the right totals and the 300 warning.
func TestTime_Bench_OQ6_HugeHistories(t *testing.T) {
	if os.Getenv(envBenchOQ6) != "1" {
		t.Skip("OQ6 bench runs about 15 minutes; " + envBenchOQ6 + "=1 runs it")
	}
	rt := newRepoTeam(t, capTime, th.Cmd("lint"))
	var ids []string
	// Every task and its seed entry first: a write on a store that already
	// holds the big rows pays for them (measured below as dWrite).
	for i := 0; i < benchTasks; i++ {
		id := rt.addTask(t, rt.c1, "bench "+strconv.Itoa(i), "--no-live")
		rt.c1.MustLore("task", "time", "log", id, "15m", "--note", "seed")
		ids = append(ids, id)
	}
	for _, id := range ids {
		fillTimeEntries(t, rt.c1, id, benchEntriesPerTask)
	}
	rt.c1.CommitAll("bench rows")
	rt.c1.AdminPush("origin", "main")

	rt.c2.Git("pull", "--ff-only", "origin", "main")
	r, dImport := timed(rt.c2, "sync")
	r.OK(t)
	r, dShow := timed(rt.c2, "task", "time", "show", ids[0], "--json")
	v := th.DecodeJSON[timeView](t, r)
	_, dWarm := timed(rt.c2, "task", "time", "show", ids[0], "--entries", "--json")
	lr, dLint := timed(rt.c2, "lint")
	wr, dWrite := timed(rt.c2, "task", "add", "one more", "--tasklist", rt.tasklist, "--no-live")
	wr.OK(t)
	t.Logf("OQ6 bench (%d tasks × %d entries): import %v, time show %v, time show --entries %v, lint %v, one task add %v",
		benchTasks, benchEntriesPerTask, dImport, dShow, dWarm, dLint, dWrite)

	if want := benchEntriesPerTask * benchEntrySeconds; v.Data.Manual != want || v.Data.Effort != want {
		t.Fatalf("a 5,000-entry task counts every entry: manual %d effort %d, want %d", v.Data.Manual, v.Data.Effort, want)
	}
	if !strings.Contains(lr.Out(), ids[0]) || !strings.Contains(lr.Out(), strconv.Itoa(benchEntriesPerTask)+" time entries") {
		t.Fatalf("lint warns at %d entries on a task: %s", benchLintWarnAt, lr)
	}
	var over []string
	for _, m := range []struct {
		what     string
		got, max time.Duration
	}{
		{"import of the 100k-entry store", dImport, benchLimits.importStore},
		{"time show of a 5,000-entry task", dShow, benchLimits.showTask},
		{"time show --entries of a 5,000-entry task", dWarm, benchLimits.showWarm},
		{"lint of the 100k-entry store", dLint, benchLimits.lint},
		{"one task add in the 100k-entry store", dWrite, benchLimits.write},
	} {
		if m.got > m.max {
			over = append(over, m.what+" took "+m.got.Round(time.Second).String()+", limit "+m.max.String())
		}
	}
	bugTimeBench.PendingIf(t, len(over) > 0, strings.Join(over, "; "))
	for _, o := range over {
		t.Errorf("OQ6: %s", o)
	}
}
