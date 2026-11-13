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

// TestReAddAdminAfterPurgeInARepo (coordinator decision, round 4): a team
// started inside a git repo attaches its store to that repo's
// .lore/stores.yaml under the team's name; purging the admin setup frees
// the team but never touches the repo. Starting the same team again on the
// same store in that repo reuses the attach (a no-op) and succeeds. Onto
// another store, the stale entry is refused with the command that drops it.
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
	purge := func() {
		t.Helper()
		for _, args := range [][]string{{"setup", "remove", setup}, {"setup", "trash", "purge", setup, "--confirm"}} {
			if out, err := w.LoreIn(ctx, loreBin, repo, args...); err != nil {
				t.Fatalf("lore %v: %v\n%s", args, err, out)
			}
		}
	}
	if err := w.StartTeam(ctx, loreBin, repo, setup, org, "crew-store"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	stores, err := os.ReadFile(filepath.Join(repo, ".lore", "stores.yaml"))
	if err != nil || !strings.Contains(string(stores), "name: "+org) {
		t.Fatalf("the team store is not attached to the repo it started in: %v\n%s", err, stores)
	}
	purge()
	if err := w.StartTeam(ctx, loreBin, repo, setup, org, "crew-store"); err != nil {
		t.Fatalf("starting the same team on the same store again after purge: %v", err)
	}
	purge()
	err = w.StartTeam(ctx, loreBin, repo, setup, org, "crew-other")
	if err == nil {
		t.Fatal("starting the team onto another store while the repo attaches the old one under its name was not refused")
	}
	for _, want := range []string{".lore/stores.yaml", TeamStoreURL(w.Accounts[0].Login, "crew-store"), "lore store remove --name " + org} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n%v", want, err)
		}
	}
}
