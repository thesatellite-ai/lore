//go:build machines && !windows

package machines

// sot_actors_helpers_test.go — helpers of sot_actors_test.go (actor
// source of truth, SOURCE_OF_TRUTH rows 2-3).

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// saActor is the part of `lore actor show --json` these tests read.
type saActor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	SourceNote  string `json:"source_note"`
	Source      struct {
		Where       string `json:"where"`
		Reachable   bool   `json:"reachable"`
		DisplayName string `json:"display_name"`
	} `json:"source"`
	Copies []struct {
		Store       string `json:"store"`
		DisplayName string `json:"display_name"`
		State       string `json:"state"`
	} `json:"copies"`
}

// saShow is `lore actor show --id <ref> --json` in cwd.
func saShow(t *testing.T, m *Machine, cwd, ref string) saActor {
	t.Helper()
	var env struct {
		Data saActor `json:"data"`
	}
	if err := json.Unmarshal([]byte(lsRun(t, m, cwd, "actor", "show", ref)), &env); err != nil {
		t.Fatalf("actor show %s: %v", ref, err)
	}
	return env.Data
}

// saAllCurrent fails unless every copy of a is current (or the source row)
// and at least min stores hold one.
func saAllCurrent(t *testing.T, a saActor, min int, step string) {
	t.Helper()
	if len(a.Copies) < min {
		t.Errorf("%s: %d copies of %s, want at least %d: %+v", step, len(a.Copies), a.ID, min, a.Copies)
	}
	for _, c := range a.Copies {
		if c.State != "current" && c.State != "source" {
			t.Errorf("%s: the copy in %s is %q (%q), want current", step, c.Store, c.State, c.DisplayName)
		}
	}
}

// saDoctorLines is the actor_copies lines of `lore doctor` in cwd.
func saDoctorLines(t *testing.T, m *Machine, cwd string) []string {
	t.Helper()
	r := m.Lore(t, cwd, "doctor")
	var out []string
	for _, l := range strings.Split(r.Stdout+r.Stderr, "\n") {
		if strings.Contains(l, "actor_copies:") {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// saTeam is an admin machine that started team store bare and invited
// email; it returns the admin and the store for the member's setup.
func saTeam(t *testing.T, member *Machine, email string) (*Machine, lsTeam) {
	t.Helper()
	bare := filepath.Join(member.Dir, "team.git")
	if out, err := exec.Command("/usr/bin/git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v %s", err, out)
	}
	tm := lsTeam{storeURL: lsTeamURL, bare: bare}
	tm.rewrite(t, member)
	admin := Build(t, ShapeOwnerLike, loreBin)
	lsGHHosts(t, admin)
	tm.rewrite(t, admin)
	lsCreate(t, admin, "boss", kindAdmin, saBossEmail, []string{"~/code/**"}, true, tm, lsGitHub{LoginBravo, "github-bravo"})
	out := lsRun(t, admin, admin.Repos[RepoWorkApp], "team", "invite", email, "--yes")
	var o struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(out), &o)
	if tm.invite = o.Text; !strings.HasPrefix(tm.invite, "lore-invite:") {
		t.Fatalf("team invite printed no invite line: %s", out)
	}
	return admin, tm
}

// Placeholder people of the team test.
const (
	saBossEmail   = "boss@example.test"
	saMemberEmail = "mem@example.test"
)
