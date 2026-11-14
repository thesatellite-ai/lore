//go:build journeys && !windows

package journeys

// land7_d_test.go — three things a person hits after an upgrade or an email
// change, each found as a limit of round 6 and fixed in round 7:
//
//	TestJourneyLand7DGitNameUpgrade   0.1.17 with no identity: its actor row
//	    says "una" (the email's local part); the new lore shows git's
//	    user.name. `lore migrate run` refreshes the copy, so doctor is clean.
//	TestJourneyLand7DTwoActors        an email change made under 0.1.17 left
//	    two actors of one name: doctor names both and the merge.
//	TestJourneyLand7DAgentAfterEmail  a new laptop that only knows the new
//	    email: the coding agent writes as the same agent actor as before.
//
// (Two folders holding one push is unit-tested: ops/stores syncheld_test.go,
// wire storepush_refused_test.go; a store clone with two conflicted folders
// is not something the journey harness builds.)

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Placeholders of these journeys.
const (
	l7dName     = "Una Upgrader"
	l7dEmail    = "una@example.test"
	l7dNewEmail = "una.new@example.test"
	l7dRepo     = "github.com/acme/l7d-app"
)

// TestJourneyLand7DGitNameUpgrade: Una ran 0.1.17 with no identity of her
// own (git's email only); she upgrades and migrates, and doctor has nothing
// to say about her actor afterwards.
func TestJourneyLand7DGitNameUpgrade(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	una := w.Persona(l7dName, l7dEmail)
	repo := w.Remote(l7dRepo)
	laptop := UseOldLore(t, una.Laptop())
	lap := laptop.Clone(repo, "app")

	w.Step("day1/old-lore-no-identity", func() {
		laptop.Old(lap, "init", "--name", "app").OK(t)
		upAddMemory(t, laptop.Old(lap, "memory", "add", "--body", upNoteBranch))
		upCommitPush(t, lap, "lore data")
		// 0.1.17 names her by the email's local part.
		laptop.Old(lap, "whoami").OK(t).Says(t, "una")
	})

	w.AdvanceDays(30)
	w.Step("month1/upgrade-then-migrate", func() {
		up := laptop.Upgrade()
		up.LoreIn(lap, "whoami").OK(t).Says(t, l7dName, "human:"+l7dEmail)
		// Before the migration the old copy is named, with its fix.
		up.LoreIn(lap, "doctor").Says(t, "actor_copies", `"una"`, l7dName)
		up.LoreIn(lap, "migrate", "run", "--dry-run").OK(t).Says(t, "actor_copies: would refresh 1 actor copy", l7dName)
		up.LoreIn(lap, "migrate", "run").OK(t).Says(t, "actor_copies: refreshed 1 actor copy", l7dName)
		up.LoreIn(lap, "doctor").Never(t, "actor_copies")
		up.LoreIn(lap, "actor", "list").OK(t).Says(t, l7dName)
		// A second run has nothing left to refresh.
		up.LoreIn(lap, "migrate", "run").OK(t).Never(t, "actor_copies")
		upCommitPush(t, lap, "lore: migrate format")
	})
}

// TestJourneyLand7DTwoActors: under 0.1.17 Una moved to a new email and
// told lore so; 0.1.17 made a second actor. After the upgrade doctor names
// both actors and says how to merge them (an org store holds the merge).
func TestJourneyLand7DTwoActors(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	una := w.Persona(l7dName, l7dEmail)
	repo := w.Remote(l7dRepo)
	laptop := UseOldLore(t, una.Laptop())
	lap := laptop.Clone(repo, "app")

	w.Step("day1/old-lore-first-email", func() {
		laptop.Old(nil, "identity", "set", l7dName, "--key", "human:"+l7dEmail).OK(t)
		laptop.Old(lap, "init", "--name", "app").OK(t)
		upAddMemory(t, laptop.Old(lap, "memory", "add", "--body", upNoteBranch))
		upCommitPush(t, lap, "lore data")
	})

	w.AdvanceDays(20)
	w.Step("month1/old-lore-new-email", func() {
		laptop.Old(nil, "identity", "set", l7dName, "--key", "human:"+l7dNewEmail).OK(t)
		una.Rename(t, l7dName, l7dNewEmail)
		upAddMemory(t, laptop.Old(lap, "memory", "add", "--body", upNoteLive))
		upCommitPush(t, lap, "lore data")
	})

	w.AdvanceDays(10)
	w.Step("month2/upgrade-doctor-names-both", func() {
		up := laptop.Upgrade()
		a := up.LoreIn(lap, "actor", "list").OK(t)
		if n := strings.Count(a.Stdout, "[human]"); n != 2 {
			t.Fatalf("the two actors 0.1.17 made: actor list shows %d humans:\n%s", n, a.Stdout)
		}
		old, err := identityActorID("human:" + l7dEmail)
		if err != nil {
			t.Fatal(err)
		}
		cur, err := identityActorID("human:" + l7dNewEmail)
		if err != nil {
			t.Fatal(err)
		}
		// No org here: the merge waits for one, and doctor says so.
		up.LoreIn(lap, "doctor").Says(t, "duplicate_actors", old, cur, "if they are one person", "lore org create")
	})
}

// TestJourneyLand7DAgentAfterEmail: Sam changes email on the laptop, then
// sets up a desk with only the new email. Claude Code on the desk writes
// as the same agent actor as on the laptop, and Sam as the same person.
func TestJourneyLand7DAgentAfterEmail(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/app")
	w.Remote(soloStore) // the personal store's empty repo on GitHub
	var appRepo *teamharness.Clone
	var agentID, personID string

	w.Step("day1/setup-and-agent-note", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, lap, soloName, soloEmail)
		soloSetupRest(t, lap, "create")
		lap.Lore("", "skill", "install", "--tool", "claude_code").OK(t)
		appRepo = lap.Clone(app, "app")
		lap.LoreIn(appRepo, "init").OK(t)
		lap.LoreIn(appRepo, "memory", "add", "--body", "app: timestamps are UTC").OK(t)
		jAgentsAdd(t, lap, appRepo, &jClaude, "cc-1", "agent: ran the migrations")
		agentID = jAgentsWhoami(t, lap, appRepo, &jClaude, "cc-1").Data.Identity.ActorID
		personID = soloActorID(t, lap, appRepo.Dir)
		appRepo.CommitAll("notes")
		appRepo.Git("push", "-q", "origin", "HEAD")
	})

	w.AdvanceDays(10)
	w.Step("week2/change-email", func() {
		soloApply(t, lap, "identity", map[string]any{"name": soloName, "email": soloNewEmail})
		sam.Rename(t, soloName, soloNewEmail)
		// The personal store's copy of Sam now carries the old email as an alias.
		lap.LoreIn(appRepo, "memory", "add", "--body", "after the new email", "--to", "me").OK(t)
		lap.LoreIn(appRepo, "sync").OK(t) // C5: the personal store copy reaches GitHub with Sam's sync
		if got := jAgentsWhoami(t, lap, appRepo, &jClaude, "cc-2").Data.Identity.ActorID; got != agentID {
			t.Errorf("on the laptop the agent is %s after the email change, want %s", got, agentID)
		}
	})

	w.AdvanceDays(5)
	w.Step("week3/desk-new-email-only", func() {
		desk := sam.NewLaptop("desk")
		desk.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, desk, soloName, soloNewEmail)
		soloSetupRest(t, desk, "existing")
		deskApp := desk.Clone(app, "app")
		if got := soloActorID(t, desk, deskApp.Dir); got != personID {
			t.Errorf("on the desk Sam is %s, want %s (one person across the email change)", got, personID)
		}
		who := jAgentsWhoami(t, desk, deskApp, &jClaude, "cc-desk")
		if who.Data.Identity.Kind != "agent" || who.Data.Identity.ActorID != agentID {
			t.Errorf("Claude Code on the desk is %+v, want the laptop's agent %s", who.Data.Identity, agentID)
		}
		jAgentsAdd(t, desk, deskApp, &jClaude, "cc-desk", "agent: desk note")
		rows := jAgentsRows(t, desk, deskApp)
		if r := rows["agent: desk note"]; r.CreatedBy != agentID {
			t.Errorf("the desk agent's note is by %s, want %s", r.CreatedBy, agentID)
		}
		if n := strings.Count(desk.LoreIn(deskApp, "actor", "list").OK(t).Stdout, "[agent]"); n != 1 {
			t.Errorf("actor list shows %d agents for one Claude Code of one person", n)
		}
	})
}
