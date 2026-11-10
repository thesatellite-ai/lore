//go:build userscen

package userscen

// Odd task titles end to end: unicode (accents, CJK, emoji), shell-ish
// characters, quotes, a title that looks like a flag (after --), several
// words joined, a very long title, a title with a newline; each one is
// stored exactly, shown in --json, listed, found by search and edited
// back. Empty and blank titles are refused with the next step.

import (
	"strings"
	"testing"
)

// TestTasksTitles_OddTitlesRoundTrip adds each title, reads it back from
// show --json and list --json, finds it by a word, renames it and back.
func TestTasksTitles_OddTitlesRoundTrip(t *testing.T) {
	s := newTasksSolo(t)
	long := strings.Repeat("long title words ", 40) // 680 characters
	cases := []struct {
		name   string
		args   []string // the title argument(s) as the person types them
		want   string
		search string
	}{
		{"accents", []string{"Café crème brûlée"}, "Café crème brûlée", "creme"},
		{"cjk and emoji", []string{"日本語のタスク 🚀"}, "日本語のタスク 🚀", "日本語*"},
		{"shell characters", []string{`fix $(rm -rf) && echo "hi" | grep 'x'; ls *`}, `fix $(rm -rf) && echo "hi" | grep 'x'; ls *`, "echo"},
		{"looks like a flag", []string{"--", "--json"}, "--json", ""},
		{"words joined", []string{"three", "separate", "words"}, "three separate words", "separate"},
		{"very long", []string{long}, strings.TrimSpace(long), "words"},
		{"newline", []string{"first line\nsecond line"}, "first line\nsecond line", "second"},
		{"reserved words", []string{"select * from tasks where 1=1"}, "select * from tasks where 1=1", "select"},
	}
	for _, tc := range cases {
		args := append([]string{"task", "add", "--json"}, tc.args...)
		id := tasksID(t, s.c.MustLore(args...))
		got := tasksShow(t, s.c, id)
		if got.Title != tc.want {
			t.Fatalf("%s: stored title %q, want %q", tc.name, got.Title, tc.want)
		}
		if !tasksHas(tasksList(t, s.c), id) {
			t.Fatalf("%s: listed", tc.name)
		}
		if tc.search != "" {
			s.c.MustLore("task", "search", tc.search).Says(t, id)
		}
		s.c.MustLore("task", "edit", id, "--title", "renamed "+tc.name)
		if got := tasksShow(t, s.c, id).Title; got != "renamed "+tc.name {
			t.Fatalf("%s: renamed: %q", tc.name, got)
		}
		s.c.MustLore("task", "edit", id, "--title", tc.want)
		if got := tasksShow(t, s.c, id).Title; got != tc.want {
			t.Fatalf("%s: renamed back: %q", tc.name, got)
		}
	}
	for _, blank := range []string{"", "   ", "\t"} {
		e := tasksJSONErr(t, s.c, "task", "add", blank)
		if e.Error.Code != "E_INVALID_INPUT" || !strings.Contains(e.Error.Message, "the title is empty") || !strings.Contains(e.Error.Hint, "lore task add") {
			t.Fatalf("a blank title %q is refused with the next step: %+v", blank, e)
		}
	}
	id := tasksAdd(t, s.c, "keep my title")
	s.c.Lore("task", "edit", id, "--title", " ").Refused(t)
	if got := tasksShow(t, s.c, id).Title; got != "keep my title" {
		t.Fatalf("a refused blank rename keeps the title: %q", got)
	}
}
