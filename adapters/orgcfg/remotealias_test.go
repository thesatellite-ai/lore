package orgcfg

import (
	"context"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestRemotesResolveSSHAlias is devco D-531: a code repo cloned as
// git@<alias>:<owner>/<repo>.git, where the ssh config renames the alias
// to github.com or an insteadOf rule maps the alias spelling back to a
// github.com url, is github.com/<owner>/<repo> for Remotes, so the org's
// `repos: github.com/<owner>/*` matches (whoami showed <alias>/<owner>/<repo>,
// org none, the fallback identity, and offered the alias as an own: pattern).
// An https url and a dotted host the config does not rename stay as written.
func TestRemotesResolveSSHAlias(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	gitbackend.SetAliasHosts(func(host string) (string, bool) {
		if host == "work-gh" {
			return "github.com", true
		}
		return "", false
	})
	t.Cleanup(func() { gitbackend.SetAliasHosts(nil) })

	t.Run("ssh config alias", func(t *testing.T) {
		dir := gitRepo(t, "git@work-gh:clientco/portal.git", "")
		if norm, _ := Remotes(ctx, dir); len(norm) != 1 || norm[0] != "github.com/clientco/portal" {
			t.Fatalf("Remotes = %v, want [github.com/clientco/portal]", norm)
		}
	})
	t.Run("ssh:// alias", func(t *testing.T) {
		dir := gitRepo(t, "ssh://git@work-gh/clientco/portal.git", "")
		if norm, _ := Remotes(ctx, dir); len(norm) != 1 || norm[0] != "github.com/clientco/portal" {
			t.Fatalf("Remotes = %v, want [github.com/clientco/portal]", norm)
		}
	})
	t.Run("insteadOf alias the ssh config does not name", func(t *testing.T) {
		dir := gitRepo(t, "git@corp-gh:clientco/portal.git", "")
		if _, err := gitRun(ctx, "-C", dir, "config", "url.git@corp-gh:clientco/.insteadOf", "git@github.com:clientco/"); err != nil {
			t.Fatal(err)
		}
		if norm, _ := Remotes(ctx, dir); len(norm) != 1 || norm[0] != "github.com/clientco/portal" {
			t.Fatalf("Remotes = %v, want [github.com/clientco/portal]", norm)
		}
	})
	t.Run("plain hosts unchanged", func(t *testing.T) {
		dir := gitRepo(t, "git@gitlab.example:team/app.git", "")
		if _, err := gitRun(ctx, "-C", dir, "remote", "add", "web", "https://work-gh/team/app"); err != nil {
			t.Fatal(err)
		}
		norm, _ := Remotes(ctx, dir)
		if len(norm) != 2 || norm[0] != "gitlab.example/team/app" || norm[1] != "work-gh/team/app" {
			t.Fatalf("Remotes = %v, want the hosts as written", norm)
		}
	})
	t.Run("org repos pattern matches the alias clone", func(t *testing.T) {
		home := t.TempDir()
		writeFile(t, ConfigPath(home), "orgs:\n  - name: acme\n    identity: {key: human:dev@acme.example}\n    own: [\"github.com/acme/*\"]\n    repos: [\"github.com/clientco/*\"]\n")
		dir := gitRepo(t, "git@work-gh:clientco/portal.git", "prj_1")
		s, err := Gather(ctx, In{Home: home, Dir: dir})
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		if s.Result.OrgName() != "acme" || s.Result.Role != orgresolve.RoleClient {
			t.Fatalf("org=%q role=%q, want acme client", s.Result.OrgName(), s.Result.Role)
		}
	})
}
