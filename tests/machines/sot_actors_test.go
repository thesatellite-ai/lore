//go:build machines && !windows

package machines

// sot_actors_test.go — the actor source of truth through the built binary
// and `lore api` (SOURCE_OF_TRUTH rows 2-3, owner 2026-11-09):
//
//	solo  actor edit --name renames the setup identity; the next write to
//	      the repo and to `me` refreshes both copies; show lists every store
//	      current; the doctor flags the stale copies before that write
//	team  an admin renames a member on the roster; the member's next write
//	      refreshes their repo copy and their config's org identity; the
//	      doctor flags the stale copy before and offers `lore actor
//	      refresh`; a member renaming someone else is refused

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSotActorsSolo(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	lsGHHosts(t, m)
	lsCreate(t, m, "solo1", kindSolo, PersonEmail, []string{"~/code/**"}, true, lsTeam{}, lsAlpha)
	repo := m.Repos[RepoWorkApp]
	key := "human:" + PersonEmail
	lsRun(t, m, repo, "memory", "add", "first memory")
	lsRun(t, m, repo, "memory", "add", "a personal note", "--to", "me")
	saAllCurrent(t, saShow(t, m, repo, key), 2, "before the rename")

	lsRun(t, m, repo, "actor", "edit", key, "--name", "Pat Renamed")
	if w := m.Lore(t, repo, "whoami"); !strings.Contains(w.Stdout, "Pat Renamed") {
		t.Errorf("whoami after actor edit --name does not name the new identity:\n%s", w.Stdout)
	}
	if got := saDoctorLines(t, m, repo); len(got) < 2 || !strings.Contains(strings.Join(got, "\n"), `"Pat Renamed"`) ||
		!strings.Contains(strings.Join(got, "\n"), "lore actor refresh") {
		t.Errorf("doctor before the next write: want a stale-copy warning per store naming the source's name and the fix, got %q", got)
	}
	a := saShow(t, m, repo, key)
	if a.DisplayName != "Pat Renamed" {
		t.Errorf("actor show after the rename shows %q, want the source's Pat Renamed", a.DisplayName)
	}

	lsRun(t, m, repo, "memory", "add", "second memory")
	lsRun(t, m, repo, "memory", "add", "another personal note", "--to", "me")
	a = saShow(t, m, repo, key)
	saAllCurrent(t, a, 2, "after the next writes")
	for _, c := range a.Copies {
		if c.DisplayName != "Pat Renamed" {
			t.Errorf("the copy in %s still says %q after a write there", c.Store, c.DisplayName)
		}
	}
	if got := saDoctorLines(t, m, repo); len(got) != 0 {
		t.Errorf("doctor after the refresh still warns: %q", got)
	}
	// lore show --id <act_id>: one actor in two stores is one actor, never
	// E_AMBIGUOUS_ID.
	if r := m.Lore(t, repo, "show", a.ID); r.Code != 0 || !strings.Contains(r.Stdout, "Pat Renamed") {
		t.Errorf("lore show --id %s: %s", a.ID, r)
	}

	// REST: the same reads through lore api.
	api := csStartAPI(t, m, repo)
	st, body := api.call(t, http.MethodGet, "/v1/actors/"+a.ID, "")
	if st != http.StatusOK {
		t.Fatalf("GET /v1/actors/%s: %d %s", a.ID, st, body)
	}
	var env struct {
		Data saActor `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if env.Data.DisplayName != "Pat Renamed" || len(env.Data.Copies) < 2 {
		t.Errorf("REST actor show: %+v", env.Data)
	}
}

func TestSotActorsTeam(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	lsGHHosts(t, m)
	admin, tm := saTeam(t, m, saMemberEmail)
	lsCreate(t, m, "acme", kindMember, saMemberEmail, []string{"~/code/**"}, true, tm, lsAlpha)
	repo, adminRepo := m.Repos[RepoWorkApp], admin.Repos[RepoWorkApp]
	memberKey, bossKey := "human:"+saMemberEmail, "human:"+saBossEmail
	lsRun(t, m, repo, "memory", "add", "member note", "--to", "repo")
	// a write to the personal store puts the member's actor copy there too
	// (a repo write no longer does: B-16), the copy the rename leaves stale
	lsRun(t, m, repo, "memory", "add", "member personal note", "--to", "me")
	before := saShow(t, m, repo, memberKey)
	if before.Source.Where == "" || !before.Source.Reachable {
		t.Fatalf("the member's actor has no reachable roster source: %+v", before)
	}

	// A member may not rename someone else.
	r := m.Lore(t, repo, "actor", "edit", bossKey, "--name", "Not Boss", "--json")
	if r.Code == 0 || !strings.Contains(r.Stdout+r.Stderr, "E_POLICY_DENIED") {
		t.Errorf("a member renaming the admin was not refused with E_POLICY_DENIED: %s", r)
	}

	// The admin renames the member on the roster.
	lsRun(t, admin, adminRepo, "actor", "edit", memberKey, "--name", "Mem Renamed")
	lsRun(t, admin, adminRepo, "sync") // C5: the rename reaches the roster with the admin's sync
	lsRun(t, m, repo, "sync")
	got := saDoctorLines(t, m, repo)
	if !strings.Contains(strings.Join(got, "\n"), `"Mem Renamed"`) || !strings.Contains(strings.Join(got, "\n"), "lore actor refresh") {
		t.Errorf("doctor on the member's clone before its next write: want the stale repo copy and the fix, got %q", got)
	}
	if a := saShow(t, m, repo, memberKey); a.DisplayName != "Mem Renamed" {
		t.Errorf("actor show on the member's clone shows %q, want the roster's Mem Renamed", a.DisplayName)
	}

	// The member's next write refreshes the repo copy and the config copy.
	lsRun(t, m, repo, "memory", "add", "after the rename", "--to", "repo")
	a := saShow(t, m, repo, memberKey)
	for _, c := range a.Copies {
		if c.Store == "repo" && (c.State != "current" || c.DisplayName != "Mem Renamed") {
			t.Errorf("the repo copy after a write there: %+v, want current Mem Renamed", c)
		}
	}
	if w := m.Lore(t, repo, "whoami"); !strings.Contains(w.Stdout, "Mem Renamed") {
		t.Errorf("whoami still names the old org identity after the roster rename:\n%s", w.Stdout)
	}
	// A store this run did not write to keeps its copy until its next
	// write; the doctor's Run it (setup check identity.actor_copies, fix
	// `lore actor refresh`) rewrites it now.
	if d := m.Lore(t, repo, "doctor"); !strings.Contains(d.Stdout+d.Stderr, "identity.actor_copies") {
		t.Errorf("doctor shows no identity.actor_copies check with the Run it:\n%s", d.Stdout)
	}
	if fx := m.Lore(t, repo, "setup", "fix", "acme", "--check", "identity.actor_copies", "--yes", "--json"); fx.Code != 0 || !strings.Contains(fx.Stdout, "actor") {
		t.Errorf("the Run it of identity.actor_copies: %s", fx)
	}
	saAllCurrent(t, saShow(t, m, repo, memberKey), 2, "member after lore actor refresh")
	if got := saDoctorLines(t, m, repo); len(got) != 0 {
		t.Errorf("doctor after the refresh still warns: %q", got)
	}
}
