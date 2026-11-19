package wire

import (
	"context"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
)

// TestResolveRemoteURLAliasD576 pins devco D-576: an ssh host alias url
// (the ssh config renames the alias to github.com) reaches the repo
// identity under the host it stands for, in its own url form, while an
// https url, a plain host and a local path stay as written.
func TestResolveRemoteURLAliasD576(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitbackend.SetAliasHosts(func(host string) (string, bool) {
		if host == "work-gh" {
			return "github.com", true
		}
		return "", false
	})
	t.Cleanup(func() { gitbackend.SetAliasHosts(nil) })
	ctx := context.Background()
	for raw, want := range map[string]string{
		"git@work-gh:clientco/portal.git":       "git@github.com:clientco/portal.git",
		"ssh://git@work-gh/clientco/portal.git": "ssh://git@github.com/clientco/portal.git",
		"git@Work-GH:clientco/portal.git":       "git@github.com:clientco/portal.git",
		"https://work-gh/clientco/portal.git":   "https://work-gh/clientco/portal.git",
		"git@gitlab.example:team/app.git":       "git@gitlab.example:team/app.git",
		"/srv/git/app.git":                      "/srv/git/app.git",
	} {
		if got := resolveRemoteURL(ctx, "", raw); got != want {
			t.Errorf("resolveRemoteURL(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := swapHost("git@work-gh-2:o/work-gh.git", "work-gh", "github.com"); got != "git@work-gh-2:o/work-gh.git" {
		t.Errorf("swapHost replaced a host that only starts with the alias, or the path: %q", got)
	}
}
