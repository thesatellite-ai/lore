//go:build stories && !windows

package stories

// O_secret_allow_client_test.go — devco:D-912 (public issue #126) on the
// owner-shaped machine (story O), replaying the issue's script: a client
// repo with no lore data of its own, linked with `lore org link` (commit-msg
// and pre-push blocks), commits a test fixture that looks like a token. The
// refusal printed a `not a secret?` line that failed with F15 ("a client
// row cannot point at an internal row") and wrote nothing, so the next
// commit was refused again and only --no-verify or a .gitleaksignore in the
// client's tree got it through. The printed line now runs as printed: the
// allow is saved in the linked store folder (never in the client repo),
// the refusal's hint names that folder instead of a .lore/data file, the
// commit and the push go through, and another value is still refused.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOSecretAllowInClientRepo(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	const folder = "client_app"
	app := s.Seed(s.Remote("github.com/acme-client/client-app"), func(dir string) { PlainRepo(t, dir) })
	// token-shaped values built at run time, never a literal key
	fixture := "gh" + "p_" + "1234567890abcdefghijklmnopqrstuvwxyz" + "AB"
	other := "gh" + "p_" + "zyxwvutsrqponmlkjihgfedcba0987654321" + "BA"

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	cc := ada.CloneTo(app, ada.OrgDir("client-app"))
	var allow string

	s.Step("D-912/a-fixture-token-is-refused-with-a-ready-line", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(cc.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t)
		cc.WriteFile("fixture.go", "package main\n\nconst fixtureToken = \""+fixture+"\"\n")
		cc.Git("add", "fixture.go")
		r := cc.TryGit("commit", "-q", "-m", "Add a fixture")
		if r.Code == 0 {
			t.Fatalf("a token-shaped value was committed in a client repo: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "not a secret? lore secrets allow --file fixture.go --rule github_token",
			"the store folder this repo is linked to", "no file to add here").
			Never(t, ".lore/data/secret_allows")
		for _, l := range strings.Split(r.Out(), "\n") {
			if _, cmd, ok := strings.Cut(l, "not a secret? lore secrets allow --file fixture.go"); ok {
				allow = "lore secrets allow --file fixture.go" + cmd
			}
		}
		if allow == "" {
			t.Fatalf("the refusal printed no lore secrets allow line:\n%s", r)
		}
	})

	s.Step("D-912/the-printed-line-runs-as-printed", func() {
		args := strings.Fields(strings.TrimPrefix(strings.Replace(allow, `"<why>"`, "test-fixture", 1), "lore "))
		ada.Lore(cc.Dir, append(args, "--yes")...).OK(t).
			Says(t, "fixture.go marked not a secret", "no file to commit here").
			Never(t, "F15", "E_LEAK_BLOCKED")
		if _, err := os.Stat(filepath.Join(cc.Dir, ".lore")); err == nil {
			t.Fatal("the allow wrote a .lore folder into the client repo")
		}
		ada.Lore(cc.Dir, "secrets", "allow", "list").OK(t).Says(t, "fixture.go", "test-fixture")
	})

	s.Step("D-912/the-commit-and-push-go-through", func() {
		if r := cc.TryGit("commit", "-q", "-m", "Add a fixture"); r.Code != 0 {
			t.Fatalf("the commit was refused after the printed allow ran: %s", r)
		}
		if r := cc.TryGit("push", "-q", "origin", "HEAD:main"); r.Code != 0 {
			t.Fatalf("the push was refused after the printed allow ran: %s", r)
		}
	})

	s.Step("D-912/another-value-is-still-refused", func() {
		cc.WriteFile("other.go", "package main\n\nconst otherToken = \""+other+"\"\n")
		cc.Git("add", "other.go")
		if r := cc.TryGit("commit", "-q", "-m", "Add another"); r.Code == 0 {
			t.Fatalf("an allow for one value in one file let another value through: %s", r)
		}
	})
}
