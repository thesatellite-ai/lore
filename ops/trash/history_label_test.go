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

// TestHistoryLinesOfARemovedRow (H-14): a row a commit removed (a git rm)
// reads "removed … by <author>", never "purged … by ?", prints the git
// command that brings it back, and the summary never says it never comes
// back.
func TestHistoryLinesOfARemovedRow(t *testing.T) {
	t.Parallel()
	h := HistoryRow{Table: "decisions", ID: "dec_1", Commit: "1291af6deadbeef", Removed: true, RemovedBy: "Omar Haddad",
		GitRestore: "git checkout 1291af6^ -- :/.lore/data/decisions/dec_1.json", Doc: []byte(`{"_table":"decisions","id":"dec_01a1d7fbb3657caeace7f01670d7e598","title":"Pin Go"}`)}
	it, _, _ := historyItem(h)
	list := ListOut{Data: []Item{it}, Count: 1, Total: 1, History: true}.Text()
	show := ShowOut{Data: it, History: true}.Text()
	for _, s := range []string{list, show} {
		if !strings.Contains(s, "removed ") || !strings.Contains(s, "by Omar Haddad") || strings.Contains(s, "by ?") || strings.Contains(strings.ReplaceAll(s, "(not purged)", ""), "purged") ||
			!strings.Contains(s, "bring it back: "+h.GitRestore) {
			t.Errorf("removed row:\n%s", s)
		}
	}
	if strings.Contains(list, "never come back") {
		t.Errorf("a removed row is said never to come back:\n%s", list)
	}
}
