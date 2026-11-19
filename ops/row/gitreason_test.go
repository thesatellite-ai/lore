package row

import (
	"strings"
	"testing"
)

// TestGitStateSaysWhy (D-456): every state but branch is said in words;
// "none" tells a create with no code repo open (no repo id) from one in a
// lore place with no git.
func TestGitStateSaysWhy(t *testing.T) {
	cases := []struct {
		g    Git
		repo string
		want string
	}{
		{Git{State: gitStateNone}, "", "written from a store folder or a folder outside any code repo"},
		{Git{State: gitStateNone}, "rep_01a1a5aca7f57659be7320c5f4b70011", "not a git repository"},
		{Git{State: gitStateUnborn}, "", "no commit yet"},
		{Git{State: gitStateDetached}, "", "not a branch"},
		{Git{State: "branch", Branch: "main"}, "", ""},
	}
	for _, c := range cases {
		got := gitReason(c.g, c.repo)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("gitReason(%s, %q) = %q, want %q", c.g.State, c.repo, got, c.want)
		}
	}
	text := Origin{Origin: OriginBlock{Recorded: true, Git: Git{State: gitStateNone, Reason: gitReason(Git{State: gitStateNone}, "")}}}.text()
	if !strings.Contains(text, "git        none: written from a store folder") {
		t.Errorf("text:\n%s", text)
	}
}
