//go:build userscen

package userscen

// campaign_sync_followups_test.go — the follow-ups of the sync campaign
// (agent scen-sync's owner list), each driven through the built binary the
// way a person meets it: commit show by short sha / HEAD / any case, the
// ci-merge dry-run wording, live purge's dry run and confirm, store init
// of a bare git repo as a folder, row move into its own store, and the
// trash texts. Helper names start with fsy so they never collide.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// fsyTask adds one accepted task (tasks need a tasklist) and returns its id.
func fsyTask(t *testing.T, c *th.Clone, title string) string {
	t.Helper()
	c.MustLore("tasklist", "add", "--title", "Inbox", "--body", "default list")
	return trashID(t, c.MustLore("task", "add", title, "--body", "b", "--commitment", "accepted", "--json"))
}

// TestCampaignSync_CommitShowResolves: `lore commit show` finds a linked
// commit by its full sha, a 7+ character prefix in any case, HEAD and a
// revision git resolves; a prefix two linked shas share is E_AMBIGUOUS_ID
// naming both; a word that is neither a sha nor a revision is
// E_INVALID_INPUT with a hint; a well-formed sha nothing links shows the
// empty text.
func TestCampaignSync_CommitShowResolves(t *testing.T) {
	_, _, c := cliSolo(t)
	c.CommitAll("first")
	tsk := fsyTask(t, c, "ship it")
	c.MustLore("link", "add", "--entity="+tsk, "--commit=HEAD")
	sha := c.Head()
	c.CommitAll("second") // HEAD moves on: HEAD~1 is the linked commit
	for _, arg := range []string{sha, sha[:7], sha[:12], strings.ToUpper(sha[:9]), "HEAD~1", " " + sha[:8] + " "} {
		r := c.Lore("commit", "show", arg).OK(t)
		if !strings.Contains(r.Stdout, sha) || !strings.Contains(r.Stdout, tsk) {
			t.Errorf("commit show %q: want the commit %s and its task %s:\n%s", arg, sha, tsk, r.Stdout)
		}
	}
	if r := c.Lore("commit", "show", "HEAD").OK(t); !strings.Contains(r.Stdout, "no entities linked") {
		t.Errorf("commit show HEAD (an unlinked commit): want the empty text:\n%s", r.Stdout)
	}
	if r := c.Lore("commit", "show", "deadbee").OK(t); !strings.Contains(r.Stdout, "no entities linked") {
		t.Errorf("commit show deadbee: want the empty text:\n%s", r.Stdout)
	}
	// two linked shas (not in this clone's history) sharing 7 characters
	a, b := "abcdef1"+strings.Repeat("0", 33), "abcdef1"+strings.Repeat("1", 33)
	c.MustLore("link", "add", "--entity="+tsk, "--commit="+a)
	c.MustLore("link", "add", "--entity="+tsk, "--commit="+b)
	r := c.Lore("commit", "show", "ABCDEF1")
	r.Fails(t, 16, "[E_AMBIGUOUS_ID]", a, b)
	if camSyncHint(r) == "" {
		t.Errorf("ambiguous prefix: a hint names the next step:\n%s", r.Out())
	}
	if r := c.Lore("commit", "show", "abcdef11").OK(t); !strings.Contains(r.Stdout, b) {
		t.Errorf("commit show abcdef11: one more character picks %s:\n%s", b, r.Stdout)
	}
	for _, bad := range []string{"nope", "abc", "abcdef", "zzzzzzz"} {
		camSyncRefuses(t, c, camSyncRefusal{args: []string{"commit", "show", bad}, code: "E_INVALID_INPUT", exit: 20, says: "is not a sha"})
	}
}

// fsyCIMergeOut is the data of `lore sync ci-merge --json`.
type fsyCIMergeOut struct {
	Data struct {
		Outcome string   `json:"outcome"`
		Merged  []string `json:"merged"`
		Commit  string   `json:"commit"`
	} `json:"data"`
}

// TestCampaignSync_CIMergeDryRunWording: with one rule edited on a branch
// and on main, `ci-merge --dry-run` says it would TRY the field-level merge
// and that a real run may still need a person (it only probed; the driver's
// verdict needs the real merge), never that the conflict is settled, and
// changes nothing; the real run then says it settled and committed.
func TestCampaignSync_CIMergeDryRunWording(t *testing.T) {
	_, a, b := camSyncPair(t)
	id := trashID(t, a.MustLore("rule", "add", "Buttons are blue", "--json"))
	a.CommitAll("lore: buttons")
	a.AdminPush("origin", "main")
	b.Git("pull", "-q", "--no-rebase", "origin", "main")
	b.Git("checkout", "-q", "-b", "feature/footer")
	b.MustLore("rule", "edit", id, "--body", "Buttons are blue, links are green.")
	b.CommitAll("lore: link colour")
	a.MustLore("rule", "edit", id, "--severity", "should")
	a.CommitAll("lore: buttons are a should")
	a.AdminPush("origin", "main")
	b.Git("fetch", "-q", "origin")

	head, before := b.Head(), camSyncTree(t, b.Dir)
	r := b.MustLore("sync", "ci-merge", "--base", "origin/main", "--dry-run").Says(t, "would try to merge origin/main field by field", "1 lore file(s) conflict", "a real run may still need a person")
	if strings.Contains(r.Stdout, "settled") {
		t.Errorf("a dry run cannot know the conflict is settled:\n%s", r.Stdout)
	}
	if j := th.DecodeJSON[fsyCIMergeOut](t, b.MustLore("sync", "ci-merge", "--base", "origin/main", "--dry-run", "--json")); j.Data.Outcome != "merged" || len(j.Data.Merged) != 1 || j.Data.Commit != "" {
		t.Errorf("dry run --json keeps outcome merged, the one file and no commit: %+v", j.Data)
	}
	if b.Head() != head || b.Status() != "" {
		t.Fatalf("a dry run changes nothing: head %s → %s, status %q", head, b.Head(), b.Status())
	}
	camSyncSameTree(t, "ci-merge --dry-run", before, camSyncTree(t, b.Dir))
	b.MustLore("sync", "ci-merge", "--base", "origin/main").Says(t, "merged and committed origin/main", "settled field by field", "push it")
}

// fsyLivePurgeOut is `lore live purge --json`.
type fsyLivePurgeOut struct {
	Purged  []string `json:"purged"`
	NotLive []string `json:"not_live"`
	DryRun  bool     `json:"dry_run"`
}

// TestCampaignSync_LivePurgeDryRunAndConfirm: `lore live purge` asks first
// like `lore trash purge` (E_NEEDS_CONFIRM naming --confirm, the live ref
// untouched), --dry-run lists what would go (naming an id that is not live)
// and writes nothing, and only --confirm purges, for the teammate too.
func TestCampaignSync_LivePurgeDryRunAndConfirm(t *testing.T) {
	st, a, b := camSyncPair(t)
	keep := camSyncAddTask(t, st, a, "pay the rent")
	leak := camSyncAddTask(t, st, a, "internal: client margin is 40%")
	const notLive = "tsk_01a1b7b892587de4895ee2ad4e285e1c" // well formed, never written
	b.MustLore("task", "list").Says(t, keep, leak)
	rev, before := st.app.Rev(syncRefLive), camSyncTree(t, a.Dir)

	r := a.Lore("live", "purge", leak)
	r.Fails(t, th.ExitNeedsConfirm, "[E_NEEDS_CONFIRM]", leak, "for good")
	if h := camSyncHint(r); !strings.Contains(h, "--confirm") {
		t.Errorf("the refusal's hint names --confirm, got %q", h)
	}
	e := trashErr(t, a, "E_NEEDS_CONFIRM", th.ExitNeedsConfirm, "live", "purge", leak)
	if !strings.Contains(e.Error.Hint, "--confirm") {
		t.Errorf("--json: the hint names --confirm: %+v", e.Error)
	}

	a.MustLore("live", "purge", leak, notLive, "--dry-run").Says(t, "would purge "+leak+", "+notLive, notLive+" is not live now", "dry run: nothing written")
	j := th.DecodeJSON[fsyLivePurgeOut](t, a.MustLore("live", "purge", leak, notLive, "--dry-run", "--json"))
	if !j.DryRun || strings.Join(j.Purged, ",") != leak+","+notLive || strings.Join(j.NotLive, ",") != notLive {
		t.Errorf("dry run --json: %+v", j)
	}
	if st.app.Rev(syncRefLive) != rev {
		t.Fatal("an unconfirmed or dry-run purge must not write the live ref")
	}
	camSyncSameTree(t, "live purge without --confirm / --dry-run", before, camSyncTree(t, a.Dir))
	a.MustLore("live", "show").Says(t, leak)

	a.MustLore("live", "purge", leak, "--confirm").Says(t, "purged "+leak+" from live", "lore live compact")
	a.MustLore("live", "show").Says(t, keep).Never(t, leak)
	b.MustLore("task", "list").Says(t, keep).Never(t, leak)
}

// TestCampaignSync_StoreInitBareRepoAsFolder: a file:// url is always a
// plain folder store, so `lore store init` of a bare git repository by
// its file:// path is E_INVALID_INPUT saying why and writes nothing into
// the repository. Its hint names a git url spelling that works for a
// local bare repository, with the real path (owner report 2026-11-11:
// the hint redacted the middle of a scratch folder path), and running
// exactly that makes the store; a plain folder, and a folder that merely
// holds a git work tree, still become folder stores as before.
func TestCampaignSync_StoreInitBareRepoAsFolder(t *testing.T) {
	w, _, c := cliSolo(t)
	// a folder named like a session id (an agent's scratch folder): the
	// path is shown whole, never redacted
	scratch := strings.Join([]string{"88f5c9c8", "3674", "4aaa", "9c3d", "971a4ab716df"}, "-")
	bare := filepath.Join(w.Root, "srv", scratch, "scratchpad", "notes.git")
	c.Git("init", "-q", "--bare", "-b", "main", bare)
	before := camSyncTree(t, bare)
	alias := "ssh://lore-local/notes.git"
	for _, name := range []string{"kb", "me"} {
		r := c.Lore("store", "init", name, fileuri.URL(bare, ""))
		r.Fails(t, 20, "[E_INVALID_INPUT]", "is a git repository", "lore reads a store's kind from its url", "nothing written")
		h := camSyncHint(r)
		want := `git config --global url."` + fileuri.URL(bare, "") + `".insteadOf ` + alias + " && lore store init " + name + " " + alias
		if !strings.Contains(h, want) || strings.Contains(r.Stderr+r.Stdout, "<redacted>") {
			t.Errorf("store init %s: the hint does not give the working spelling %q with the real path, got %q", name, want, h)
		}
		e := trashErr(t, c, "E_INVALID_INPUT", 20, "store", "init", name, fileuri.URL(bare, ""))
		if !strings.Contains(e.Error.Hint, "git url") {
			t.Errorf("--json: the hint names the git url: %+v", e.Error)
		}
	}
	camSyncSameTree(t, "a refused store init", before, camSyncTree(t, bare))
	if r := c.MustLore("store", "list"); strings.Contains(r.Stdout, "notes.git") {
		t.Errorf("a refused init attaches nothing:\n%s", r.Stdout)
	}
	// the hint's spelling works: the bare repository is a git store
	c.Git("config", "--global", "url."+fileuri.URL(bare, "")+".insteadOf", alias)
	c.MustLore("store", "init", "kb", alias).Says(t, "sto_", alias)
	if log := c.Git("--git-dir", bare, "log", "--oneline", "main"); !strings.Contains(log, "store init") {
		t.Errorf("the store's first commit is not in the bare repository: %q", log)
	}

	plain := filepath.Join(w.Root, "srv", "plain")
	c.MustLore("store", "init", "plain", fileuri.URL(plain, "")).Says(t, "sto_")
	work := filepath.Join(w.Root, "srv", "work")
	c.Git("init", "-q", work)
	c.MustLore("store", "init", "work", fileuri.URL(work, "")).Says(t, "sto_")
}

// TestCampaignSync_RowMoveIntoItsOwnStore: `lore row move` (and `lore
// move`) into the store that already holds the row is E_INVALID_INPUT
// "already in store X" with a hint, the same as memory move, for a row
// never moved and for a move run twice; a trashed row still answers
// E_IN_TRASH; nothing is written either way.
func TestCampaignSync_RowMoveIntoItsOwnStore(t *testing.T) {
	tw := newTrashWorld(t, true)
	c := tw.c
	stay := trashID(t, c.MustLore("memory", "add", "the repo keeps this one", "--json"))
	moved := trashID(t, c.MustLore("memory", "add", "this one goes to kb", "--json"))
	c.MustLore("row", "move", moved, "--to", trashStoreKB, "--yes").Says(t, "moved to store "+trashStoreKB)
	before := camSyncTree(t, c.Dir)
	for _, tc := range []struct {
		verb []string
		id   string
		to   string
	}{
		{[]string{"row", "move"}, stay, trashStoreRepo},
		{[]string{"move"}, stay, trashStoreRepo},
		{[]string{"row", "move"}, moved, trashStoreKB},
		{[]string{"move"}, moved, trashStoreKB},
	} {
		args := append(append([]string{}, tc.verb...), tc.id, "--to", tc.to, "--yes")
		camSyncRefuses(t, c, camSyncRefusal{args: args, code: "E_INVALID_INPUT", exit: 20, says: tc.id + " is already in store " + tc.to})
	}
	camSyncSameTree(t, "a refused move into its own store", before, camSyncTree(t, c.Dir))
	if got := trashShow(t, c, moved); got.Store != trashStoreKB {
		t.Fatalf("the moved row stays in %s: %s", trashStoreKB, got.Store)
	}

	c.MustLore("memory", "delete", stay, "--yes")
	trashErr(t, c, "E_IN_TRASH", 23, "row", "move", stay, "--to", trashStoreRepo, "--yes")
	trashErr(t, c, "E_IN_TRASH", 23, "row", "move", stay, "--to", trashStoreKB, "--yes")
}

// TestCampaignSync_TrashTextsAndVisibilityHint: the trash texts say "1 row"
// (never "1 rows"), and `store visibility <s> bogus` names the two values
// in its hint.
func TestCampaignSync_TrashTextsAndVisibilityHint(t *testing.T) {
	tw := newTrashWorld(t, true)
	c := tw.c
	id := trashID(t, c.MustLore("memory", "add", "the staging password rotates monthly", "--json"))
	c.MustLore("memory", "delete", id, "--yes")
	c.MustLore("trash", "list").Says(t, id, "1 row in the trash").Never(t, "1 rows")
	c.MustLore("trash", "stats").Says(t, "1 row in the trash (0 dependents)").Never(t, "1 rows")
	c.MustLore("trash", "purge", id, "--dry-run").Says(t, "would purge 1 row").Never(t, "1 rows")
	c.MustLore("trash", "restore", id).Says(t, "restored 1 row (batch ").Never(t, "1 rows")
	two := []string{
		trashID(t, c.MustLore("memory", "add", "first of two", "--json")),
		trashID(t, c.MustLore("memory", "add", "second of two", "--json")),
	}
	for _, x := range two {
		c.MustLore("memory", "delete", x, "--yes")
	}
	c.MustLore("trash", "list").Says(t, "2 rows in the trash")

	camSyncRefuses(t, c, camSyncRefusal{args: []string{"store", "visibility", trashStoreKB, "bogus"}, code: "E_INVALID_INPUT", exit: 20, says: "must be internal or shareable"})
	if h := camSyncHint(c.Lore("store", "visibility", trashStoreKB, "bogus")); !strings.Contains(h, "internal (the default) or shareable") {
		t.Errorf("visibility hint: %q", h)
	}
}

// TestCampaignSync_TrashNamesTheDeleterByHandle: in an org whose roster
// gives the person a handle, `lore trash list` and `trash show` print
// "by @mira", not the act_ id; --json keeps the act_ id in deleted_by.
func TestCampaignSync_TrashNamesTheDeleterByHandle(t *testing.T) {
	o := newOrgScen(t)
	c := o.app
	id := trashID(t, c.MustLore("memory", "add", "the client prefers Friday demos", "--json"))
	c.MustLore("memory", "delete", id, "--yes")
	c.MustLore("trash", "list").Says(t, id, "by @mira").Never(t, "by act_")
	c.MustLore("trash", "show", id).Says(t, "by @mira").Never(t, "by act_")
	if it := trashByID(trashList(t, c))[id]; !strings.HasPrefix(it.DeletedBy, "act_") {
		t.Errorf("--json keeps the act_ id in deleted_by: %+v", it)
	}
}
