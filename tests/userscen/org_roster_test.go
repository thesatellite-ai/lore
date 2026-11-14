//go:build userscen

package userscen

// Org roster scenarios, through the real binary (TestOrg*):
//   - create an org on a store and join it from a second laptop: both
//     clones list the same roster (ids, names, keys, handles, the roster
//     store as _source) and each whoami names its own person;
//   - the profile a person edits is what `actor show` / `actor list` print
//     in the org's own repo (the roster copy, not the repo's
//     {id, display_name, kind} copy), on both clones;
//   - register twice is a refresh (same id, created false, one row);
//   - refusals with code, exit and hint: delete, unknown handle, unknown
//     actor, a bad --status, a bad agent label, profile writes in a repo
//     with no org;
//   - agents: `lore agent start` registers one actor per label (twice is
//     the same actor), an agent session may read and change people (it
//     acts for its person) until a person sets agents.may_manage_people
//     false (E_POLICY_DENIED).

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestOrgCreateJoinSameRosterOnBothClones: mira creates, bob imports;
// each clone lists the same two people from the roster store and each
// whoami resolves its own person in the org.
func TestOrgCreateJoinSameRosterOnBothClones(t *testing.T) {
	o := newOrgScen(t)
	_, bc := o.join(t, "bob", orgBobEmail, orgBobName, "bob")

	type person struct{ name, key, handle string }
	want := []person{{orgMiraName, orgMiraKey, "mira"}, {orgBobName, orgBobKey, "bob"}}
	var ids [2][]string
	for i, c := range []*th.Clone{o.app, bc} {
		l := listActors(t, c)
		if l.Count != len(want) {
			t.Fatalf("clone %d: the roster has %d people: %v", i, l.Count, displayNames(l))
		}
		for _, p := range want {
			a, ok := byName(l, p.name)
			if !ok {
				t.Fatalf("clone %d: %s missing from actor list: %v", i, p.name, displayNames(l))
			}
			if a.StableKey != p.key || a.Handle != p.handle || a.Kind != "human" || !a.Active || a.Status != "active" {
				t.Fatalf("clone %d: %s listed as %+v", i, p.name, a)
			}
			ids[i] = append(ids[i], a.ID)
		}
	}
	if strings.Join(ids[0], ",") != strings.Join(ids[1], ",") {
		t.Fatalf("both clones must name the same actors: %v vs %v", ids[0], ids[1])
	}

	type whoami struct {
		Data struct {
			Org       string `json:"org"`
			RepoRole  string `json:"repo_role"`
			MatchedBy string `json:"matched_by"`
			Identity  struct {
				StableKey string `json:"stable_key"`
				Name      string `json:"name"`
				Kind      string `json:"kind"`
				ActorID   string `json:"actor_id"`
			} `json:"identity"`
		} `json:"data"`
	}
	for i, c := range []*th.Clone{o.app, bc} {
		w := th.DecodeJSON[whoami](t, c.MustLore("whoami", "--json"))
		if w.Data.Org != orgName || w.Data.RepoRole != "own" || !strings.Contains(w.Data.MatchedBy, orgOwnGlob) {
			t.Fatalf("clone %d whoami org: %+v", i, w.Data)
		}
		if w.Data.Identity.StableKey != want[i].key || w.Data.Identity.Name != want[i].name ||
			w.Data.Identity.Kind != "human" || w.Data.Identity.ActorID != ids[0][i] {
			t.Fatalf("clone %d whoami identity: %+v (want %s, %s)", i, w.Data.Identity, want[i].key, ids[0][i])
		}
	}
}

// TestOrgActorShowIsTheRosterProfile: in the org's own repo, the repo
// holds a {id, display_name, kind} copy of its first writer (made at
// `lore init`, display = git user.name, no key). `actor show` and `actor
// list` print the roster's copy: the name, key, handle and every profile
// field edited since, on the editor's clone and on a teammate's.
// Regression: both printed the repo copy ("mira", empty stable key, no
// title), so an edit looked lost.
func TestOrgActorShowIsTheRosterProfile(t *testing.T) {
	o := newOrgScen(t)
	_, bc := o.join(t, "bob", orgBobEmail, orgBobName, "bob")
	o.app.MustLore("actor", "edit", "@mira", "--title", "Backend engineer", "--timezone", "Europe/Istanbul",
		"--location", "Ankara, TR", "--birthday", "--04-17").Says(t, "edited", "title", "timezone", "location", "birthday")
	o.app.MustLore("sync") // C5: mira's writes reach bob with her sync
	for i, c := range []*th.Clone{o.app, bc} {
		for _, ref := range []string{"@mira", orgMiraKey} {
			a := showActor(t, c, ref)
			if a.DisplayName != orgMiraName || a.StableKey != orgMiraKey || a.Handle != "mira" {
				t.Fatalf("clone %d show %s: the roster copy, not the repo's: %+v", i, ref, a)
			}
			if a.Profile.Title != "Backend engineer" || a.Profile.Timezone != "Europe/Istanbul" || a.Profile.Location != "Ankara, TR" || a.Profile.Birthday == "" {
				t.Fatalf("clone %d show %s: profile %+v", i, ref, a.Profile)
			}
		}
		c.MustLore("actor", "show", "@mira").Says(t, "display:     "+orgMiraName, "stable_key:  "+orgMiraKey, "title:       Backend engineer")
		l := listActors(t, c)
		if _, ok := byName(l, orgMiraName); !ok || l.Count != 2 {
			t.Fatalf("clone %d: actor list must show the roster names once each: %v", i, displayNames(l))
		}
	}
	// --store still picks one copy: the repo's is the bare one.
	repo := th.DecodeJSON[orgActorShow](t, o.app.MustLore("actor", "show", "@mira", "--store", "repo", "--json")).Data
	if repo.StableKey != "" || repo.Profile.Title != "" {
		t.Fatalf("the repo's copy is {id, display_name, kind} only: %+v", repo)
	}
	roster := th.DecodeJSON[orgActorShow](t, o.app.MustLore("actor", "show", "@mira", "--store", orgRosterStore, "--json")).Data
	if roster.StableKey != orgMiraKey {
		t.Fatalf("--store %s shows the roster copy: %+v", orgRosterStore, roster)
	}
}

// TestOrgRegisterTwiceIsARefresh: `lore actor register` again (and from
// a second clone of the same person) keeps one actor: same id, created
// false, "already registered (refreshed)".
func TestOrgRegisterTwiceIsARefresh(t *testing.T) {
	o := newOrgScen(t)
	type reg struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Name    string `json:"display_name"`
		Handle  string `json:"handle"`
		Created bool   `json:"created"`
		Where   string `json:"where"`
	}
	first := orgJSON[reg](t, o.app.MustLore("actor", "register", "--json"))
	if first.Created || first.Kind != "human" || first.Name != orgMiraName || first.Handle != "mira" || first.ID == "" {
		t.Fatalf("org create already registered mira; register is a refresh: %+v", first)
	}
	o.app.MustLore("actor", "register").Says(t, first.ID, orgMiraName, "(@mira)", "already registered (refreshed)")
	second := o.mira.Clone(o.appR, "app2")
	again := orgJSON[reg](t, second.MustLore("actor", "register", "--json"))
	if again.ID != first.ID || again.Created {
		t.Fatalf("a second clone of the same person is the same actor: %+v vs %+v", again, first)
	}
	if l := listActors(t, o.app); l.Count != 1 {
		t.Fatalf("one person, one roster row: %v", displayNames(l))
	}
}

// TestOrgActorRefusals: what a person mistypes on the roster commands
// answers its code, exit and next step, and changes nothing.
func TestOrgActorRefusals(t *testing.T) {
	o := newOrgScen(t)
	cases := []struct {
		name string
		args []string
		code int
		say  []string
	}{
		{"delete without id", []string{"actor", "delete"}, th.ExitRefused, []string{"E_REFUSED", "actors are never deleted", "lore actor deactivate"}},
		{"delete an id", []string{"actor", "delete", "act_0123"}, th.ExitRefused, []string{"E_REFUSED", "actor act_0123 cannot be deleted"}},
		{"deactivate unknown handle", []string{"actor", "deactivate", "@nobody"}, orgExitBadInput, []string{"E_INVALID_INPUT", "no one is @nobody", "lore actor register"}},
		{"show unknown handle", []string{"actor", "show", "@nobody"}, th.ExitNotFound, []string{"E_NOT_FOUND", `actor "@nobody" not found`, "lore actor list"}},
		{"show unknown id", []string{"actor", "show", "act_7fd72ec1e1c473978cd0c00b3703d400"}, th.ExitNotFound, []string{"E_NOT_FOUND"}},
		{"show a non-actor id", []string{"actor", "show", "tsk_01a1b4a2edaf78f5959066a022572bdc"}, th.ExitNotFound, []string{"E_NOT_FOUND"}},
		{"show unknown store", []string{"actor", "show", "@mira", "--store", "agency"}, th.ExitNotFound, []string{"E_NOT_FOUND", `not found in store "agency"`, "lore actor list --store agency"}},
		{"list bad status", []string{"actor", "list", "--status", "gone"}, orgExitBadInput, []string{"E_INVALID_INPUT", `--status "gone"`, "active, on_leave, left, archived"}},
		{"agent label with a space", []string{"actor", "register", "--kind", "agent", "--label", "front end"}, orgExitBadInput, []string{"E_INVALID_INPUT", "one word"}},
		{"agent label with a colon", []string{"actor", "register", "--kind", "agent", "--label", "a:b"}, orgExitBadInput, []string{"E_INVALID_INPUT", "one word"}},
		{"agent without label", []string{"actor", "register", "--kind", "agent"}, orgExitBadInput, []string{"E_INVALID_INPUT"}},
		{"agent of an unknown owner", []string{"actor", "register", "--kind", "agent", "--label", "fe", "--owner", "@ghost"}, orgExitBadInput, []string{"E_INVALID_INPUT", "@ghost"}},
		{"unknown kind", []string{"actor", "register", "--kind", "robot"}, orgExitBadInput, []string{"E_INVALID_INPUT"}},
		{"edit a bad timezone", []string{"actor", "edit", "@mira", "--timezone", "Mars/Olympus"}, orgExitBadInput, []string{"E_INVALID_INPUT"}},
		{"edit a bad birthday", []string{"actor", "edit", "@mira", "--birthday", "2026-02-30"}, orgExitBadInput, []string{"E_INVALID_INPUT"}},
		{"edit a bad photo url", []string{"actor", "edit", "@mira", "--photo-url", "ftp://x"}, orgExitBadInput, []string{"E_INVALID_INPUT"}},
		{"edit nothing", []string{"actor", "edit", "@mira"}, orgExitBadInput, []string{"E_INVALID_INPUT"}},
		{"edit unknown person", []string{"actor", "edit", "@nobody", "--title", "x"}, th.ExitNotFound, []string{"E_NOT_FOUND"}},
	}
	before := o.notes.Rev("refs/heads/main")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o.app.Lore(c.args...).Fails(t, c.code, c.say...)
		})
	}
	if after := o.notes.Rev("refs/heads/main"); after != before {
		t.Fatalf("a refused roster command pushes nothing to the store: %s -> %s", before, after)
	}
}

// TestOrgNoOrgRepoRefusesProfileWrites: a repo no org applies to keeps
// its roster in the repo; profile writes there would publish contacts
// with the code, so they are refused naming `lore org create`; reads work.
func TestOrgNoOrgRepoRefusesProfileWrites(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	solo := w.Human("solo", "solo@example.com")
	solo.WriteConfig(th.Config{Identity: &th.Identity{Key: "human:solo@example.com", Name: "Solo Poe", Handle: "solo"},
		Personal: &th.Personal{Store: th.PersonalLocal}, Live: th.FreshFetch()})
	c := solo.Clone(w.Remote("github.com/solo/tool"), "tool")
	c.MustLore("init", "--non-interactive", "--name=tool")
	c.CommitAll("lore")
	c.AdminPush("origin", "main")
	c.MustLore("actor", "register").Says(t, "Solo Poe", "(@solo)", "in this repo")
	for _, args := range [][]string{
		{"actor", "edit", "@solo", "--title", "x"},
		{"actor", "contact", "add", "@solo", "github", "solo-dev"},
		{"role", "add", "tech_writer"},
		{"actor", "period", "add", "@solo", "--type", "employee", "--started-on", "2026-01-01"},
		{"actor", "status", "@solo", "on_leave"},
		{"contact-type", "add", "bitbucket", "--kind", "handle"},
	} {
		c.Lore(args...).Fails(t, th.ExitRefused, "E_REFUSED", "team profiles live in an org store", "lore org create")
	}
	c.MustLore("actor", "show", "@solo").Says(t, "Solo Poe")
	c.MustLore("role", "list").Says(t, "dev")
	if st := c.Status(); strings.Contains(st, "contacts") || strings.Contains(st, "roles") {
		t.Fatalf("a refused profile write leaves nothing in the repo: %q", st)
	}
}

// TestOrgAgentSessionsAndPeopleChanges: `lore agent start` registers one
// agent actor per label, owned by the caller (twice: the same actor); with
// no policy set the agent may read and change people (default true, owner
// decision 2026-11-11); once a person sets agents.may_manage_people false,
// its writes are refused (E_POLICY_DENIED naming the key, needs a person)
// and change nothing.
func TestOrgAgentSessionsAndPeopleChanges(t *testing.T) {
	o := newOrgScen(t)
	r1 := o.app.MustLore("agent", "start", "--label", "fe-1").Says(t, "agent fe-1 (owner @mira) registered", "LORE_ACTOR=agent:fe-1", "LORE_SESSION=")
	id1 := orgID(t, r1, "act")
	r2 := o.app.MustLore("agent", "start", "--label", "fe-1")
	if id2 := orgID(t, r2, "act"); id2 != id1 {
		t.Fatalf("one actor per label: %s then %s", id1, id2)
	}
	o.app.MustLore("agent", "start", "--label", "fe-2")
	l := listActors(t, o.app)
	agents := 0
	for _, a := range l.Data {
		if a.Kind == "agent" {
			agents++
			if a.OwnerID == "" || a.Label == "" || a.StableKey != "agent:"+a.Label {
				t.Fatalf("an agent row names its label, key and owner: %+v", a)
			}
		}
	}
	if agents != 2 {
		t.Fatalf("two labels, two agents: %v", displayNames(l))
	}

	ag := o.app.With(th.EnvLoreActor+"=agent:fe-1", th.EnvLoreSession+"=s-scen0001")
	ag.MustLore("whoami").Says(t, "agent:fe-1")
	ag.MustLore("actor", "list").Says(t, orgMiraName)
	ag.MustLore("actor", "show", "@mira").Says(t, orgMiraKey)
	ag.MustLore("role", "list").Says(t, "dev")
	// No policy row anywhere: the default lets the agent act for mira.
	ag.MustLore("actor", "edit", "@mira", "--title", "by the agent")
	ag.MustLore("role", "add", "agent_role").Says(t, "role agent_role added")

	o.app.MustLore("policy", "set", "agents.may_manage_people", "false", "--yes")
	writes := [][]string{
		{"actor", "edit", "@mira", "--title", "again by the agent"},
		{"role", "add", "agent_role2"},
		{"actor", "contact", "add", "@mira", "github", "mira-agent", "--identity"},
		{"actor", "status", "@mira", "on_leave"},
		{"actor", "offboard", "@mira", "--yes"},
	}
	for _, args := range writes {
		ag.Lore(args...).Fails(t, th.ExitRefused, "E_POLICY_DENIED", "agents.may_manage_people is false", "agent:fe-1")
	}
	j := ag.Lore("role", "add", "agent_role2", "--json")
	if !strings.Contains(j.Stdout, `"needs_person": true`) {
		t.Fatalf("the refusal tells a script a person must act: %s", j)
	}
	if a := showActor(t, o.app, "@mira"); a.Profile.Title != "by the agent" || a.Status != "active" {
		t.Fatalf("a refused agent write changes nothing: %+v", a)
	}
}
