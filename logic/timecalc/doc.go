// Package timecalc is the pure interval math of task time tracking
// (TIME_TRACKING_DESIGN 5, CODE ORG L1): spans in, effort / elapsed /
// manual and per-worker and per-owner totals out. No git, no engine: the
// task time operations (ops/task) feed it the row's entries and open spans.
//
//	ops/task (time show, list rollups)
//	   │ []Span{worker, owner, start, end | seconds, manual}
//	   ▼
//	Compute ──► per worker: union of auto spans (D5, rule 4) + manual
//	   │        effort  = Σ workers (rule 5)
//	   │        elapsed = union of every auto span (rule 6)
//	   │        owner subtotal = union over owner + their agents (TQ1, rule 7)
//	   ▼
//	Result{Effort, Elapsed, Manual, Workers, Owners}
//
//	Coalesce(prev, next, gap) ── adjacent spans of one worker merge at write (TQ4)
//	LongSpan(seconds, limit)   ── flag only, never capped (TQ2, D9)
//
// Invariants: input order never changes a result; adding a span never
// lowers effort; without manual time effort ≥ elapsed.
package timecalc
