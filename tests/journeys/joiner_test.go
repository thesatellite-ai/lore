//go:build journeys && !windows

package journeys

// joiner_test.go — persona 4, the new joiner: Pat already works with a
// personal setup from another job (job B, solo setup "jobb" over all of
// ~/work), and joins job A's team from an invite on day 1 (member setup
// "joba" over ~/work/joba, inside jobb's folders). Which setup applies
// where must be clear and stable, each job's team notes stay in its own
// org stores, and Pat's own notes live in one personal store `me`, shared
// by both setups (one `me` per person, decided 2026-11-09): the page and
// `lore store show me` say so and name it. When Pat leaves job A that
// setup goes while jobb keeps working, with nothing of job A's team left
// in jobb's folders, stores or config, and `me` untouched.
//
// Undo paths along the way: the setup page left halfway on day 1 and
// finished the next day, a setup edited and edited back, a note deleted
// and restored, the setup removed and restored, purged and re-joined from
// the same invite (regret), then removed for good.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Placeholders of the joiner journey.
const (
	jJoinerName      = "Pat Joiner"
	jJoinerHomeEmail = "pat@jobb.example"  // job B's address (setup jobb)
	jJoinerWorkEmail = "pat@acme.example"  // job A's address, the invite's
	jJoinerBossEmail = "boss@acme.example" // job A's admin
	jJoinerOrg       = "acme"              // job A's org entry
	jJoinerLoginB    = "pat-jobb"          // GitHub login for job B
	jJoinerLoginA    = "pat-acme"          // GitHub login for job A
	jJoinerAliasA    = "github-acme"       // its ssh alias
	jJoinerLoginBoss = "boss-acme"         // the admin's login
	jJoinerSetupB    = "jobb"              // the personal setup from job B
	jJoinerSetupA    = "joba"              // the member setup of job A
	jJoinerTeamNote  = "acme deploys go through staging first"
	jJoinerHomeNote  = "jobb invoices are sent on the first"
	jJoinerSecret    = "acme-internal-codename-heron"              // a job A note that must never leave job A
	jJoinerANote     = "acme personal: ask about the on-call rota" // Pat's own note while at job A (--to me)
	jJoinerBNote     = "jobb personal: call the accountant"        // Pat's own note for job B (--to me)
)

// jJoinerSteps is the member setup's page steps in order (setup plan
// --kind member), the answers the joiner gives each.
func jJoinerSteps(invite, foldersA string) []jJoinerAnswer {
	return []jJoinerAnswer{
		{"kind", map[string]any{"kind": "member", "name": jJoinerSetupA}},
		{"identity", map[string]any{"name": jJoinerName, "email": jJoinerWorkEmail}},
		{"github", map[string]any{"github_account": jJoinerLoginA, "host_alias": jJoinerAliasA}},
		{"personal_store", map[string]any{"personal_store": "local"}},
		{"join", map[string]any{"invite": invite}},
		{"tools", map[string]any{"tools": []string{"claude_code"}}},
		{"secrets", map[string]any{"backend": "none"}},
		{"folders", map[string]any{"folders": []string{foldersA}, "default": false}},
		{"this_folder", map[string]any{}},
		{"summary", map[string]any{}},
	}
}

type jJoinerAnswer struct {
	step    string
	answers map[string]any
}

// jJoinerApply saves one setup page step (lore setup apply, as the page's
// Save & next does); a refusal fails the test with what setup check says.
func jJoinerApply(t *testing.T, m *Machine, dir, setup string, a jJoinerAnswer) teamharness.Result {
	t.Helper()
	b, err := json.Marshal(a.answers)
	if err != nil {
		t.Fatal(err)
	}
	r := m.Lore(dir, "setup", "apply", setup, "--step", a.step, "--answers", string(b), "--json")
	if r.Code != 0 {
		c := m.Lore(dir, "setup", "check", setup, "--step", a.step, "--answers", string(b), "--json")
		t.Fatalf("setup apply %s --step %s: %s\ncheck says: %s", setup, a.step, r, c.Stdout)
	}
	return r
}

// jJoinerList is the part of setup list --json the joiner reads.
type jJoinerList struct {
	Setups []struct {
		Name    string   `json:"name"`
		Kind    string   `json:"kind"`
		Org     string   `json:"org"`
		Default bool     `json:"default"`
		Folders []string `json:"folders"`
	} `json:"setups"`
	Active struct {
		Name string `json:"name"`
		Why  string `json:"why"`
	} `json:"active"`
	Here struct {
		IdentityKey string `json:"identity_key"`
	} `json:"here"`
	Trashed int `json:"trashed"`
}

func jJoinerSetups(t *testing.T, m *Machine, dir string) jJoinerList {
	t.Helper()
	r := m.Lore(dir, "setup", "list", "--json")
	var l jJoinerList
	if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &l) != nil {
		t.Fatalf("setup list in %s: %s", dir, r)
	}
	return l
}

// jJoinerSetupEntry is one setups[] entry of config.yaml as setup show
// --json prints it, raw, to compare before and after.
func jJoinerShow(t *testing.T, m *Machine, name string) map[string]any {
	t.Helper()
	r := m.Lore("", "setup", "show", name, "--json")
	var o struct {
		Setup map[string]any `json:"setup"`
	}
	if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &o) != nil {
		t.Fatalf("setup show %s: %s", name, r)
	}
	return o.Setup
}

func jJoinerJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// jJoinerGrep lists the files under root whose bytes contain s (a
// placeholder text that must not be there).
func jJoinerGrep(root, s string) []string {
	var hits []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), s) {
			hits = append(hits, p)
		}
		return nil
	})
	return hits
}

// jJoinerRemoteHas reports whether any commit of the bare repo holds s.
func jJoinerRemoteHas(w *World, r *teamharness.Remote, s string) bool {
	res := w.TryGit(r.Dir, "log", "--all", "-p", "--format=%H")
	return strings.Contains(res.Stdout, s)
}

func TestJourneyJoiner(t *testing.T) {
	w := NewWorld(t)
	pat := w.Persona(jJoinerName, jJoinerHomeEmail)
	m := pat.Laptop()
	m.GitHubAccounts(t, GitHubAccount{Login: jJoinerLoginB, Key: "id_ed25519"},
		GitHubAccount{Login: jJoinerLoginA, Key: "acme_key", Alias: jJoinerAliasA})
	boss := w.Persona("Boss Acme", jJoinerBossEmail)
	bm := boss.Laptop()
	bm.GitHubAccounts(t, GitHubAccount{Login: jJoinerLoginBoss, Key: "id_ed25519"})

	work := filepath.Join(w.Root, "work", m.Name) // where Pat's clones live
	foldersB := work + "/**"
	foldersA := filepath.Join(work, "joba") + "/**"
	siteRemote := w.Remote("github.com/jobb-co/site")
	appRemote := w.Remote("github.com/acme-co/app")
	teamRemote := w.Remote("github.com/acme-co/lore-team")
	var site, app *teamharness.Clone
	var invite string

	w.Step("before/personal-setup", func() {
		// Pat's existing setup from job B: solo, the default, over every
		// clone, a local personal store; a repo with a note in it.
		site = m.Clone(siteRemote, "site")
		m.LoreIn(site, "init", "--name", "site").OK(t)
		for _, a := range []jJoinerAnswer{
			{"kind", map[string]any{"kind": "solo", "name": jJoinerSetupB, "default": true}},
			{"identity", map[string]any{"name": jJoinerName, "email": jJoinerHomeEmail}},
			{"github", map[string]any{"github_account": jJoinerLoginB, "host_alias": "github.com"}},
			{"personal_store", map[string]any{"personal_store": "local"}},
			{"tools", map[string]any{"tools": []string{"claude_code"}}},
			{"secrets", map[string]any{"backend": "none"}},
			{"folders", map[string]any{"folders": []string{foldersB}, "default": true}},
			{"summary", map[string]any{}},
		} {
			jJoinerApply(t, m, site.Dir, jJoinerSetupB, a)
		}
		m.LoreIn(site, "memory", "add", "--body", jJoinerHomeNote).OK(t)
		m.LoreIn(site, "memory", "add", "--body", "jobb personal: renew the domain", "--to", "me").OK(t)
		t.Logf("%s", m.Lore("", "setup", "list").Stdout)
	})

	w.Step("before/team-started", func() {
		// The admin of job A starts the team on its own laptop, puts lore
		// in the team's app repo and invites Pat's work address.
		bapp := bm.Clone(appRemote, "app")
		for _, a := range []jJoinerAnswer{
			{"kind", map[string]any{"kind": "admin", "name": "acme"}},
			{"identity", map[string]any{"name": "Boss Acme", "email": jJoinerBossEmail}},
			{"github", map[string]any{"github_account": jJoinerLoginBoss, "host_alias": "github.com"}},
			{"personal_store", map[string]any{"personal_store": "none"}},
			{"team", map[string]any{"org": jJoinerOrg, "store_url": teamRemote.SSHURL()}},
			{"tools", map[string]any{"tools": []string{"claude_code"}}},
			{"secrets", map[string]any{"backend": "none"}},
			{"folders", map[string]any{"folders": []string{filepath.Join(w.Root, "work", bm.Name) + "/**"}, "default": true}},
			{"summary", map[string]any{}},
		} {
			jJoinerApply(t, bm, bapp.Dir, "acme", a)
		}
		bm.LoreIn(bapp, "init", "--name", "app").OK(t)
		t.Logf("%s", bm.LoreIn(bapp, "org", "link", jJoinerOrg, "--project", "app", "--yes"))
		bapp.CommitAll("lore init")
		bapp.AdminPush("origin", "main")
		r := bm.LoreIn(bapp, "team", "invite", jJoinerWorkEmail, "--yes", "--json").OK(t)
		var o struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal([]byte(r.Stdout), &o)
		invite = o.Text
		if !strings.HasPrefix(invite, "lore-invite:") {
			t.Fatalf("team invite printed no invite line: %s", r)
		}
	})

	steps := jJoinerSteps(invite, foldersA)
	var jobbBefore string
	cfgPath := m.LorePath("config.yaml")
	readCfg := func() string { b, _ := os.ReadFile(cfgPath); return string(b) }
	// which asserts which setup applies in dir and as whom, three times
	// over (stable: the answer never depends on the run).
	which := func(dir, setup, why, key string) {
		t.Helper()
		for range 3 {
			l := jJoinerSetups(t, m, dir)
			if l.Active.Name != setup || l.Active.Why != why {
				t.Errorf("[%s] in %s the active setup is %q (%s), want %q (%s)", w.CurrentStep(), rel(m.Home, dir), l.Active.Name, l.Active.Why, setup, why)
			}
			if key != "" && l.Here.IdentityKey != key {
				t.Errorf("[%s] in %s lore writes as %q, want %q", w.CurrentStep(), rel(m.Home, dir), l.Here.IdentityKey, key)
			}
		}
	}

	w.Step("day1/join-halfway", func() {
		// The invite arrives; Pat clones the team's repo and starts the
		// setup page (kind, identity, github), then closes it for the day.
		app = m.Clone(appRemote, "joba/app")
		jobbBefore = jJoinerJSON(jJoinerShow(t, m, jJoinerSetupB))
		before := readCfg()
		for _, a := range steps[:3] {
			jJoinerApply(t, m, app.Dir, jJoinerSetupA, a)
		}
		if after := readCfg(); after != before {
			t.Errorf("a team setup left before its join step changed config.yaml:\n--- before\n%s\n--- after\n%s", before, after)
		}
		// Nothing is saved yet, so job B still applies everywhere, and
		// lore says so instead of guessing.
		which(app.Dir, jJoinerSetupB, "folder", "human:"+jJoinerHomeEmail)
		which(site.Dir, jJoinerSetupB, "folder", "human:"+jJoinerHomeEmail)
	})

	jJoinerUnsaved := func(want bool) {
		t.Helper()
		l := jJoinerSetups(t, m, "")
		var r struct {
			Unsaved []struct {
				Name string   `json:"name"`
				Kind string   `json:"kind"`
				Done []string `json:"done"`
				Next string   `json:"next"`
			} `json:"unsaved"`
		}
		_ = json.Unmarshal([]byte(m.Lore("", "setup", "list", "--json").Stdout), &r)
		text := m.Lore("", "setup", "list").Stdout
		if !want {
			if len(r.Unsaved) != 0 || strings.Contains(text, "not saved yet") {
				t.Errorf("[%s] setup list still names an unsaved setup: %+v\n%s", w.CurrentStep(), r.Unsaved, text)
			}
			return
		}
		for _, s := range l.Setups {
			if s.Name == jJoinerSetupA {
				t.Errorf("[%s] the half-made setup %s is listed as saved: %+v", w.CurrentStep(), jJoinerSetupA, s)
			}
		}
		if len(r.Unsaved) != 1 || r.Unsaved[0].Name != jJoinerSetupA || r.Unsaved[0].Kind != "member" ||
			strings.Join(r.Unsaved[0].Done, ",") != "kind,identity,github" || r.Unsaved[0].Next != "personal_store" {
			t.Errorf("[%s] setup list --json unsaved = %+v, want joba (member) with kind,identity,github done and personal_store next", w.CurrentStep(), r.Unsaved)
		}
		if !strings.Contains(text, "not saved yet: "+jJoinerSetupA) || !strings.Contains(text, "lore setup --setup "+jJoinerSetupA) {
			t.Errorf("[%s] setup list does not tell how to go on with %s:\n%s", w.CurrentStep(), jJoinerSetupA, text)
		}
	}

	var afterJoin string
	w.AdvanceDays(1)
	w.Step("day2/come-back-and-finish", func() {
		// The next morning: lore still knows the half-made setup and the
		// page shows yesterday's answers.
		jJoinerUnsaved(true)
		r := m.Lore("", "setup", "plan", "--setup", jJoinerSetupA, "--json").OK(t)
		// The page puts each saved step's answers over the step's
		// defaults (web steps/logic.ts baselineAnswers): setup plan's
		// progress rows are what it shows.
		var plan struct {
			Progress []struct {
				Step    string                     `json:"step"`
				Answers map[string]json.RawMessage `json:"answers"`
			} `json:"progress"`
		}
		_ = json.Unmarshal([]byte(r.Stdout), &plan)
		got := map[string]string{}
		for _, p := range plan.Progress {
			for k, v := range p.Answers {
				var a struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(v, &a) != nil || a.Text == "" {
					a.Text = strings.Trim(string(v), `"`)
				}
				got[p.Step+"."+k] = a.Text
			}
		}
		for k, v := range map[string]string{"identity.email": jJoinerWorkEmail, "github.github_account": jJoinerLoginA, "kind.kind": "member"} {
			if got[k] != v {
				t.Errorf("coming back, the page shows %s = %q, yesterday's answer was %q (%v)", k, got[k], v, got)
			}
		}
		// The page offers job A "A local folder" as Pat's one personal
		// store `me`, shared with job B's setup, and names both.
		var q struct {
			Steps []struct {
				ID        string `json:"id"`
				Questions []struct {
					ID      string `json:"id"`
					Choices struct {
						Static []struct {
							Value  string `json:"value"`
							Detail string `json:"detail"`
						} `json:"static"`
					} `json:"choices"`
				} `json:"questions"`
			} `json:"steps"`
		}
		_ = json.Unmarshal([]byte(r.Stdout), &q)
		detail := ""
		for _, st := range q.Steps {
			for _, qq := range st.Questions {
				for _, c := range qq.Choices.Static {
					if st.ID == "personal_store" && qq.ID == "personal_store" && c.Value == "local" {
						detail = c.Detail
					}
				}
			}
		}
		if !strings.Contains(detail, "file://~/.lore/stores/me ") || !strings.Contains(detail, "shared with your setup "+jJoinerSetupB) {
			t.Errorf("the page offers job A the local folder %q; want ~/.lore/stores/me, shared with setup %s", detail, jJoinerSetupB)
		}
		for _, a := range steps[3:] {
			jJoinerApply(t, m, app.Dir, jJoinerSetupA, a)
		}
		jJoinerUnsaved(false)
		a := jJoinerShow(t, m, jJoinerSetupA)
		gh, _ := a["github"].(map[string]any)
		if gh["user"] != jJoinerLoginA || gh["host_alias"] != jJoinerAliasA {
			t.Errorf("joba's GitHub account is %v, Pat chose %s over %s", a["github"], jJoinerLoginA, jJoinerAliasA)
		}
		// Both jobs picked "A local folder": one personal store `me` per
		// person, so both setups share ~/.lore/stores/me.
		if ps, _ := a["personal_store"].(string); !strings.HasSuffix(ps, "/.lore/stores/me") || a["org"] != jJoinerOrg || jJoinerJSON(a["folders"]) != jJoinerJSON([]string{foldersA}) {
			t.Errorf("joba is saved as %v; want the shared personal store ~/.lore/stores/me, org %s, folders [%s]", a, jJoinerOrg, foldersA)
		}
		if ps, _ := jJoinerShow(t, m, jJoinerSetupB)["personal_store"].(string); !strings.HasSuffix(ps, "/.lore/stores/me") {
			t.Errorf("jobb's personal store is %q, want ~/.lore/stores/me", ps)
		}
		if b := jJoinerJSON(jJoinerShow(t, m, jJoinerSetupB)); b != jobbBefore {
			t.Errorf("joining job A changed the job B setup:\n%s\nwas\n%s", b, jobbBefore)
		}
		afterJoin = jJoinerJSON(a)
		which(app.Dir, jJoinerSetupA, "folder", "human:"+jJoinerWorkEmail)
		which(site.Dir, jJoinerSetupB, "folder", "human:"+jJoinerHomeEmail)
		which(work, jJoinerSetupB, "folder", "")
	})

	var teamNoteID string
	w.Step("day2/first-notes", func() {
		r := m.LoreIn(app, "memory", "add", "--body", jJoinerTeamNote+" ("+jJoinerSecret+")", "--json").OK(t)
		var o struct {
			ID    string `json:"id"`
			Store string `json:"store"`
		}
		_ = json.Unmarshal([]byte(r.Stdout), &o)
		teamNoteID = o.ID
		if o.Store == "me" || o.Store == "" {
			t.Errorf("a note in job A's repo went to store %q: %s", o.Store, r)
		}
		m.LoreIn(app, "sync").OK(t)
		if !jJoinerRemoteHas(w, teamRemote, jJoinerSecret) {
			t.Errorf("the team note never reached job A's team store")
		}
		m.LoreIn(site, "memory", "add", "--body", jJoinerBNote, "--to", "me").OK(t)
		m.LoreIn(app, "memory", "add", "--body", jJoinerANote, "--to", "me").OK(t)
		for _, n := range []string{jJoinerANote, jJoinerBNote} {
			if hits := jJoinerGrep(m.LorePath("stores", "me"), n); len(hits) == 0 {
				t.Errorf("Pat's own note %q is not in the shared personal store ~/.lore/stores/me", n)
			}
		}
		// Each repo reads its own job's team and repo notes, and Pat's own
		// notes from the one personal store.
		if l := m.LoreIn(app, "memory", "list").Stdout; strings.Contains(l, jJoinerHomeNote) || !strings.Contains(l, jJoinerTeamNote) || !strings.Contains(l, jJoinerANote) || !strings.Contains(l, jJoinerBNote) {
			t.Errorf("memory list in job A's repo:\n%s", l)
		}
		if l := m.LoreIn(site, "memory", "list").Stdout; strings.Contains(l, jJoinerTeamNote) || !strings.Contains(l, jJoinerHomeNote) || !strings.Contains(l, jJoinerANote) {
			t.Errorf("memory list in job B's repo:\n%s", l)
		}
		// `lore store show me` names the one personal store and both
		// setups sharing it, from either job's repo.
		for _, rp := range []*teamharness.Clone{app, site} {
			r := m.LoreIn(rp, "store", "show", "me", "--json").OK(t)
			var out struct {
				Push struct {
					Store    string   `json:"store"`
					URL      string   `json:"url"`
					Folder   bool     `json:"folder"`
					SharedBy []string `json:"shared_by"`
				} `json:"push"`
			}
			_ = json.Unmarshal([]byte(r.Stdout), &out)
			v := out.Push
			if v.Store != "me" || !strings.HasSuffix(v.URL, "/.lore/stores/me") || !v.Folder || jJoinerJSON(v.SharedBy) != jJoinerJSON([]string{jJoinerSetupB, jJoinerSetupA}) {
				t.Errorf("lore store show me in %s: want the folder store ~/.lore/stores/me shared by [%s %s], got %s", rp.Dir, jJoinerSetupB, jJoinerSetupA, r.Stdout)
			}
			txt := m.LoreIn(rp, "store", "show", "me").OK(t).Stdout
			if !strings.Contains(txt, "shared by your setups "+jJoinerSetupB+", "+jJoinerSetupA) || !strings.Contains(txt, "/.lore/stores/me") {
				t.Errorf("lore store show me does not say the personal store is shared by both setups:\n%s", txt)
			}
		}
	})
	w.Fact("job A's notes never reach job B's repo or personal store", func(c Ctx) {
		for _, root := range []string{site.Dir, m.LorePath("stores")} {
			if hits := jJoinerGrep(root, jJoinerSecret); len(hits) > 0 {
				c.Errorf("job A's note is in %v", hits)
			}
		}
		if jJoinerRemoteHas(w, siteRemote, jJoinerSecret) {
			c.Errorf("job A's note reached job B's GitHub repo")
		}
	})
	w.Fact("one personal store me per person, shared by both setups", func(c Ctx) {
		ents, _ := os.ReadDir(m.LorePath("stores"))
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "me-") {
				c.Errorf("a second local personal store ~/.lore/stores/%s exists; a person has one `me`", e.Name())
			}
		}
		for _, n := range []string{jJoinerANote, jJoinerBNote} {
			if hits := jJoinerGrep(m.LorePath("stores", "me"), n); len(hits) == 0 {
				c.Errorf("Pat's own note %q is not in ~/.lore/stores/me", n)
			}
		}
	})
	w.Fact("job B's notes and address never reach job A's stores", func(c Ctx) {
		for _, s := range []string{jJoinerHomeNote, "jobb personal", jJoinerHomeEmail} {
			if jJoinerRemoteHas(w, teamRemote, s) || jJoinerRemoteHas(w, appRemote, s) {
				c.Errorf("job A's GitHub repos hold %q", s)
			}
		}
	})

	w.AdvanceDays(1)
	w.Step("day3/edit-folders-and-back", func() {
		only := app.Dir + "/**"
		m.Lore("", "setup", "edit", jJoinerSetupA, "--folder", only).OK(t)
		which(app.Dir, jJoinerSetupA, "folder", "human:"+jJoinerWorkEmail)
		m.Lore("", "setup", "edit", jJoinerSetupA, "--folder", foldersA).OK(t)
		if a := jJoinerJSON(jJoinerShow(t, m, jJoinerSetupA)); a != afterJoin {
			t.Errorf("joba edited and edited back reads\n%s\nwas\n%s", a, afterJoin)
		}
		which(app.Dir, jJoinerSetupA, "folder", "human:"+jJoinerWorkEmail)
	})

	w.AdvanceDays(1)
	w.Step("day4/delete-note-and-restore", func() {
		m.LoreIn(app, "memory", "delete", teamNoteID, "--yes").OK(t)
		if l := m.LoreIn(app, "memory", "list").Stdout; strings.Contains(l, jJoinerTeamNote) {
			t.Errorf("a deleted note is still listed:\n%s", l)
		}
		m.LoreIn(app, "trash", "restore", teamNoteID).OK(t)
		if l := m.LoreIn(app, "memory", "list").Stdout; !strings.Contains(l, jJoinerTeamNote) {
			t.Errorf("the restored note is not listed:\n%s", l)
		}
	})

	w.AdvanceDays(1)
	w.Step("day5/remove-setup-and-restore", func() {
		m.Lore("", "setup", "remove", jJoinerSetupA).OK(t)
		// Job A's folders fall back to job B's setup while joba is in the
		// trash, but job A's repo still belongs to org acme (its orgs[]
		// entry stays until a purge), so what Pat writes there is still
		// stamped with job A's address, never job B's.
		which(app.Dir, jJoinerSetupB, "folder", "human:"+jJoinerWorkEmail)
		m.Lore("", "setup", "trash", "restore", jJoinerSetupA).OK(t)
		if a := jJoinerJSON(jJoinerShow(t, m, jJoinerSetupA)); a != afterJoin {
			t.Errorf("joba restored from the trash reads\n%s\nwas\n%s", a, afterJoin)
		}
		which(app.Dir, jJoinerSetupA, "folder", "human:"+jJoinerWorkEmail)
		which(site.Dir, jJoinerSetupB, "folder", "human:"+jJoinerHomeEmail)
	})

	rejoin := func() {
		t.Helper()
		for _, a := range jJoinerSteps(invite, foldersA) {
			jJoinerApply(t, m, app.Dir, jJoinerSetupA, a)
		}
	}
	w.AdvanceDays(1)
	w.Step("day6/purge-then-regret", func() {
		m.Lore("", "setup", "remove", jJoinerSetupA).OK(t)
		m.Lore("", "setup", "trash", "purge", jJoinerSetupA, "--confirm").OK(t)
		if c := readCfg(); strings.Contains(c, jJoinerWorkEmail) {
			t.Errorf("after the purge config.yaml still names job A's address:\n%s", c)
		}
		// Regret: Pat joins again with the same invite line.
		rejoin()
		which(app.Dir, jJoinerSetupA, "folder", "human:"+jJoinerWorkEmail)
		if l := m.LoreIn(app, "memory", "list").Stdout; !strings.Contains(l, jJoinerTeamNote) || !strings.Contains(l, jJoinerANote) {
			t.Errorf("after joining again the team note or Pat's own job A note is gone:\n%s", l)
		}
		if b := jJoinerJSON(jJoinerShow(t, m, jJoinerSetupB)); b != jobbBefore {
			t.Errorf("purging and re-joining job A changed the job B setup:\n%s\nwas\n%s", b, jobbBefore)
		}
	})

	w.AdvanceDays(90)
	w.Step("month3/leave-job-a", func() {
		meBefore := jJoinerTree(t, m.LorePath("stores", "me"))
		m.Lore("", "setup", "remove", jJoinerSetupA).OK(t)
		r := m.Lore("", "setup", "trash", "purge", jJoinerSetupA, "--confirm").OK(t)
		// The purge says the team's notes stay on this machine until
		// Pat forgets the store, and how.
		if !strings.Contains(r.Stdout, "lore store forget "+teamRemote.SSHURL()) {
			t.Errorf("leaving job A, the purge does not say its notes stay on this machine nor how to drop them:\n%s", r.Stdout)
		}
		// The personal store `me` is named as staying, shared by all of
		// Pat's setups and never touched by the purge.
		if !strings.Contains(r.Stdout, "/.lore/stores/me") || !strings.Contains(r.Stdout, "shared by all your setups") {
			t.Errorf("leaving job A, the purge does not say the shared personal store me stays untouched:\n%s", r.Stdout)
		}
		m.Lore("", "store", "forget", teamRemote.SSHURL()).OK(t)
		if err := os.RemoveAll(app.Dir); err != nil { // Pat deletes job A's checkout
			t.Fatal(err)
		}
		if os.RemoveAll(filepath.Dir(app.Dir)) != nil {
			t.Fatal("remove the joba folder")
		}
		m.Lore("", "cache", "prune", "--yes").OK(t) // as the purge said
		c := readCfg()
		for _, s := range []string{jJoinerWorkEmail, jJoinerLoginA, "acme-co"} {
			if strings.Contains(c, s) {
				t.Errorf("after leaving job A config.yaml still names %q:\n%s", s, c)
			}
		}
		// Nothing of job A's team notes is left anywhere under ~/.lore
		// (stores, clones, caches), and no store or clone names its address.
		if hits := jJoinerGrep(m.LorePath(), jJoinerSecret); len(hits) > 0 {
			t.Errorf("after leaving job A, ~/.lore still holds %q in %v", jJoinerSecret, hits)
		}
		// Leaving job A never touches `me`: the same files, Pat's own
		// notes from both jobs still in it.
		if after := jJoinerTree(t, m.LorePath("stores", "me")); after != meBefore {
			t.Errorf("leaving job A changed the personal store me:\n%s\nwas\n%s", after, meBefore)
		}
		// No store or clone but `me` names job A's address: Pat's own
		// note written at job A carries the actor of that address, and `me`
		// is Pat's own across jobs, so it keeps it.
		for _, d := range []string{"stores", "clones", "clones-v017"} {
			var hits []string
			for _, h := range jJoinerGrep(m.LorePath(d), jJoinerWorkEmail) {
				if !strings.HasPrefix(h, m.LorePath("stores", "me")+string(filepath.Separator)) {
					hits = append(hits, h)
				}
			}
			if len(hits) > 0 {
				t.Errorf("after leaving job A, ~/.lore/%s still names its address in %v", d, hits)
			}
		}
		which(site.Dir, jJoinerSetupB, "folder", "human:"+jJoinerHomeEmail)
		m.LoreIn(site, "memory", "add", "--body", "jobb keeps working after job A").OK(t)
		if l := m.LoreIn(site, "memory", "list").Stdout; !strings.Contains(l, jJoinerBNote) || !strings.Contains(l, jJoinerANote) || !strings.Contains(l, jJoinerHomeNote) {
			t.Errorf("after leaving job A, job B's notes are not all there:\n%s", l)
		}
		if b := jJoinerJSON(jJoinerShow(t, m, jJoinerSetupB)); b != jobbBefore {
			t.Errorf("leaving job A changed the job B setup:\n%s\nwas\n%s", b, jobbBefore)
		}
	})
}

// jJoinerTree is every file under dir (but .git) with a hash of its
// content, one per line, sorted: two equal trees read the same.
func jJoinerTree(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		lines = append(lines, fmt.Sprintf("%s %x", rel, sha256.Sum256(b)))
		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
