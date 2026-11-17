//go:build journeys && !windows

package journeys

// repoonly_reads_test.go — a lead runs the reads whose data lives in each
// code repo from the home folder and the org store's clone (devco D-509).
// Before: from the home folder migrate status, session list and
// why-context refused E_NOT_A_STORE with git's "cd into a git checkout; a
// write can also name its store…", and in the store clone migrate status
// and sync trash list refused "run it in a code repo; inside a store
// folder…"; none said --store does not apply.
//
//	home folder ─▶ migrate status | session list | why-context show
//	            ─▶ one refusal: lives in each code repo, cd into it
//	store clone ─▶ migrate status: the same refusal (no 0.1.17 data there)
//	store clone ─▶ sync trash list: the clone's own (empty) local trash
//	code repo   ─▶ migrate status answers as before

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestJourneyRepoOnlyReadsSayWhereTheyLive(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	app := tt.repo[tlLee.Email].Dir
	parent := filepath.Join(lee.Home, "code")
	var storeDir string

	w.Step("day1/a-team-and-its-store-clone", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(app, "init").OK(t)
		out := lee.Lore(parent, "task", "add", "find the store clone", "--to", tlOrg+"/org", "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no task id in %s", out)
		}
		p := jPath(t, lee.Lore(parent, "path", m[1], "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join("org", "tasks", m[1]+".json"))
	})

	w.Step("day1/repo-only-reads-from-home", func() {
		for _, args := range [][]string{{"migrate", "status"}, {"session", "list"}, {"why-context", "show"}} {
			lee.Lore(lee.Home, args...).Fails(t, 23, "E_UNSUPPORTED", "in each code repo", "is not inside a code repo", "cd into the code repo", "--store does not apply").Never(t, "git init", "--setup", "--to <org>")
		}
	})

	w.Step("day1/the-store-clone", func() {
		lee.Lore(storeDir, "migrate", "status").Fails(t, 23, "E_UNSUPPORTED", "is a lore store clone, not a code repo", "cd into the code repo", "--store does not apply")
		lee.Lore(storeDir, "sync", "trash", "list").OK(t).Says(t, "trash is empty").Never(t, "needs a code repo")
		lee.Lore(app, "migrate", "status").OK(t).Never(t, "in each code repo")
	})
}
