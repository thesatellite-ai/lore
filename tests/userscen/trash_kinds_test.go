//go:build userscen

package userscen

// Trash area, the kind × store matrix (DELETION 2.2-2.6, CLI_REFERENCE 3.9)
// driven through the real binary:
//
//   - every table of the schema registry is either a trash kind with a
//     recipe here or listed with the reason it has no user delete path, so
//     a new table fails TestTrash_EveryKindClassified until someone decides;
//   - for every kind × store (repo's own rows, the personal folder store
//     `me`, a team git store `kb`): add → delete → in the trash (list, show,
//     table, store, reason, batch) and gone from show, search and render →
//     restore → the same id with every stored field identical (tags
//     included), its comment still there → delete → purge refused without
//     --confirm → purge → gone from show, the trash, search, render, the
//     files of the store and the tip of every ref of its remote after a
//     sync; a purged id cannot be restored (E_PURGED) and a second purge
//     says E_PURGED too.

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// trashKind is how a person adds and deletes rows of one table.
type trashKind struct {
	noun []string
	// add returns the arguments after "<noun> add"; mark is a word the
	// row's stored text holds (search, files, render look for it).
	add func(tw *trashWorld, store, mark string) []string
	// byName: the delete takes the row's name (mark), not its id.
	byName bool
	// del overrides the delete verb ("rm" for vars), addVerb the add verb
	// ("set" for vars).
	del, addVerb string
	// toStore: the add honours --to <store>; false = repo only.
	toStore bool
	// noReason: the delete verb has no --reason (bugs.md question).
	noReason bool
}

// trashTitleBody is the add of a kind with --title and --body.
func trashTitleBody(_ *trashWorld, _, mark string) []string {
	return []string{"--title", mark + " title", "--body", mark + " body text"}
}

// trashKinds maps a schema table to its recipe.
var trashKinds = map[string]trashKind{
	"architecture_notes": {noun: []string{"architecturenote"}, add: trashTitleBody, toStore: true},
	"behaviours":         {noun: []string{"behaviour"}, add: trashTitleBody, toStore: true},
	"cookbook_recipes":   {noun: []string{"cookbookrecipe"}, add: trashTitleBody, toStore: true},
	"decisions":          {noun: []string{"decision"}, add: trashTitleBody, toStore: true},
	"hotfixes": {noun: []string{"hotfix"}, toStore: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{"--title", m + " title", "--body", m + " body text", "--severity", "high"}
	}},
	"incidents": {noun: []string{"incident"}, add: trashTitleBody, toStore: true},
	"memories": {noun: []string{"memory"}, toStore: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{"--body", m + " memory body"}
	}},
	"missions": {noun: []string{"mission"}, toStore: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{m + " mission title"}
	}},
	"patterns":  {noun: []string{"pattern"}, add: trashTitleBody, toStore: true},
	"plans":     {noun: []string{"plan"}, add: trashTitleBody, toStore: true},
	"playbooks": {noun: []string{"playbook"}, add: trashTitleBody, toStore: true},
	"prompts":   {noun: []string{"prompt"}, add: trashTitleBody, toStore: true},
	"reminders": {noun: []string{"reminder"}, toStore: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{"--due", "2027-01-01", m + " reminder text"}
	}},
	"rules": {noun: []string{"rule"}, toStore: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{"--body", m + " must hold", "--severity", "must"}
	}},
	"snapshots":   {noun: []string{"snapshot"}, add: trashTitleBody, toStore: true},
	"suggestions": {noun: []string{"suggestion"}, add: trashTitleBody, toStore: true},
	"tasks": {noun: []string{"task"}, toStore: true, add: func(tw *trashWorld, s, m string) []string {
		return []string{m + " task title", "--tasklist", tw.tasklists[s]}
	}},
	"task_lists": {noun: []string{"tasklist"}, add: trashTitleBody, toStore: true},
	"taste_prefs": {noun: []string{"tastepref"}, toStore: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{"--body", m + " taste"}
	}},
	"tech_docs": {noun: []string{"techdoc"}, toStore: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{"--name", m, "--base-url", "https://example.com/" + m}
	}},
	"workflows":  {noun: []string{"workflow"}, add: trashTitleBody, toStore: true},
	"workspaces": {noun: []string{"workspace"}, add: trashTitleBody, toStore: true},
	"handoffs": {noun: []string{"handoff"}, add: func(tw *trashWorld, _, m string) []string {
		return []string{"--for", tw.me, m + " handoff note"}
	}},
	"meta_keys": {noun: []string{"meta"}, byName: true, toStore: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{m, "--type", "text", "--description", m + " key"}
	}},
	"vars": {noun: []string{"vars"}, byName: true, del: "rm", addVerb: "set", toStore: true, noReason: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{m, m + "value"}
	}},
	"task_views": {noun: []string{"task", "view"}, byName: true, toStore: true, noReason: true, add: func(_ *trashWorld, _, m string) []string {
		return []string{m, "--where", "priority>=high"}
	}},
}

// trashNotAKind is every other table and why the matrix does not drive it.
var trashNotAKind = map[string]string{
	"actors":             "the roster: actor delete / offboard has its own flow (engine/actor_delete_test.go)",
	"projects":           "project delete: the on-delete restrict and cascade flows (trash_flows_test.go)",
	"repos":              "a repos row goes with its project delete (r3-projdel), covered by the project flow",
	"policies":           "policy unset removes a level's row; not a trash delete",
	"meta_values":        "cascade dependents of a meta key (DELETION 3.4); deleted with their key",
	"kinds":              "resource kinds: no delete verb (rename / retire only)",
	"kind_fields":        "cascade dependents of a kind",
	"secret_rules":       "secrets rule on/off toggles a row; no delete verb",
	"secret_allows":      "written by --not-a-secret; no delete verb",
	"tags":               "tags: no delete verb (attach / detach only)",
	"resources":          "resources: the resource noun's own delete and merge flows (ops/settings)",
	"scopes":             "folder scopes are written by render and moved by scope move; no delete verb",
	"comments":           "comment delete: covered in trash_flows_test.go (a dependent kind, needs a target row)",
	"commit_links":       "link remove: covered in trash_flows_test.go (needs a commit)",
	"task_templates":     "task template delete needs a YAML file; covered in trash_flows_test.go",
	"external_sources":   "external-source enable / disable only; no delete verb",
	"knowledge_refs":     "links between knowledge rows, written by relate commands; tombstone_only referrers",
	"rule_verifier_refs": "cascade dependents of a rule",
	"runs":               "machine-local agent runs (never synced); no delete verb",
	"sessions":           "machine-local agent sessions; no delete verb",
	"render_histories":   "machine-local render log; no delete verb",
	"tech_doc_pages":     "machine-local fetched doc pages; no delete verb",
	"pii_patterns":       "machine-local; the old pii-pattern noun only enables / disables",
	"teams":              "teams: no delete verb",
	"machines":           "machine profiles: written by lore itself; no delete verb",
	"actor_contacts":     "team profile rows (never client): actor contact flows",
	"contact_types":      "contact types are retired, not deleted",
	"roles":              "roles are retired, not deleted",
	"actor_periods":      "team profile rows: actor period flows",
	"project_members":    "team profile rows: project member flows",
	"invites":            "team invite revoke flow",
	"setup_progress":     "machine-local guided setup state",
	"known_repos":        "machine-local list of clones",
}

func TestTrash_EveryKindClassified(t *testing.T) {
	seen := map[string]bool{}
	for _, def := range schema.Builtin() {
		name := string(def.Name)
		seen[name] = true
		_, kind := trashKinds[name]
		_, other := trashNotAKind[name]
		switch {
		case kind && other:
			t.Errorf("%s is both a trash kind and listed as not one", name)
		case !kind && !other:
			t.Errorf("table %s is new: give it a trash recipe in trashKinds or a reason in trashNotAKind", name)
		}
	}
	for name := range trashKinds {
		if !seen[name] {
			t.Errorf("trashKinds names %s, which the schema registry no longer has", name)
		}
	}
	for name := range trashNotAKind {
		if !seen[name] {
			t.Errorf("trashNotAKind names %s, which the schema registry no longer has", name)
		}
	}
}

// trashCase is one kind × store row of the matrix while it runs.
type trashCase struct {
	table, store, mark, id, comment string
	k                               trashKind
	before                          trashShowOut
	searchable, rendered            bool
}

// trashMark is the case's marker: lowercase letters and underscores only,
// so it is a valid snake_case name and one search token.
func trashMark(table, store string) string {
	return "tmk_" + strings.ReplaceAll(table, "_", "") + "_" + store
}

// trashRender renders and returns LORE.md + LORE.full.md of the clone.
func trashRender(t *testing.T, c *th.Clone) string {
	t.Helper()
	c.MustLore("render")
	var b strings.Builder
	for _, f := range []string{"LORE.md", "LORE.full.md"} {
		if s, err := os.ReadFile(filepath.Join(c.Dir, ".lore", f)); err == nil {
			b.Write(s)
		}
	}
	return b.String()
}

// trashStoreDir is where a store's row files live on this machine.
func trashStoreDir(tw *trashWorld, store string) string {
	switch store {
	case trashStoreMe:
		return tw.a.LorePath("stores", "_me")
	case trashStoreKB:
		return "" // a git store: checked on its remote
	}
	return filepath.Join(tw.c.Dir, ".lore", "data")
}

// trashDeleteArgs is the delete command of a case.
func trashDeleteArgs(tc *trashCase, reason string) []string {
	verb := "delete"
	if tc.k.del != "" {
		verb = tc.k.del
	}
	target := tc.id
	if tc.k.byName {
		target = tc.mark
	}
	args := append(slices.Clone(tc.k.noun), verb, target, "--yes")
	if reason != "" && !tc.k.noReason {
		args = append(args, "--reason", reason)
	}
	return args
}

// trashShards is how many Worlds share one store's kinds. Every kind's
// lifecycle in one World took about 25 lore commands per kind in sequence
// (the suite's longest test); split round-robin over the sorted kinds,
// each World still holds rows of many kinds side by side through every
// phase, so a delete, restore or purge that touched another kind's row is
// still caught, and the shards run in parallel.
const trashShards = 3

// trashPositives counts, over every shard of one store, the added rows
// search found and render showed: the positive controls of the
// gone-everywhere checks, which need at least one of each per store.
type trashPositives struct {
	mu                 sync.Mutex
	searched, rendered int
}

func TestTrash_KindLifecycleMatrix(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	names := make([]string, 0, len(trashKinds))
	for name := range trashKinds {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, store := range trashStores {
		t.Run(store, func(t *testing.T) {
			scenParallel(t)
			var kinds []string
			for _, table := range names {
				if trashKinds[table].toStore || store == trashStoreRepo {
					kinds = append(kinds, table)
				}
			}
			pos := &trashPositives{}
			if store == trashStoreRepo {
				// Runs after every shard (sub-tests finish before their
				// parent's cleanup): some kinds are found by search and
				// some are rendered, or those checks would pass vacuously.
				t.Cleanup(func() {
					pos.mu.Lock()
					defer pos.mu.Unlock()
					if pos.searched == 0 || pos.rendered == 0 {
						t.Errorf("search found %d and render showed %d of the added rows; both checks need positives", pos.searched, pos.rendered)
					}
				})
			}
			for i := range trashShards {
				var shard []string
				for j := i; j < len(kinds); j += trashShards {
					shard = append(shard, kinds[j])
				}
				t.Run("shard"+strconv.Itoa(i+1), func(t *testing.T) {
					trashMatrixStore(t, store, shard, pos) // one World per shard
				})
			}
		})
	}
}

// trashMatrixStore runs the lifecycle of the given kinds in one store, in
// one World, and adds its positive controls to pos.
func trashMatrixStore(t *testing.T, store string, kinds []string, pos *trashPositives) {
	tw := newTrashWorld(t, store != trashStoreRepo)
	tw.me = trashMe(t, tw.c)
	c := tw.c
	var tag string
	if store == trashStoreRepo {
		tag = "trash_matrix_tag"
		c.MustLore("tag", "add", "--name", tag)
	}
	renderedBefore := ""
	var cases []*trashCase

	// Phase A: add every kind, with a tag and a comment in the repo store.
	for _, table := range kinds {
		k := trashKinds[table]
		tc := &trashCase{table: table, store: store, k: k, mark: trashMark(table, store)}
		verb := "add"
		if k.addVerb != "" {
			verb = k.addVerb
		}
		args := append(append(slices.Clone(k.noun), verb), k.add(tw, store, tc.mark)...)
		if k.toStore {
			args = append(args, "--to", store)
		}
		tc.id = trashID(t, c.MustLore(append(args, "--json")...))
		if tag != "" {
			if def, ok := trashDef(table); ok {
				if _, has := def.Field("tag_ids"); has {
					c.MustLore("tag", "attach", "--on-table", table, "--on-id", tc.id, "--tag", tag)
				}
			}
			tc.comment = trashID(t, c.MustLore("comment", "add", "--on-table", table, "--on-id", tc.id, "--body", "a comment on the row", "--json"))
		}
		tc.before = trashShow(t, c, tc.id)
		if tc.before.Table != table || tc.before.Store != store {
			t.Fatalf("%s: show says table %q store %q, want %s in %s", tc.mark, tc.before.Table, tc.before.Store, table, store)
		}
		tc.searchable = trashContains(trashSearchIDs(t, c, tc.mark), tc.id)
		cases = append(cases, tc)
	}
	renderedBefore = trashRender(t, c)
	trashPublish(tw)
	for _, tc := range cases {
		tc.rendered = strings.Contains(renderedBefore, tc.mark)
		trashAssertStored(t, tw, tc, true)
	}
	pos.mu.Lock()
	for _, tc := range cases {
		if tc.searchable {
			pos.searched++
		}
		if tc.rendered {
			pos.rendered++
		}
	}
	pos.mu.Unlock()

	// Phase B: delete → trash → restore → identical.
	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) { trashDeleteRestore(t, tw, tc) })
	}
	// Phase C: delete → purge.
	for _, tc := range cases {
		t.Run(tc.table+"/purge", func(t *testing.T) { trashPurge(t, tw, tc) })
	}
	// Render first: LORE.md is committed with the rows, so the published
	// tip must carry the render made after the purges.
	rendered := trashRender(t, c)
	trashPublish(tw)
	for _, tc := range cases {
		if strings.Contains(rendered, tc.mark) {
			t.Errorf("%s: a purged row is still rendered", tc.mark)
		}
		trashAssertStored(t, tw, tc, false)
	}
	if st := th.DecodeJSON[struct {
		Rows       int `json:"rows"`
		Tombstones []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"tombstones"`
	}](t, c.MustLore("trash", "stats", "--json")); st.Rows != 0 {
		t.Errorf("the trash is empty after every purge: %+v", st)
	} else {
		n := 0
		for _, ts := range st.Tombstones {
			if ts.Name == store {
				n = ts.Count
			}
		}
		if n < len(cases) {
			t.Errorf("trash stats counts %d tombstones in %s, want at least %d (one per purged row)", n, store, len(cases))
		}
	}
}

// trashPublish is what a person does to share: lore sync (stores), then
// commit .lore, pull main (live rows lore landed there) and push it (the
// repo's own rows travel with the code).
func trashPublish(tw *trashWorld) {
	tw.c.MustLore("sync")
	if tw.c.Status() != "" {
		tw.c.CommitAll("lore rows")
		tw.c.Git("pull", "-q", "--no-rebase", "origin", "main")
		tw.c.AdminPush("origin", "main")
	}
}

// trashDef is the registry definition of a table.
func trashDef(table string) (schema.TableDef, bool) {
	for _, d := range schema.Builtin() {
		if string(d.Name) == table {
			return d, true
		}
	}
	return schema.TableDef{}, false
}

// trashMe is the person's own act_ id (whoami --json).
func trashMe(t *testing.T, c *th.Clone) string {
	t.Helper()
	r := c.MustLore("whoami", "--json")
	i := strings.Index(r.Stdout, "act_")
	if i < 0 || len(r.Stdout) < i+36 {
		t.Fatalf("whoami names no act_ id: %s", r)
	}
	return r.Stdout[i : i+36]
}

// trashAssertStored checks whether the case's marker is in the store's
// files on this machine and in the tips of its remote (after a sync).
func trashAssertStored(t *testing.T, tw *trashWorld, tc *trashCase, want bool) {
	t.Helper()
	var where []string
	if dir := trashStoreDir(tw, tc.store); dir != "" {
		where = append(where, trashFilesHolding(t, dir, tc.mark)...)
	}
	switch tc.store {
	case trashStoreKB:
		where = append(where, trashTipsHolding(t, tw.w, tw.kb, tc.mark)...)
	case trashStoreRepo:
		// The repo's rows travel with the code: what the person commits and
		// pushes (trashPublish) is what a teammate's clone gets.
		where = append(where, trashTipsHolding(t, tw.w, tw.app, tc.mark)...)
	}
	if want && len(where) == 0 {
		t.Errorf("%s: the added row is in no file or ref of store %s (the check would pass vacuously)", tc.mark, tc.store)
	}
	if !want && len(where) > 0 {
		t.Errorf("%s: purged content is still in %v", tc.mark, where)
	}
}

// trashDeleteRestore is phase B of one case.
func trashDeleteRestore(t *testing.T, tw *trashWorld, tc *trashCase) {
	c := tw.c
	reason := "scenario " + tc.mark
	c.MustLore(trashDeleteArgs(tc, reason)...)
	if tc.k.noReason {
		reason = ""
	}

	// Gone from the normal reads, with the trash named.
	e := trashErr(t, c, "E_IN_TRASH", th.ExitRefused, "show", tc.id)
	if !strings.Contains(e.Error.Hint, "lore trash restore "+tc.id) {
		t.Errorf("show of a trashed row names the restore: %+v", e.Error)
	}
	if tc.searchable && trashContains(trashSearchIDs(t, c, tc.mark), tc.id) {
		t.Errorf("search still finds the trashed %s", tc.id)
	}
	if tc.rendered && strings.Contains(trashRender(t, c), tc.mark) {
		t.Errorf("render still shows the trashed %s", tc.id)
	}

	// In the trash, as the person deleted it.
	it, ok := trashByID(trashList(t, c))[tc.id]
	if !ok {
		t.Fatalf("trash list does not list %s", tc.id)
	}
	if it.Table != tc.table || it.Store != tc.store || it.Reason != reason || !strings.HasPrefix(it.Batch, "bat_") ||
		it.DeletedAt == "" || !strings.HasPrefix(it.DeletedBy, "act_") || it.Dependent {
		t.Errorf("trash item of %s: %+v", tc.id, it)
	}
	// The list filters once per kind in the repo store (the other stores
	// keep the run short; their store filter is checked here).
	filters := [][]string{{"--to", tc.store}}
	if tc.store == trashStoreRepo {
		filters = append(filters, []string{"--type", tc.table}, []string{"--batch", it.Batch}, []string{"--search", tc.mark})
	}
	for _, flags := range filters {
		if _, ok := trashByID(trashList(t, c, flags...))[tc.id]; !ok {
			t.Errorf("trash list %v does not list %s", flags, tc.id)
		}
	}
	sh := th.DecodeJSON[struct {
		Data   trashItem         `json:"data"`
		Fields map[string]string `json:"fields"`
	}](t, c.MustLore("trash", "show", tc.id, "--json"))
	if reason != "" && tc.store == trashStoreRepo {
		if _, ok := trashByID(trashList(t, c, "--reason", tc.mark))[tc.id]; !ok {
			t.Errorf("trash list --reason does not list %s", tc.id)
		}
	}
	if sh.Data.ID != tc.id || sh.Fields["deleted_reason"] != reason {
		t.Errorf("trash show %s: %+v", tc.id, sh)
	}
	for k, v := range tc.before.Fields {
		if sh.Fields[k] != v {
			t.Errorf("trash show keeps %s=%q of the row, got %q", k, v, sh.Fields[k])
		}
	}

	// Restore: the same id, every stored field as before.
	res := th.DecodeJSON[trashResultOut](t, c.MustLore("trash", "restore", tc.id, "--json"))
	if res.Action != "restored" || res.Count != 1 || len(res.Rows) != 1 || res.Rows[0].ID != tc.id || res.Batch != it.Batch {
		t.Errorf("restore output of %s: %+v", tc.id, res)
	}
	after := trashShow(t, c, tc.id)
	if !trashMapsEqual(after.Fields, tc.before.Fields) || after.Store != tc.store {
		t.Errorf("restored %s differs from before:\nbefore %v\nafter  %v", tc.id, tc.before.Fields, after.Fields)
	}
	if _, still := trashByID(trashList(t, c))[tc.id]; still {
		t.Errorf("restored %s is still in the trash", tc.id)
	}
	if tc.searchable && !trashContains(trashSearchIDs(t, c, tc.mark), tc.id) {
		t.Errorf("search does not find the restored %s", tc.id)
	}
	if tc.rendered && !strings.Contains(trashRender(t, c), tc.mark) {
		t.Errorf("render does not show the restored %s", tc.id)
	}
	if tc.comment != "" {
		cl := c.MustLore("comment", "list", "--on-id", tc.id, "--json")
		if !strings.Contains(cl.Stdout, tc.comment) {
			t.Errorf("the comment on %s is gone after the restore: %s", tc.id, cl)
		}
	}
	// A second restore of a live row says where it is.
	trashErr(t, c, "E_NOT_FOUND", th.ExitNotFound, "trash", "restore", tc.id)
}

// trashPurge is phase C of one case.
func trashPurge(t *testing.T, tw *trashWorld, tc *trashCase) {
	c := tw.c
	c.MustLore(trashDeleteArgs(tc, "")...)
	e := trashErr(t, c, "E_NEEDS_CONFIRM", th.ExitNeedsConfirm, "trash", "purge", tc.id)
	if !strings.Contains(e.Error.Hint, "--confirm") {
		t.Errorf("purge without --confirm names --confirm: %+v", e.Error)
	}
	if _, ok := trashByID(trashList(t, c))[tc.id]; !ok {
		t.Fatalf("a refused purge leaves %s in the trash", tc.id)
	}
	res := th.DecodeJSON[trashResultOut](t, c.MustLore("trash", "purge", tc.id, "--confirm", "--json"))
	if res.Action != "purged" || res.Count < 1 || !slices.ContainsFunc(res.Rows, func(i trashItem) bool { return i.ID == tc.id }) {
		t.Errorf("purge output of %s: %+v", tc.id, res)
	}
	trashAssertPurgedShow(t, c, tc.id)
	if _, ok := trashByID(trashList(t, c))[tc.id]; ok {
		t.Errorf("purged %s is still in the trash", tc.id)
	}
	if trashContains(trashSearchIDs(t, c, tc.mark), tc.id) {
		t.Errorf("search finds the purged %s", tc.id)
	}
	for _, args := range [][]string{{"trash", "restore", tc.id}, {"trash", "purge", tc.id, "--confirm"}} {
		e := trashErr(t, c, "E_PURGED", th.ExitRefused, args...)
		if !strings.Contains(e.Error.Message, tc.id) || !strings.Contains(e.Error.Message, "never come back") {
			t.Errorf("%v: names the id and that it never comes back: %+v", args, e.Error)
		}
	}
	// The SQLite cache has no row for it either.
	q := th.DecodeJSON[struct {
		Count int `json:"count"`
	}](t, c.MustLore("sql", "select id from "+tc.table+" where id='"+tc.id+"'", "--json"))
	if q.Count != 0 {
		t.Errorf("the cache still answers the purged %s", tc.id)
	}
}

// trashMapsEqual reports whether two string maps hold the same entries.
func trashMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
