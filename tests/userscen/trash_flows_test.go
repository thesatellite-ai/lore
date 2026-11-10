//go:build userscen

package userscen

// Trash area, the flows around one delete (DELETION 2.2-2.6, 3.4, 3.7,
// 4.2, DQ2, DQ4) driven through the real binary:
//
//   - on-delete restrict: a project that rows still point at is refused
//     with E_HAS_DEPENDENTS naming the row and field, and nothing moves;
//   - cascade: a tasklist goes to the trash with its tasks as one batch
//     (one listed root, dependents counted, listed with --with-dependents),
//     a task alone cannot come back while its tasklist is in the trash,
//     the batch comes back whole, and a purge takes the tasks too;
//   - restore after the same unique name was re-created (prompts, vars,
//     metadata keys) is refused naming the live row, and works once the
//     live row is gone;
//   - bulk delete with --where (dry run first), one batch, restored and
//     purged by its batch id;
//   - delete --purge skips the trash (needs --confirm);
//   - wrong input: every refusal's code, exit, message and next step;
//   - trash list filters, paging and its --json shape; odd delete reasons
//     (unicode, very long) kept as written;
//   - two clones of a team store: deleted on A is in B's trash after sync
//     and B's restore reaches A; deleted on A while B edits keeps the row
//     alive on both (delete_overruled); purged on A is E_PURGED on B.

import (
	"strconv"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestTrash_RestrictRefusesProjectWithRows(t *testing.T) {
	tw := newTrashWorld(t, false)
	c := tw.c
	prj := th.DecodeJSON[struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}](t, c.MustLore("project", "list", "--json")).Data
	if len(prj) != 1 {
		t.Fatalf("one project after init, got %v", prj)
	}
	mem := trashID(t, c.MustLore("memory", "add", "--body", "points at the project", "--json"))
	e := trashErr(t, c, "E_HAS_DEPENDENTS", th.ExitRefused, "project", "delete", prj[0].ID, "--yes")
	for _, want := range []string{mem, "memories.project_id", prj[0].ID} {
		if !strings.Contains(e.Error.Message, want) {
			t.Errorf("the refusal names %q: %+v", want, e.Error)
		}
	}
	if !strings.Contains(e.Error.Hint, "delete or move the pointing rows first") {
		t.Errorf("the refusal says what to do: %+v", e.Error)
	}
	// Nothing moved: the project and the memory are alive, the trash empty.
	trashShow(t, c, prj[0].ID)
	trashShow(t, c, mem)
	if l := trashList(t, c, "--with-dependents"); l.Count != 0 {
		t.Fatalf("a refused delete moves nothing to the trash: %+v", l)
	}
	// Twice: the same refusal, still nothing moved.
	trashErr(t, c, "E_HAS_DEPENDENTS", th.ExitRefused, "project", "delete", prj[0].ID, "--yes")
	trashShow(t, c, prj[0].ID)
}

func TestTrash_TasklistCascadeBatch(t *testing.T) {
	tw := newTrashWorld(t, false)
	c := tw.c
	tl := tw.tasklists[trashStoreRepo]
	t1 := trashID(t, c.MustLore("task", "add", "write the changelog", "--tasklist", tl, "--json"))
	t2 := trashID(t, c.MustLore("task", "add", "tag the release", "--tasklist", tl, "--json"))
	before1, before2 := trashShow(t, c, t1), trashShow(t, c, t2)

	// The group holds tasks: no --yes and no terminal is a question.
	e := trashErr(t, c, "E_NEEDS_CONFIRM", th.ExitNeedsConfirm, "tasklist", "delete", tl)
	if !strings.Contains(e.Error.Message, "2 task") {
		t.Errorf("the question counts the tasks that go with it: %+v", e.Error)
	}
	trashShow(t, c, t1)
	c.MustLore("tasklist", "delete", tl, "--yes", "--reason", "replanned")

	l := trashList(t, c)
	if l.Count != 1 || l.Data[0].ID != tl || l.Dependents != 2 || l.Total != 1 {
		t.Fatalf("one root listed, its two tasks counted: %+v", l)
	}
	batch := l.Data[0].Batch
	all := trashByID(trashList(t, c, "--with-dependents"))
	for _, id := range []string{t1, t2} {
		if it := all[id]; it.Batch != batch || !it.Dependent || it.Table != "tasks" {
			t.Errorf("task %s is a dependent of the batch %s: %+v", id, batch, it)
		}
		trashErr(t, c, "E_IN_TRASH", th.ExitRefused, "show", id)
	}

	// A task alone is refused while its tasklist is in the trash.
	e = trashErr(t, c, "E_PARENT_DELETED", th.ExitRefused, "trash", "restore", t1)
	if !strings.Contains(e.Error.Message, tl) || !strings.Contains(e.Error.Hint, "lore trash restore "+batch) {
		t.Errorf("the refusal names the tasklist and the batch to restore: %+v", e.Error)
	}
	// The batch comes back whole, with the same ids and fields.
	res := th.DecodeJSON[trashResultOut](t, c.MustLore("trash", "restore", batch, "--json"))
	if res.Count != 3 || res.Batch != batch {
		t.Fatalf("the batch restore brings back the tasklist and both tasks: %+v", res)
	}
	if a := trashShow(t, c, t1); !trashMapsEqual(a.Fields, before1.Fields) {
		t.Errorf("task %s differs after the batch restore:\n%v\n%v", t1, before1.Fields, a.Fields)
	}
	if a := trashShow(t, c, t2); !trashMapsEqual(a.Fields, before2.Fields) {
		t.Errorf("task %s differs after the batch restore", t2)
	}

	// Purging the tasklist purges its tasks: neither comes back.
	c.MustLore("tasklist", "delete", tl, "--yes")
	pr := th.DecodeJSON[trashResultOut](t, c.MustLore("trash", "purge", tl, "--confirm", "--json"))
	if pr.Count != 3 {
		t.Errorf("the purge takes the tasklist and its two tasks: %+v", pr)
	}
	for _, id := range []string{tl, t1, t2} {
		trashErr(t, c, "E_PURGED", th.ExitRefused, "trash", "restore", id)
	}
	if l := trashList(t, c, "--with-dependents"); l.Count != 0 {
		t.Errorf("nothing is left in the trash: %+v", l)
	}
}

func TestTrash_RestoreAfterRecreateSameKey(t *testing.T) {
	tw := newTrashWorld(t, false)
	c := tw.c
	cases := []struct {
		table string
		add   []string // re-run verbatim for the re-create
		del   func(target string) []string
		// byName: the delete takes the name.
		byName bool
		name   string
	}{
		{table: "prompts", name: "release", add: []string{"prompt", "add", "--title", "release", "--body", "tag and push"},
			del: func(id string) []string { return []string{"prompt", "delete", id, "--yes"} }},
		{table: "vars", name: "db_host", byName: true, add: []string{"vars", "set", "db_host", "db.example.com"},
			del: func(n string) []string { return []string{"vars", "rm", n, "--yes"} }},
		{table: "meta_keys", name: "release_window", byName: true, add: []string{"meta", "add", "release_window", "--type", "text"},
			del: func(n string) []string { return []string{"meta", "delete", n, "--yes"} }},
	}
	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			old := trashID(t, c.MustLore(append(tc.add, "--json")...))
			if tc.table == "prompts" {
				// A second add of a live key is E_NAME_TAKEN (exit 23, as
				// docs/reference/errors.md says), naming the field, its
				// value and the live row (INTEG-QUESTIONS-1).
				e := trashErr(t, c, "E_NAME_TAKEN", 23, tc.add...)
				if e.Error.Message != `prompts: name "release" already exists (`+old+`)` || !strings.Contains(e.Error.Hint, old) {
					t.Fatalf("duplicate add names the field and the live row %s: %+v", old, e.Error)
				}
			}
			target := old
			if tc.byName {
				target = tc.name
			}
			c.MustLore(tc.del(target)...)
			// While the old row is in the trash the name is free again.
			fresh := trashID(t, c.MustLore(append(tc.add, "--json")...))
			if fresh == old {
				t.Fatalf("the re-created %s got the trashed row's id %s", tc.table, old)
			}
			// The restore would make two rows with one unique key: refused,
			// naming the live row, and the old row stays in the trash.
			e := trashErr(t, c, "E_NAME_TAKEN", 23, "trash", "restore", old)
			if !strings.Contains(e.Error.Message, "already exists ("+fresh+")") || !strings.Contains(e.Error.Hint, fresh) {
				t.Fatalf("the refusal names the live row %s: %+v", fresh, e.Error)
			}
			if _, ok := trashByID(trashList(t, c))[old]; !ok {
				t.Fatalf("a refused restore leaves %s in the trash", old)
			}
			trashShow(t, c, fresh)
			// Once the live row is gone, the old one comes back.
			freshTarget := fresh
			if tc.byName {
				freshTarget = tc.name
			}
			c.MustLore(tc.del(freshTarget)...)
			c.MustLore("trash", "restore", old)
			if got := trashShow(t, c, old); got.ID != old {
				t.Fatalf("restored %s: %+v", old, got)
			}
			// And now the re-created one cannot come back over it.
			trashErr(t, c, "E_NAME_TAKEN", 23, "trash", "restore", fresh)
		})
	}
	// A kind without a unique key restores next to its twin.
	m1 := trashID(t, c.MustLore("memory", "add", "--body", "same text twice", "--json"))
	c.MustLore("memory", "delete", m1, "--yes")
	m2 := trashID(t, c.MustLore("memory", "add", "--body", "same text twice", "--json"))
	c.MustLore("trash", "restore", m1)
	trashShow(t, c, m1)
	trashShow(t, c, m2)
}

func TestTrash_BulkDeleteWhere(t *testing.T) {
	tw := newTrashWorld(t, false)
	c := tw.c
	var gone []string
	for i := range 3 {
		gone = append(gone, trashID(t, c.MustLore("memory", "add", "--body", "episode "+strconv.Itoa(i), "--kind", "episodic", "--json")))
	}
	keep := trashID(t, c.MustLore("memory", "add", "--body", "a core fact", "--kind", "core", "--json"))

	// memory delete asks up front even for a dry run (bugs.md question).
	dry := c.MustLore("memory", "delete", "--where", "kind=episodic", "--dry-run", "--yes")
	for _, id := range gone {
		if !strings.Contains(dry.Out(), id) {
			t.Errorf("the dry run lists %s: %s", id, dry)
		}
	}
	if strings.Contains(dry.Out(), keep) {
		t.Errorf("the dry run lists only matching rows: %s", dry)
	}
	if l := trashList(t, c); l.Count != 0 {
		t.Fatalf("a dry run writes nothing: %+v", l)
	}
	c.MustLore("memory", "delete", "--where", "kind=episodic", "--yes", "--reason", "bulk clean")
	l := trashByID(trashList(t, c))
	batch := ""
	for _, id := range gone {
		it, ok := l[id]
		if !ok || it.Reason != "bulk clean" {
			t.Fatalf("%s is in the trash with the bulk reason: %+v", id, it)
		}
		if batch == "" {
			batch = it.Batch
		} else if it.Batch != batch {
			t.Errorf("one bulk delete is one batch: %s vs %s", it.Batch, batch)
		}
	}
	trashShow(t, c, keep)
	if bl := trashList(t, c, "--batch", batch); bl.Count != 3 {
		t.Errorf("--batch lists the three rows: %+v", bl)
	}
	// Twice: nothing matches any more.
	if r := c.Lore("memory", "delete", "--where", "kind=episodic", "--yes"); r.Code == 0 && trashList(t, c).Count != 3 {
		t.Errorf("a second bulk delete moves nothing more: %s", r)
	}
	res := th.DecodeJSON[trashResultOut](t, c.MustLore("trash", "restore", batch, "--json"))
	if res.Count != 3 {
		t.Fatalf("the batch restore brings all three back: %+v", res)
	}
	c.MustLore("memory", "delete", "--where", "kind=episodic", "--yes")
	b2 := trashList(t, c).Data[0].Batch
	if pr := th.DecodeJSON[trashResultOut](t, c.MustLore("trash", "purge", b2, "--confirm", "--json")); pr.Count != 3 {
		t.Errorf("purging the batch purges all three: %+v", pr)
	}
	for _, id := range gone {
		trashAssertPurgedShow(t, c, id)
	}
	trashShow(t, c, keep)
}

func TestTrash_DeletePurgeSkipsTrash(t *testing.T) {
	tw := newTrashWorld(t, false)
	c := tw.c
	id := trashID(t, c.MustLore("memory", "add", "--body", "leaked by mistake", "--json"))
	trashErr(t, c, "E_NEEDS_CONFIRM", th.ExitNeedsConfirm, "memory", "delete", id, "--purge", "--yes")
	trashShow(t, c, id)
	c.MustLore("memory", "delete", id, "--purge", "--confirm")
	if l := trashList(t, c); l.Count != 0 {
		t.Errorf("delete --purge never goes through the trash: %+v", l)
	}
	trashErr(t, c, "E_PURGED", th.ExitRefused, "trash", "restore", id)
	if f := trashFilesHolding(t, c.Dir+"/.lore/data", "leaked by mistake"); len(f) > 0 {
		t.Errorf("the purged text is still in %v", f)
	}
}

func TestTrash_BadInput(t *testing.T) {
	tw := newTrashWorld(t, false)
	c := tw.c
	// A well-formed id no row ever had: a real id with its last digit changed.
	real := trashID(t, c.MustLore("memory", "add", "--body", "id template", "--json"))
	last := "0"
	if strings.HasSuffix(real, "0") {
		last = "1"
	}
	unknown := real[:len(real)-1] + last
	cases := []struct {
		name string
		args []string
		code string
		exit int
		say  string
	}{
		{"show a malformed id", []string{"trash", "show", "not-an-id"}, "E_INVALID_ID", 20, "not a row id"},
		{"show a huge malformed id", []string{"trash", "show", strings.Repeat("x", 5000)}, "E_INVALID_ID", 20, "not a row id"},
		{"show an unknown id", []string{"trash", "show", unknown}, "E_NOT_FOUND", th.ExitNotFound, "is not in the trash"},
		{"restore an unknown id", []string{"trash", "restore", unknown}, "E_NOT_FOUND", th.ExitNotFound, unknown},
		{"purge with nothing to purge", []string{"trash", "purge", "--confirm"}, "E_INVALID_INPUT", 20, "give exactly one of"},
		{"purge an id and --expired", []string{"trash", "purge", unknown, "--expired", "--confirm"}, "E_INVALID_INPUT", 20, "give exactly one of"},
		{"list --since nonsense", []string{"trash", "list", "--since", "bogus"}, "E_INVALID_INPUT", 20, "want a duration"},
		{"list a negative --limit", []string{"trash", "list", "--limit", "-1"}, "E_INVALID_INPUT", 20, "positive number"},
		{"list a negative --page", []string{"trash", "list", "--page", "-2"}, "E_INVALID_INPUT", 20, "positive number"},
		{"restore --from-history", []string{"trash", "restore", unknown, "--from-history"}, "E_PURGED", th.ExitRefused, "never come back"},
		{"delete an unknown memory", []string{"memory", "delete", unknown, "--yes"}, "E_NOT_FOUND", th.ExitNotFound, unknown},
		{"delete with no id and no --where", []string{"memory", "delete", "--yes"}, "E_INVALID_INPUT", 20, "give an id or --where"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := trashErr(t, c, tc.code, tc.exit, tc.args...)
			if !strings.Contains(e.Error.Message, tc.say) {
				t.Errorf("the message says %q: %+v", tc.say, e.Error)
			}
			if len(e.Error.Message) > 600 {
				t.Errorf("the message never floods the terminal (%d bytes)", len(e.Error.Message))
			}
		})
	}
	// Restoring a live row and deleting a trashed row name the place.
	id := trashID(t, c.MustLore("memory", "add", "--body", "alive", "--json"))
	e := trashErr(t, c, "E_NOT_FOUND", th.ExitNotFound, "trash", "restore", id)
	if !strings.Contains(e.Error.Message, "alive in repo") {
		t.Errorf("restore of a live row says it is alive: %+v", e.Error)
	}
	c.MustLore("memory", "delete", id, "--yes")
	if r := c.Lore("memory", "delete", id, "--yes", "--json"); r.Code == 0 {
		t.Errorf("deleting a row already in the trash is not a silent success: %s", r)
	}
	if l := trashList(t, c); l.Count != 1 {
		t.Errorf("a second delete does not add a second trash entry: %+v", l)
	}
}

func TestTrash_ListFiltersPagingReasons(t *testing.T) {
	tw := newTrashWorld(t, false)
	c := tw.c
	tw.me = trashMe(t, c)
	reasons := []string{
		"plain reason",
		"ünïcødé — 日本語 reason ✓",
		strings.Repeat("r", 200),
		"",
	}
	ids := map[string]string{}
	for i, r := range reasons {
		id := trashID(t, c.MustLore("decision", "add", "--title", "decision "+strconv.Itoa(i), "--body", "b", "--json"))
		args := []string{"decision", "delete", id, "--yes"}
		if r != "" {
			args = append(args, "--reason", r)
		}
		c.MustLore(args...)
		ids[id] = r
	}
	mem := trashID(t, c.MustLore("memory", "add", "--body", "a memory", "--json"))
	// A reason past the 200-character limit is refused and nothing moves.
	e := trashErr(t, c, "E_INVALID_INPUT", 20, "memory", "delete", mem, "--yes", "--reason", strings.Repeat("r", 201))
	if !strings.Contains(e.Error.Message, "200 characters") {
		t.Errorf("the refusal names the limit: %+v", e.Error)
	}
	trashShow(t, c, mem)
	c.MustLore("memory", "delete", mem, "--yes")

	all := trashList(t, c)
	if all.Count != 5 || all.Total != 5 || all.From != 0 {
		t.Fatalf("five rows, no paging: %+v", all)
	}
	if all.Data[0].ID != mem {
		t.Errorf("newest first: %s before %s", all.Data[0].ID, mem)
	}
	for id, r := range ids {
		if got := trashByID(all)[id].Reason; got != strings.TrimSpace(r) && got != r {
			t.Errorf("reason of %s kept as written: %q, want %q", id, got, r)
		}
	}
	for _, f := range []struct {
		flags []string
		want  int
	}{
		{[]string{"--type", "decision"}, 4},
		{[]string{"--type", "decisions"}, 4},
		{[]string{"--type", "memory"}, 1},
		{[]string{"--type", "memories"}, 1},
		{[]string{"--type", "tasks"}, 0},
		{[]string{"--to", "repo"}, 5},
		{[]string{"--reason", "日本語"}, 1},
		{[]string{"--reason", "PLAIN"}, 1},
		{[]string{"--search", "decision 1"}, 1},
		{[]string{"--since", "1h"}, 5},
		{[]string{"--since", "2999-01-01"}, 0},
		{[]string{"--by", tw.me}, 5},
	} {
		if got := trashList(t, c, f.flags...); got.Count != f.want || got.Total != f.want {
			t.Errorf("trash list %v: %d rows (total %d), want %d", f.flags, got.Count, got.Total, f.want)
		}
	}
	trashErr(t, c, "E_NOT_FOUND", th.ExitNotFound, "trash", "list", "--to", "nowhere")
	p1 := trashList(t, c, "--limit", "2")
	p3 := trashList(t, c, "--limit", "2", "--page", "3")
	if p1.Count != 2 || p1.Total != 5 || p1.From != 1 || p3.Count != 1 || p3.From != 5 {
		t.Errorf("paging: page 1 %+v, page 3 %+v", p1, p3)
	}
	if past := trashList(t, c, "--limit", "2", "--page", "9"); past.Count != 0 || past.Total != 5 {
		t.Errorf("a page past the end is empty, the total kept: %+v", past)
	}
	// The text listing names the restore; the stats agree with the list.
	c.MustLore("trash", "list").Says(t, mem)
	st := th.DecodeJSON[struct {
		Rows    int `json:"rows"`
		ByTable []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"by_table"`
	}](t, c.MustLore("trash", "stats", "--json"))
	if st.Rows != 5 {
		t.Errorf("trash stats counts five rows: %+v", st)
	}
}

func TestTrash_TwoClonesTeamStore(t *testing.T) {
	tw := newTrashWorld(t, true)
	alice := tw.c
	bobA := tw.w.Human("bob", "bob@example.com")
	bobA.WriteConfig(th.Config{Live: trashLive()})
	bob := bobA.Clone(tw.app, "app")
	bob.MustLore("sync")

	// Deleted on A: in B's trash after the sync; B's restore reaches A.
	id := trashID(t, alice.MustLore("memory", "add", "--body", "shared team note", "--to", trashStoreKB, "--json"))
	alice.MustLore("sync")
	bob.MustLore("sync")
	before := trashShow(t, bob, id)
	alice.MustLore("memory", "delete", id, "--yes", "--reason", "outdated")
	alice.MustLore("sync")
	bob.MustLore("sync")
	it, ok := trashByID(trashList(t, bob))[id]
	if !ok || it.Store != trashStoreKB || it.Reason != "outdated" {
		t.Fatalf("bob's clone lists alice's delete in the kb store: %+v", it)
	}
	trashErr(t, bob, "E_IN_TRASH", th.ExitRefused, "show", id)
	bob.MustLore("trash", "restore", id)
	bob.MustLore("sync")
	alice.MustLore("sync")
	if got := trashShow(t, alice, id); !trashMapsEqual(got.Fields, before.Fields) {
		t.Errorf("bob's restore reaches alice with the same fields:\n%v\n%v", before.Fields, got.Fields)
	}

	// Deleted on A while B edits (B not yet synced): the edit keeps the
	// row alive on both, listed under --overruled, not in the trash.
	bob.With(th.EnvAutopush+"=0").MustLore("memory", "edit", id, "--body", "shared team note, still needed")
	alice.MustLore("memory", "delete", id, "--yes", "--reason", "obsolete")
	alice.MustLore("sync")
	bob.MustLore("sync")
	alice.MustLore("sync")
	for name, c := range map[string]*th.Clone{"alice": alice, "bob": bob} {
		if got := trashShow(t, c, id); got.Fields["body"] != "shared team note, still needed" {
			t.Errorf("%s: the edit wins over the concurrent delete: %v", name, got.Fields)
		}
		if _, in := trashByID(trashList(t, c))[id]; in {
			t.Errorf("%s: the kept-alive row is not in the trash", name)
		}
		if _, over := trashByID(trashList(t, c, "--overruled"))[id]; !over {
			t.Errorf("%s: trash list --overruled lists %s", name, id)
		}
	}

	// Purged on A: gone on B for good.
	alice.MustLore("memory", "delete", id, "--yes")
	alice.MustLore("trash", "purge", id, "--confirm")
	alice.MustLore("sync")
	bob.MustLore("sync")
	trashAssertPurgedShow(t, bob, id)
	trashErr(t, bob, "E_PURGED", th.ExitRefused, "trash", "restore", id)
	if tips := trashTipsHolding(t, tw.w, tw.kb, "shared team note"); len(tips) > 0 {
		t.Errorf("the purged text is in the kb store's %v", tips)
	}
}

// TestTrash_ReasonOnEveryDelete (INTEG-TRASH-3): under policy
// delete.require_reason every verb that moves a row to the trash takes
// --reason. task view delete, task template delete, link remove and vars
// rm had none (and vars rm's --reason was only the secret answer), so
// under the policy those rows could never be deleted.
func TestTrash_ReasonOnEveryDelete(t *testing.T) {
	tw := newTrashWorld(t, false)
	c := tw.c
	c.MustLore("policy", "set", "delete.require_reason", "true", "--yes")
	task := trashID(t, c.MustLore("task", "add", "a task to snapshot", "--tasklist", tw.tasklists[trashStoreRepo], "--json"))
	view := trashID(t, c.MustLore("task", "view", "add", "open_ones", "--where", "status=todo", "--json"))
	tpl := trashID(t, c.MustLore("task", "template", "add", "release_steps", "--from", task, "--json"))
	link := trashID(t, c.MustLore("link", "add", "--entity", task, "--commit", "HEAD", "--json"))
	vr := trashID(t, c.MustLore("vars", "set", "build_dir", "dist", "--json"))
	cases := []struct {
		name, id string
		del      []string
	}{
		{"task view", view, []string{"task", "view", "delete", "open_ones", "--yes"}},
		{"task template", tpl, []string{"task", "template", "delete", "release_steps", "--yes"}},
		{"commit link", link, []string{"link", "remove", link, "--yes"}},
		{"variable", vr, []string{"vars", "rm", "build_dir", "--yes"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trashErr(t, c, "E_REASON_REQUIRED", 20, tc.del...)
			if _, in := trashByID(trashList(t, c))[tc.id]; in {
				t.Fatalf("a refused delete moved nothing: %s is in the trash", tc.id)
			}
			why := "cleanup of " + tc.name
			c.MustLore(append(tc.del, "--reason", why)...)
			if got := trashByID(trashList(t, c))[tc.id].Reason; got != why {
				t.Errorf("trash list keeps the reason of %s: %q, want %q", tc.id, got, why)
			}
		})
	}
}

// TestTrash_CommentsStayLiveOnTrashedRow (INTEG-TRASH-1): a comment is its
// own row with a tombstone_only pointer at its target (schema
// danglingEntity), so deleting the target leaves the comment live and out
// of the trash, and the help no longer calls comments dependents.
func TestTrash_CommentsStayLiveOnTrashedRow(t *testing.T) {
	tw := newTrashWorld(t, false)
	c := tw.c
	dec := trashID(t, c.MustLore("decision", "add", "--title", "use sqlite", "--body", "b", "--json"))
	cmt := trashID(t, c.MustLore("comment", "add", "--on-table", "decisions", "--on-id", dec, "--body", "agreed", "--json"))
	c.MustLore("decision", "delete", dec, "--yes")
	for _, flags := range [][]string{nil, {"--with-dependents"}} {
		l := trashList(t, c, flags...)
		if _, in := trashByID(l)[cmt]; in || l.Dependents != 0 {
			t.Errorf("trash list %v: the comment is not trashed with its decision: %+v", flags, l)
		}
	}
	c.MustLore("comment", "list", "--on-id", dec, "--json").Says(t, cmt)
	h := c.MustLore("trash", "list", "--help")
	h.Says(t, "comments and links stay live")
}
