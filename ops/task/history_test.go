package task

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

func entriesN(n int) []HistoryEntry {
	out := make([]HistoryEntry, n)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range out {
		f := "status"
		if i%3 == 0 {
			f = "priority"
		}
		out[i] = HistoryEntry{ID: "the_" + strconv.Itoa(1000+i), At: base.Add(time.Duration(i) * time.Minute).Format(histTimeLayout), Field: f}
	}
	return out
}

// TestFoldHistory: cap at max (200 and the 1000 ceiling), one summary with
// counts, re-fold sums the old summary, and two folds of the same entries
// agree (deterministic summary id).
func TestFoldHistory(t *testing.T) {
	for _, maxN := range []int{defaultHistoryMax, ceilingHistoryMax} {
		in := entriesN(maxN + 10)
		got, err := foldHistory("tsk_x", in, maxN)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != maxN || got[0].Field != histSummaryField {
			t.Fatalf("max %d: len %d first %q", maxN, len(got), got[0].Field)
		}
		total := 0
		for _, c := range got[0].Counts {
			total += c
		}
		if total != 11 {
			t.Fatalf("max %d: folded %d entries, want 11", maxN, total)
		}
		again, _ := foldHistory("tsk_x", entriesN(maxN+10), maxN)
		if again[0].ID != got[0].ID {
			t.Fatal("summary id is not deterministic")
		}
		// re-fold: 5 more entries fold the old summary into a new one.
		more := slices.Concat(got, entriesN(maxN + 15)[maxN+10:])
		re, err := foldHistory("tsk_x", more, maxN)
		if err != nil {
			t.Fatal(err)
		}
		total = 0
		for _, c := range re[0].Counts {
			total += c
		}
		if len(re) != maxN || total != 16 {
			t.Fatalf("re-fold: len %d total %d", len(re), total)
		}
	}
	short := entriesN(3)
	if got, _ := foldHistory("tsk_x", short, 200); len(got) != 3 {
		t.Fatal("short list changed")
	}
}

// TestDiffHistory: only tracked fields that change are recorded; null is
// none; the note lands on the status entry.
func TestDiffHistory(t *testing.T) {
	cur := row.Row{Fields: map[string]value.Value{"status": txt("todo"), "priority": txt("high")}}
	patch := map[string]value.Value{
		"status": txt("done"), "priority": txt("high"), "title": txt("x"), "due_at": value.Null{},
	}
	got, err := diffHistory(cur, patch, "act_a", time.Now(), "open_items_at_close: 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Field != "status" || got[0].From != "todo" || got[0].To != "done" || got[0].Note != "open_items_at_close: 2" {
		t.Fatalf("got %+v", got)
	}
}

func txt(s string) value.Value {
	v, err := value.TextOf(s)
	if err != nil {
		panic(err)
	}
	return v
}
