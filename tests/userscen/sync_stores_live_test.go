//go:build userscen

package userscen

// sync_stores_live_test.go — the store and live-ref commands a person runs
// rarely but must trust, through the real binary (docs/sync/stores.md
// step 4, docs/team/identity-and-stores.md step 7, docs/team/use-cases/
// staging.md, docs/team/commands.md "Live rows and landing"). Gaps left by
// sync_store_test.go and tests/teame2e (F8 compaction, side effects 28/36):
//
//   - store folders: `store folder add` creates a folder once (again is a
//     no-op), refuses `org`, a non-snake_case name and an unknown store;
//     a write to a folder that does not exist asks first and writes
//     nothing; the first write to notes/golang attaches the folder entry in
//     stores.yaml, labels the row, lands in the store's golang folder and a
//     teammate reads it after pulling stores.yaml
//   - store branches: `store init <name> '<url>?branch=staging'` creates the
//     branch on purpose, `store add --branch staging` attaches it, a typo
//     branch is refused and never created; a staging write never reaches
//     main
//   - `store open`: by name, by url, by url with ?branch=, by an entry with
//     branch:, with --branch; the folder is the store's own clone under
//     ~/.lore/stores (<repo>@<branch> for another branch) at the remote's
//     commit and holds the store files; an unknown name and a missing
//     branch are refused
//   - `live compact`: --dry-run changes nothing, compact keeps every row and
//     a local backup only, raises the epoch by one (live log agrees), a
//     second compact of the one-commit ref writes nothing and keeps the
//     epoch, the teammate integrates without a bump of its own and keeps
//     reading and writing
//   - `live purge`: a typo or shortened id is refused and nothing is
//     written; a real id is gone for the teammate too
//   - `live restore`: with the remote intact it keeps the teammate's rows;
//     after the remote live ref is deleted writes stop with E_READ_ONLY and
//     its hint, restore brings the rows back, writes work again, and a
//     second restore changes nothing

import (
	"os"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// syncStagingRef is the second store branch the staging scenarios make.
const syncStagingRef = "refs/heads/staging"

// syncOpenOut is `lore store open --json`.
type syncOpenOut struct {
	Dir    string `json:"dir"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

// syncRemoteHolds reports whether any file of ref on r names id.
func syncRemoteHolds(r *th.Remote, ref, id string) bool {
	for _, f := range r.Files(ref) {
		if strings.Contains(f, id) {
			return true
		}
	}
	return false
}

// syncFolderSource is `<store>/<folder>` of memory id's --json _source in
// c ("" when the row is missing or has no store).
func syncFolderSource(t *testing.T, c *th.Clone, id string) string {
	t.Helper()
	type src struct {
		Data []struct {
			ID     string `json:"id"`
			Source *struct {
				Store  string `json:"store"`
				Folder string `json:"folder"`
			} `json:"_source"`
		} `json:"data"`
	}
	for _, d := range th.DecodeJSON[src](t, c.MustLore("memory", "list", "--json")).Data {
		if d.ID == id && d.Source != nil {
			return d.Source.Store + "/" + d.Source.Folder
		}
	}
	return ""
}

// Store folders: created on purpose, attached by the first write, read by
// a teammate.
func TestSyncStores_FolderAddWriteAndTeammate(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	r := st.w.Remote(syncSlugStore)
	a.MustLore("store", "init", "notes", r.URL(), "--yes")

	a.MustLore("store", "folder", "add", "notes", "golang").Says(t, "created folder golang in notes", "shown only in repos that attach it")
	before := r.Rev(syncStoreRef)
	a.MustLore("store", "folder", "add", "notes", "golang").Says(t, "notes already has a folder golang", "nothing changed")
	if r.Rev(syncStoreRef) != before {
		t.Fatal("adding an existing folder again must not write to the store")
	}
	a.Lore("store", "folder", "add", "notes", "org").Fails(t, 20, "E_INVALID_INPUT", "`org` is the org folder's name")
	a.Lore("store", "folder", "add", "notes", "Bad-Name").Fails(t, 20, "E_INVALID_INPUT", "`Bad-Name` must be snake_case")
	a.Lore("store", "folder", "add", "nope", "golang").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "no store named nope", "lore store list")
	a.Lore("memory", "add", "must not land", "--to", "notes/missing").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "no folder missing", "nothing written", "existing: golang")
	if r.Rev(syncStoreRef) != before {
		t.Fatal("refused folder commands must not write to the store")
	}
	if y, _ := a.ReadFile(".lore/stores.yaml"); strings.Contains(y, "folder:") {
		t.Fatalf("a folder is attached only by its first write:\n%s", y)
	}

	mem := th.DecodeJSON[syncAddOut](t, a.MustLore("memory", "add", "close every channel from the sender side", "--to", "notes/golang", "--json"))
	if mem.Store != "notes/golang" {
		t.Fatalf("memory add --to notes/golang went to %q", mem.Store)
	}
	y, _ := a.ReadFile(".lore/stores.yaml")
	if !strings.Contains(y, "name: notes/golang") || !strings.Contains(y, "folder: golang") {
		t.Fatalf("the first write attaches the folder entry:\n%s", y)
	}
	a.MustLore("sync") // C5: the write and the new folder reach the remote with lore sync
	if fs := r.FilesMatching(syncStoreRef, mem.ID); len(fs) != 1 || !strings.Contains(fs[0], "golang/") {
		t.Fatalf("the row lands in the store's golang folder: %v", fs)
	}
	a.MustLore("memory", "list").Says(t, mem.ID, "[notes/golang]")

	a.CommitAll("attach notes/golang")
	a.AdminPush("origin", "main")
	b.Git("pull", "-q", "--no-rebase", "origin", "main")
	b.MustLore("memory", "list").Says(t, mem.ID, "[notes/golang]")
	for _, c := range []*th.Clone{a, b} {
		if src := syncFolderSource(t, c, mem.ID); src != "notes/golang" {
			t.Fatalf("--json _source names store notes, folder golang: %q", src)
		}
	}
}

// Store branches are made only on purpose; a staging row stays on its
// branch; `store open` opens every spelling of a store and its branch.
func TestSyncStores_BranchesAndOpen(t *testing.T) {
	st := newSyncTeam(t, 1)
	a, alice := st.clones[0], st.people[0]
	r := st.w.Remote(syncSlugStore)
	a.MustLore("store", "init", "notes", r.URL(), "--yes")

	a.Lore("store", "add", "typo", r.URL(), "--branch", "stagin", "--yes").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "no branch stagin", "create the branch")
	if r.Has("refs/heads/stagin") {
		t.Fatal("a refused branch must never be created")
	}
	a.MustLore("store", "init", "stag", r.URL()+"?branch=staging", "--yes").Says(t, "created store stag")
	if !r.Has(syncStagingRef) || r.Parents(syncStagingRef) != 0 {
		t.Fatalf("store init ?branch= makes the branch with one parentless commit: has=%v", r.Has(syncStagingRef))
	}
	attachTwice(t, a, r.URL()) // a second name for stag's place: refused, so written into stores.yaml

	mem := th.DecodeJSON[syncAddOut](t, a.MustLore("memory", "add", "seed staging with demo data", "--to", "stag2", "--json")).ID
	a.MustLore("sync") // C5: the write reaches the remote with lore sync
	if !syncRemoteHolds(r, syncStagingRef, mem) || syncRemoteHolds(r, syncStoreRef, mem) {
		t.Fatalf("a staging row lands on staging and never on main: staging=%v main=%v",
			syncRemoteHolds(r, syncStagingRef, mem), syncRemoteHolds(r, syncStoreRef, mem))
	}

	stores := alice.LorePath("stores")
	main := th.DecodeJSON[syncOpenOut](t, a.MustLore("store", "open", "notes", "--json"))
	if main.Branch != "main" || main.Commit != r.Rev(syncStoreRef) || !strings.HasPrefix(main.Dir, stores) {
		t.Fatalf("store open notes: %+v (remote main %s)", main, r.Rev(syncStoreRef))
	}
	if !syncFolderHolds(t, main.Dir, "") || syncFolderHolds(t, main.Dir, mem) {
		t.Fatalf("the main folder holds the store files and no staging row: %s", main.Dir)
	}
	a.MustLore("store", "open", "notes").Says(t, main.Dir, "the store's own clone")
	if _, err := os.Stat(alice.LorePath("worktrees")); err == nil {
		t.Fatal("store open made a worktrees folder beside the store's own clone")
	}

	// INTEG-SYNC-5: every spelling of the staging branch opens it, the
	// `?branch=` url form included (it failed with E_OFFLINE before).
	for _, args := range [][]string{
		{"stag2"},
		{"stag"},
		{r.URL() + "?branch=staging"},
		{r.URL(), "--branch", "staging"},
		{"notes", "--branch", "staging"},
	} {
		got := th.DecodeJSON[syncOpenOut](t, a.MustLore(append(append([]string{"store", "open"}, args...), "--json")...))
		if got.Branch != "staging" || got.Commit != r.Rev(syncStagingRef) || got.Dir != main.Dir+"@staging" {
			t.Fatalf("store open %v: %+v (remote staging %s)", args, got, r.Rev(syncStagingRef))
		}
		if !syncFolderHolds(t, got.Dir, mem) {
			t.Fatalf("store open %v: the staging folder holds the staging row", args)
		}
	}

	a.Lore("store", "open", "nope").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "no store named nope", "lore store open --url <url>")
	a.Lore("store", "open", "notes", "--branch", "nope").Fails(t, 20, "E_INVALID_INPUT", "has no branch nope")
	if r.Has("refs/heads/nope") {
		t.Fatal("store open never creates a branch")
	}
}

// live compact, purge and restore as a two-person team sees them.
func TestSyncLive_CompactPurgeRestore(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	add := func(c *th.Clone, title string) string {
		return th.DecodeJSON[syncShowOut](t, c.MustLore("task", "add", title, "--tasklist", st.tasklist, "--json")).Data.ID
	}
	one, two := add(a, "send the March invoices"), add(a, "chase the unpaid invoice")

	rev := st.app.Rev(syncRefLive)
	a.MustLore("live", "compact", "--dry-run").Says(t, "compacting would keep", "epoch 1 → 2")
	if st.app.Rev(syncRefLive) != rev {
		t.Fatal("compact --dry-run must not change the remote live ref")
	}
	a.MustLore("live", "compact").Says(t, "compacted refs/lore/live to 1 commit (epoch 2)", "backup kept locally")
	if st.app.CommitCount(syncRefLive) != 1 || len(st.app.Refs("refs/lore-backup/")) != 0 {
		t.Fatalf("compacted: %d commits on the remote, backups pushed %v", st.app.CommitCount(syncRefLive), st.app.Refs("refs/lore-backup/"))
	}
	a.MustLore("live", "log").Says(t, "epoch 2", "lore: live compact (epoch 2)")
	// F8 epoch rule: a ref that is already one commit has nothing to
	// compact; the second compact (and its dry run) writes nothing and the
	// epoch stays 2.
	rev = st.app.Rev(syncRefLive)
	a.MustLore("live", "compact").Says(t, "nothing to compact", "already 1 commit", "epoch 2, unchanged")
	a.MustLore("live", "compact", "--dry-run").Says(t, "nothing to compact", "epoch 2, unchanged")
	if st.app.Rev(syncRefLive) != rev {
		t.Fatal("a compact with nothing to do must not write the remote live ref")
	}
	a.MustLore("live", "log").Says(t, "epoch 2")
	b.MustLore("task", "list").Says(t, one, two)
	// The teammate integrated the compaction once: same epoch, no bump of
	// its own.
	b.MustLore("live", "log").Says(t, "epoch 2", "lore: live compact (epoch 2)")
	three := add(b, "close the books for Q1")
	a.MustLore("task", "list").Says(t, three)
	b.MustLore("live", "log").Says(t, "epoch 2")

	// INTEG-SYNC-6: an id that is not a full row id is refused, nothing
	// is written.
	rev = st.app.Rev(syncRefLive)
	for _, bad := range []string{"tsk_nope", one[:12] + "…" + one[len(one)-4:]} {
		a.Lore("live", "purge", bad).Fails(t, 20, "E_INVALID_INPUT", "is not a full row id", "nothing purged")
	}
	a.Lore("live", "purge").Fails(t, 20, "E_INVALID_INPUT")
	if st.app.Rev(syncRefLive) != rev {
		t.Fatal("a refused purge must not write the live ref")
	}
	a.MustLore("live", "purge", two, "--confirm").Says(t, "purged "+two+" from live", "lore live compact")
	b.MustLore("task", "list").Says(t, one, three).Never(t, two)

	// Restore with the remote intact keeps the teammate's newest row.
	four := add(b, "file the VAT return")
	a.MustLore("live", "restore").Says(t, "restored refs/lore/live")
	b.MustLore("task", "list").Says(t, one, three, four).Never(t, two)

	st.app.DeleteRef(syncRefLive)
	a.Lore("task", "add", "after the delete", "--tasklist", st.tasklist).Fails(t, th.ExitRefused, "E_READ_ONLY", "remote live ref was deleted or rewritten", "lore live restore")
	a.MustLore("live", "restore").Says(t, "restored refs/lore/live", "left out")
	if !st.app.Has(syncRefLive) || len(st.app.Refs("refs/lore-backup/")) != 0 {
		t.Fatal("restore pushes the live ref back, never the backups")
	}
	five := add(a, "after the restore")
	b.MustLore("task", "list").Says(t, one, three, four, five).Never(t, two)
	rev = st.app.Rev(syncRefLive)
	again := a.MustLore("live", "restore").Says(t, "restored refs/lore/live").Stdout
	if st.app.Rev(syncRefLive) != rev {
		t.Fatalf("a second restore with nothing missing must not change the remote: %s", again)
	}
	if fs := st.app.FilesMatching(syncRefLive, two); len(fs) != 0 {
		t.Fatalf("restore never brings a purged row back: %v", fs)
	}
}
