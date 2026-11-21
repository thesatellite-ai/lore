package memory

import (
	"strings"
	"testing"
	"time"
)

// TestListAndSearchMarkHistory (devco D-820): memory list and memory
// search print a superseded memory or one past its valid_until with a
// [history: …] mark, so an agent reading the list never takes the old
// fact for the current one; a valid_until still ahead is no mark.
func TestListAndSearchMarkHistory(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	list := ListOut{Kind: KindList, now: now, Data: []Record{
		{ID: "mem_a", Kind: "retrieved", Body: "My gym: ironworks gym.", SupersededByID: "mem_b"},
		{ID: "mem_b", Kind: "retrieved", Body: "My gym: peakfit gym."},
		{ID: "mem_c", Kind: "retrieved", Body: "My car insurance: harbourline.", ValidUntil: now.Add(-time.Hour)},
		{ID: "mem_d", Kind: "retrieved", Body: "My dentist: thursdays.", ValidUntil: now.AddDate(0, 1, 0)},
	}}
	text := list.Text()
	for _, want := range []string{
		"mem_a [retrieved] My gym: ironworks gym. [history: superseded by mem_b]",
		"mem_b [retrieved] My gym: peakfit gym.\n",
		"mem_c [retrieved] My car insurance: harbourline. [history: valid until 2026-11-21]",
		"mem_d [retrieved] My dentist: thursdays.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("memory list: missing %q in\n%s", want, text)
		}
	}
	s := SearchOut{Data: []SearchHit{{ID: "mem_b", Row: HitRow{Body: "My gym: peakfit gym."}}, {ID: "mem_a", Row: HitRow{Body: "My gym: ironworks gym."}, history: "history: superseded by mem_b"}}}
	if got := s.Text(); !strings.Contains(got, "mem_a My gym: ironworks gym. [history: superseded by mem_b]\n") || !strings.Contains(got, "mem_b My gym: peakfit gym.\n") {
		t.Errorf("memory search text:\n%s", got)
	}
}
