package wire

import (
	"strings"
	"testing"
)

// TestHistoryLinesNameFileLineAndRef (devco:D-788): a plain file's
// findings let through as already in history are named by path:line and
// the ref that held them, one line per ref, in commit and push wording;
// rows keep the count line; a commit with no remote ref says HEAD; more
// than maxHistoryPlaces places are cut with "and N more".
// Break-verified: with historyLines counting every hit under the row
// wording (the old historyLine(n)) every file case fails.
func TestHistoryLinesNameFileLineAndRef(t *testing.T) {
	t.Parallel()
	hs := []historyHit{
		{Path: ".lore/data/memories/m.json", Line: 3, Row: true},
		{Path: "cfg/Taskfile.yml", Line: 2, Ref: "origin/main"},
		{Path: "sub/a.yml", Line: 1, Ref: "tag v1.2"},
		{Path: "sub/b.yml", Line: 4, Ref: "tag v1.2"},
	}
	got := historyLines(hs, checkWhereCommit)
	want := []string{
		"lore: 1" + historyNotice,
		"lore: cfg/Taskfile.yml:2 holds a secret-looking value that is already on the remote in the same file (origin/main), so nothing new is sent and it does not block this commit",
		"lore: sub/a.yml:1, sub/b.yml:4 hold secret-looking values that are already on the remote in the same files (tag v1.2), so nothing new is sent and they do not block this commit",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commit lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	push := historyLines(hs[1:2], LeakWherePush)
	if len(push) != 1 || !strings.HasSuffix(push[0], "(origin/main), so nothing new is sent and it does not block this push") {
		t.Fatalf("push line: %q", push)
	}
	head := historyLines([]historyHit{{Path: "notes.md", Line: 1}}, checkWhereCommit)
	if len(head) != 1 || head[0] != "lore: notes.md:1 holds a secret-looking value that was already in the same file at HEAD, not new, so it does not block this commit" {
		t.Fatalf("no-remote line: %q", head)
	}
	var many []historyHit
	for i := 1; i <= maxHistoryPlaces+2; i++ {
		many = append(many, historyHit{Path: "f.yml", Line: i, Ref: "origin/main"})
	}
	if l := historyLines(many, checkWhereCommit); len(l) != 1 || !strings.Contains(l[0], "f.yml:5 and 2 more hold") || strings.Contains(l[0], "f.yml:6") {
		t.Fatalf("cap: %q", l)
	}
	if historyLines(nil, checkWhereCommit) != nil {
		t.Fatal("no hits: want no line")
	}
}
