package coding

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// idPattern is a knowledge row id as lore prints it (rul_, hfx_, dec_,
// pat_, mem_, arc_ and 20+ hex digits).
var idPattern = regexp.MustCompile(`\b(?:rul|hfx|dec|pat|mem|arc)_[0-9a-f]{20,}\b`)

// IDsIn is every knowledge row id printed in out, each once, in order.
func IDsIn(out string) []string {
	var ids []string
	for _, id := range idPattern.FindAllString(out, -1) {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// Grade is one arm's verdict on one task.
type Grade struct {
	Shown   []string // keys of the rows printed (ids unknown to the gate as "?<id>")
	Missing []string // required keys not printed
	Noise   []string // printed keys that do not apply
}

// Recall is the share of required rows printed (1 when none is required).
func (g Grade) Recall(t Task) float64 {
	if len(t.Required) == 0 {
		return 1
	}
	return float64(len(t.Required)-len(g.Missing)) / float64(len(t.Required))
}

// Precision is the share of printed rows that apply (1 when none printed).
func (g Grade) Precision() float64 {
	if len(g.Shown) == 0 {
		return 1
	}
	return float64(len(g.Shown)-len(g.Noise)) / float64(len(g.Shown))
}

// GradeIDs grades the ids an arm printed for t; keyOf maps a row id to
// the gate's key.
func GradeIDs(t Task, ids []string, keyOf map[string]string) Grade {
	ok := map[string]bool{}
	for _, k := range append(t.Applies(), Always()...) {
		ok[k] = true
	}
	var g Grade
	shown := map[string]bool{}
	for _, id := range ids {
		k, known := keyOf[id]
		if !known {
			k = "?" + id
		}
		if shown[k] {
			continue
		}
		shown[k] = true
		g.Shown = append(g.Shown, k)
		if !ok[k] {
			g.Noise = append(g.Noise, k)
		}
	}
	for _, k := range t.Required {
		if !shown[k] {
			g.Missing = append(g.Missing, k)
		}
	}
	return g
}

// ArmResult is one arm over every task.
type ArmResult struct {
	Name   string
	Grades map[string]Grade // by task id
	Chars  map[string]int   // characters printed, by task id
}

// Totals are an arm's sums: required rows shown of required, rows that
// apply of rows shown.
type Totals struct {
	Required, Hit, Shown, Applies int
	Perfect                       int // tasks with every required row shown
}

// Total sums an arm over ts.
func (a ArmResult) Total(ts []Task) Totals {
	var s Totals
	for _, t := range ts {
		g := a.Grades[t.ID]
		s.Required += len(t.Required)
		s.Hit += len(t.Required) - len(g.Missing)
		s.Shown += len(g.Shown)
		s.Applies += len(g.Shown) - len(g.Noise)
		if len(g.Missing) == 0 {
			s.Perfect++
		}
	}
	return s
}

// Recall is Hit/Required (1 with nothing required).
func (s Totals) Recall() float64 {
	if s.Required == 0 {
		return 1
	}
	return float64(s.Hit) / float64(s.Required)
}

// Precision is Applies/Shown (1 with nothing shown).
func (s Totals) Precision() float64 {
	if s.Shown == 0 {
		return 1
	}
	return float64(s.Applies) / float64(s.Shown)
}

// The bars (owner 2026-11-21, accuracy first).
const (
	BarRecall    = 1.0
	BarPrecision = 0.8
)

// Misses are the bars an arm misses, one line each.
func (a ArmResult) Misses(ts []Task) []string {
	s := a.Total(ts)
	var out []string
	if s.Recall() < BarRecall {
		var lost []string
		for _, t := range ts {
			if g := a.Grades[t.ID]; len(g.Missing) > 0 {
				lost = append(lost, t.ID+" "+strings.Join(g.Missing, ","))
			}
		}
		out = append(out, fmt.Sprintf("%s: recall %d/%d of must-rules and critical hotfixes (want all): %s", a.Name, s.Hit, s.Required, strings.Join(lost, "; ")))
	}
	if s.Precision() < BarPrecision {
		out = append(out, fmt.Sprintf("%s: precision %.0f%% (%d of %d rows shown apply; want >= %.0f%%)", a.Name, 100*s.Precision(), s.Applies, s.Shown, 100*BarPrecision))
	}
	return out
}

// Line is the arm's summary: recall and precision.
func (a ArmResult) Line(ts []Task) string {
	s := a.Total(ts)
	return fmt.Sprintf("%s recall %d/%d (%d/%d tasks), precision %d/%d (%.0f%%)", a.Name, s.Hit, s.Required, s.Perfect, len(ts), s.Applies, s.Shown, 100*s.Precision())
}
