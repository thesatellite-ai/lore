package ask

import (
	"slices"
	"strings"
	"testing"
)

// jobqNote is a long markdown note (over longRow) whose sentence about a
// duplicate enqueue sits in a section deep in it (Sync benchmark set 6
// S6-01's shape, synthetic text).
const jobqNote = `# runq: the background job queue

## Overview
runq runs background jobs for the app. Workers poll the queue table, claim a job with a lease and heartbeat while they run it. Jobs are retried with backoff until max attempts, then parked as dead.

## Workers
Each worker claims one job at a time. The lease is 30 seconds and the heartbeat renews it every 10 seconds. A worker that misses two heartbeats loses its lease and the job goes back to the queue.

## Retries
A failed job is retried with exponential backoff (1s, 2s, 4s, up to 5 minutes). After max attempts the job is parked as dead and shows on the dead-jobs page.

## Scheduling
Jobs can be scheduled for later with a run-at time. The scheduler moves due jobs to the ready queue every second.

## Deduplication
Enqueue takes options; when the dedup key of a new job matches a run that is not terminal yet (queued, claimed or running), Enqueue writes nothing and returns the first run's id. Once that run is terminal (done, dead or cancelled) the same key enqueues a new run.

## Run states
A run moves from queued to claimed to running, and ends as done, dead or cancelled. A run that returns an error goes back to queued with its attempt count raised. Cancelling a queued run marks it cancelled at once; cancelling a running run asks the worker to stop at its next heartbeat.

## Operations
Operators pause a job kind from the admin page; paused kinds keep their queued runs and workers skip them until the kind is resumed. A run stuck in claimed for more than five minutes is reported on the stuck-runs page.

## Tables
The queue keeps its runs in one table with an index on kind, state and run-at time. Finished runs older than 30 days are deleted by a nightly sweep, which keeps the table small and the claim query fast.`

// TestNotInMemoryHeldAsScoreReadsIt (devco D-974, bug hunt H-233, Sync
// benchmark set 6 S6-01): "not in memory" is never printed for a name a
// returned row holds the way score reads it. A code identifier the
// question writes (Queue.Enqueue, EnqueueOptions.DedupKey) is held by a
// row holding each of its parts, and a capitalised word starting the
// question's second sentence is no name. An identifier with a part no
// row holds, and a name after "Dr.", still give the hint.
func TestNotInMemoryHeldAsScoreReadsIt(t *testing.T) {
	t.Parallel()
	d := data{rows: []textRow{mem(t, "mem_q1", "acme/eng", "2026-11-20", jobqNote)}}
	for _, q := range []string{
		"What does Queue.Enqueue return when EnqueueOptions.DedupKey matches a run that is not terminal?",
		"what does runq do with a dedup key while the first run is not terminal? Explain the states.",
		"what does the queue do with a duplicate key. Describe the run states",
	} {
		o := build(parseQuestion(q, now), d, 0, 0)
		if o.Abstain.NotInMemory || strings.HasPrefix(o.Text(), "not in memory") {
			t.Errorf("%q: abstains %+v though mem_q1 holds it\n%s", q, o.Abstain, o.Text())
		}
		if len(o.Rows) == 0 || o.Rows[0].ID != "mem_q1" {
			t.Errorf("%q: rows %v, want mem_q1", q, rowIDs(o.Rows))
		}
	}
	for q, want := range map[string][]string{
		"what does Queue.Reprioritize do with a waiting run":  {"queue.reprioritize"},
		"when did Dr. Okafor review the job queue":            {"okafor"},
		"does the queue page Okafor when a run is stuck? Why": {"okafor"},
	} {
		o := build(parseQuestion(q, now), d, 0, 0)
		if !o.Abstain.NotInMemory || !slices.Equal(o.Abstain.Missing, want) {
			t.Errorf("%q: abstain %+v, want %v missing", q, o.Abstain, want)
		}
	}
}

// TestCutNoteNamesLeftOutWords (devco D-974): a row cut to fit says which
// of the question's words the part left out holds, whatever part the
// excerpt keeps: every question word the whole row holds is in the
// printed text or named by the cut note (Row.LeftOut).
func TestCutNoteNamesLeftOutWords(t *testing.T) {
	t.Parallel()
	d := data{rows: []textRow{mem(t, "mem_q1", "acme/eng", "2026-11-20", jobqNote)}}
	q := "how does the job queue handle a dedup key while the first run is not terminal"
	sawLeftOut := false
	for _, maxChars := range []int{0, 900, 600, 400} {
		o := build(parseQuestion(q, now), d, 0, maxChars)
		if len(o.Rows) == 0 || o.Rows[0].FullChars == 0 {
			t.Fatalf("max %d: rows %v not cut\n%s", maxChars, rowIDs(o.Rows), o.Text())
		}
		r := o.Rows[0]
		printed := tokensOf(r.Title + "\n" + r.Body)
		for _, w := range r.Matched {
			if !slices.Contains(o.q.words, w) {
				continue // a topic name
			}
			if !hasWord(printed, w) && !slices.Contains(r.LeftOut, w) {
				t.Errorf("max %d: %q is in the row, not in the excerpt, and not in left_out %v\n%s", maxChars, w, r.LeftOut, o.Text())
			}
		}
		for _, w := range r.LeftOut {
			if hasWord(printed, w) {
				t.Errorf("max %d: left_out names %q, which the excerpt prints", maxChars, w)
			}
		}
		if len(r.LeftOut) > 0 {
			sawLeftOut = true
			note := "and the part left out holds " + strings.Join(r.LeftOut, ", ") + "; lore show --id mem_q1 prints it whole)"
			if !strings.Contains(o.Text(), note) {
				t.Errorf("max %d: text lacks %q\n%s", maxChars, note, o.Text())
			}
		}
	}
	if !sawLeftOut {
		t.Fatal("no cut left out a question word: the test no longer reaches the cut note")
	}
	if got := leftOutOf(parseQuestion(q, now), []string{"dedup", "terminal", "queue"}, "", "the queue is terminal", nil); !slices.Equal(got, []string{"dedup"}) {
		t.Errorf("leftOutOf = %v, want [dedup]", got)
	}
}
