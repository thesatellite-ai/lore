package main

import (
	"sort"
	"testing"
)

// TestJoinOrUnchanged pins `lore sync install-git` output for empty lists.
func TestJoinOrUnchanged(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, unchangedWord},
		{[]string{}, unchangedWord},
		{[]string{"pre-commit"}, "pre-commit"},
		{[]string{"commit-msg", "pre-commit"}, "commit-msg, pre-commit"},
	} {
		if got := joinOrUnchanged(tc.in); got != tc.want {
			t.Errorf("joinOrUnchanged(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHookLinesNamesAreSortable pins the hook set install-git walks in
// sorted order (the printed and JSON list must be stable run to run).
func TestHookLinesNamesAreSortable(t *testing.T) {
	t.Parallel()
	hooks := hookLines(".")
	names := make([]string, 0, len(hooks))
	for h := range hooks {
		names = append(names, h)
	}
	sort.Strings(names)
	for _, want := range []string{"commit-msg", "post-checkout", "post-commit", "post-merge", "post-rewrite", "pre-commit", "pre-push"} {
		i := sort.SearchStrings(names, want)
		if i >= len(names) || names[i] != want {
			t.Errorf("hookLines lacks %s: %v", want, names)
		}
	}
}
