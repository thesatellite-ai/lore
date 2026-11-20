package orgcfg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// TestOrgCommitGivesCloneTheOrgIdentity (devco:D-782): `lore org store
// add` made the added store's clone through this layer (its marker and
// floor commits), and that clone kept the person's global git user (a
// personal email in an org store) while every clone the git backend makes
// gets the org identity. Every commit this layer makes now gives the clone
// the identity it signs with, by the backend's rule: a clone with no
// repo-local user.email gets it (also when the store turns out not to be
// a lore store yet), lore's own earlier value follows a changed identity,
// and a person's own repo-local user.email stays.
func TestOrgCommitGivesCloneTheOrgIdentity(t *testing.T) {
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	gc := filepath.Join(root, "gitconfig")
	writeFile(t, gc, "[user]\n\tname = Ada\n\temail = ada@home.example\n[init]\n\tdefaultBranch = main\n[url \""+filepath.ToSlash(hosts)+"/\"]\n\tinsteadOf = https://github.com/\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(envAutopush, "")
	seedRepo := func(name, file, body string) {
		origin := filepath.Join(hosts, "acme-co", name+".git")
		if err := os.MkdirAll(origin, 0o700); err != nil {
			t.Fatal(err)
		}
		testGit(t, origin, "init", "-q", "--bare", "-b", "main")
		seed := filepath.Join(root, "seed-"+name)
		testGit(t, root, "clone", "-q", "https://github.com/acme-co/"+name, seed)
		writeFile(t, filepath.Join(seed, file), body)
		testGit(t, seed, "add", ".")
		testGit(t, seed, "commit", "-q", "-m", "init")
		testGit(t, seed, "push", "-q", "origin", "main")
	}
	seedRepo("lore-ops", storeMarkerFile, "lore_store: 4\nvisibility: internal\npush: auto\n")
	seedRepo("plain", "README.md", "not a store\n")
	home := filepath.Join(root, "home")
	g := &GitLinks{Home: home, Config: &orgconf.Config{}}
	ctx := context.Background()
	when := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	mark := OrgStoreMark{URL: "https://github.com/acme-co/lore-ops", Org: "acme", MainStore: "https://github.com/acme-co/lore-team",
		Signature: gitref.Signature{Name: "Ada", Email: "ada@acme.example", When: when}}
	clone := func(url string) string {
		norm, err := repourl.NormalizeStore(url)
		if err != nil {
			t.Fatal(err)
		}
		dir, _ := cloneDir(home, norm)
		return dir
	}
	user := func(dir string) string {
		return strings.TrimSpace(testGit(t, dir, "config", "--local", "--get", "user.email")) // testGit passes its own -c user.email, so only the clone's .git/config counts
	}

	if _, err := g.MarkOrgStore(ctx, mark); err != nil {
		t.Fatalf("MarkOrgStore: %v", err)
	}
	if got := user(clone(mark.URL)); got != "ada@acme.example" {
		t.Fatalf("the clone this layer made has git user %q, want the org identity ada@acme.example", got)
	}
	plain := mark
	plain.URL = "https://github.com/acme-co/plain"
	if res, err := g.MarkOrgStore(ctx, plain); err != nil || !res.NotAStore {
		t.Fatalf("a repo with no marker: %+v, %v", res, err)
	}
	if got := user(clone(plain.URL)); got != "ada@acme.example" {
		t.Errorf("a clone of a repo that is no store yet has git user %q, want ada@acme.example", got)
	}

	// lore's own value follows a changed org identity
	moved := mark
	moved.MainStore = "https://github.com/acme-co/lore-main"
	moved.Signature.Email = "ada@acme-new.example"
	if _, err := g.MarkOrgStore(ctx, moved); err != nil {
		t.Fatalf("MarkOrgStore (new identity): %v", err)
	}
	if got := user(clone(mark.URL)); got != "ada@acme-new.example" {
		t.Errorf("after the org identity changed the clone has git user %q, want ada@acme-new.example", got)
	}

	// a person's own repo-local user.email stays
	testGit(t, clone(mark.URL), "config", "--local", "user.email", "ada.own@acme.example")
	if _, err := g.MarkOrgStore(ctx, mark); err != nil {
		t.Fatalf("MarkOrgStore (own identity): %v", err)
	}
	if got := user(clone(mark.URL)); got != "ada.own@acme.example" {
		t.Errorf("a person's own repo-local user.email became %q, want it kept", got)
	}
}
