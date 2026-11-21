package recall

// accuracy.go — the accuracy bar of the recall gate (owner 2026-11-21:
// retrieval accuracy comes first, before speed). A question's answer is
// graded from `lore ask --json`: the structured answer is read as one
// ranked list in the reading order lore gives in its ranked list (the
// order it prints; an older lore without one: resources, then tasks, then
// rows) and the bar looks at the top of that list, the part an agent
// reads first. Why a ranked list and not the whole output: an answer that
// holds the fact somewhere below ten feed rows makes an agent read the
// noise or stop early; the text grade (Grade) cannot see that.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The accuracy bars (owner 2026-11-21), checked before the speed bars.
const (
	BarTopK     = 3  // recall@3: the answer within the first 3 items
	BarTop1Pct  = 90 // top-1 right on at least this share of all questions
	BarNoiseTop = 1  // at most this many irrelevant items in the top 3
	// the recall bar is 100% and the stale bar 0: every question.
)

// Item is one answer item of `lore ask --json` in the order lore gives it.
type Item struct {
	Kind string // resource, task or row
	ID   string
	// Text is everything the item says, its history (a field's former
	// value) included: what recall reads.
	Text string
	// Answer is what the item presents as the answer, its history left
	// out: what the stale check reads.
	Answer string
	// History marks a row lore says is no longer the current answer
	// (superseded, past valid_until, replaced by a newer row): it presents
	// nothing as the answer, so it is never stale and never top-1 right.
	History bool
}

// askJSON mirrors the parts of ask.run's JSON output the grade reads (the
// bench reads the binary's output, so it keeps its own copy of the shape).
type askJSON struct {
	Resources []struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Fields []struct {
			Name      string `json:"name"`
			Value     string `json:"value"`
			Was       string `json:"was"`
			ChangedAt string `json:"changed_at"`
		} `json:"fields"`
		Linked []struct {
			Name   string   `json:"name"`
			Via    string   `json:"via"`
			Fields []string `json:"fields"`
		} `json:"linked"`
	} `json:"resources"`
	Tasks []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
		OwedBy string `json:"owed_by"`
		OwedTo string `json:"owed_to"`
	} `json:"tasks"`
	Rows []struct {
		ID      string `json:"id"`
		At      string `json:"at"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		Note    string `json:"note"`
		History string `json:"history"`
	} `json:"rows"`
	// Ranked is the reading order (kind resource, task or row + id).
	Ranked []struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"ranked"`
}

// Items reads `lore ask --json` output as the ranked answer list.
func Items(out []byte) ([]Item, error) {
	var a askJSON
	if err := json.Unmarshal(out, &a); err != nil {
		return nil, fmt.Errorf("read lore ask --json: %w", err)
	}
	var items []Item
	for _, r := range a.Resources {
		var ans, hist []string
		ans = append(ans, r.Kind+" "+r.Name)
		for _, f := range r.Fields {
			ans = append(ans, f.Name+": "+f.Value)
			if f.Was != "" {
				hist = append(hist, fmt.Sprintf("%s was %s (changed %s)", f.Name, f.Was, f.ChangedAt))
			}
		}
		for _, l := range r.Linked {
			ans = append(ans, l.Name+" ("+l.Via+") "+strings.Join(l.Fields, " "))
		}
		answer := strings.Join(ans, "\n")
		items = append(items, Item{Kind: "resource", ID: r.ID, Answer: answer, Text: answer + "\n" + strings.Join(hist, "\n")})
	}
	for _, t := range a.Tasks {
		s := fmt.Sprintf("%s [%s] owed by %s to %s", t.Title, t.Status, t.OwedBy, t.OwedTo)
		items = append(items, Item{Kind: "task", ID: t.ID, Answer: s, Text: s})
	}
	for _, r := range a.Rows {
		s := strings.TrimSpace(r.At + " " + r.Title + "\n" + r.Body + "\n" + r.Note)
		if r.History != "" { // what used to be true: history, not the answer
			items = append(items, Item{Kind: "row", ID: r.ID, Text: s + "\n" + r.History, History: true})
			continue
		}
		items = append(items, Item{Kind: "row", ID: r.ID, Answer: s, Text: s})
	}
	if len(a.Ranked) == 0 {
		return items, nil
	}
	byRef := map[[2]string]Item{}
	for _, it := range items {
		byRef[[2]string{it.Kind, it.ID}] = it
	}
	ranked := make([]Item, 0, len(items))
	for _, r := range a.Ranked {
		if it, ok := byRef[[2]string{r.Kind, r.ID}]; ok {
			ranked = append(ranked, it)
			delete(byRef, [2]string{r.Kind, r.ID})
		}
	}
	for _, it := range items { // an item the ranked list misses still counts, last
		if _, ok := byRef[[2]string{it.Kind, it.ID}]; ok {
			ranked = append(ranked, it)
		}
	}
	return ranked, nil
}

// Accuracy is one question's grade on the accuracy bar.
type Accuracy struct {
	Recall bool // every required fact within the top K items
	Top1   bool // the first item answers it (holds a required fact, not stale)
	Stale  []string
	Noise  int // irrelevant items in the top 3
	// Missing names what failed, for the report.
	Missing []string
}

// TopK is how many items recall reads for q: 3, or the number of items
// the answer needs when that is more (a week's seven journal days, every
// task owed to someone), since one item holds one of those.
func (q Question) TopK() int { return max(BarTopK, q.Need) }

// GradeAccuracy grades the ranked answer items of q. A required fact
// counts when the top K items' text holds it; a stale fact is a forbidden
// one an item in the top K presents as its answer (history such as a
// field's former value does not count) without the current value next to
// it; an item is irrelevant when it holds no required fact and does not
// name the question's subject.
func GradeAccuracy(q Question, items []Item) Accuracy {
	var a Accuracy
	low := strings.ToLower
	has := func(s, fact string) bool { return strings.Contains(low(s), low(fact)) }
	top := items[:min(len(items), q.TopK())]
	var all strings.Builder
	for _, it := range top {
		all.WriteString(it.Text + "\n")
	}
	a.Recall = true
	for _, r := range q.Required {
		if !has(all.String(), r) {
			a.Recall = false
			a.Missing = append(a.Missing, fmt.Sprintf("not in top %d: %s", q.TopK(), r))
		}
	}
	staleIn := func(it Item) []string {
		var out []string
		for _, f := range q.Forbidden {
			if has(it.Answer, f) && (q.Current == "" || !has(it.Answer, q.Current)) {
				out = append(out, f)
			}
		}
		return out
	}
	for _, it := range top {
		for _, f := range staleIn(it) {
			a.Stale = append(a.Stale, f)
			a.Missing = append(a.Missing, fmt.Sprintf("stale %s %s: %s", it.Kind, it.ID, f))
		}
	}
	relevant := func(it Item) bool {
		for _, f := range append(append([]string{}, q.Required...), q.Subject...) {
			if has(it.Text, f) {
				return true
			}
		}
		return false
	}
	if len(items) > 0 {
		first := items[0]
		a.Top1 = len(staleIn(first)) == 0 && !first.History
		hit := false
		for _, r := range q.Required {
			if has(first.Text, r) {
				hit = true
			}
		}
		a.Top1 = a.Top1 && hit
	}
	if !a.Top1 {
		a.Missing = append(a.Missing, "top-1 wrong")
	}
	for _, it := range items[:min(len(items), BarTopK)] {
		if !relevant(it) {
			a.Noise++
		}
	}
	if a.Noise > BarNoiseTop {
		a.Missing = append(a.Missing, fmt.Sprintf("%d irrelevant in top %d", a.Noise, BarTopK))
	}
	return a
}

// GrepAccuracy grades the grep arm the way it applies to a pile of text:
// present (every required fact in the output) and stale (a forbidden fact
// in it without the current value).
func GrepAccuracy(q Question, out string) (present bool, stale []string) {
	low := strings.ToLower(out)
	has := func(s string) bool { return strings.Contains(low, strings.ToLower(s)) }
	present = true
	for _, r := range q.Required {
		if !has(r) {
			present = false
		}
	}
	for _, f := range q.Forbidden {
		if has(f) && (q.Current == "" || !has(q.Current)) {
			stale = append(stale, f)
		}
	}
	return present, stale
}
