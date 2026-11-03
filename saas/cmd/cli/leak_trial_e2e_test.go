//go:build teame2e

package main

// leak_trial_e2e_test.go — the leak findings of the real-user trial
// (F7–F11), real binary and real git in the agency world: alice's notes go
// to the northwind store, acme/app is the client repo (bare origin).
//
//   - F11: `lore share <mem> --to repo --yes` of a note long enough for the
//     text rule works, commits and pushes; a share whose text also quotes a
//     second internal note is refused, and the hint is not the same command.
//   - F7: a `git commit --no-verify` carrying an internal sentence is
//     refused by the pre-push hook and nothing reaches origin; a clean push
//     goes through.
//   - F8, F9: a row written with --to repo is checked before it is
//     written, for every kind; no success line and no row on refusal.
//   - F10: the commit refusal of a leaking repo row names THAT row.
//   - write-time check: a no-op in a plain repo with no org; fail closed in
//     a client repo whose store cannot be read.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// Internal sentences: long enough for the text rule (≥ 8 words).
const (
	leakNote   = "they approve estimates faster when we send them on whatsapp first"
	leakShared = "the staging site resets its database every night at two"
	leakOther  = "their cto prefers calls on tuesday mornings before the standup"
)

// storeMem adds an internal (store) note from alice's client checkout.
func (a *agency) storeMem(t *testing.T, body string) string {
	t.Helper()
	mem := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", body), "mem")
	if _, ok := a.aliceApp.ReadFile(filepath.Join(".lore", "data", tblMemories, mem+".json")); ok {
		t.Fatalf("precondition: %s must go to the store, not the repo", mem)
	}
	return mem
}

// repoRows lists the row files of one table in a clone's .lore/data.
func repoRows(t *testing.T, c *th.Clone, table string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(c.Dir, ".lore", "data", table))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestLeak_TrialF11_ShareLongNoteToRepo(t *testing.T) {
	a := newAgency(t, th.Cmd("share"))
	mem := a.storeMem(t, leakShared)
	out := a.aliceApp.MustLore("share", mem, "--to", "repo", "--yes").Out()
	cp := mustID(t, th.Result{Stdout: strings.SplitN(out, "✓", 2)[1]}, "mem")
	body, ok := a.aliceApp.ReadFile(filepath.Join(".lore", "data", tblMemories, cp+".json"))
	if !ok || !strings.Contains(body, leakShared) || strings.Contains(body, mem) {
		t.Fatalf("the copy %s must hold the text and no pointer back: %v %s", cp, ok, body)
	}
	a.aliceApp.Git("switch", "-q", "-c", "chore/share")
	a.aliceApp.Git("add", "-A")
	if r := a.aliceApp.TryGit("commit", "-m", "share the staging note"); r.Code != 0 {
		t.Fatalf("the shared copy must pass the commit hooks: %s", r.Out())
	}
	if r := a.aliceApp.TryGit("push", "-q", "origin", "chore/share"); r.Code != 0 {
		t.Fatalf("the shared copy must pass the pre-push check: %s", r.Out())
	}
	if !a.app.Has("refs/heads/chore/share") {
		t.Fatal("the push did not reach origin")
	}
}

func TestLeak_TrialF11_ShareQuotingAnotherNoteRefused(t *testing.T) {
	a := newAgency(t, th.Cmd("share"))
	other := a.storeMem(t, leakOther)
	mem := a.storeMem(t, leakShared+"; as noted, "+leakOther)
	r := a.aliceApp.Lore("share", mem, "--to", "repo", "--yes")
	r.Fails(t, 19, "E_LEAK_BLOCKED", other, "Remove that part from "+mem)
	r.Never(t, "lore share "+mem, "✓")
	if rows := repoRows(t, a.aliceApp, tblMemories); len(rows) != 0 {
		t.Fatalf("a refused share writes nothing: %v", rows)
	}
}

func TestLeak_TrialF7_PrePushBlocksNoVerifyCommit(t *testing.T) {
	a := newAgency(t)
	a.storeMem(t, leakNote)
	c := a.aliceApp
	c.Git("switch", "-q", "-c", "feat/leak")
	c.WriteFile("src/notes.ts", "// "+leakNote+"\n")
	c.Git("add", "src/notes.ts")
	c.Git("commit", "-q", "--no-verify", "-m", "add notes, hooks skipped")
	r := c.TryGit("push", "-u", "origin", "feat/leak")
	r.Fails(t, 1, "E_LEAK_BLOCKED", "src/notes.ts", "rewrite the commit")
	if a.app.Has("refs/heads/feat/leak") {
		t.Fatal("the refused push reached origin")
	}
	if found := a.app.AllObjectsContain("whatsapp first"); len(found) != 0 {
		t.Fatalf("the internal sentence reached origin: %v", found)
	}
	// The same check on the commit message alone.
	c.Git("switch", "-q", "-c", "feat/msg", "main")
	c.WriteFile("src/ok.ts", "export const ok = 1\n")
	c.Git("add", "src/ok.ts")
	c.Git("commit", "-q", "--no-verify", "-m", "fix: "+leakNote)
	c.TryGit("push", "origin", "feat/msg").Fails(t, 1, "E_LEAK_BLOCKED", "message of commit")
	if a.app.Has("refs/heads/feat/msg") {
		t.Fatal("the refused push reached origin")
	}
	// A clean branch still pushes.
	c.Git("switch", "-q", "-c", "feat/clean", "main")
	c.WriteFile("src/clean.ts", "export const total = (a: number, b: number) => a + b\n")
	c.Git("add", "src/clean.ts")
	c.Git("commit", "-q", "--no-verify", "-m", "add a total helper")
	if r := c.TryGit("push", "-q", "origin", "feat/clean"); r.Code != 0 {
		t.Fatalf("a clean push must go through: %s", r.Out())
	}
	if !a.app.Has("refs/heads/feat/clean") {
		t.Fatal("the clean push did not reach origin")
	}
}

// F8, F9: every kind written with --to repo is checked before the write.
func TestLeak_TrialF8F9_RepoWriteCheckedFirst(t *testing.T) {
	a := newAgency(t)
	a.storeMem(t, leakNote)
	cases := []struct {
		name  string
		table string
		args  []string
	}{
		{"task title", tblTasks, []string{"task", "add", leakNote, "--to", "repo", "--commitment", "accepted"}},
		{"task body", tblTasks, []string{"task", "add", "fix checkout totals", "--body", leakNote, "--to", "repo", "--commitment", "accepted"}},
		{"rule", "rules", []string{"rule", "add", "--body", "INTERNAL " + leakNote, "--to", "repo"}},
		{"memory", tblMemories, []string{"memory", "add", "--body", leakNote, "--to", "repo"}},
		{"decision body", "decisions", []string{"decision", "add", "--title", "estimates", "--body", leakNote, "--to", "repo"}},
		{"decision title", "decisions", []string{"decision", "add", "--title", leakNote, "--body", "send estimates early", "--to", "repo"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { // sequential: one checkout
			before := repoRows(t, a.aliceApp, c.table)
			r := a.aliceApp.Lore(c.args...)
			if r.Code != 19 || !strings.Contains(r.Out(), "E_LEAK_BLOCKED") || !strings.Contains(r.Out(), "Nothing written") {
				t.Errorf("want E_LEAK_BLOCKED (19) before the write: %s", r)
			}
			if strings.Contains(r.Out(), "✓") {
				t.Errorf("no success line on a refusal: %s", r)
			}
			if after := repoRows(t, a.aliceApp, c.table); len(after) != len(before) {
				t.Errorf("a refused write left a row: %v → %v", before, after)
			}
		})
	}
	if out := a.aliceApp.MustLore("task", "list", "--all").Out(); strings.Contains(out, "whatsapp") {
		t.Fatalf("the refused task exists: %s", out)
	}
	// A clean repo write still works.
	a.aliceApp.MustLore("rule", "add", "--body", "money is integer cents, never floats", "--to", "repo").Says(t, "✓")
}

// F10: a leaking repo row committed by hand is named by its own id.
func TestLeak_TrialF10_CommitHintNamesRepoRow(t *testing.T) {
	a := newAgency(t)
	mem := a.storeMem(t, leakNote)
	rul := mustID(t, a.aliceApp.MustLore("rule", "add", "--body", "money is integer cents, never floats", "--to", "repo"), "rul")
	rel := filepath.Join(".lore", "data", "rules", rul+".json")
	body, ok := a.aliceApp.ReadFile(rel)
	if !ok {
		t.Fatalf("no rule file %s", rel)
	}
	a.aliceApp.WriteFile(rel, strings.Replace(body, "money is integer cents, never floats", leakNote, 1))
	a.aliceApp.Git("add", "-A")
	r := a.aliceApp.TryGit("commit", "-m", "add a rule")
	r.Fails(t, 1, "E_LEAK_BLOCKED", "lore move "+rul+" --to <org>", "lore rule delete "+rul+" --confirm")
	r.Never(t, "lore move "+mem, "lore share "+mem)
}

// The write-time check costs a plain repo (no org, no store) nothing.
func TestLeak_WriteCheckNoOpWithoutOrg(t *testing.T) {
	w := newTeamWorld(t)
	solo := w.Human("solo", "solo@example.com")
	solo.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}})
	c := solo.Clone(w.Remote("github.com/solo/app"), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	calls := len(w.GitCalls("solo"))
	c.MustLore("rule", "add", "--body", leakNote, "--to", "repo").Says(t, "✓")
	// The leak index lives in the external cache; a skipped check never
	// opens (and so never creates) it.
	if _, err := os.Stat(solo.LorePath("cache")); !os.IsNotExist(err) {
		t.Fatalf("a plain repo write must not open the store cache: %v", err)
	}
	for _, l := range w.GitCalls("solo")[calls:] {
		if strings.HasPrefix(l, "fetch") || strings.HasPrefix(l, "ls-remote") || strings.HasPrefix(l, "clone") {
			t.Fatalf("a plain repo write must not reach any store: %s", l)
		}
	}
}

// The write-time check fails closed in a client repo whose store cannot
// be read (no mirror, no cache, offline), like the commit hook.
func TestLeak_WriteCheckFailsClosedWhenStoreUnreadable(t *testing.T) {
	a := newAgency(t)
	a.storeMem(t, leakNote)
	for _, d := range []string{"cache", "clones"} {
		if err := os.RemoveAll(a.dev1.LorePath(d)); err != nil {
			t.Fatal(err)
		}
	}
	a.dev1.Offline()
	before := repoRows(t, a.dev1App, "rules")
	r := a.dev1App.Lore("rule", "add", "--body", leakNote, "--to", "repo")
	r.Fails(t, 19, "E_LEAK_BLOCKED", "cannot check")
	r.Never(t, "✓")
	if after := repoRows(t, a.dev1App, "rules"); len(after) != len(before) {
		t.Fatalf("a refused write left a row: %v → %v", before, after)
	}
}

// F7 with F6: in a client repo with a protected main, a repo task done on
// a branch rides on the next commit, and that push passes the pre-push
// leak check (the ride commit carries only the client's own row).
func TestLeak_TrialF7_RideAlongStillPushesInClientRepo(t *testing.T) {
	a := newAgency(t, capLand)
	a.storeMem(t, leakNote)
	a.app.ProtectMain()
	c := a.aliceApp
	c.Git("switch", "-q", "-c", "feat/totals")
	id := mustID(t, c.MustLore("task", "add", "fix checkout totals for inr", "--to", "repo", "--commitment", "accepted"), "tsk")
	c.MustLore("task", "done", id).Says(t, "protected")
	c.WriteFile("src/totals.ts", "export const cents = (n: number) => Math.round(n * 100)\n")
	c.CommitAll("fix checkout totals")
	if subj := commitSubjects(c, "HEAD", 1); !strings.HasPrefix(subj[0], "lore: land") {
		t.Fatalf("expected the ride commit on top: %v", subj)
	}
	if r := c.TryGit("push", "-q", "-u", "origin", "feat/totals"); r.Code != 0 {
		t.Fatalf("the ride-along push must pass the leak check: %s", r.Out())
	}
	if _, ok := readRow(t, a.app, "refs/heads/feat/totals", mainRowPath("", tblTasks, id)); !ok {
		t.Fatal("the pushed branch must carry the landed row")
	}
}

// F7: the pre-push check fails closed like the commit hook: a client repo
// whose store cannot be read (store gone, no clone, no cache) refuses the
// push.
func TestLeak_TrialF7_PrePushFailsClosedWhenStoreUnreadable(t *testing.T) {
	a := newAgency(t)
	c := a.dev1App
	c.MustLore("sync") // installs lore's hooks in this checkout
	if b, _ := os.ReadFile(filepath.Join(c.Dir, ".git", "hooks", "pre-push")); !strings.Contains(string(b), "sync hook pre-push") {
		t.Fatalf("precondition: lore's pre-push block is installed: %q", b)
	}
	c.Git("switch", "-q", "-c", "feat/x")
	c.WriteFile("src/x.ts", "export const x = 'a perfectly ordinary line of client code here'\n")
	c.Git("add", "-A")
	c.Git("commit", "-q", "--no-verify", "-m", "add x")
	for _, d := range []string{"cache", "clones"} {
		if err := os.RemoveAll(a.dev1.LorePath(d)); err != nil {
			t.Fatal(err)
		}
	}
	// The store goes away; the client's origin stays reachable.
	if err := os.Rename(a.store.Dir, a.store.Dir+".gone"); err != nil {
		t.Fatal(err)
	}
	c.TryGit("push", "-q", "origin", "feat/x").Fails(t, 1, "E_LEAK_BLOCKED", "cannot check")
	if a.app.Has("refs/heads/feat/x") {
		t.Fatal("a push lore could not check reached origin")
	}
}
