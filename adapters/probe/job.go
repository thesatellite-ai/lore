package probe

// job.go — the calls of work that outlives its request (owner 2026-11-08:
// the setup page's background account discovery ran its gh, GitHub API and
// ssh -T probes after setup.discover had answered, so they landed in a
// trace nobody read again and the debug log showed none of them).
//
//	surface ─▶ WithJobSink(ctx, sink) ─▶ op ─▶ StartJob(ctx, "discovery job") ─▶ job ctx (its own Trace)
//	                                           job ─▶ probes ─▶ trace.add(Call)
//	                                           finish(err) ─▶ sink(JobRecord)   (the surface logs it as its own entry)
//
// A job's Trace starts with the request's registered secrets, so a token
// the request registered is redacted in the job's calls too. Without a
// sink in ctx StartJob still detaches the job from the request's Trace
// (that trace was already read when the request answered) and records
// nothing; like Trace, the sink rides in the context: no package state.

import (
	"context"
	"time"
)

// JobRecord is one finished background job and the calls it made (every
// text already redacted by the job's Trace).
type JobRecord struct {
	// Name says what the job was ("discovery job").
	Name    string
	Start   time.Time
	Took    time.Duration
	Calls   []Call
	Dropped int
	// Err is the job's error, redacted ("" = it succeeded).
	Err string
}

// JobSink receives a job's record when it finishes. It runs on the job's
// goroutine, so it must be safe for concurrent use.
type JobSink func(JobRecord)

type jobSinkKey struct{}

// WithJobSink returns ctx carrying sink (a nil sink returns ctx unchanged).
func WithJobSink(ctx context.Context, sink JobSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, jobSinkKey{}, sink)
}

// StartJob gives a background job started under ctx its own Trace and
// returns the job's ctx and finish, which hands the job's calls to the
// sink in ctx (if any). Call finish exactly once, when the job ends.
func StartJob(ctx context.Context, name string) (context.Context, func(error)) {
	sink, _ := ctx.Value(jobSinkKey{}).(JobSink)
	if sink == nil {
		// No one will read the job's calls: record none, and never write
		// into the request's trace after it was read.
		return context.WithValue(ctx, traceKey{}, (*Trace)(nil)), func(error) {}
	}
	t := &Trace{}
	if parent := TraceFrom(ctx); parent != nil {
		parent.mu.Lock()
		t.secrets = append([]string(nil), parent.secrets...)
		parent.mu.Unlock()
	}
	start := time.Now()
	jctx := context.WithValue(ctx, traceKey{}, t)
	return jctx, func(err error) {
		rec := JobRecord{Name: name, Start: start, Took: time.Since(start), Calls: t.Calls(), Dropped: t.Dropped()}
		if err != nil {
			rec.Err = t.Redact(err.Error())
		}
		sink(rec)
	}
}
