package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// spGit runs git in dir for a test (a fixed identity, no system config).
func spGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestIsOrgAdmin is decision code (who may change an org store's shared
// settings and compact it): only an admin setup OF THAT org counts.
// Break-verified by returning true (TestE47 and this test fail).
func TestIsOrgAdmin(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := "setups:\n  - name: acme-member\n    kind: member\n    org: acme\n  - name: other-admin\n    kind: admin\n    org: globex\n"
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	for org, want := range map[string]bool{"acme": false, "globex": true, "": false, "initech": false} {
		if got := isOrgAdmin(home, org); got != want {
			t.Errorf("isOrgAdmin(%q) = %v, want %v", org, got, want)
		}
	}
	if isOrgAdmin(t.TempDir(), "acme") {
		t.Error("a machine without config.yaml is an admin")
	}
}

// TestOtherClonesUnpushed is decision code (an org compaction is refused
// while another clone of the store on this machine holds unpushed work):
// a branch clone with a local commit its remote lacks is named; the clone
// the compaction runs in is not. Break-verified by returning nil.
func TestOtherClonesUnpushed(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	remote := filepath.Join(t.TempDir(), "team.git")
	spGit(t, filepath.Dir(remote), "init", "-q", "--bare", "-b", "main", remote)
	seed := filepath.Join(t.TempDir(), "seed")
	spGit(t, filepath.Dir(seed), "init", "-q", "-b", "main", seed)
	spGit(t, seed, "commit", "-q", "--allow-empty", "-m", "init")
	spGit(t, seed, "push", "-q", remote, "main")
	base := filepath.Join(home, "stores", "acme", "team")
	mk := func(dir string) string {
		spGit(t, home, "clone", "-q", remote, dir)
		// the store url as origin (the clone found its siblings by it); no
		// network is used: the unpushed check compares local refs only
		spGit(t, dir, "remote", "set-url", "origin", "https://github.com/acme/team")
		return dir
	}
	work := mk(base)
	if got := otherClonesUnpushed(context.Background(), home, work); len(got) != 0 {
		t.Fatalf("clean clones reported %v", got)
	}
	// Unpushed work in the clone itself is not "another clone".
	spGit(t, work, "commit", "-q", "--allow-empty", "-m", "own")
	if got := otherClonesUnpushed(context.Background(), home, work); len(got) != 0 {
		t.Fatalf("the compacting clone itself was reported: %v", got)
	}
	branch := mk(base + branchCloneSep + "staging")
	spGit(t, branch, "commit", "-q", "--allow-empty", "-m", "waits")
	got := otherClonesUnpushed(context.Background(), home, work)
	if len(got) == 0 || !strings.Contains(strings.Join(got, " "), "unpushed commit") {
		t.Fatalf("the branch clone's unpushed commit was not named: %v", got)
	}
	if got := otherClonesUnpushed(context.Background(), home, filepath.Join(t.TempDir(), "elsewhere", "work")); len(got) == 0 {
		t.Fatal("a folder with no store origin was not refused (fail closed)")
	}
}

// TestRefinePlaceOrgFile is decision code (a stores.yaml store gets push
// settings like an org store, decided 2026-11-10, land7 A; but an org's
// store attached by url alone stays that org's, admin only): a worktree
// holding a lore-org.yaml in the marker's org_folder, in "org" or at the
// root is that org's store; one without stays a plain stores.yaml store;
// the personal store and an org store already named are never changed.
// Break-verified by returning place unchanged (the org cases fail, and
// TestE56 lets a member change the org's setting).
func TestRefinePlaceOrgFile(t *testing.T) {
	t.Parallel()
	write := func(t *testing.T, dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name    string
		files   map[string]string
		place   pushPlace
		org     string
		want    pushPlace
		wantOrg string
	}{
		{"plain store", map[string]string{"lore-store.yaml": "lore_store: 1\n"}, placeStore, "", placeStore, ""},
		{"org file in org/", map[string]string{"org/.lore/lore-org.yaml": "name: acme\n"}, placeStore, "", placeOrg, "acme"},
		{"org file at the root", map[string]string{".lore/lore-org.yaml": "name: globex\n"}, placeStore, "", placeOrg, "globex"},
		{"org file in the marker's org_folder", map[string]string{"lore-store.yaml": "lore_store: 1\norg_folder: company\n", "company/.lore/lore-org.yaml": "name: initech\n"}, placeStore, "", placeOrg, "initech"},
		{"org file naming no org", map[string]string{"org/.lore/lore-org.yaml": "stores: []\n"}, placeStore, "", placeStore, ""},
		{"personal store untouched", map[string]string{"org/.lore/lore-org.yaml": "name: acme\n"}, placePersonal, "", placePersonal, ""},
		{"org store keeps its org", map[string]string{"org/.lore/lore-org.yaml": "name: other\n"}, placeOrg, "acme", placeOrg, "acme"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for rel, body := range c.files {
			write(t, dir, rel, body)
		}
		if got, org := refinePlace(dir, c.place, c.org); got != c.want || org != c.wantOrg {
			t.Errorf("%s: refinePlace = %v %q, want %v %q", c.name, got, org, c.want, c.wantOrg)
		}
	}
}
