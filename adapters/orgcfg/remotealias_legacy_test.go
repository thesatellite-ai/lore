package orgcfg

import (
	"context"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestRemoteAliasLegacyKeysD532 (devco D-532): every place that keys a
// code repo by its remote resolves the ssh host alias (ResolveRemote), and
// the plain alias spelling lore keyed by before stays findable: a repo
// cloned through an alias and linked by its remote alone before D-531 holds
// a link row whose project id came from <alias>/<owner>/<repo>; Gather
// still finds that row and names the spelling it is keyed by.
func TestRemoteAliasLegacyKeysD532(t *testing.T) {
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

	t.Run("ResolveRemote and PlainRemote", func(t *testing.T) {
		for _, raw := range []string{"git@work-gh:clientco/portal.git", "ssh://git@work-gh/clientco/portal.git"} {
			n, err := ResolveRemote(ctx, "", raw)
			if err != nil || n != "github.com/clientco/portal" {
				t.Fatalf("ResolveRemote(%q) = %q, %v; want github.com/clientco/portal", raw, n, err)
			}
			if p := PlainRemote(raw, n); p != "work-gh/clientco/portal" {
				t.Fatalf("PlainRemote(%q) = %q, want the alias spelling", raw, p)
			}
		}
		if p := PlainRemote("git@github.com:clientco/portal.git", "github.com/clientco/portal"); p != "" {
			t.Fatalf("PlainRemote of a github.com url = %q, want none", p)
		}
		dir := gitRepo(t, "git@corp-gh:clientco/portal.git", "")
		if _, err := gitRun(ctx, "-C", dir, "config", "url.git@corp-gh:clientco/.insteadOf", "git@github.com:clientco/"); err != nil {
			t.Fatal(err)
		}
		if n, err := ResolveRemote(ctx, dir, "git@corp-gh:clientco/portal.git"); err != nil || n != "github.com/clientco/portal" {
			t.Fatalf("ResolveRemote with an insteadOf rule = %q, %v", n, err)
		}
	})

	t.Run("link row keyed by the alias spelling", func(t *testing.T) {
		home := t.TempDir()
		writeFile(t, ConfigPath(home), "orgs:\n  - name: acme\n    identity: {key: human:a@acme.example}\n    stores:\n      - url: https://github.com/acme/notes\n        org_folder: org\n      - url: https://github.com/acme/notes-two\n")
		const legacy = "work-gh/clientco/portal"
		pid, err := orgresolve.RemoteProjectID(legacy)
		if err != nil {
			t.Fatal(err)
		}
		src := &remoteLinks{pid: pid, remote: legacy, folder: "portal"}
		s, err := Gather(ctx, In{Home: home, Dir: gitRepo(t, "git@work-gh:clientco/portal.git", ""), Links: src})
		if err != nil || s.ResolveErr != nil {
			t.Fatalf("Gather: %v %v", err, s.ResolveErr)
		}
		if s.ProjectID != pid || s.LinkedByRemote != legacy {
			t.Fatalf("project %q by remote %q, want %q by %q (the row made before D-531)", s.ProjectID, s.LinkedByRemote, pid, legacy)
		}
		if len(s.Remotes) != 1 || s.Remotes[0] != "github.com/clientco/portal" {
			t.Fatalf("Remotes = %v, want the resolved spelling", s.Remotes)
		}
		if s.Result.OrgName() != "acme" {
			t.Fatalf("org %q, want acme (the old link row)", s.Result.OrgName())
		}
	})
}
