//go:build journeys && !windows

package journeys

// teamlead_test.go — persona 3, the team lead: starts a team, invites
// three people (each on their own machine, sharing the team store's bare
// repo), one goes on leave, one changes name, the admin changes, a
// teammate's laptop is offline for a week and comes back with unpushed
// work, two people edit the same row at once, the store is compacted while
// one clone has unpushed work; plus every undo path a lead takes.
//
//	TestJourneyTeamLeadTeam  the team's first weeks (day 1 → week 4)
//	TestJourneyTeamLeadUndo  the undo paths, on a team of two

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The team's names (placeholders).
const (
	tlOrg       = "acmeteam"
	tlProject   = "platform"
	tlStoreSlug = "github.com/acme-co/lore-team"
	tlAppSlug   = "github.com/acme-co/app"
	tlLeadSetup = "lead"
	tlTeamSetup = "acme"
	tlFolders   = "~/code/**"
	tlRuleBody  = "run the linter before every push"
)

var (
	tlLee = tlPerson{"Lee Lead", "lee@example.test", "lee-gh"}
	tlAnn = tlPerson{"Ann Able", "ann@example.test", "ann-gh"}
	tlBob = tlPerson{"Bob Brook", "bob@example.test", "bob-gh"}
	tlCat = tlPerson{"Cat Cole", "cat@example.test", "cat-gh"}
	tlDan = tlPerson{"Dan Dale", "dan@example.test", "dan-gh"}
)

// tlTeam is one team's world: the people, their laptops and app clones.
type tlTeam struct {
	t     *testing.T
	w     *World
	store *teamharness.Remote
	app   *teamharness.Remote
	m     map[string]*Machine
	repo  map[string]*teamharness.Clone
}

func newTLTeam(t *testing.T) *tlTeam {
	w := NewWorld(t)
	return &tlTeam{t: t, w: w, store: w.Remote(tlStoreSlug), app: w.Remote(tlAppSlug),
		m: map[string]*Machine{}, repo: map[string]*teamharness.Clone{}}
}

// person gives p a laptop with their GitHub login and the app cloned at
// ~/code/app (inside the setup's folders).
func (tt *tlTeam) person(p tlPerson) *Machine {
	m := tt.w.Persona(p.Name, p.Email).Laptop()
	tlGitHub(tt.t, tt.w, m, p.Login)
	c := m.Clone(tt.app, "app")
	m.MoveClone(tt.t, c, filepath.Join(m.Home, "code", "app"))
	tt.m[p.Email], tt.repo[p.Email] = m, c
	return m
}

// lore runs lore in p's app clone.
func (tt *tlTeam) lore(p tlPerson, args ...string) teamharness.Result {
	tt.t.Helper()
	return tt.m[p.Email].LoreIn(tt.repo[p.Email], args...)
}

// tlJSON decodes a --json answer into v, failing the test when it is not JSON.
func tlJSON(t testing.TB, r teamharness.Result, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.Stdout), v); err != nil {
		t.Fatalf("not JSON (%v): %s", err, r)
	}
}

// tlActor is the part of an actor row a lead reads.
type tlActor struct {
	ID         string `json:"id"`
	Display    string `json:"display_name"`
	StableKey  string `json:"stable_key"`
	Status     string `json:"status"`
	LeaveUntil string `json:"leave_until"`
}

// tlRows is the data array of a list --json answer.
func tlRows(t testing.TB, r teamharness.Result) []map[string]any {
	t.Helper()
	var out struct {
		Data []map[string]any `json:"data"`
	}
	tlJSON(t, r, &out)
	return out.Data
}

// roster is p's `actor list --json`, keyed by stable key.
func (tt *tlTeam) roster(t testing.TB, p tlPerson) map[string]tlActor {
	t.Helper()
	r := tt.lore(p, "actor", "list", "--json").OK(t)
	var out struct {
		Data []tlActor `json:"data"`
	}
	tlJSON(t, r, &out)
	m := map[string]tlActor{}
	for _, a := range out.Data {
		m[a.StableKey] = a
	}
	return m
}

// TestJourneyTeamLeadTeam is the team's first weeks.
func TestJourneyTeamLeadTeam(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)

	w.Step("day1/start-team", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "setup", "list").OK(t).Says(t, tlLeadSetup, "admin", tlOrg)
		tt.lore(tlLee, "whoami").OK(t).Says(t, tlOrg, tlLee.Email)
		if a, ok := tt.roster(t, tlLee)["human:"+tlLee.Email]; !ok || a.Display != tlLee.Name {
			t.Errorf("the lead is not on the roster as %s: %+v", tlLee.Name, tt.roster(t, tlLee))
		}
		if !tt.store.Has("refs/heads/main") {
			t.Error("the team store has no main branch after starting the team")
		}
	})

	var ruleID, annNote, leeNote string
	ruleBody := tlRuleBody
	w.Step("day1/team-project", func() {
		// the lead makes app a lore repo for everyone: init, commit, push
		tt.lore(tlLee, "init").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		r := tt.lore(tlLee, "memory", "add", tlRuleBody, "--to", tlOrg+"/"+tlProject, "--create", "--json").OK(t)
		var o struct {
			ID string `json:"id"`
		}
		tlJSON(t, r, &o)
		ruleID = o.ID
		tt.lore(tlLee, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t).Says(t, tlProject)
		tt.seesRule(t, tlLee, ruleID, tlRuleBody)
		// The folder the rule made is a project the lead can name.
		tt.lore(tlLee, "project", "list").OK(t).Says(t, tlProject)
	})
	w.Fact("the team store keeps its marker on main", func(c Ctx) {
		if _, ok := tt.store.Read("refs/heads/main", "lore-store.yaml"); !ok {
			c.Errorf("lore-store.yaml is gone from the team store's main")
		}
	})

	lines := map[string]string{}
	w.Step("day1/invite-three", func() {
		for _, p := range []tlPerson{tlAnn, tlBob, tlCat} {
			_, lines[p.Email] = tlInvite(t, lee, tt.repo[tlLee.Email].Dir, p.Email)
		}
		tt.invites(t, tlLee, map[string]string{tlAnn.Email: "pending", tlBob.Email: "pending", tlCat.Email: "pending"})
	})

	for _, p := range []tlPerson{tlAnn, tlBob, tlCat} {
		w.Step("day1/"+strings.ToLower(strings.Fields(p.Name)[0])+"-joins", func() {
			m := tt.person(p)
			tlJoin(t, m, tt.repo[p.Email].Dir, tlTeamSetup, lines[p.Email], p, []string{tlFolders})
			tt.lore(p, "setup", "list").OK(t).Says(t, tlTeamSetup, "member", tlOrg)
			tt.lore(p, "whoami").OK(t).Says(t, tlOrg, p.Email)
			tt.seesRule(t, p, ruleID, tlRuleBody)
			tt.lore(tlLee, "sync", "run").OK(t)
			if a, ok := tt.roster(t, tlLee)["human:"+p.Email]; !ok || a.Display != p.Name {
				t.Errorf("the lead's roster has no %s after they joined: %+v", p.Name, a)
			}
			tt.invites(t, tlLee, map[string]string{p.Email: "accepted"})
		})
	}

	w.Step("week1/bob-on-leave", func() {
		w.AdvanceDays(3)
		from, until := w.Now().Format("2006-01-02"), w.Now().AddDate(0, 0, 14).Format("2006-01-02")
		tt.lore(tlLee, "actor", "status", "@bob", "on_leave", "--from", from, "--until", until, "--note", "parental leave").OK(t).Says(t, "on_leave")
		tt.lore(tlAnn, "sync", "run").OK(t)
		if a := tt.roster(t, tlAnn)["human:"+tlBob.Email]; a.Status != "on_leave" || a.LeaveUntil != until {
			t.Errorf("a teammate does not see Bob on leave until %s: %+v", until, a)
		}
		tt.lore(tlAnn, "actor", "list").OK(t).Says(t, "on_leave until "+until)
	})

	w.Step("week1/cat-changes-name", func() {
		w.AdvanceDays(1)
		w.Personas()[3].Rename(t, "Cat Doe", tlCat.Email)
		tlApply(t, tt.m[tlCat.Email], tt.repo[tlCat.Email].Dir, tlTeamSetup, "identity", map[string]any{"name": "Cat Doe", "email": tlCat.Email})
		tt.lore(tlCat, "whoami").OK(t).Says(t, "Cat Doe")
		// The new name reaches the roster: hers, and the lead's after a sync.
		if a := tt.roster(t, tlCat)["human:"+tlCat.Email]; a.Display != "Cat Doe" {
			t.Errorf("Cat's own roster still names %q after the rename", a.Display)
		}
		tt.lore(tlLee, "sync", "run").OK(t)
		if a := tt.roster(t, tlLee)["human:"+tlCat.Email]; a.Display != "Cat Doe" {
			t.Errorf("the lead's roster still names %q after Cat renamed", a.Display)
		}
	})

	w.Step("week2/same-row-at-once", func() {
		w.AdvanceDays(4)
		tt.lore(tlAnn, "sync", "run").OK(t)
		edits := map[string]string{tlLee.Email: tlRuleBody + " and the tests", tlAnn.Email: tlRuleBody + " (fix what it finds)"}
		done := make(chan teamharness.Result, 2)
		for _, p := range []tlPerson{tlLee, tlAnn} {
			go func() { done <- tt.lore(p, "memory", "edit", ruleID, "--body", edits[p.Email]) }()
		}
		for range 2 {
			(<-done).OK(t)
		}
		// Whoever pushed second finds out at their next sync.
		winner, loser := tlLee, tlAnn
		if r := tt.lore(tlLee, "sync", "run"); r.Code != 0 {
			winner, loser = tlAnn, tlLee
		}
		tt.lore(winner, "sync", "run").OK(t)
		r := tt.lore(loser, "sync", "run")
		r.Fails(t, r.Code, "E_STALE", ruleID, "both edits are kept")
		if r.Code == 0 {
			t.Fatalf("both edits of one row synced without a word: %s", r)
		}
		// The conflict is reported once, under the store folder that holds
		// the row; the store's other folders say their push waits on it
		// (owner decision 7: one clone pushes every folder at once).
		holder := tlOrg + "/" + tlProject
		heldLine := "held: waiting on " + holder + "'s conflict (" + ruleID + ")"
		if n := strings.Count(r.Out(), "refused 1"); n != 1 {
			t.Errorf("sync reports the conflict %d times, want once under %s:\n%s", n, holder, r)
		}
		r.Says(t, "store "+holder+": sent 0, received 0, merged 0, refused 1", "store "+tlOrg+"/org: "+heldLine, "store "+tlOrg+": "+heldLine)
		st := tt.lore(loser, "sync", "status").OK(t).Says(t, ruleID, "edit it here so both agree")
		st.Never(t, "push rule", "store "+tlOrg+"/org: E_STALE", "store "+tlOrg+": E_STALE")
		st.Says(t, "refused pushes:   1", "store "+holder+": E_STALE", "held pushes:      2",
			"store "+tlOrg+"/org: "+heldLine, "store "+tlOrg+": "+heldLine)
		// Nothing is lost: each side still reads its own edit.
		if got := tt.body(t, winner, ruleID); got != edits[winner.Email] {
			t.Errorf("the pushed edit reads %q", got)
		}
		if got := tt.body(t, loser, ruleID); got != edits[loser.Email] {
			t.Errorf("the held-back edit reads %q, want it kept: %q", got, edits[loser.Email])
		}
		// They agree on the winner's text; the loser makes theirs match.
		tt.lore(loser, "show", ruleID).OK(t).Says(t, edits[loser.Email])
		tt.lore(loser, "memory", "edit", ruleID, "--body", edits[winner.Email]).OK(t)
		tt.lore(loser, "sync", "run").OK(t)
		tt.lore(loser, "sync", "status").OK(t).Says(t, "refused pushes:   0")
		tt.lore(winner, "sync", "run").OK(t)
		for _, p := range []tlPerson{winner, loser} {
			tt.seesRule(t, p, ruleID, edits[winner.Email])
		}
		ruleBody = edits[winner.Email]
	})

	w.Step("week2/ann-offline-a-week", func() {
		w.AdvanceDays(1)
		ann := tt.m[tlAnn.Email]
		ann.Offline()
		r := tt.lore(tlAnn, "memory", "add", "flaky checkout test: retry once", "--to", tlOrg+"/"+tlProject, "--json").OK(t)
		r.Says(t, "unreachable", "lore sync")
		var o struct {
			ID string `json:"id"`
		}
		tlJSON(t, r, &o)
		annNote = o.ID
		tt.lore(tlAnn, "sync", "run").OK(t).Says(t, "offline", "kept")
		tt.lore(tlAnn, "store", "doctor").Says(t, "unreachable")
		r = tt.lore(tlLee, "memory", "add", "release branch cut on thursdays", "--to", tlOrg+"/"+tlProject, "--json").OK(t)
		tlJSON(t, r, &o)
		leeNote = o.ID
		if tt.body(t, tlLee, annNote) != "" {
			t.Error("the lead sees a note that never left Ann's laptop")
		}
	})

	w.Step("week3/ann-back-online", func() {
		w.AdvanceDays(7)
		tt.m[tlAnn.Email].Online()
		r := tt.lore(tlAnn, "sync", "run").OK(t)
		r.Never(t, "offline")
		tt.lore(tlAnn, "sync", "status").OK(t).Says(t, "refused pushes:   0")
		tt.lore(tlLee, "sync", "run").OK(t)
		for _, p := range []tlPerson{tlLee, tlAnn} {
			if tt.body(t, p, annNote) == "" || tt.body(t, p, leeNote) == "" {
				t.Errorf("%s does not see both notes of the offline week", p.Name)
			}
			tt.seesRule(t, p, ruleID, ruleBody)
		}
	})
	w.Fact("the offline week's notes stay on the team store", func(c Ctx) {
		for _, id := range []string{annNote, leeNote} {
			if len(tt.store.FilesMatching("refs/heads/main", id)) == 0 {
				c.Errorf("%s is not on the team store's main", id)
			}
		}
	})

	w.Step("week3/admin-changes", func() {
		w.AdvanceDays(2)
		ann, dir := tt.m[tlAnn.Email], tt.repo[tlAnn.Email].Dir
		// Ann takes over: her member setup becomes the org's admin setup.
		tlApply(t, ann, dir, tlTeamSetup, "kind", map[string]any{"kind": "admin", "name": tlTeamSetup})
		tlApply(t, ann, dir, tlTeamSetup, "team", map[string]any{"org": tlOrg, "store_url": tt.store.SSHURL()})
		tt.lore(tlAnn, "setup", "list").OK(t).Says(t, tlTeamSetup, "admin", tlOrg)
		// Lee hands over and leaves the team.
		tt.lore(tlAnn, "actor", "offboard", "@lee", "--reason", "moved to another team", "--yes").OK(t).Says(t, "left")
		tt.lore(tlLee, "sync", "run").OK(t)
		if a := tt.roster(t, tlLee)["human:"+tlLee.Email]; a.Status != "left" {
			t.Errorf("Lee's own roster says %q after the offboard", a.Status)
		}
		tt.lore(tlLee, "setup", "remove", tlLeadSetup).OK(t)
		tt.lore(tlLee, "team", "invite", tlDan.Email, "--yes").Refused(t)
		// The new admin invites the next hire, who joins.
		_, line := tlInvite(t, ann, dir, tlDan.Email)
		dan := tt.person(tlDan)
		tlJoin(t, dan, tt.repo[tlDan.Email].Dir, tlTeamSetup, line, tlDan, []string{tlFolders})
		tt.seesRule(t, tlDan, ruleID, ruleBody)
		tt.lore(tlAnn, "sync", "run").OK(t)
		tt.invites(t, tlAnn, map[string]string{tlDan.Email: "accepted"})
		ros := tt.roster(t, tlAnn)
		if ros["human:"+tlDan.Email].Status != "active" || ros["human:"+tlLee.Email].Status != "left" {
			t.Errorf("the new admin's roster: dan %+v lee %+v", ros["human:"+tlDan.Email], ros["human:"+tlLee.Email])
		}
	})

	w.Step("week3/bob-back-from-leave", func() {
		w.AdvanceDays(5)
		tt.lore(tlAnn, "actor", "status", "@bob", "active").OK(t)
		tt.lore(tlBob, "sync", "run").OK(t)
		if a := tt.roster(t, tlBob)["human:"+tlBob.Email]; a.Status != "active" || a.LeaveUntil != "" {
			t.Errorf("Bob is back but his roster says %+v", a)
		}
		tt.lore(tlBob, "actor", "list").OK(t).Never(t, "on_leave")
	})

	w.Step("week4/compact-with-unpushed-work", func() {
		w.AdvanceDays(3)
		cat := tt.m[tlCat.Email]
		tt.lore(tlCat, "sync", "run").OK(t)
		cat.Offline()
		r := tt.lore(tlCat, "memory", "add", "cache warmup takes five minutes", "--to", tlOrg+"/"+tlProject, "--json").OK(t)
		var o struct {
			ID string `json:"id"`
		}
		tlJSON(t, r, &o)
		before := tt.store.CommitCount("refs/heads/main")
		// Ann squashes everything older than a week; every row is kept.
		tt.lore(tlAnn, "store", "compact", tlOrg, "--before", "7d", "--yes").OK(t).
			Says(t, "compacted "+tlOrg, "row file(s) kept")
		if after := tt.store.CommitCount("refs/heads/main"); after >= before {
			t.Errorf("compact left %d commits on main (was %d)", after, before)
		}
		// Cat comes back online: her note, written on the old history, still lands.
		cat.Online()
		tt.lore(tlCat, "sync", "run").OK(t)
		for _, p := range []tlPerson{tlAnn, tlBob} {
			tt.lore(p, "sync", "run").OK(t)
			if tt.body(t, p, o.ID) == "" {
				t.Errorf("Cat's note written before the compact never reached %s", p.Name)
			}
		}
		for _, p := range []tlPerson{tlAnn, tlCat, tlBob} {
			if tt.body(t, p, ruleID) != ruleBody || tt.body(t, p, annNote) == "" {
				t.Errorf("%s lost rows across the compact", p.Name)
			}
		}
	})
}

// TestJourneyTeamLeadUndo is every undo path a team lead takes, on a team
// of two (Lee the lead, Ann) plus a joiner who leaves the setup page
// halfway: a mistyped invite revoked, a rule deleted then restored, purged
// then regretted, edited then edited back, a setup removed then restored,
// leave started then ended, an offboard then a rejoin.
func TestJourneyTeamLeadUndo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	leeDir := tt.repo[tlLee.Email].Dir
	var ruleID string
	w.Step("day1/team-of-two", func() {
		tlStartTeam(t, lee, leeDir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "init").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		r := tt.lore(tlLee, "memory", "add", tlRuleBody, "--to", tlOrg+"/"+tlProject, "--create", "--json").OK(t)
		var o struct {
			ID string `json:"id"`
		}
		tlJSON(t, r, &o)
		ruleID = o.ID
		tt.lore(tlLee, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t)
		// A folder made on its own first, written to later: a project too.
		tt.lore(tlLee, "store", "folder", "add", "team", "design").OK(t)
		tt.lore(tlLee, "memory", "add", "icons are svg only", "--to", tlOrg+"/design").OK(t)
		tt.lore(tlLee, "project", "list").OK(t).Says(t, tlProject, "design")
	})

	w.Step("day1/invite-revoked-then-resent", func() {
		// A typo: revoked before anyone used it.
		typoID, _ := tlInvite(t, lee, leeDir, "ann@exmaple.test")
		tt.lore(tlLee, "team", "invite", "revoke", typoID, "--yes").OK(t)
		// The right address, but the lead forgot the project: revoke, invite again.
		firstID, first := tlInvite(t, lee, leeDir, tlAnn.Email)
		tt.lore(tlLee, "team", "invite", "revoke", firstID, "--yes").OK(t)
		tt.invites(t, tlLee, map[string]string{"ann@exmaple.test": "revoked", tlAnn.Email: "revoked"})
		_, line := tlInvite(t, lee, leeDir, tlAnn.Email, "--project", tlProject, "--role", "dev")
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		// Ann pastes the first line she got: refused, saying why, nothing joined.
		b, _ := json.Marshal(map[string]any{"invite": first})
		ann.LoreIn(tt.repo[tlAnn.Email], "setup", "apply", tlTeamSetup, "--step", "join", "--answers", string(b), "--json").Refused(t, "revoked")
		tlApply(t, ann, dir, tlTeamSetup, "join", map[string]any{"invite": line})
		tlLastSteps(t, ann, dir, tlTeamSetup, []string{tlFolders})
		tt.seesRule(t, tlAnn, ruleID, tlRuleBody)
		tt.lore(tlLee, "sync", "run").OK(t)
		got := map[string]int{}
		var out struct {
			Invites []map[string]any `json:"invites"`
		}
		tlJSON(t, tt.lore(tlLee, "team", "invite", "list", "--json").OK(t), &out)
		for _, row := range out.Invites {
			e, _ := row["email"].(string)
			st, _ := row["status"].(string)
			got[e+" "+st]++
		}
		if got[tlAnn.Email+" revoked"] != 1 || got[tlAnn.Email+" accepted"] != 1 || got["ann@exmaple.test revoked"] != 1 {
			t.Errorf("the lead's invite list after the resend: %v", got)
		}
	})

	w.Step("week1/edit-then-edit-back", func() {
		w.AdvanceDays(2)
		tt.lore(tlAnn, "memory", "edit", ruleID, "--body", "run the linter when you remember").OK(t)
		tt.lore(tlLee, "sync", "run").OK(t)
		tt.seesRule(t, tlLee, ruleID, "run the linter when you remember")
		tt.lore(tlLee, "memory", "edit", ruleID, "--body", tlRuleBody).OK(t)
		tt.lore(tlAnn, "sync", "run").OK(t)
		tt.seesRule(t, tlAnn, ruleID, tlRuleBody)
	})

	w.Step("week1/delete-then-restore", func() {
		w.AdvanceDays(1)
		tt.lore(tlLee, "memory", "delete", ruleID, "--yes").OK(t).Says(t, "trash restore "+ruleID)
		tt.lore(tlAnn, "sync", "run").OK(t)
		if tt.body(t, tlAnn, ruleID) != "" {
			t.Error("Ann still sees the rule the lead deleted")
		}
		tt.lore(tlLee, "trash", "restore", ruleID, "--yes").OK(t)
		tt.lore(tlAnn, "sync", "run").OK(t)
		for _, p := range []tlPerson{tlLee, tlAnn} {
			tt.seesRule(t, p, ruleID, tlRuleBody)
		}
	})

	w.Step("week2/purge-then-regret", func() {
		w.AdvanceDays(4)
		r := tt.lore(tlLee, "memory", "add", "staging password rotates monthly", "--to", tlOrg+"/"+tlProject, "--json").OK(t)
		var o struct {
			ID string `json:"id"`
		}
		tlJSON(t, r, &o)
		tt.lore(tlAnn, "sync", "run").OK(t)
		tt.lore(tlLee, "memory", "delete", o.ID, "--yes").OK(t)
		tt.lore(tlLee, "trash", "purge", o.ID, "--confirm").OK(t)
		// Regret: the restore says it is gone for good, and why.
		tt.lore(tlLee, "trash", "restore", o.ID, "--yes").Refused(t, o.ID, "purged", "new row")
		tt.lore(tlAnn, "sync", "run").OK(t)
		if tt.body(t, tlAnn, o.ID) != "" {
			t.Error("the purged note is still on Ann's laptop")
		}
		tt.lore(tlAnn, "trash", "list").OK(t).Never(t, o.ID)
		tt.seesRule(t, tlAnn, ruleID, tlRuleBody)
	})

	w.Step("week2/remove-setup-then-re-add", func() {
		w.AdvanceDays(1)
		before := tt.roster(t, tlAnn)["human:"+tlAnn.Email].ID
		tt.lore(tlAnn, "setup", "remove", tlTeamSetup).OK(t)
		tt.lore(tlAnn, "setup", "list").OK(t).Never(t, "member of "+tlOrg)
		tt.lore(tlAnn, "setup", "trash", "restore", tlTeamSetup).OK(t)
		tt.lore(tlAnn, "setup", "list").OK(t).Says(t, tlTeamSetup, "member", tlOrg)
		tt.lore(tlAnn, "whoami").OK(t).Says(t, tlAnn.Email, before)
		tt.seesRule(t, tlAnn, ruleID, tlRuleBody)
	})

	w.Step("week3/leave-then-back", func() {
		w.AdvanceDays(5)
		from := w.Now().Format("2006-01-02")
		tt.lore(tlLee, "actor", "status", "@ann", "on_leave", "--from", from).OK(t)
		tt.lore(tlAnn, "sync", "run").OK(t)
		if a := tt.roster(t, tlAnn)["human:"+tlAnn.Email]; a.Status != "on_leave" {
			t.Errorf("Ann's roster says %q while on leave", a.Status)
		}
		tt.lore(tlLee, "actor", "status", "@ann", "active").OK(t)
		tt.lore(tlAnn, "sync", "run").OK(t)
		if a := tt.roster(t, tlAnn)["human:"+tlAnn.Email]; a.Status != "active" || a.LeaveUntil != "" {
			t.Errorf("Ann is back but her roster says %+v", a)
		}
	})

	w.Step("week4/offboard-then-rejoin", func() {
		w.AdvanceDays(7)
		id := tt.roster(t, tlLee)["human:"+tlAnn.Email].ID
		tt.lore(tlLee, "actor", "offboard", "@ann", "--reason", "contract ended", "--yes").OK(t).Says(t, "left")
		tt.lore(tlAnn, "sync", "run").OK(t)
		if a := tt.roster(t, tlAnn)["human:"+tlAnn.Email]; a.Status != "left" {
			t.Errorf("Ann's roster says %q after the offboard", a.Status)
		}
		w.AdvanceDays(14)
		tt.lore(tlLee, "actor", "rejoin", "@ann", "--type", "employee", "--yes").OK(t)
		tt.lore(tlAnn, "sync", "run").OK(t)
		a := tt.roster(t, tlAnn)["human:"+tlAnn.Email]
		if a.Status != "active" || a.ID != id {
			t.Errorf("after the rejoin Ann's actor is %+v, want %s active", a, id)
		}
		tt.lore(tlAnn, "actor", "period", "list", "@ann").OK(t).Says(t, "contract ended")
	})

	w.Step("week4/join-page-left-halfway", func() {
		_, dline := tlInvite(t, lee, leeDir, tlDan.Email)
		dan := tt.person(tlDan)
		dir := tt.repo[tlDan.Email].Dir
		tlFirstSteps(t, dan, dir, tlTeamSetup, "member", tlDan)
		// Dan closes the page; the half-made setup is not a working one.
		tt.lore(tlDan, "whoami").Never(t, "member of "+tlOrg)
		w.AdvanceDays(1)
		tt.lore(tlDan, "setup", "list").OK(t).Says(t, tlTeamSetup)
		tlApply(t, dan, dir, tlTeamSetup, "join", map[string]any{"invite": dline})
		tlLastSteps(t, dan, dir, tlTeamSetup, []string{tlFolders})
		tt.lore(tlDan, "setup", "list").OK(t).Says(t, tlTeamSetup, "member", tlOrg)
		tt.seesRule(t, tlDan, ruleID, tlRuleBody)
	})
}

// seesRule checks p's app clone lists the team rule with its body.
func (tt *tlTeam) seesRule(t testing.TB, p tlPerson, id, body string) {
	t.Helper()
	if got := tt.body(t, p, id); got != body {
		t.Errorf("%s's app shows the team rule %s as %q, want %q", p.Name, id, got, body)
	}
}

// body is the memory's body in p's app clone ("" when p does not see it).
func (tt *tlTeam) body(t testing.TB, p tlPerson, id string) string {
	t.Helper()
	for _, row := range tlRows(t, tt.lore(p, "memory", "list", "--json").OK(t)) {
		if row["id"] == id {
			b, _ := row["body"].(string)
			return b
		}
	}
	return ""
}

// invites checks p's `team invite list` gives each email the status.
func (tt *tlTeam) invites(t testing.TB, p tlPerson, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	var out struct {
		Invites []map[string]any `json:"invites"`
	}
	tlJSON(t, tt.lore(p, "team", "invite", "list", "--json").OK(t), &out)
	for _, row := range out.Invites {
		e, _ := row["email"].(string)
		st, _ := row["status"].(string)
		got[e] = st
	}
	for e, st := range want {
		if got[e] != st {
			t.Errorf("invite for %s is %q in %s's list, want %q (all: %v)", e, got[e], p.Name, st, got)
		}
	}
}
