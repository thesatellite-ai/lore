package trash

import (
	"strings"
	"testing"
)

// TestHistoryLinesUseDeleterName: the --from-history list and show lines
// print the purger's name when the roster gave one (bug hunt W-7), and
// the id otherwise.
func TestHistoryLinesUseDeleterName(t *testing.T) {
	it := Item{ID: "mem_1", Table: "memories", Title: "t", DeletedAt: "2026-11-02 10:02", DeletedBy: "act_1", LastIn: "abc^", byLabel: "Nina Newcomer"}
	list := ListOut{Data: []Item{it}, Count: 1, Total: 1, History: true}.Text()
	show := ShowOut{Data: it, History: true}.Text()
	for _, s := range []string{list, show} {
		if !strings.Contains(s, "by Nina Newcomer") || strings.Contains(s, "by act_1") {
			t.Errorf("history line:\n%s", s)
		}
	}
	it.byLabel = ""
	if s := (ShowOut{Data: it, History: true}).Text(); !strings.Contains(s, "by act_1") {
		t.Errorf("no name: the id prints:\n%s", s)
	}
}
