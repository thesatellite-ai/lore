package orgcfg

// B11 port of saas/cmd/cli org_remotes_test.go TestGitRemotesIgnoreInsteadOf:
// remotes are read as written in git config, never through url.insteadOf
// (F10 edge cases, Q2), so a mirror rewrite cannot change the org match.

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/thesatellite-ai/lore/logic/repourl"
)

func TestRemotesIgnoreInsteadOf(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := gitRepo(t, "https://github.com/acme/app", "")
	ctx := context.Background()
	for _, args := range [][]string{
		{"config", "remote.origin.pushurl", "git@github.com:acme/app-push.git"},
		{"remote", "add", "up.stream", "https://gitlab.com/acme/upstream.git"}, // a dotted remote name
		// Every hosted URL is rewritten to a local path, as a mirror setup does.
		{"config", "url./mirror/github/.insteadOf", "https://github.com/"},
		{"config", "--add", "url./mirror/gh-ssh/.insteadOf", "git@github.com:"},
		{"config", "url./mirror/gitlab/.insteadOf", "https://gitlab.com/"},
	} {
		if _, err := gitRun(ctx, append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	var want []string
	for _, u := range []string{"https://github.com/acme/app", "git@github.com:acme/app-push.git", "https://gitlab.com/acme/upstream.git"} {
		n, err := repourl.Normalize(u)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, n)
	}
	sort.Strings(want)
	got, notes := Remotes(ctx, dir)
	if !reflect.DeepEqual(got, want) || len(notes) != 0 {
		t.Fatalf("Remotes = %v (notes %v), want %v and no notes", got, notes, want)
	}
}
