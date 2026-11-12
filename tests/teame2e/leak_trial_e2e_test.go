//go:build teame2e

package teame2e

// leak_trial_e2e_test.go — the leak findings of the real-user trial
// (F7–F11), real binary and real git in the agency world: alice's notes go
// to the northwind store, acme/app is the client repo (bare origin).
//
//   - F11: `lore move <mem> --to repo --yes` of a note long enough for the
//     text rule works, commits and pushes; a move whose text also quotes a
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
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
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

// The trial's F11 finding: a long internal note deliberately given to the
// client repo must pass the commit and push checks. BATCH 2 (owner): the
// transfer is `lore move` (same id, one home; `lore share` is removed).
func TestLeak_TrialF11_MoveLongNoteToRepo(t *testing.T) {
	a := newAgency(t, th.Cmd("move"))
	mem := a.storeMem(t, leakShared)
	a.aliceApp.MustLore("move", mem, "--to", "repo", "--yes")
	body, ok := a.aliceApp.ReadFile(filepath.Join(".lore", "data", tblMemories, mem+".json"))
	if !ok || !strings.Contains(body, leakShared) {
		t.Fatalf("the moved row %s must hold the text in the client repo: %v %s", mem, ok, body)
	}
	a.aliceApp.Git("switch", "-q", "-c", "chore/share")
	a.aliceApp.Git("add", "-A")
	if r := a.aliceApp.TryGit("commit", "-m", "move the staging note"); r.Code != 0 {
		t.Fatalf("the moved row must pass the commit hooks: %s", r.Out())
	}
	if r := a.aliceApp.TryGit("push", "-q", "origin", "chore/share"); r.Code != 0 {
		t.Fatalf("the moved row must pass the pre-push check: %s", r.Out())
	}
	if !a.app.Has("refs/heads/chore/share") {
		t.Fatal("the push did not reach origin")
	}
}

// The trial's F11 refusal: a note quoting ANOTHER internal note must not
// reach the client repo whole. The transfer is `lore move` (BATCH 2), and a
// move is a write (coordinator decision, fail closed): the leak check runs
// before anything is asked or written, naming the quoted note (exit 19, no
// success line, no row in the client repo, the row still in its store).
func TestLeak_TrialF11_MoveQuotingAnotherNoteRefused(t *testing.T) {
	a := newAgency(t, th.Cmd("move"))
	other := a.storeMem(t, leakOther)
	mem := a.storeMem(t, leakShared+"; as noted, "+leakOther)
	m := a.aliceApp.Lore("move", mem, "--to", "repo", "--yes")
	m.Fails(t, 19, "E_LEAK_BLOCKED", other, "Remove that part from "+mem)
	m.Never(t, "✓", "This becomes visible")
	if rows := repoRows(t, a.aliceApp, tblMemories); len(rows) != 0 {
		t.Fatalf("a refused move writes nothing: %v", rows)
	}
	a.aliceApp.MustLore("memory", "show", mem).Says(t, leakOther)
	// The commit hook still guards a hand-made copy.
	a.aliceApp.WriteFile(filepath.Join(".lore", "data", tblMemories, "mem_01a1a67a33b7777587cca0cfc9e5d8ee.json"),
		`{"_table":"memories","_v":2,"id":"mem_01a1a67a33b7777587cca0cfc9e5d8ee","body":"`+leakOther+`"}`)
	a.aliceApp.Git("add", "-A")
	c := a.aliceApp.TryGit("commit", "-m", "a note that quotes another")
	if c.Code == 0 || !strings.Contains(c.Out(), "E_LEAK_BLOCKED") || !strings.Contains(c.Out(), other) {
		t.Fatalf("the quoted internal note must block the commit, naming %s: %s", other, c.Out())
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
	r.Never(t, "lore move "+mem)
}

// The write-time check costs a plain repo (no org, no store) nothing.
func TestLeak_WriteCheckNoOpWithoutOrg(t *testing.T) {
	w := newTeamWorld(t)
	solo := w.Human("solo", "solo@example.com")
	solo.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}})
	c := solo.Clone(w.Remote("github.com/solo/app"), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	calls := len(w.GitCalls("solo"))
	// CONFIG_GUIDE CFG3 / STORE_SERVER_SPEC 7.4: every attached store has
	// its cache under ~/.lore/cache/stores/, the personal store's included,
	// so ~/.lore/cache exists after init (v0.1.17 created it only for the
	// external cache the leak index read), and every command attaches the
	// personal cache. The check is still a no-op: the write creates no new
	// cache file, and never builds the v0.1.17 external cache
	// ~/.lore/cache/<project-id>.db.
	before := cacheFiles(t, solo.LorePath("cache"))
	c.MustLore("rule", "add", "--body", leakNote, "--to", "repo").Says(t, "✓")
	if after := cacheFiles(t, solo.LorePath("cache")); !maps.Equal(before, after) {
		t.Fatalf("a plain repo write must not open the store cache: %v → %v", before, after)
	}
	if m, _ := filepath.Glob(solo.LorePath("cache", "prj_*.db")); len(m) != 0 {
		t.Fatalf("a plain repo write must not build the external cache: %v", m)
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
	if err := os.RemoveAll(a.dev1.LorePath("cache")); err != nil {
		t.Fatal(err)
	}
	a.dev1.RemoveStoreClones(t)
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
	if subj := commitSubjects(c, 1); !strings.HasPrefix(subj[0], "lore: land") {
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
	if err := os.RemoveAll(a.dev1.LorePath("cache")); err != nil {
		t.Fatal(err)
	}
	a.dev1.RemoveStoreClones(t)
	// The store goes away; the client's origin stays reachable.
	if err := os.Rename(a.store.Dir, a.store.Dir+".gone"); err != nil {
		t.Fatal(err)
	}
	c.TryGit("push", "-q", "origin", "feat/x").Fails(t, 1, "E_LEAK_BLOCKED", "cannot check")
	if a.app.Has("refs/heads/feat/x") {
		t.Fatal("a push lore could not check reached origin")
	}
}

// F14 printRoute, v0.1.17's order: a routed create prints its success
// line, then `  filed: <rule>`, then the op's detail lines (scope: …).
func TestF14_FiledLineFollowsTheSuccessLine(t *testing.T) {
	a := newAgency(t)
	out := a.aliceApp.MustLore("memory", "add", "--body", "the build needs node 22 on ci").Stdout
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "✓ mem_") || !strings.HasPrefix(lines[1], "  filed: ") {
		t.Fatalf("want the ✓ line, then filed:, then the rest: %q", out)
	}
	for _, l := range lines[2:] {
		if strings.HasPrefix(l, "  filed: ") {
			t.Fatalf("filed: printed twice: %q", out)
		}
	}
}

// A fresh clone where only core commands run (lore.db is gitignored, so
// it has .lore/data and no lore.db) is wired like v0.1.17's per-command
// sync: the merge driver config and the hooks are installed by its first
// write (F18's precondition: without the driver the first pull of an actor
// row both machines wrote is a text conflict).
func TestF18_FreshCloneCoreWriteWiresTheMergeDriver(t *testing.T) {
	a := newAgency(t, th.Flag("to", "rule", "add"))
	m := a.dev1.Clone(a.app, "app-fresh")
	if r := m.TryGit("config", "--get", "merge.lore.driver"); r.Code == 0 {
		t.Fatalf("precondition: a fresh clone has no merge driver: %s", r.Out())
	}
	m.MustLore("rule", "add", "--body", "keep migrations reversible", "--to", "repo")
	if r := m.TryGit("config", "--get", "merge.lore.driver"); r.Code != 0 || !strings.Contains(r.Out(), "merge-driver") {
		t.Fatalf("the first core write wires the merge driver: %s", r.Out())
	}
	if b, _ := os.ReadFile(filepath.Join(m.Dir, ".git", "hooks", "pre-commit")); !strings.Contains(string(b), "lore") {
		t.Fatalf("and lore's hooks: %q", b)
	}
}

// cacheFiles is the set of files under dir, to tell whether a command
// created a cache file. A missing dir is empty.
func cacheFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil || d.IsDir() {
			return err
		}
		out[p] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
