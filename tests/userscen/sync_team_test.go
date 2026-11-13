//go:build userscen

package userscen

// sync_team_test.go — what a team sees when two or three people work in
// clones of ONE bare remote (github.com/acme/app), driven through the real
// binary. Gaps left by tests/teame2e (which covers the live ref internals,
// landing, ride-along, compaction and two-clone races); scenarios here:
//
//   - three clones: a live task reaches the other two at once; a committed
//     row reaches them only through git (after a pull), with the same id
//   - `lore sync` run twice and three times on a settled clone is a no-op:
//     "· already in sync", an empty sync.report, no new commit anywhere
//   - offline / remote refusing refs/lore/*: the write waits, and `lore
//     sync` says so (text and --json live_pending) instead of "already in
//     sync"; back online one sync delivers it and the teammate sees it
//   - three people edit DIFFERENT fields of one live task while offline:
//     after each syncs, every clone shows all three edits
//   - two people edit the SAME text field of a committed row: git's merge
//     leaves markers, `lore sync conflicts` / `sync status` surface it,
//     `lore sync resolve` refuses bad input with the exact code and settles
//     kept|other (matrix), and the settled file travels back through git
//   - delete versus edit: the delete is seen at once (E_IN_TRASH with the
//     restore hint), restore + edit wins over the delete in the git merge
//   - live off for a kind (live.defaults) keeps rows with the code; --live
//     and --no-live override per write
//   - nothing machine-local (HOME paths, ~/.lore files) lands in the git
//     objects anyone who clones the remote can read

import (
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Refs and paths of the live ref the scenarios read (LORE_TEAM_SPEC §1, §2).
const (
	syncRefMain   = "refs/heads/main"
	syncRefLive   = "refs/lore/live"
	syncRootDir   = "_root"
	syncAlready   = "· already in sync"
	syncSlugApp   = "github.com/acme/app"
	syncTblTasks  = "tasks"
	syncTblMemory = "memories"
)

// syncPeople are the placeholder people of a team scenario, in clone order.
var syncPeople = []struct{ name, email string }{
	{"alice", "alice@example.com"},
	{"bob", "bob@example.com"},
	{"person", "person@example.com"},
}

// syncTeam is one code repo with lore at its root and n people, each with
// their own HOME and clone. clones[0] ran lore init and pushed it.
type syncTeam struct {
	w        *th.World
	app      *th.Remote
	people   []*th.Actor
	clones   []*th.Clone
	tasklist string
}

// newSyncTeam bootstraps acme/app for n people (2 or 3). cfg, when set,
// is person i's config; otherwise every person gets fetch_ttl 0s.
func newSyncTeam(t *testing.T, n int, cfg ...th.Config) *syncTeam {
	t.Helper()
	scenParallel(t)
	w := newWorld(t)
	st := &syncTeam{w: w, app: w.Remote(syncSlugApp)}
	for i := 0; i < n; i++ {
		p := w.Human(syncPeople[i].name, syncPeople[i].email)
		c := th.Config{Live: th.FreshFetch()}
		if i < len(cfg) {
			c = cfg[i]
		}
		p.WriteConfig(c)
		st.people = append(st.people, p)
	}
	first := st.people[0].Clone(st.app, "app")
	first.MustLore("init", "--non-interactive", "--name=app")
	st.tasklist = th.DecodeJSON[syncShowOut](t, first.MustLore("tasklist", "add", "--title", "Inbox", "--body", "default list", "--json")).Data.ID
	first.CommitAll("lore")
	first.AdminPush("origin", "main")
	st.clones = append(st.clones, first)
	for _, p := range st.people[1:] {
		c := p.Clone(st.app, "app")
		c.MustLore("task", "list")
		st.clones = append(st.clones, c)
	}
	return st
}

// syncShowOut is the envelope of `<noun> add|show --json` for tasks and
// task lists: the row under data.
type syncShowOut struct {
	Data struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
		Body     string `json:"body"`
	} `json:"data"`
}

// syncAddOut is `memory|rule add --json`: the new id and its store.
type syncAddOut struct {
	ID    string `json:"id"`
	Store string `json:"store"`
}

// syncListOut is `<noun> list --json`.
type syncListOut struct {
	Data []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Body  string `json:"body"`
	} `json:"data"`
}

// syncReport is `lore sync --json` (ops/sync RunData).
type syncReport struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          struct {
		Pass      string   `json:"pass"`
		Imported  []string `json:"imported"`
		Exported  []string `json:"exported"`
		Conflicts []struct {
			Path string `json:"path"`
		} `json:"conflicts"`
		Errors []struct {
			Path string `json:"path"`
		} `json:"errors"`
		LivePending int `json:"live_pending"`
		Stores      []struct {
			Store    string   `json:"store"`
			Sent     int      `json:"sent"`
			Received int      `json:"received"`
			Merged   int      `json:"merged"`
			Refused  int      `json:"refused"`
			Problems []string `json:"problems"`
			Offline  bool     `json:"offline"`
		} `json:"stores"`
	} `json:"data"`
}

// syncConflictsOut is `lore sync conflicts --json`.
type syncConflictsOut struct {
	Kind string `json:"kind"`
	Data []struct {
		ID       int64  `json:"id"`
		Table    string `json:"table"`
		RowID    string `json:"row_id"`
		Kept     string `json:"kept"`
		Resolved bool   `json:"resolved"`
	} `json:"data"`
}

// syncStatusOut is `lore sync status --json`.
type syncStatusOut struct {
	Kind string `json:"kind"`
	Data struct {
		OpenConflicts int `json:"open_conflicts"`
	} `json:"data"`
}

// syncLiveDiff is `lore live diff --json`.
type syncLiveDiff struct {
	Local    string   `json:"local"`
	Tracking string   `json:"tracking"`
	Pending  []string `json:"pending"`
}

// syncErrOut is the --json error envelope.
type syncErrOut struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Exit    int    `json:"exit"`
	} `json:"error"`
}

// syncDecodeErr decodes the --json error envelope of a failed command.
func syncDecodeErr(t *testing.T, r th.Result) syncErrOut {
	t.Helper()
	if r.Code == 0 {
		t.Fatalf("expected a failure: %s", r)
	}
	var e syncErrOut
	if err := json.Unmarshal([]byte(r.Stdout), &e); err != nil {
		t.Fatalf("decode the error envelope of %s: %v", r, err)
	}
	return e
}

// addTask adds a task in c and returns its id.
func (st *syncTeam) addTask(t *testing.T, c *th.Clone, title string, extra ...string) string {
	t.Helper()
	r := c.MustLore(append([]string{"task", "add", title, "--tasklist", st.tasklist, "--json"}, extra...)...)
	return th.DecodeJSON[syncShowOut](t, r).Data.ID
}

// syncTitles lists the task titles c sees.
func syncTitles(t *testing.T, c *th.Clone) []string {
	t.Helper()
	var out []string
	for _, d := range th.DecodeJSON[syncListOut](t, c.MustLore("task", "list", "--json")).Data {
		out = append(out, d.Title)
	}
	return out
}

// syncHas reports whether list holds s.
func syncHas(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// syncMemoryBodies lists the memory bodies c sees, by id.
func syncMemoryBodies(t *testing.T, c *th.Clone) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, d := range th.DecodeJSON[syncListOut](t, c.MustLore("memory", "list", "--json")).Data {
		out[d.ID] = d.Body
	}
	return out
}

// liveRowFile is a row's path on the live ref.
func liveRowFile(table, id string) string { return syncRootDir + "/" + table + "/" + id + ".json" }

// ---------------------------------------------------------------- reach

// Three clones: a task (live by default) reaches both teammates on their
// next command with no git step; a memory (kept with the code) reaches them
// only after the author commits and pushes and they pull, with the same id.
func TestSyncTeam_ThreeClonesLiveAtOnceCommittedAfterPull(t *testing.T) {
	st := newSyncTeam(t, 3)
	a, b, c := st.clones[0], st.clones[1], st.clones[2]
	st.addTask(t, a, "ship v1")
	for _, x := range []*th.Clone{b, c} {
		if !syncHas(syncTitles(t, x), "ship v1") {
			t.Fatalf("%s must see the live task at once", x.Dir)
		}
	}
	mem := th.DecodeJSON[syncAddOut](t, a.MustLore("memory", "add", "notes travel with the code", "--json"))
	if mem.Store != "repo" || !strings.HasPrefix(mem.ID, "mem_") {
		t.Fatalf("memory add --json: %+v", mem)
	}
	if _, ok := syncMemoryBodies(t, b)[mem.ID]; ok {
		t.Fatal("a committed row must not reach a teammate before it is pushed")
	}
	if !strings.Contains(a.Status(), ".lore/data/") {
		t.Fatalf("a committed row is a file in the author's working tree: %q", a.Status())
	}
	a.CommitAll("memory")
	a.AdminPush("origin", "main")
	for _, x := range []*th.Clone{b, c} {
		x.Git("pull", "-q", "--no-rebase", "origin", "main")
		if got := syncMemoryBodies(t, x)[mem.ID]; got != "notes travel with the code" {
			t.Fatalf("%s after pull: memory %s body %q", x.Dir, mem.ID, got)
		}
	}
}

// ---------------------------------------------------------------- idempotence

// Sync on a settled clone, three times in a row, in every clone: exit 0,
// "· already in sync", an empty report, and nothing moves (no commit on
// main or the live ref, no change in the working tree).
func TestSyncTeam_SyncTwiceIsANoOp(t *testing.T) {
	st := newSyncTeam(t, 3)
	st.addTask(t, st.clones[0], "settled")
	for _, c := range st.clones {
		c.MustLore("sync") // first sync may settle anything left
		mainBefore, liveBefore := st.app.Rev(syncRefMain), st.app.Rev(syncRefLive)
		head, status := c.Head(), c.Status()
		for i := 0; i < 3; i++ {
			r := c.MustLore("sync")
			if strings.TrimSpace(r.Stdout) != syncAlready {
				t.Fatalf("run %d: settled sync must say %q, got %q", i+1, syncAlready, r.Stdout)
			}
			rep := th.DecodeJSON[syncReport](t, c.MustLore("sync", "--json"))
			if rep.Kind != "sync.report" || rep.SchemaVersion != 1 || rep.Data.Pass != "live" ||
				len(rep.Data.Imported)+len(rep.Data.Exported)+len(rep.Data.Conflicts)+len(rep.Data.Errors) != 0 || rep.Data.LivePending != 0 {
				t.Fatalf("run %d: settled sync report must be empty: %+v", i+1, rep)
			}
		}
		if st.app.Rev(syncRefMain) != mainBefore || st.app.Rev(syncRefLive) != liveBefore {
			t.Fatal("a no-op sync must not move main or the live ref on the remote")
		}
		if c.Head() != head || c.Status() != status {
			t.Fatalf("a no-op sync must not touch the checkout: %q → %q", status, c.Status())
		}
	}
}

// ---------------------------------------------------------------- offline

// A live write that cannot reach the remote waits in the clone, and `lore
// sync` must say it is waiting (never "already in sync", which would tell
// the person their teammates have it). Once the remote takes it again, one
// sync delivers it and the next is a no-op. Matrix: the machine is offline;
// the host refuses refs/lore/*.
func TestSyncTeam_UnpushedWritesAreReportedBySync(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	cases := []struct {
		name     string
		cut, fix func(st *syncTeam)
	}{
		{"offline", func(st *syncTeam) { st.people[0].Offline() }, func(st *syncTeam) { st.people[0].Online() }},
		{"remote refuses lore refs", func(st *syncTeam) { st.app.RefuseLoreRefs() }, func(st *syncTeam) {
			st.w.Git(st.app.Dir, "config", "core.hooksPath", "/dev/null")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newSyncTeam(t, 2)
			a, b := st.clones[0], st.clones[1]
			tc.cut(st)
			r := a.MustLore("task", "add", "written while cut off", "--tasklist", st.tasklist, "--json")
			r.Says(t, "not pushed")
			id := th.DecodeJSON[syncShowOut](t, r).Data.ID
			diff := th.DecodeJSON[syncLiveDiff](t, a.MustLore("live", "diff", "--json"))
			if len(diff.Pending) != 1 || diff.Pending[0] != liveRowFile(syncTblTasks, id) {
				t.Fatalf("live diff must list the waiting write: %+v", diff)
			}
			s := a.MustLore("sync")
			s.Never(t, syncAlready).Says(t, "1 write(s) not pushed", "next sync")
			rep := th.DecodeJSON[syncReport](t, a.MustLore("sync", "--json"))
			if rep.Data.LivePending != 1 {
				t.Fatalf("sync --json must count the waiting write: %+v", rep.Data)
			}
			if syncHas(syncTitles(t, b), "written while cut off") {
				t.Fatal("the teammate cannot have a write that never left the machine")
			}
			tc.fix(st)
			rep = th.DecodeJSON[syncReport](t, a.MustLore("sync", "--json"))
			if rep.Data.LivePending != 0 {
				t.Fatalf("back online, sync must deliver the write: %+v", rep.Data)
			}
			if _, ok := st.app.Read(syncRefLive, liveRowFile(syncTblTasks, id)); !ok {
				t.Fatal("the waiting write must be on the remote live ref after sync")
			}
			if !syncHas(syncTitles(t, b), "written while cut off") {
				t.Fatal("the teammate must see the write after the author's sync")
			}
			if strings.TrimSpace(a.MustLore("sync").Stdout) != syncAlready {
				t.Fatal("the sync after delivery is a no-op")
			}
			if d := th.DecodeJSON[syncLiveDiff](t, a.MustLore("live", "diff", "--json")); len(d.Pending) != 0 || d.Local != d.Tracking {
				t.Fatalf("nothing waits after delivery: %+v", d)
			}
		})
	}
}

// ---------------------------------------------------------------- field merge

// Three people edit three different fields of one live task while offline,
// then come back and sync one after another: every clone ends with all
// three edits (the live merge is per field), and no conflict is reported.
func TestSyncTeam_ThreeOfflineEditsOfDifferentFieldsMerge(t *testing.T) {
	st := newSyncTeam(t, 3)
	id := st.addTask(t, st.clones[0], "draft title")
	for _, c := range st.clones[1:] {
		c.MustLore("task", "list")
	}
	edits := [][]string{
		{"--title", "final title"},
		{"--priority", "high"},
		{"--status", "in_progress"},
	}
	for i, p := range st.people {
		p.Offline()
		st.clones[i].MustLore(append([]string{"task", "edit", id}, edits[i]...)...).Says(t, "not pushed")
	}
	for i, p := range st.people {
		p.Online()
		if rep := th.DecodeJSON[syncReport](t, st.clones[i].MustLore("sync", "--json")); rep.Data.LivePending != 0 {
			t.Fatalf("%s: sync must deliver the offline edit: %+v", p.Name, rep.Data)
		}
	}
	for _, c := range st.clones {
		got := th.DecodeJSON[syncShowOut](t, c.MustLore("task", "show", id, "--json")).Data
		if got.Title != "final title" || got.Priority != "high" || got.Status != "in_progress" {
			t.Fatalf("%s: all three edits must survive the merge: %+v", c.Dir, got)
		}
		if cf := th.DecodeJSON[syncConflictsOut](t, c.MustLore("sync", "conflicts", "--json")); len(cf.Data) != 0 {
			t.Fatalf("edits of different fields are not a conflict: %+v", cf)
		}
	}
}

// ---------------------------------------------------------------- text clash

// syncClash makes the same-field clash on a committed memory: alice pushes
// one body to main, bob commits another and pulls. It returns the memory id.
func syncClash(t *testing.T, st *syncTeam) string {
	t.Helper()
	a, b := st.clones[0], st.clones[1]
	mem := th.DecodeJSON[syncAddOut](t, a.MustLore("memory", "add", "base body", "--json")).ID
	a.CommitAll("memory")
	a.AdminPush("origin", "main")
	b.Git("pull", "-q", "--no-rebase", "origin", "main")
	a.MustLore("memory", "edit", mem, "--body", "alice body")
	a.CommitAll("alice edit")
	a.AdminPush("origin", "main")
	b.MustLore("memory", "edit", mem, "--body", "bob body")
	b.CommitAll("bob edit")
	pull := b.TryGit("pull", "--no-rebase", "origin", "main")
	if pull.Code == 0 {
		t.Fatalf("two different bodies must not merge silently: %s", pull)
	}
	pull.Says(t, "needs a manual merge")
	return mem
}

// Two people change the same text field of a committed row: git's merge
// (through lore's driver) leaves both versions in the field, lore surfaces
// it in `sync conflicts` and `sync status`, refuses bad resolve input with
// the exact code, message and next step, and `sync resolve` keeps the side
// asked for; the settled file is a normal row that travels back through git.
func TestSyncTeam_SameFieldClashIsSurfacedAndResolved(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	cases := []struct {
		take, want string
	}{
		{"kept", "bob body"},    // this branch (the side that pulled)
		{"other", "alice body"}, // the branch merged in
	}
	for _, tc := range cases {
		t.Run(tc.take, func(t *testing.T) {
			st := newSyncTeam(t, 2)
			a, b := st.clones[0], st.clones[1]
			mem := syncClash(t, st)

			cf := th.DecodeJSON[syncConflictsOut](t, b.MustLore("sync", "conflicts", "--json"))
			if cf.Kind != "sync.conflicts" || len(cf.Data) != 1 {
				t.Fatalf("one open conflict expected: %+v", cf)
			}
			got := cf.Data[0]
			if got.Table != syncTblMemory || got.RowID != mem || got.Kept != "both" || got.Resolved || got.ID != 1 {
				t.Fatalf("conflict entry: %+v", got)
			}
			if s := th.DecodeJSON[syncStatusOut](t, b.MustLore("sync", "status", "--json")); s.Data.OpenConflicts != 1 {
				t.Fatalf("sync status must count the open conflict: %+v", s)
			}
			b.MustLore("sync", "status").Says(t, "conflicted files: 1", ".lore/data/"+syncTblMemory+"/"+mem+".json")

			// Wrong input: each names the code, the problem and the next step.
			b.Lore("sync", "resolve", "1", "--take", "bogus").Fails(t, 20, "E_INVALID_INPUT", "--take must be kept or other", "kept keeps")
			b.Lore("sync", "resolve", "1").Fails(t, 20, "E_INVALID_INPUT")
			e := syncDecodeErr(t, b.Lore("sync", "resolve", "9", "--take", tc.take, "--json"))
			if e.Error.Code != "E_NOT_FOUND" || e.Error.Exit != 27 || !strings.Contains(e.Error.Message, "#9") || !strings.Contains(e.Error.Hint, "lore sync conflicts") {
				t.Fatalf("an unknown conflict number: %+v", e)
			}

			b.MustLore("sync", "resolve", "1", "--take", tc.take).Says(t, "resolved #1", "took "+tc.take)
			if body := syncMemoryBodies(t, b)[mem]; body != tc.want {
				t.Fatalf("--take %s must keep %q, got %q", tc.take, tc.want, body)
			}
			if cf := th.DecodeJSON[syncConflictsOut](t, b.MustLore("sync", "conflicts", "--json")); len(cf.Data) != 0 {
				t.Fatalf("a resolved conflict leaves the list: %+v", cf)
			}
			// Resolving it again is not found, not a second write.
			b.Lore("sync", "resolve", "1", "--take", tc.take).Fails(t, 27, "E_NOT_FOUND", "no open conflict #1")

			b.Git("add", "-A")
			b.Git("commit", "-q", "-m", "settle memory")
			b.AdminPush("origin", "main")
			a.Git("pull", "-q", "--no-rebase", "origin", "main")
			if body := syncMemoryBodies(t, a)[mem]; body != tc.want {
				t.Fatalf("the settled row must reach the teammate: %q", body)
			}
		})
	}
}

// ---------------------------------------------------------------- delete vs edit

// alice deletes a committed rule; bob sees the delete at once (his edit is
// refused with E_IN_TRASH naming the restore command). bob restores and
// edits it; when the two branches meet in git, the edit wins over the delete
// (modify beats delete) and both people end with bob's rule.
func TestSyncTeam_DeleteVersusEditKeepsTheEdit(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	rul := th.DecodeJSON[syncAddOut](t, a.MustLore("rule", "add", "base rule", "--json")).ID
	a.CommitAll("rule")
	a.AdminPush("origin", "main")
	b.Git("pull", "-q", "--no-rebase", "origin", "main")

	a.MustLore("rule", "delete", rul, "--yes")
	a.CommitAll("delete rule")
	a.AdminPush("origin", "main")

	b.Lore("rule", "edit", rul, "--body", "bob rule").Refused(t, "E_IN_TRASH", "lore trash restore --id "+rul)
	b.MustLore("trash", "restore", rul, "--yes").Says(t, "restored 1")
	b.MustLore("rule", "edit", rul, "--body", "bob rule")
	b.CommitAll("bob keeps the rule")
	pull := b.TryGit("pull", "--no-rebase", "origin", "main")
	pull.Says(t, "deleted on one side, edited on the other")
	if strings.Contains(b.Status(), "UU .lore/data/rules/") {
		t.Fatalf("delete versus edit must not leave a git conflict on the rule: %s", b.Status())
	}
	if pull.Code != 0 {
		b.Git("commit", "-q", "--no-edit", "-a")
	}
	b.AdminPush("origin", "main")
	a.Git("pull", "-q", "--no-rebase", "origin", "main")
	for _, c := range st.clones {
		got := th.DecodeJSON[syncShowOut](t, c.MustLore("rule", "show", rul, "--json")).Data
		if got.Body != "bob rule" {
			t.Fatalf("%s: the edited rule must win over the delete: %+v", c.Dir, got)
		}
	}
}

// ---------------------------------------------------------------- live on/off

// live.defaults tasks:false keeps bob's tasks with the code (a file in his
// working tree, nothing on the live ref) until git carries them; --live on
// one write overrides it, and --no-live overrides alice's live default.
// Matrix over (default, flag) → where the row goes.
func TestSyncTeam_LiveDefaultsAndFlagsPickWhereARowGoes(t *testing.T) {
	liveOff := th.Config{Live: &th.Live{FetchTTL: "0s", Autopush: th.Bool(true), Defaults: map[string]bool{syncTblTasks: false}}}
	st := newSyncTeam(t, 2, th.Config{Live: th.FreshFetch()}, liveOff)
	cases := []struct {
		who  int
		flag string
		live bool
	}{
		{0, "", true},           // default on
		{0, "--no-live", false}, // default on, flag off
		{1, "", false},          // default off
		{1, "--live", true},     // default off, flag on
	}
	for i, tc := range cases {
		c, other := st.clones[tc.who], st.clones[1-tc.who]
		title := "row " + string(rune('a'+i))
		var extra []string
		if tc.flag != "" {
			extra = append(extra, tc.flag)
		}
		id := st.addTask(t, c, title, extra...)
		_, onLive := st.app.Read(syncRefLive, liveRowFile(syncTblTasks, id))
		if onLive != tc.live {
			t.Fatalf("case %d (%s %q): on live ref = %v, want %v", i, st.people[tc.who].Name, tc.flag, onLive, tc.live)
		}
		seen := syncHas(syncTitles(t, other), title)
		if seen != tc.live {
			t.Fatalf("case %d: teammate sees it before any git step = %v, want %v", i, seen, tc.live)
		}
		if !tc.live && !strings.Contains(c.Status(), ".lore/data/") {
			t.Fatalf("case %d: a row kept with the code is a working-tree file: %q", i, c.Status())
		}
	}
}

// ---------------------------------------------------------------- leaks

// After a day of team work (live rows, committed rows, an offline write, a
// sync) nothing machine-local is in any git object of the remote: no HOME
// path of either person, no ~/.lore file name, no world path.
func TestSyncTeam_NoMachineLocalDataInGitObjects(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	st.addTask(t, a, "live row")
	a.MustLore("memory", "add", "committed row")
	a.CommitAll("memory")
	a.AdminPush("origin", "main")
	st.people[1].Offline()
	st.addTask(t, b, "offline row")
	st.people[1].Online()
	b.MustLore("sync")
	needles := []string{st.w.Root, st.people[0].Home, st.people[1].Home, "api-token", "/.lore/cache", "/.lore/clones"}
	if found := st.app.AllObjectsContain(needles...); len(found) > 0 {
		t.Fatalf("machine-local data reached the remote's git objects: %v", found)
	}
}
