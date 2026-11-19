//go:build setupe2e

package setupe2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStartATeamOutsideARepo: Start a team runs from HOME, a folder that is
// no git repo (`lore setup` in ~). The team step made the store and then
// read the repo's identity and attached the store to .lore/stores.yaml,
// both E_NOT_A_STORE outside a repo ("git: the folder is not inside a git
// work tree"). Now the team, its org entry and an invite line are made,
// and nothing is written into the start folder.
func TestStartATeamOutsideARepo(t *testing.T) {
	t.Parallel()
	for _, start := range []string{StartPlain, StartGitRepo} {
		t.Run(start, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := Shape{GHAccounts: 1, GHActive: ActiveFirst, SSH: SSHSeveral, Keys: KeysWithPub, LoreHome: HomeEmpty, Start: start}
			w, err := Build(ctx, filepath.Join(t.TempDir(), "w"), s, loreBin)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Start(ctx, 0, loreBin); err != nil {
				t.Fatal(err)
			}
			defer w.Stop()
			line, err := w.Invite(ctx, loreBin, "member@example.com")
			if err != nil {
				t.Fatalf("start a team from HOME (%s start folder): %v", start, err)
			}
			if !strings.HasPrefix(line, "lore-invite:") {
				t.Fatalf("invite line = %q", line)
			}
			cfg, err := os.ReadFile(filepath.Join(w.Home, ".lore", "config.yaml"))
			if err != nil || !strings.Contains(string(cfg), "name: "+InviteOrg) {
				t.Fatalf("config.yaml has no %s org: %v\n%s", InviteOrg, err, cfg)
			}
			if _, err := os.Stat(filepath.Join(w.Home, ".lore", "stores.yaml")); err == nil {
				t.Errorf("HOME got a .lore/stores.yaml (HOME is no repo)")
			}
		})
	}
}

// TestReAddAdminAfterPurgeInARepo (coordinator decision, round 4; D-63):
// a team started inside a git repo makes its store for this machine: the
// org entry in ~/.lore/config.yaml points at it and the repo's
// .lore/stores.yaml gets nothing (docs/team/guided-setup.md; before D-63
// the step attached the internal team-store url to that repo). Purging
// the admin setup frees the team, so starting the same team again on the
// same store succeeds, and so does starting it onto another store: the
// org entry then names the new store and the repo is still untouched.
func TestReAddAdminAfterPurgeInARepo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := Shape{GHAccounts: 1, GHActive: ActiveFirst, SSH: SSHSeveral, Keys: KeysWithPub, LoreHome: HomeEmpty, Start: StartGitRepo}
	w, err := Build(ctx, filepath.Join(t.TempDir(), "w"), s, loreBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx, 0, loreBin); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	repo := w.StartDir()
	const setup, org = "teamadmin", "crew"
	login := w.Accounts[0].Login
	purge := func() {
		t.Helper()
		for _, args := range [][]string{{"setup", "remove", setup}, {"setup", "trash", "purge", setup, "--confirm"}} {
			if out, err := w.LoreIn(ctx, loreBin, repo, args...); err != nil {
				t.Fatalf("lore %v: %v\n%s", args, err, out)
			}
		}
	}
	// started checks what a person sees after a start onto store: the org
	// entry names it (and no other team store), the repo attaches nothing.
	started := func(store, other string) {
		t.Helper()
		cfg, err := os.ReadFile(filepath.Join(w.Home, ".lore", "config.yaml"))
		if err != nil || !strings.Contains(string(cfg), "name: "+org) || !strings.Contains(string(cfg), TeamStoreURL(login, store)) {
			t.Fatalf("config.yaml has no %s org on %s: %v\n%s", org, TeamStoreURL(login, store), err, cfg)
		}
		if other != "" && strings.Contains(string(cfg), TeamStoreURL(login, other)) {
			t.Fatalf("config.yaml still names the purged team's store %s:\n%s", TeamStoreURL(login, other), cfg)
		}
		stores, err := os.ReadFile(filepath.Join(repo, ".lore", "stores.yaml"))
		if err == nil && (strings.Contains(string(stores), "name: "+org) || strings.Contains(string(stores), login+"/crew-")) {
			t.Fatalf("the team step attached its store to the repo it started in (D-63: it is the machine's store):\n%s", stores)
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := w.StartTeam(ctx, loreBin, repo, setup, org, "crew-store"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	started("crew-store", "")
	purge()
	if err := w.StartTeam(ctx, loreBin, repo, setup, org, "crew-store"); err != nil {
		t.Fatalf("starting the same team on the same store again after purge: %v", err)
	}
	started("crew-store", "")
	purge()
	if err := w.StartTeam(ctx, loreBin, repo, setup, org, "crew-other"); err != nil {
		t.Fatalf("starting the team onto another store after purge (the repo attaches nothing): %v", err)
	}
	started("crew-other", "crew-store")
}
