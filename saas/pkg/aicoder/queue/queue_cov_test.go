package queue

import (
	"strings"
	"testing"
)

func TestPriorityRank(t *testing.T) {
	cases := map[string]int{
		"low": PriorityLow, "medium": PriorityMedium, "high": PriorityHigh, "urgent": PriorityUrgent,
		"": PriorityMedium, "LOW": PriorityMedium, " high": PriorityMedium, "critical": PriorityMedium, "日本": PriorityMedium,
	}
	for in, want := range cases {
		if got := PriorityRank(in); got != want {
			t.Errorf("PriorityRank(%q) = %d, want %d", in, got, want)
		}
	}
	if !(PriorityRank("low") < PriorityRank("medium") && PriorityRank("medium") < PriorityRank("high") && PriorityRank("high") < PriorityRank("urgent")) {
		t.Fatal("ranks are ordered low < medium < high < urgent")
	}
}

// A task waiting on a done-but-unmerged commit names the commit by its
// short id (git's 7-character abbreviation), never the full id.
func TestWaitReasonShortCommit(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name     string
		commit   string
		want     string
		mustMiss string
	}{
		{"full sha", full, "0123456 not on the default branch", full},
		{"already short", "abc", "abc not on the default branch", ""},
		{"exactly seven", "abcdefg", "abcdefg not on the default branch", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := task("tsk_up", status(StatusDone), func(t *Task) { t.DoneCommit = tc.commit })
			down := task("tsk_down", after("tsk_up", UntilMerged))
			res := Next(Input{Tasks: []Task{up, down}, Now: now})
			if len(res.Waiting) != 1 {
				t.Fatalf("waiting: %+v", res.Waiting)
			}
			r := res.Waiting[0].Reason
			if !strings.Contains(r, tc.want) || (tc.mustMiss != "" && strings.Contains(r, tc.mustMiss)) {
				t.Fatalf("reason %q, want %q", r, tc.want)
			}
		})
	}
	if shortSHA("") != "" {
		t.Fatal("empty stays empty")
	}
}
