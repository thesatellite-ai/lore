//go:build userscen

package userscen

// Fixture of the org area (TestOrg*): one agency org on a lore store, the
// way a team starts one for real. mira creates the store and the org from
// her clone of the agency's own app repo (`lore store init`, `lore org
// create`); a teammate joins with `lore org import <store url>` from
// outside any repo, then clones the app. Every person has their own HOME,
// a local personal store and live.fetch_ttl 0s (each command sees the
// others' pushes at once).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Placeholder names of the org fixture (agent-rules: example.com only).
const (
	orgSlugNotes = "github.com/agency/notes"
	orgSlugApp   = "github.com/agency/app"
	orgName      = "agency"
	orgOwnGlob   = "github.com/agency/*"
	orgMiraEmail = "mira@example.com"
	orgMiraKey   = "human:" + orgMiraEmail
	orgMiraName  = "Mira Moe"
	orgBobEmail  = "bob@example.com"
	orgBobKey    = "human:" + orgBobEmail
	orgBobName   = "Bob Roe"
	// orgRosterStore is the attached store holding the roster: the org
	// folder ("org") of the org's store entry.
	orgRosterStore = orgName + "/org"
)

// Exit codes the harness does not name (model/errs classes).
const (
	orgExitBadInput = 20 // E_INVALID_INPUT, E_CONTACT_TYPE, …
)

// orgScen is the agency: mira's app clone with the org created.
type orgScen struct {
	w     *th.World
	notes *th.Remote
	appR  *th.Remote
	mira  *th.Actor
	app   *th.Clone
}

// orgPersonConfig is a fresh person's machine: no orgs yet, a local
// personal store, every fetch fresh.
func orgPersonConfig() th.Config {
	return th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Live: th.FreshFetch()}
}

// newOrgScen builds the agency with mira as its only member.
func newOrgScen(t *testing.T) *orgScen {
	t.Helper()
	scenParallel(t)
	w := newWorld(t)
	w.RequireTestClock()
	o := &orgScen{w: w, notes: w.Remote(orgSlugNotes), appR: w.Remote(orgSlugApp)}
	o.mira = w.Human("mira", orgMiraEmail)
	o.mira.WriteConfig(orgPersonConfig())
	dir := filepath.Join(o.mira.Home, "store-init", "notes")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	w.Git(dir, "init", "-q", "-b", "main")
	o.mira.Lore(dir, "init", "--non-interactive", "--name=notes").OK(t)
	o.mira.Lore(dir, "store", "init", "notes", "https://"+orgSlugNotes, "--yes").OK(t)
	o.app = o.mira.Clone(o.appR, "app")
	o.app.MustLore("init", "--non-interactive", "--name=app")
	o.app.CommitAll("lore")
	o.app.AdminPush("origin", "main")
	o.app.MustLore("org", "create", orgName, "--store", "https://"+orgSlugNotes, "--own", orgOwnGlob,
		"--key", orgMiraKey, "--name", orgMiraName, "--handle", "mira", "--yes")
	return o
}

// outside is a folder of a's HOME that is no git repo (org import runs
// there, as on a new laptop).
func orgOutside(t *testing.T, a *th.Actor) string {
	t.Helper()
	d := filepath.Join(a.Home, "outside")
	if err := os.MkdirAll(d, 0o750); err != nil {
		t.Fatal(err)
	}
	return d
}

// join is a teammate joining: `lore org import` of the store with their
// org key, name and handle, then a clone of the app.
func (o *orgScen) join(t *testing.T, name, email, display, handle string) (*th.Actor, *th.Clone) {
	t.Helper()
	return o.joinOn(t, time.Time{}, name, email, display, handle)
}

// joinOn is join with the newcomer's clock pinned to day (zero: the real
// clock). A test that later pins the org's clock to a fixed day must pin
// the join too, or the newcomer's stint starts on the real date, which can
// be after the pinned day (TestOrgStatusMoves failed once the real clock
// passed 2026-11-07: offboard's default --on fell before bob's stint).
func (o *orgScen) joinOn(t *testing.T, day time.Time, name, email, display, handle string) (*th.Actor, *th.Clone) {
	t.Helper()
	p := o.w.Human(name, email)
	if !day.IsZero() {
		p.SetNow(day)
	}
	p.WriteConfig(orgPersonConfig())
	p.Lore(orgOutside(t, p), "org", "import", "https://"+orgSlugNotes, "--key", "human:"+email, "--name", display,
		"--handle", handle, "--yes").OK(t)
	return p, p.Clone(o.appR, "app")
}

// orgActor is one actor of `lore actor list|show --json`.
type orgActor struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	StableKey   string `json:"stable_key"`
	Handle      string `json:"handle"`
	Label       string `json:"label"`
	OwnerID     string `json:"owner_actor_id"`
	Active      bool   `json:"active"`
	LeftAt      string `json:"left_at"`
	Status      string `json:"status"`
	LeaveFrom   string `json:"leave_from"`
	LeaveUntil  string `json:"leave_until"`
	Profile     struct {
		Title    string `json:"title"`
		Timezone string `json:"timezone"`
		Location string `json:"location"`
		PhotoURL string `json:"photo_url"`
		Bio      string `json:"bio"`
		Birthday string `json:"birthday"`
	} `json:"profile"`
	Contacts []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Value    string `json:"value"`
		Label    string `json:"label"`
		Primary  bool   `json:"primary"`
		Identity bool   `json:"identity"`
	} `json:"contacts"`
	Periods     []orgPeriod `json:"periods"`
	Memberships []struct {
		ID        string   `json:"id"`
		ProjectID string   `json:"project_id"`
		Project   string   `json:"project"`
		Roles     []string `json:"roles"`
		StartedOn string   `json:"started_on"`
		EndedOn   string   `json:"ended_on"`
		EndReason string   `json:"end_reason"`
	} `json:"memberships"`
}

// orgPeriod is one company stint.
type orgPeriod struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	StartedOn string `json:"started_on"`
	EndedOn   string `json:"ended_on"`
	OrgRole   string `json:"org_role"`
	EndReason string `json:"end_reason"`
	Source    string `json:"source"`
}

// orgActorList is `lore actor list --json`.
type orgActorList struct {
	SchemaVersion int        `json:"schema_version"`
	Kind          string     `json:"kind"`
	Count         int        `json:"count"`
	Data          []orgActor `json:"data"`
}

// orgActorShow is `lore actor show --json`.
type orgActorShow struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Data          orgActor `json:"data"`
}

// listActors runs `lore actor list --json` (plus extra) in c.
func listActors(t *testing.T, c *th.Clone, extra ...string) orgActorList {
	t.Helper()
	v := th.DecodeJSON[orgActorList](t, c.MustLore(append([]string{"actor", "list", "--json"}, extra...)...))
	if v.SchemaVersion != 1 || v.Kind != "actor.list" || v.Count != len(v.Data) {
		t.Fatalf("actor list --json envelope: version %d kind %q count %d of %d", v.SchemaVersion, v.Kind, v.Count, len(v.Data))
	}
	return v
}

// showActor runs `lore actor show <ref> --json` in c.
func showActor(t *testing.T, c *th.Clone, ref string) orgActor {
	t.Helper()
	v := th.DecodeJSON[orgActorShow](t, c.MustLore("actor", "show", ref, "--json"))
	if v.SchemaVersion != 1 || v.Kind != "actor.show" {
		t.Fatalf("actor show --json envelope: version %d kind %q", v.SchemaVersion, v.Kind)
	}
	return v.Data
}

// byName is the actor list entry whose display name is name.
func byName(l orgActorList, name string) (orgActor, bool) {
	for _, a := range l.Data {
		if a.DisplayName == name {
			return a, true
		}
	}
	return orgActor{}, false
}

// displayNames is every display name of l, in order.
func displayNames(l orgActorList) []string {
	out := make([]string, 0, len(l.Data))
	for _, a := range l.Data {
		out = append(out, a.DisplayName)
	}
	return out
}

// orgIDRe finds a prefixed row id in output.
func orgID(t *testing.T, r th.Result, prefix string) string {
	t.Helper()
	m := regexp.MustCompile(`\b` + prefix + `_[0-9a-z]+\b`).FindString(r.Out())
	if m == "" {
		t.Fatalf("no %s_ id in output: %s", prefix, r)
	}
	return m
}

// orgJSON decodes a non-enveloped --json answer into T.
func orgJSON[T any](t *testing.T, r th.Result) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("decode --json: %v\n%s", err, r)
	}
	return v
}

// mustContainAll fails unless s holds every needle.
func mustContainAll(t *testing.T, what, s string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(s, n) {
			t.Fatalf("%s must contain %q:\n%s", what, n, s)
		}
	}
}
