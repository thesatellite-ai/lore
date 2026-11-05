//go:build teame2e

package main

// F16 on the landing commit (LORE_TEAM_SPEC F16, §6.3): a row on the live
// ref of a client repo that quotes an internal note cannot reach the
// default branch. The landing commit never passes a git hook, so the check
// runs inside the landing pass itself (wire/host LiveConfig.LeakCheck on
// cmd/lore, the live wiring of the old binary).

import (
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// The live writes pass because the personal note does not exist yet
// (autopush off, so nothing lands on its own); the note added after them
// makes the landing a leak: `lore land` pushes the live ref, then refuses
// E_LEAK_BLOCKED naming the note by id, never quoting it, and the row stays
// on the live ref. A write made after the note is refused itself (the old
// binary refuses it before the write, cmd/lore at the live write).
func TestLeak_LandingOfClientRowQuotingAPersonalNoteIsBlocked(t *testing.T) {
	w := newTeamWorld(t, th.Flag("live", "task", "add"), th.Flag("to", "memory", "add"), capLand)
	app := w.Remote(slugApp)
	alice := w.Human("alice", "alice@northwind.example")
	alice.WriteConfig(th.Config{
		Personal: &th.Personal{Store: th.PersonalLocal},
		Orgs: []th.Org{{
			Name:     orgNorthwind,
			Identity: th.Identity{Key: aliceKey, Name: aliceName, Handle: "alice"},
			Repos:    []string{"github.com/acme/*"}, // acme/app is a client repo
		}},
		Live: &th.Live{FetchTTL: "0s", Autopush: th.Bool(false)},
	})
	c := alice.Clone(app, "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	tl := createTasklist(t, c)
	c.CommitAll("lore")
	c.AdminPush("origin", "main")
	app.ProtectMain()

	const quote = "acme renewal slips unless the northwind discount reaches forty percent this quarter"
	id := mustID(t, c.MustLore("task", "add", quote, "--tasklist", tl, "--to", "repo", "--live"), "tsk")
	c.MustLore("task", "done", id)
	mem := mustID(t, c.MustLore("memory", "add", "--to", "me", quote), "mem")

	c.Lore("land", id).Refused(t, "E_LEAK_BLOCKED", "landing", mem).Never(t, "forty percent", "cannot check")
	if len(app.FilesMatching(refLive, id)) == 0 {
		t.Fatalf("control: lore land pushes the live ref before landing, so %s must be on the remote live ref", id)
	}
	if f := app.FilesMatching(refMain, id); len(f) != 0 {
		t.Fatalf("the row quoting a personal note reached main: %v", f)
	}

	c.Lore("task", "add", quote+" again", "--tasklist", tl, "--to", "repo", "--live").
		Refused(t, "E_LEAK_BLOCKED", mem).Never(t, "forty percent")
}

// The commit and push hooks refuse a credential in every repo (no org, no
// store), in a code file and in a hand-made .lore/data row, naming the
// file and the rule, never the value; a commit made with --no-verify is
// still refused by pre-push.
func TestLeak_HooksRefuseASecretInAPlainRepo(t *testing.T) {
	w := newTeamWorld(t)
	solo := w.Human("solo", "solo@example.com")
	solo.WriteConfig(th.Config{Live: th.FreshFetch()})
	c := solo.Clone(w.Remote("github.com/solo/app"), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	c.CommitAll("lore")
	c.AdminPush("origin", "main")
	c.MustLore("rule", "add", "--body", "keep migrations reversible") // wires the hooks
	key := "AKIA" + "IOSFODNN7EXAMPLE"                                // the AWS example key, built at run time (convention 8)

	c.WriteFile("src/config.ts", "export const key = '"+key+"'\n")
	c.Git("add", "src/config.ts")
	r := c.TryGit("commit", "-m", "config")
	if r.Code == 0 || !strings.Contains(r.Out(), "src/config.ts") || !strings.Contains(r.Out(), "secret") || strings.Contains(r.Out(), key) {
		t.Fatalf("a staged credential must block the commit, naming the file, never the value: %s", r.Out())
	}
	c.Git("reset", "-q", "--hard", "HEAD")

	c.WriteFile(".lore/data/rules/rul_01a1a67a33b7777587cca0cfc9e5d8ef.json", `{"_table":"rules","_v":2,"id":"rul_01a1a67a33b7777587cca0cfc9e5d8ef","body":"use `+key+`"}`)
	c.Git("add", "-A")
	if r := c.TryGit("commit", "-m", "hand rule"); r.Code == 0 || !strings.Contains(r.Out(), "rul_01a1a67a33b7777587cca0cfc9e5d8ef.json") || strings.Contains(r.Out(), key) {
		t.Fatalf("a credential in a hand-made row must block the commit and name the file: %s", r.Out())
	}
	c.Git("reset", "-q", "--hard", "HEAD")

	c.Git("switch", "-q", "-c", "feat/key")
	c.WriteFile("src/config.ts", "export const key = '"+key+"'\n")
	c.Git("add", "src/config.ts")
	c.Git("commit", "-q", "--no-verify", "-m", "hooks skipped")
	if r := c.TryGit("push", "-u", "origin", "feat/key"); r.Code == 0 || !strings.Contains(r.Out(), "secret") {
		t.Fatalf("pre-push must refuse the credential a --no-verify commit carries: %s", r.Out())
	}
	if found := w.Remote("github.com/solo/app").AllObjectsContain(key); len(found) != 0 {
		t.Fatalf("the credential reached origin: %v", found)
	}
}
