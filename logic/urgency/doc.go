// Package urgency is the task urgency score (TASK_SYSTEM_DESIGN 13, TSQ13;
// CODE ORG L1): one integer per open task, in hundredths so every machine
// gets the same sum, made of named terms so `task next --why` and
// `task show --why` can explain every point. Pure: no git, no DB, no clock
// (the caller passes now).
//
//	ops/task show --why, old queue (task next, --dry-run)
//	   │ []Task{id, priority, status, commitment, created, due,
//	   │        mission target, parent, after, next tag, sla risk}
//	   ▼
//	Compute(tasks, now, weights)
//	   ├─ effective priority: own ∨ ancestors ∨ open waiters (6.10, 13.2)
//	   ├─ terms: priority due mission_target blocking in_progress age
//	   │         sla_risk next_tag proposed
//	   ▼
//	map[id]Score{Total, Terms[]}  ──►  Less (urgency desc, created asc, id asc)
//
// Hard filters (dependencies, gates, claims, deferral, commitment) are NOT
// scores here (13.1): the queue filters first and ranks what is left.
package urgency
