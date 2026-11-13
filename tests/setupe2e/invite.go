//go:build setupe2e

package setupe2e

// invite.go — the invite fixture: a team on the world's own machine and an
// invite line for a member, so the page's Join (member) lifecycle has a
// real invite to paste.
//
//	Invite(ctx, lore, email)
//	  ├─ bare repo remote/<login>/lore-team.git (the fake git serves it as git@github.com:<login>/lore-team.git)
//	  ├─ lore setup apply <InviteHub> --step <each admin step> --answers <the plan's values>
//	  │    (team: org InviteOrg, the store above; folders: ~/hubwork/** so the
//	  │     member's own folders never overlap; not the default)
//	  └─ lore team invite <email> --setup <InviteHub> ─▶ the "lore setup join <line>" it prints
//
// It runs after Start (the fake GitHub API is up) with the server's env,
// from HOME, outside any repo: starting a team needs none (the org entry
// in config.yaml points at the team store), and the start repo stays as
// the shape made it.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

// Invite fixture names.
const (
	// InviteHub is the admin setup the invite comes from.
	InviteHub = "hubadmin"
	// InviteOrg is its team (the org a member who joins gets a setup named after).
	InviteOrg = "hub"
	// inviteRepo is the team store's GitHub repo name.
	inviteRepo = "lore-team"
	// hubFolders is the hub setup's folder glob: nowhere the member's start folder is.
	hubFolders = "~/hubwork/**"
	// inviteTimeout bounds the whole fixture (each lore call probes the fakes).
	inviteTimeout = 3 * time.Minute
)

// reInviteLine is the invite token `lore team invite` prints after
// "lore setup join --invite" (the named form every printed command uses
// since U-4; the 0.1.17 positional form "lore setup join <token>" is still
// matched so an older lore under test works too).
var reInviteLine = regexp.MustCompile(`lore setup join (?:--invite )?(lore-invite:\S+)`)

// Invite makes the hub team (from HOME, no repo) and returns an invite
// line for email. The world must be started and have a gh account (the
// team store's owner).
func (w *World) Invite(ctx context.Context, lore, email string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, inviteTimeout)
	defer cancel()
	if err := w.StartTeam(ctx, lore, w.Home, InviteHub, InviteOrg, inviteRepo); err != nil {
		return "", fmt.Errorf("invite: %w", err)
	}
	out, err := w.loreRunIn(ctx, lore, w.Home, "team", "invite", email, "--setup", InviteHub)
	if err != nil {
		return "", fmt.Errorf("invite: lore team invite: %w\n%s", err, out)
	}
	m := reInviteLine.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("invite: no invite line in:\n%s", out)
	}
	w.invite = m[1]
	return m[1], nil
}

// StartTeam saves every step of an admin setup named setup that starts
// team org on the store <first login>/<repo> (made as a bare repo when
// missing), with lore run in dir (HOME, or a repo: then the team store is
// attached to that repo's .lore/stores.yaml too).
func (w *World) StartTeam(ctx context.Context, lore, dir, setup, org, repo string) error {
	return w.StartTeamPush(ctx, lore, dir, setup, org, repo, "")
}

// StartTeamPush is StartTeam whose team step also answers push (the team
// store's push mode; "" leaves the question empty).
func (w *World) StartTeamPush(ctx context.Context, lore, dir, setup, org, repo, push string) error {
	if len(w.Accounts) == 0 || len(w.runEnv) == 0 {
		return fmt.Errorf("start team: needs a started world with a gh account")
	}
	owner := w.Accounts[0]
	bare := filepath.Join(w.Dir, "remote", owner.Login, repo+".git")
	if _, err := os.Stat(bare); err != nil {
		if err := w.git(ctx, "", "init", "-q", "--bare", "-b", "main", bare); err != nil {
			return err
		}
	}
	email0 := fallbackEmail
	if len(owner.Emails) > 0 {
		email0 = owner.Emails[0]
	}
	steps := []struct {
		id      string
		answers map[string]any
	}{
		{"kind", map[string]any{"kind": "admin", "name": setup}},
		{"identity", map[string]any{"email": email0, "name": gitName}},
		{"github", map[string]any{"github_account": owner.Login}},
		{"personal_store", map[string]any{"personal_store": "local"}},
		{"team", map[string]any{"org": org, "store_url": TeamStoreURL(owner.Login, repo)}},
		{"tools", nil},
		{"secrets", nil},
		{"folders", map[string]any{"folders": []string{hubFolders}, "default": false}},
		{"this_folder", nil},
		{"summary", nil},
	}
	for _, s := range steps {
		if s.id == "team" && push != "" {
			s.answers[qidPush] = push
		}
		args := []string{"setup", "apply", setup, "--step", s.id, "--json"}
		if s.answers != nil {
			b, err := json.Marshal(s.answers)
			if err != nil {
				return fmt.Errorf("start team: %w", err)
			}
			args = append(args, "--answers", string(b))
		}
		if out, err := w.loreRunIn(ctx, lore, dir, args...); err != nil {
			return fmt.Errorf("start team: lore %v: %w\n%s", args, err, out)
		}
	}
	return nil
}

// TeamStoreURL is the store url StartTeam gives a team on login/repo.
func TeamStoreURL(login, repo string) string { return "git@github.com:" + login + "/" + repo + ".git" }

// LoreIn runs lore with the server's env in dir and returns its combined output.
func (w *World) LoreIn(ctx context.Context, lore, dir string, args ...string) (string, error) {
	return w.loreRunIn(ctx, lore, dir, args...)
}

// loreRunIn runs lore with the server's env (fake tools, fake GitHub API) in dir.
func (w *World) loreRunIn(ctx context.Context, lore, dir string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, lore, args...)
	c.Dir = dir
	c.Env = w.runEnv
	out, err := c.CombinedOutput()
	return string(out), err
}
