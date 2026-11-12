//go:build journeys && !windows

package journeys

// folders_adopt_test.go — moving a whole v0.1.17 repo into an org store
// folder (owner report 2026-11-11). Lee's largest repo holds 5,000 rows in
// .lore/data plus 20 tasks and a mission only on refs/lore/live, the
// mission stamped by an agent ("…/0000/agent"). Moving it row by row with
// `lore move` took minutes per row; `lore store adopt` moves it in one
// store commit and one push, keeping every id and HLC, leaves the repo as
// it was, does nothing the second time, and a client repo linked to the
// folder then finds the rows. --remove-source finally deletes the adopted
// row files from the repo as a diff Lee commits.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The legacy repo (placeholders): its project, the id stems of its rows
// (a fixed UUIDv7 head, a counter in the last 12 hex digits) and a word
// only one memory holds.
const (
	jAdoptLegacySlug = "github.com/acme-co/legacy-app"
	jAdoptHubSlug    = "github.com/client-co/legacy-hub"
	jAdoptFolder     = "client_legacy"
	jAdoptProject    = "prj_01a1cb5d817172998c6a1880a90a20bc"
	jAdoptActor      = "act_541757c158dc7fb58c1c338b0a60f137"
	jAdoptMemStem    = "mem_01a1cb5d827d726cabe9"
	jAdoptRuleStem   = "rul_01a1cb5d97ea7a29a7e5"
	jAdoptTaskStem   = "tsk_01a1cb5d849374588a70"
	jAdoptMission    = "msn_01a1cb5d9a1c7e3b8f42aa11bb22cc33"
	jAdoptWord       = "zanzibarquartz"
	jAdoptMemories   = 4500
	jAdoptRules      = 500
	jAdoptLiveTasks  = 20
	// jAdoptBudget is the owner's bar: well under a minute for 5,000 rows.
	jAdoptBudget = 60 * time.Second
)

// jAdoptID is stem + n as 12 hex digits.
func jAdoptID(stem string, n int) string { return fmt.Sprintf("%s%012x", stem, 0x200000+n) }

// jAdoptLegacy writes the v0.1.17 repo into c: its .lore/data (v1 row
// files, the shapes lore 0.1.17 wrote), its .gitignore block, one commit
// pushed, and the live ref holding the open tasks and the agent's mission.
func jAdoptLegacy(t *testing.T, c *teamharness.Clone) {
	t.Helper()
	data := filepath.Join(c.Dir, ".lore", "data")
	jWrite(t, filepath.Join(data, "_meta.json"), `{"_v": 1, "project_id": "`+jAdoptProject+`"}`+"\n")
	jWrite(t, filepath.Join(data, "projects", jAdoptProject+".json"), `{"_table": "projects", "_v": 1, "created_at": "2026-11-01T09:00:00.000000000Z", "id": "`+jAdoptProject+`", "name": "legacy-app", "updated_at": "2026-11-01T09:00:00.000000000Z"}`+"\n")
	jWrite(t, filepath.Join(data, "actors", jAdoptActor+".json"), `{"_table": "actors", "_v": 1, "display_name": "Lee Lead", "id": "`+jAdoptActor+`", "kind": "human"}`+"\n")
	for i := range jAdoptMemories {
		body := fmt.Sprintf("memory %d about the billing widget %d", i, i*7)
		if i == 4242 {
			body = "the " + jAdoptWord + " cache warms at boot"
		}
		id := jAdoptID(jAdoptMemStem, i)
		jWrite(t, filepath.Join(data, "memories", id+".json"), `{"_table": "memories", "_v": 1, "body": "`+body+`", "created_at": "2026-11-02T10:00:00.000000000Z", "created_by_actor_id": "`+jAdoptActor+
			`", "id": "`+id+`", "kind": "retrieved", "project_id": "`+jAdoptProject+`", "source_kind": "manual", "trust_score": 0.5, "updated_at": "2026-11-02T10:00:00.000000000Z"}`+"\n")
	}
	for i := range jAdoptRules {
		id := jAdoptID(jAdoptRuleStem, i)
		jWrite(t, filepath.Join(data, "rules", id+".json"), `{"_table": "rules", "_v": 1, "activation": "always", "body": "rule `+fmt.Sprint(i)+` of the billing service", "created_at": "2026-11-02T10:00:00.000000000Z", "created_by_actor_id": "`+jAdoptActor+
			`", "id": "`+id+`", "project_id": "`+jAdoptProject+`", "severity": "must", "source_kind": "manual", "trust_score": 0.5, "updated_at": "2026-11-02T10:00:00.000000000Z"}`+"\n")
	}
	jWrite(t, filepath.Join(c.Dir, ".gitignore"), "# lore\n.lore/lore.db\n.lore/lore.db-shm\n.lore/lore.db-wal\n.lore/lore.db.broken.*\n.lore/state/\n.lore/backups/\n")
	c.CommitAll("v0.1.17 notes")
	c.Git("push", "-q", "origin", "HEAD:main")
	live := map[string]string{}
	for i := range jAdoptLiveTasks {
		id := jAdoptID(jAdoptTaskStem, i)
		live["_root/tasks/"+id+".json"] = `{"_live": {"hlc": "2026-11-03T14:44:5` + fmt.Sprint(i%10) + `.429Z/0000/` + jAdoptActor + `"}, "_table": "tasks", "_v": 1, "attempts": 0, "claim_epoch": 0, "commitment": "accepted", "created_at": "2026-11-03T14:44:00.000000000Z", "id": "` + id +
			`", "priority": "medium", "project_id": "` + jAdoptProject + `", "status": "todo", "title": "open task ` + fmt.Sprint(i) + `", "updated_at": "2026-11-03T14:44:00.000000000Z"}`
	}
	live["_root/missions/"+jAdoptMission+".json"] = `{"_live": {"hlc": "2026-11-03T14:44:58.429Z/0000/agent"}, "_table": "missions", "_v": 1, "created_at": "2026-11-03T14:44:58.400000000Z", "id": "` + jAdoptMission +
		`", "project_id": "` + jAdoptProject + `", "status": "active", "title": "ship the importer", "updated_at": "2026-11-03T14:44:58.429000000Z"}`
	jAdoptLiveRef(t, c.Dir, live)
}

// jAdoptLiveRef points refs/lore/live of the repo at dir at a commit whose
// tree holds files, as lore 0.1.17 kept it (never in HEAD's history).
func jAdoptLiveRef(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(t.TempDir(), "index"),
		"GIT_AUTHOR_NAME=lore", "GIT_AUTHOR_EMAIL=lore@example.test", "GIT_COMMITTER_NAME=lore", "GIT_COMMITTER_EMAIL=lore@example.test")
	git := func(stdin string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	for _, p := range slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	}) {
		oid := git(files[p], "hash-object", "-w", "--stdin")
		git("", "update-index", "--add", "--cacheinfo", "100644,"+oid+","+p)
	}
	git("", "update-ref", "refs/lore/live", git("", "commit-tree", git("", "write-tree"), "-m", "live"))
}

func TestJourneyFoldersAdopt(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email]
	mainRef := "refs/heads/main"
	var legacy *teamharness.Clone
	var before string
	var commits int
	var files []string

	w.Step("day1/a-team-and-the-legacy-repo", func() {
		tlStartTeam(t, lee, app.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jAdoptFolder).OK(t)
		legacy = lee.Clone(w.Remote(jAdoptLegacySlug), "legacy-app")
		lee.MoveClone(t, legacy, filepath.Join(parent, "legacy-app"))
		jAdoptLegacy(t, legacy)
	})

	w.Step("day2/see-what-would-move", func() {
		w.AdvanceDays(1)
		commits = tt.store.CommitCount(mainRef)
		lee.Lore(parent, "store", "adopt", "team/"+jAdoptFolder, "--from", legacy.Dir, "--dry-run").OK(t).
			Says(t, "would adopt 5022 rows", "memories 4500", "rules 500", "tasks 20", "missions 1", "from the repo's live ref (refs/lore/live): 21", "projects: 1 row (the folder is its own project)")
		// it asks before writing
		lee.Lore(parent, "store", "adopt", "team/"+jAdoptFolder, "--from", legacy.Dir).Refused(t).Says(t, "copy 5022 rows", "one store commit", "--yes")
		if n := tt.store.CommitCount(mainRef); n != commits {
			t.Fatalf("a dry run or a refused adopt committed to the store (%d → %d)", commits, n)
		}
	})

	w.Step("day3/adopt-in-one-commit", func() {
		w.AdvanceDays(1)
		// taken here: the shared invariants run lore doctor in every clone
		// after each step, which wires its hooks
		before = jRepoSnapshot(t, legacy.Dir)
		start := time.Now()
		var out struct {
			Written int  `json:"written"`
			Live    int  `json:"live"`
			Pushed  bool `json:"pushed"`
		}
		tlJSON(t, lee.Lore(parent, "store", "adopt", "team/"+jAdoptFolder, "--from", legacy.Dir, "--yes", "--json").OK(t), &out)
		if took := time.Since(start); took > jAdoptBudget {
			t.Errorf("adopting 5,022 rows took %s, want under %s", took, jAdoptBudget)
		}
		if out.Written != 5022 || out.Live != 21 || !out.Pushed {
			t.Fatalf("adopt = %+v, want 5022 rows written (21 from the live ref), pushed", out)
		}
		if n := tt.store.CommitCount(mainRef); n != commits+1 {
			t.Errorf("adopt made %d store commits, want exactly one", n-commits)
		}
		files = tt.store.FilesMatching(mainRef, jAdoptFolder+"/")
		if got := len(slices.DeleteFunc(slices.Clone(files), func(p string) bool { return !strings.Contains(p, "/memories/") })); got != jAdoptMemories {
			t.Errorf("the folder holds %d memories, want %d", got, jAdoptMemories)
		}
		// ids and HLCs kept: the live mission keeps its stamp's time and
		// the bare "agent" writer is a stable actor; the row belongs to
		// the folder's project
		doc, ok := tt.store.Read(mainRef, jAdoptFolder+"/missions/"+jAdoptMission+".json")
		if !ok || !strings.Contains(doc, `"_hlc": "2026-11-03T14:44:58.429Z/0000/act_`) || strings.Contains(doc, jAdoptProject) {
			t.Errorf("the adopted mission: %s", doc)
		}
		// the source repo is as it was
		if after := jRepoSnapshot(t, legacy.Dir); after != before {
			t.Errorf("adopt changed the source repo:\nbefore %s\nafter  %s", before, after)
		}
	})

	w.Step("day4/again-is-a-no-op", func() {
		w.AdvanceDays(1)
		lee.Lore(parent, "store", "adopt", "team/"+jAdoptFolder, "--from", legacy.Dir, "--yes").OK(t).Says(t, "nothing to adopt", "5022 unchanged")
		if n := tt.store.CommitCount(mainRef); n != commits+1 {
			t.Errorf("a second adopt committed (%d commits since day 2)", n-commits)
		}
		if again := tt.store.FilesMatching(mainRef, jAdoptFolder+"/"); !slices.Equal(again, files) {
			t.Errorf("a second adopt changed the folder's files: %d → %d", len(files), len(again))
		}
	})

	w.Step("day5/a-linked-repo-finds-the-rows", func() {
		w.AdvanceDays(1)
		hub := jPlainRepo(t, lee, w.Remote(jAdoptHubSlug), filepath.Join(parent, "legacy-hub"))
		lee.LoreIn(hub, "org", "link", tlOrg, "--project", jAdoptFolder, "--yes").OK(t)
		lee.LoreIn(hub, "search", jAdoptWord).OK(t).Says(t, jAdoptID(jAdoptMemStem, 4242))
		lee.LoreIn(hub, "task", "list").OK(t).Says(t, "open task 7")
		lee.LoreIn(hub, "show", jAdoptMission).OK(t).Says(t, "ship the importer")
	})

	w.Step("day6/remove-the-source", func() {
		w.AdvanceDays(1)
		var out struct {
			Written int `json:"written"`
			Removed int `json:"removed"`
		}
		commits = tt.store.CommitCount(mainRef) // the link of day 5 is a store commit too
		tlJSON(t, lee.Lore(parent, "store", "adopt", "team/"+jAdoptFolder, "--from", legacy.Dir, "--remove-source", "--yes", "--json").OK(t), &out)
		if out.Written != 0 || out.Removed != jAdoptMemories+jAdoptRules+1 { // + the actor copy
			t.Fatalf("--remove-source = %+v, want nothing written and %d files removed", out, jAdoptMemories+jAdoptRules+1)
		}
		st := legacy.Git("status", "--porcelain")
		lines := strings.Split(st, "\n")
		if len(lines) != jAdoptMemories+jAdoptRules+1 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "D .lore/data/") {
			t.Errorf("the removal is not a plain diff of deleted row files (%d lines, first %q)", len(lines), lines[0])
		}
		for _, keep := range []string{"_meta.json", "projects/" + jAdoptProject + ".json"} {
			if _, err := os.Stat(filepath.Join(legacy.Dir, ".lore", "data", filepath.FromSlash(keep))); err != nil {
				t.Errorf("--remove-source removed %s: %v", keep, err)
			}
		}
		if n := tt.store.CommitCount(mainRef); n != commits {
			t.Errorf("--remove-source with nothing new committed to the store (%d commits)", n-commits)
		}
	})
}
