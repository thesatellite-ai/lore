package urgency

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Term names a part of the score. Closed set: the names are policy keys
// (tasks.urgency.<term>) and JSON values of `--why`, so never rename one.
type Term string

// The terms (TASK 13.2).
const (
	TermPriority      Term = "priority"
	TermDue           Term = "due"
	TermMissionTarget Term = "mission_target"
	TermBlocking      Term = "blocking"
	TermInProgress    Term = "in_progress"
	TermAge           Term = "age"
	TermSLARisk       Term = "sla_risk"
	TermNextTag       Term = "next_tag"
	TermProposed      Term = "proposed"
)

// Terms lists every term in display order.
func Terms() []Term {
	return []Term{TermPriority, TermDue, TermMissionTarget, TermBlocking, TermInProgress, TermAge, TermSLARisk, TermNextTag, TermProposed}
}

// Priority is a task priority name (schema.TaskPriorities).
type Priority string

// The priorities, lowest first.
const (
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

// rank orders priorities; an unknown name ranks as medium (the schema default).
func (p Priority) rank() int {
	switch p {
	case PriorityLow:
		return 1
	case PriorityHigh:
		return 3
	case PriorityUrgent:
		return 4
	case PriorityMedium:
		return 2
	}
	return 2
}

// norm maps an unknown priority to medium.
func (p Priority) norm() Priority {
	switch p {
	case PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent:
		return p
	}
	return PriorityMedium
}

// Status is the slice of task statuses the score reads.
type Status string

// Statuses (schema.TaskStatuses).
const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
	StatusBlocked    Status = "blocked"
)

// Open reports whether work can still happen on the task.
func (s Status) Open() bool { return s == StatusTodo || s == StatusInProgress || s == "" }

// Task is what the score reads about one task.
type Task struct {
	ID        string
	Priority  Priority
	Status    Status
	CreatedAt time.Time
	// Proposed is commitment proposed (13.2: −3.00).
	Proposed bool
	// Due is due_at; zero = none.
	Due time.Time
	// MissionTarget is the mission's target_date; zero = none or no mission.
	MissionTarget time.Time
	// Parent is the parent task id ("" = top level).
	Parent string
	// After are the ids this task waits on (blocked_by edges).
	After []string
	// NextTag: tagged `next` (13.2).
	NextTag bool
	// SLARisk: past 80% of its SLA (18.4); false while SLAs are not read.
	SLARisk bool
}

// Weights are the term values in hundredths (13.2 defaults; a level may
// override them through tasks.urgency.<term>).
type Weights struct {
	PriorityPoints map[Priority]int
	DueMax         int
	DueMin         int // the points at the far end of the window (0.20)
	DueWindow      time.Duration
	MissionMax     int
	BlockingEach   int
	BlockingMax    int
	InProgress     int
	AgeMax         int
	AgeWindow      time.Duration
	SLARisk        int
	NextTag        int
	Proposed       int
}

// day is 24 hours (dates are UTC instants; no calendar math here).
const day = 24 * time.Hour

// Default returns the 13.2 defaults.
func Default() Weights {
	return Weights{
		PriorityPoints: map[Priority]int{PriorityUrgent: 900, PriorityHigh: 600, PriorityMedium: 390, PriorityLow: 180},
		DueMax:         1200, DueMin: 20, DueWindow: 14 * day,
		MissionMax:   400,
		BlockingEach: 400, BlockingMax: 1200,
		InProgress: 400,
		AgeMax:     200, AgeWindow: 60 * day,
		SLARisk:  500,
		NextTag:  1500,
		Proposed: -300,
	}
}

// Set overrides one term (hundredths). For TermPriority the key is
// "priority.<level>"; the caller passes the level in sub. An unknown term
// is an error so a typo in a policy row never silently does nothing.
func (w *Weights) Set(term Term, sub string, hundredths int) error {
	switch term {
	case TermPriority:
		p := Priority(sub)
		if p.norm() != p {
			return fmt.Errorf("urgency: unknown priority %q", sub)
		}
		m := make(map[Priority]int, len(w.PriorityPoints))
		for k, v := range w.PriorityPoints {
			m[k] = v
		}
		m[p] = hundredths
		w.PriorityPoints = m
	case TermDue:
		w.DueMax = hundredths
	case TermMissionTarget:
		w.MissionMax = hundredths
	case TermBlocking:
		w.BlockingEach = hundredths
	case TermInProgress:
		w.InProgress = hundredths
	case TermAge:
		w.AgeMax = hundredths
	case TermSLARisk:
		w.SLARisk = hundredths
	case TermNextTag:
		w.NextTag = hundredths
	case TermProposed:
		w.Proposed = hundredths
	default:
		return fmt.Errorf("urgency: unknown term %q", term)
	}
	return nil
}

// Part is one term's contribution.
type Part struct {
	Term   Term   `json:"term"`
	Points int    `json:"points"` // hundredths
	Detail string `json:"detail"`
}

// Score is one task's urgency.
type Score struct {
	Total int    `json:"urgency"` // hundredths
	Terms []Part `json:"terms"`
	// Effective is the effective priority the priority term used.
	Effective Priority `json:"effective_priority"`
}

// Format renders hundredths as a decimal ("17.40", "-3.00").
func Format(h int) string {
	sign := ""
	if h < 0 {
		sign, h = "-", -h
	}
	return fmt.Sprintf("%s%d.%02d", sign, h/100, h%100)
}

// Lines are the --why lines ("+6.00  priority high (…)").
func (s Score) Lines() []string {
	out := make([]string, 0, len(s.Terms))
	for _, p := range s.Terms {
		sign := "+"
		if p.Points < 0 {
			sign = ""
		}
		out = append(out, fmt.Sprintf("%s%s  %s", sign, Format(p.Points), p.Detail))
	}
	return out
}

// eff is an effective priority with where it came from.
type eff struct {
	p    Priority
	from string // "" own, else "parent <id>" / "ancestor <id>" / "waiting task <id>"
}

// Compute scores every open task of tasks. Closed tasks get no score but
// still count as ancestors (never as waiters: only open tasks wait).
func Compute(tasks []Task, now time.Time, w Weights) map[string]Score {
	byID := make(map[string]Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	effs := effective(tasks, byID)
	waiters := map[string][]string{}
	for _, t := range tasks {
		if !t.Status.Open() {
			continue
		}
		for _, d := range uniq(t.After) {
			waiters[d] = append(waiters[d], t.ID)
		}
	}
	out := make(map[string]Score, len(tasks))
	for _, t := range tasks {
		if !t.Status.Open() {
			continue
		}
		out[t.ID] = score(t, effs[t.ID], waiters[t.ID], now, w)
	}
	return out
}

// effective computes 6.10's effective priority: the highest of own,
// ancestors', and of open tasks waiting on it (transitively). A fixpoint
// bounded by the task count, so cycles (merged trees, dependency loops)
// terminate.
func effective(tasks []Task, byID map[string]Task) map[string]eff {
	e := make(map[string]eff, len(tasks))
	ordered := append([]Task(nil), tasks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, t := range ordered {
		e[t.ID] = eff{p: t.Priority.norm()}
	}
	raise := func(id string, cand eff) bool {
		cur, ok := e[id]
		if !ok || cand.p.rank() <= cur.p.rank() {
			return false
		}
		e[id] = cand
		return true
	}
	for range ordered {
		changed := false
		for _, t := range ordered {
			// Down the tree: a child inherits its parent's effective priority.
			if t.Parent != "" {
				if par, ok := e[t.Parent]; ok {
					from := "parent " + t.Parent
					if par.from != "" {
						from = par.from
						if rest, ok := strings.CutPrefix(from, "parent "); ok {
							from = "ancestor " + rest
						}
					}
					if raise(t.ID, eff{p: par.p, from: from}) {
						changed = true
					}
				}
			}
			// Up the dependency edges: an open waiter lends its priority.
			if !t.Status.Open() {
				continue
			}
			for _, d := range t.After {
				if _, ok := byID[d]; ok && raise(d, eff{p: e[t.ID].p, from: "waiting task " + t.ID}) {
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return e
}

func uniq(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// score builds one task's terms; zero terms are left out of the list.
func score(t Task, e eff, waiters []string, now time.Time, w Weights) Score {
	s := Score{Effective: e.p}
	add := func(term Term, pts int, detail string) {
		if pts == 0 {
			return
		}
		s.Terms = append(s.Terms, Part{Term: term, Points: pts, Detail: detail})
		s.Total += pts
	}
	pd := "priority " + string(e.p)
	if e.from != "" {
		pd += " (inherited from " + e.from + ")"
	}
	add(TermPriority, w.PriorityPoints[e.p], pd)
	if !t.Due.IsZero() {
		add(TermDue, curve(t.Due.Sub(now), w.DueMax, w.DueMin, w.DueWindow), "due "+whenWord(t.Due, now))
	} else if !t.MissionTarget.IsZero() {
		minPts := w.DueMin * w.MissionMax / max(w.DueMax, 1)
		add(TermMissionTarget, curve(t.MissionTarget.Sub(now), w.MissionMax, minPts, w.DueWindow), "mission target "+whenWord(t.MissionTarget, now))
	}
	if n := len(waiters); n > 0 {
		sort.Strings(waiters)
		add(TermBlocking, min(n*w.BlockingEach, w.BlockingMax), fmt.Sprintf("blocks %d open task%s (%s)", n, plural(n), strings.Join(waiters, ", ")))
	}
	if t.Status == StatusInProgress {
		add(TermInProgress, w.InProgress, "in progress")
	}
	if !t.CreatedAt.IsZero() && w.AgeWindow > 0 {
		age := max(now.Sub(t.CreatedAt), 0)
		pts := w.AgeMax
		if age < w.AgeWindow {
			pts = int(int64(w.AgeMax) * int64(age) / int64(w.AgeWindow))
		}
		add(TermAge, pts, fmt.Sprintf("age %d day%s", int(age/day), plural(int(age/day))))
	}
	if t.SLARisk {
		add(TermSLARisk, w.SLARisk, "past 80% of its SLA")
	}
	if t.NextTag {
		add(TermNextTag, w.NextTag, "tagged next")
	}
	if t.Proposed {
		add(TermProposed, w.Proposed, "commitment proposed")
	}
	return s
}

// curve is 13.2's due curve: maxPts when due now or overdue, linear down
// to minPts at window away, 0 beyond the window.
func curve(left time.Duration, maxPts, minPts int, window time.Duration) int {
	switch {
	case left <= 0:
		return maxPts
	case left > window:
		return 0
	}
	return maxPts - int(int64(maxPts-minPts)*int64(left)/int64(window))
}

// whenWord says how far a date is ("2026-11-13, in 9 days", "overdue 2 days").
func whenWord(at, now time.Time) string {
	d := at.UTC().Format(time.DateOnly)
	left := at.Sub(now)
	switch {
	case left < 0:
		n := int(-left / day)
		return fmt.Sprintf("%s, overdue %d day%s", d, n, plural(n))
	case left < day:
		n := int(left / time.Hour)
		return fmt.Sprintf("%s, in %d hour%s", d, n, plural(n))
	}
	n := int(left / day)
	return fmt.Sprintf("%s, in %d day%s", d, n, plural(n))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Less is 13.2's tie-break: higher urgency, then older created_at, then
// the smaller id. a and b are tasks with their scores.
func Less(a, b Task, sa, sb Score) bool {
	if sa.Total != sb.Total {
		return sa.Total > sb.Total
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

// Rank returns the open tasks ordered by Less with their scores.
func Rank(tasks []Task, now time.Time, w Weights) ([]Task, map[string]Score) {
	scores := Compute(tasks, now, w)
	var open []Task
	for _, t := range tasks {
		if t.Status.Open() {
			open = append(open, t)
		}
	}
	sort.SliceStable(open, func(i, j int) bool {
		return Less(open[i], open[j], scores[open[i].ID], scores[open[j].ID])
	})
	return open, scores
}
