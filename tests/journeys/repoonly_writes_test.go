//go:build journeys && !windows

package journeys

// repoonly_writes_test.go — devco D-523: the writes that change a code
// repo's own files say where they work, like the repo-only reads (D-509).
//
// Before: from the home folder `store add`, `org link`, `migrate run` and
// `sync install-action` refused E_NOT_A_STORE each in its own words ("it
// edits a repo's .lore/stores.yaml, and … is not inside a git repo"), and
// in a store clone `store set-url`, `directive install` ("lore isn't set up
// in …") and `store add` the same; none said to cd into the code repo or
// that --store does not apply. A roster change from the home folder named
// --setup only, while the roster reads named --store too.
//
//	home folder ─▶ store add | org link | migrate run | sync install-action
//	            ─▶ one refusal: lives in each code repo, cd into it
//	store clone ─▶ store add | store set-url | directive install: the same
//	home folder ─▶ team add: --setup <setup>, and --store is the reads'

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestJourneyRepoOnlyWritesSayWhereTheyWork(t *testing.T) {
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

	w.Step("day1/repo-only-writes-from-home", func() {
		lee.Lore(lee.Home, "store", "add", "--name", "extra", "--url", "git@example.test:acme/extra.git", "--yes").Fails(t, 19, "E_NOT_A_STORE", "edits a repo's .lore/stores.yaml", "is not inside a code repo", "cd into the code repo", "--store does not apply").Never(t, "git init")
		for _, args := range [][]string{
			{"org", "link", "--org", tlOrg, "--project", "web", "--yes"},
			{"migrate", "run"},
			{"sync", "install-action", "--yes"},
		} {
			lee.Lore(lee.Home, args...).Fails(t, 23, "E_UNSUPPORTED", "in each code repo", "is not inside a code repo", "cd into the code repo", "--store does not apply").Never(t, "git init", "--setup", "--to <org>")
		}
	})

	w.Step("day1/repo-only-writes-in-the-store-clone", func() {
		for _, args := range [][]string{
			{"store", "add", "--name", "extra", "--url", "git@example.test:acme/extra.git", "--yes"},
			{"store", "set-url", "--name", tlOrg, "--url", "git@example.test:acme/moved.git", "--yes"},
		} {
			lee.Lore(storeDir, args...).Fails(t, 19, "E_NOT_A_STORE", "edits a repo's .lore/stores.yaml", "is a lore store clone, not a code repo", "cd into the code repo", "--store does not apply")
		}
		lee.Lore(storeDir, "directive", "install", "--yes").Fails(t, 23, "E_UNSUPPORTED", "is a lore store clone, not a code repo", "cd into the code repo", "--store does not apply").Never(t, "lore isn't set up")
	})

	w.Step("day1/a-roster-change-from-home-names-both", func() {
		lee.Lore(lee.Home, "team", "add", "--handle", "matrix_team", "--yes").Fails(t, 19, "E_NOT_A_STORE", "no roster to change", "--setup <setup>", "--store <org>").Never(t, "git init")
	})
}
