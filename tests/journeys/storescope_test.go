//go:build journeys && !windows

package journeys

// storescope_test.go — a lead reads the whole company store in one
// command (devco D-431, D-432, D-433, D-434, D-454). The lead tried the
// store clone's top first and got "(no rules)"; inside a folder, --store
// naming another folder or the whole org was "not attached"; from the
// plain parent folder an edit or a move by id needed a repo; --assign in a
// store folder could not find the roster; task next blamed a missing git
// repository in a folder that is one.
//
//	clone top            ─▶ every folder of the store, rows labelled by folder
//	clone folder         ─▶ that folder; --store <org> or --store <org>/<f> widen
//	plain parent folder  ─▶ --store <org> covers every folder; edits by id work

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Names of the store-scope journey (placeholders).
const (
	jssSync     = "sync"
	jssOps      = "ops"
	jssSyncRule = "sync jobs retry three times"
	jssOpsRule  = "ops deploys need two reviewers"
	jssSyncTask = "rotate the sync token"
	jssOpsTask  = "renew the ops domain"
	jssDecision = "deploy with blue green"
	jssMemo     = "the staging box reboots on sundays"
	jssMemoEdit = "the staging box reboots on mondays"
)

func TestJourneyStoreScope(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	ann := tt.person(tlAnn)
	parent := filepath.Join(lee.Home, "code")
	idOf := func(prefix, out string) string {
		t.Helper()
		m := regexp.MustCompile(`"id": "(` + prefix + `_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no %s id in: %s", prefix, out)
		}
		return m[1]
	}
	var storeDir, syncTask, memo string

	w.Step("day1/company-store-with-two-folders", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jssSync).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", jssOps).OK(t)
		lee.Lore(parent, "rule", "add", "--to", tlOrg+"/"+jssSync, "--body", jssSyncRule, "--yes").OK(t)
		lee.Lore(parent, "rule", "add", "--to", tlOrg+"/"+jssOps, "--body", jssOpsRule, "--yes").OK(t)
		syncTask = idOf("tsk", lee.Lore(parent, "task", "add", jssSyncTask, "--to", tlOrg+"/"+jssSync, "--json").OK(t).Out())
		lee.Lore(parent, "task", "add", jssOpsTask, "--to", tlOrg+"/"+jssOps).OK(t)
		lee.Lore(parent, "decision", "add", "--to", tlOrg+"/"+jssOps, "--title", jssDecision, "--body", "fewer outages", "--status", "proposed").OK(t)
		memo = idOf("mem", lee.Lore(parent, "memory", "add", "--to", tlOrg+"/"+jssSync, "--body", jssMemo, "--json").OK(t).Out())
		p := jPath(t, lee.Lore(parent, "path", syncTask, "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join(jssSync, "tasks", syncTask+".json"))
		if storeDir == p.Path {
			t.Fatalf("lore path %s = %s, not under the %s folder of the store clone", syncTask, p.Path, jssSync)
		}
		lee.Lore(parent, "sync").OK(t)
	})

	w.Step("day2/the-clone-top-reads-every-folder", func() {
		w.AdvanceDays(1)
		syncL, opsL := "["+tlOrg+"/"+jssSync+"]", "["+tlOrg+"/"+jssOps+"]"
		lee.Lore(storeDir, "rule", "list").OK(t).Says(t, jssSyncRule, jssOpsRule, syncL, opsL).Never(t, "(no rules)")
		lee.Lore(storeDir, "task", "list").OK(t).Says(t, jssSyncTask, jssOpsTask, syncL, opsL)
		lee.Lore(storeDir, "decision", "list").OK(t).Says(t, jssDecision, opsL)
		lee.Lore(storeDir, "search", "--query", "reviewers").OK(t).Says(t, jssOpsRule, opsL)
		lee.Lore(storeDir, "sql", "--query", "select count(*) as n from rules_all").OK(t).Says(t, "2").Never(t, "a source needs")
	})

	w.Step("day2/a-folder-reads-itself-and-widens-by-name", func() {
		sync := filepath.Join(storeDir, jssSync)
		lee.Lore(sync, "rule", "list").OK(t).Says(t, jssSyncRule).Never(t, jssOpsRule)
		lee.Lore(sync, "task", "list", "--store", tlOrg+"/"+jssOps).OK(t).Says(t, jssOpsTask).Never(t, jssSyncTask, "not attached")
		lee.Lore(sync, "task", "list", "--store", tlOrg).OK(t).Says(t, jssSyncTask, jssOpsTask)
		lee.Lore(sync, "decision", "list", "--project", jssOps).OK(t).Says(t, jssDecision).Never(t, "no project")
		lee.Lore(sync, "sql", "--query", "select count(*) as n from rules_all").OK(t).Never(t, "a source needs")
		// D-433: the roster is found from a store folder
		lee.Lore(sync, "task", "edit", "--id", syncTask, "--assign", "@lee").OK(t).Says(t, tlLee.Name).Never(t, "no store named")
		// devco:D-679: since D-666 (store tasks are claimable from home and
		// a store clone) a store folder's task next ranks that folder's own
		// tasks instead of refusing as D-434 once did; it never reaches into
		// a sibling folder and never asks for a git repository.
		lee.Lore(sync, "task", "next").OK(t).Says(t, jssSyncTask).Never(t, jssOpsTask, "needs a git repository", "store clone, not a code repo")
	})

	w.Step("day2/the-parent-folder-covers-the-org", func() {
		lee.Lore(parent, "task", "list", "--store", tlOrg).OK(t).Says(t, jssSyncTask, jssOpsTask)
		// D-433: an edit and a move by id from a folder that is no repo
		lee.Lore(parent, "memory", "edit", "--id", memo, "--body", jssMemoEdit).OK(t).Never(t, "E_NOT_A_STORE", "git init")
		lee.Lore(parent, "move", "--id", memo, "--to", tlOrg+"/"+jssOps, "--yes").OK(t).Never(t, "no row")
		lee.Lore(parent, "memory", "list", "--store", tlOrg+"/"+jssOps).OK(t).Says(t, jssMemoEdit)
		lee.Lore(parent, "sync").OK(t)
	})

	w.Step("day3/a-member-home-reads-the-same", func() {
		w.AdvanceDays(1)
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		rel, err := filepath.Rel(lee.LorePath("stores"), storeDir)
		if err != nil {
			t.Fatal(err)
		}
		annStore := filepath.Join(ann.LorePath("stores"), rel)
		ann.Lore(annStore, "rule", "list").OK(t).Says(t, jssSyncRule, jssOpsRule)
		ann.Lore(filepath.Join(annStore, jssSync), "rule", "list").OK(t).Says(t, jssSyncRule)
	})

	// devco:D-486: a one-field edit in a member's store folder commits only
	// the task row, in one commit: actor and machine rows stay where the
	// store keeps them, never copied into the folder the write touched.
	w.Step("day3/a-members-folder-edit-commits-only-the-task", func() {
		rel, err := filepath.Rel(lee.LorePath("stores"), storeDir)
		if err != nil {
			t.Fatal(err)
		}
		annStore := filepath.Join(ann.LorePath("stores"), rel)
		annSync := filepath.Join(annStore, jssSync)
		before := strings.TrimSpace(w.Git(annStore, "rev-parse", "HEAD"))
		ann.Lore(annSync, "task", "edit", "--id", syncTask, "--assign", "@ann", "--yes").OK(t).Says(t, tlAnn.Name)
		commits := strings.Fields(w.Git(annStore, "rev-list", before+"..HEAD"))
		files := strings.Fields(w.Git(annStore, "diff", "--name-only", before, "HEAD"))
		want := filepath.ToSlash(filepath.Join(jssSync, "tasks", syncTask+".json"))
		if len(commits) != 1 || len(files) != 1 || files[0] != want {
			t.Fatalf("task edit in %s: %d commit(s) touching %v, want 1 commit touching only %s", annSync, len(commits), files, want)
		}
		// her coding agent's session: its first write in the clone adds the
		// agent's own row to the roster's folder (org/), once; the task
		// commit holds only the task, and no folder gets actor or machine
		// copies
		folder := *tt.repo[tlAnn.Email]
		folder.Dir = annSync
		agent := jAgentsAs(&folder, &jClaude, "st-folder")
		before = strings.TrimSpace(w.Git(annStore, "rev-parse", "HEAD"))
		agent.Lore("task", "edit", "--id", syncTask, "--title", jssSyncTask+" again", "--yes").OK(t)
		// devco:D-983: the commit is the person's (author, committer,
		// message); the agent is credited on its row only
		if msg := w.Git(annStore, "log", "-1", "--format=%an <%ae> | %cn <%ce>%n%B"); strings.Contains(msg, "Lore-Agent:") || strings.Contains(msg, " via ") || strings.Contains(msg, "@lore.invalid") {
			t.Fatalf("the edit from the agent session names the agent in its commit: %s", msg)
		}
		if got := strings.Fields(w.Git(annStore, "show", "--name-only", "--format=", "HEAD")); len(got) != 1 || got[0] != want {
			t.Fatalf("agent task edit commit touches %v, want only %s", got, want)
		}
		for _, f := range strings.Fields(w.Git(annStore, "diff", "--name-only", before, "HEAD")) {
			if f != want && !strings.HasPrefix(f, "org/actors/") {
				t.Fatalf("agent task edit in %s wrote %s, want only the task and the agent's own row in org/actors", annSync, f)
			}
		}
		before = strings.TrimSpace(w.Git(annStore, "rev-parse", "HEAD"))
		agent.Lore("task", "edit", "--id", syncTask, "--title", jssSyncTask+" once more", "--yes").OK(t)
		commits = strings.Fields(w.Git(annStore, "rev-list", before+"..HEAD"))
		files = strings.Fields(w.Git(annStore, "diff", "--name-only", before, "HEAD"))
		if len(commits) != 1 || len(files) != 1 || files[0] != want {
			t.Fatalf("second agent task edit in %s: %d commit(s) touching %v, want 1 commit touching only %s", annSync, len(commits), files, want)
		}
		ann.Lore(annSync, "doctor").Never(t, "actor_roster")
	})
}
