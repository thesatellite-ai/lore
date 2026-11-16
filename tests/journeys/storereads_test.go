//go:build journeys && !windows

package journeys

// storereads_test.go — a lead reads the company store from its folders
// and from a code repo (devco D-443, D-444, D-445, D-446, D-455, D-456,
// D-458). Before: `project list` said "no rows yet" for every folder;
// `project member list` in a folder failed on the roster and, with
// --project, warned about every team of the org; sql in a repo attaching
// the store whole read its empty root only, and two names on one place
// doubled every row; `show --json` left out the origin and flattened
// lists; `show --id` in one folder could not find a row of another, and
// `--store <org>/<folder>` in a repo said "not attached".
//
//	store folder ─▶ members of that folder, row counts, org kinds, any row by id
//	code repo    ─▶ sql over every folder once, --store <org>/<folder> read-only

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Names of the store-reads journey (placeholders).
const (
	jsrSync     = "sync"
	jsrOps      = "ops"
	jsrKind     = "code_repo"
	jsrRes      = "syncrepo"
	jsrSyncTask = "rotate the sync token"
	jsrOpsTask  = "renew the ops domain"
	jsrTeam     = "clientx"
)

func TestJourneyStoreReadsEverything(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	var storeDir, syncTask string

	w.Step("day1/company-store-with-two-folders", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jsrSync).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", jsrOps).OK(t)
		for _, f := range []string{jsrSync, jsrOps, "org"} {
			lee.Lore(parent, "rule", "add", "--to", tlOrg+"/"+f, "--body", f+" rule one", "--yes").OK(t)
		}
		lee.Lore(parent, "resource", "kind", "add", "--kind", jsrKind, "--to", tlOrg+"/org", "--yes").OK(t)
		lee.Lore(parent, "resource", "add", "--kind", jsrKind, "--name", jsrRes, "--to", tlOrg+"/"+jsrSync, "--yes").OK(t)
		out := lee.Lore(parent, "task", "add", jsrSyncTask, "--to", tlOrg+"/"+jsrSync, "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no task id in %s", out)
		}
		syncTask = m[1]
		lee.Lore(parent, "task", "add", jsrOpsTask, "--to", tlOrg+"/"+jsrOps).OK(t)
		lee.Lore(parent, "team", "add", "@"+jsrTeam, "--members", "@lee", "--yes").OK(t)
		p := jPath(t, lee.Lore(parent, "path", syncTask, "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join(jsrSync, "tasks", syncTask+".json"))
		lee.Lore(parent, "sync").OK(t)
	})

	w.Step("day2/a-folder-reads-its-project-and-the-org", func() {
		w.AdvanceDays(1)
		sync := filepath.Join(storeDir, jsrSync)
		// D-444: row counts, never "no rows yet"
		lee.Lore(storeDir, "project", "list").OK(t).Says(t, jsrSync, jsrOps, " rows").Never(t, "no rows yet")
		// D-445: the folder is the default project; only the teams it uses warn
		lee.Lore(sync, "project", "member", "list").OK(t).Never(t, "no org store named", "W_TEAM_NOT_MEMBER")
		lee.Lore(sync, "project", "member", "list", "--project", jsrSync).OK(t).Never(t, "W_TEAM_NOT_MEMBER", "@"+jsrTeam)
		// D-446: the org folder's kinds and rows from a product folder and from anywhere
		lee.Lore(sync, "resource", "list", "--kind", jsrKind).OK(t).Says(t, jsrRes).Never(t, "E_KIND_UNKNOWN")
		lee.Lore(sync, "resource", "show", "--id", jsrRes).OK(t).Says(t, jsrRes)
		lee.Lore(sync, "resource", "report").OK(t).Says(t, jsrKind)
		lee.Lore(parent, "resource", "list", "--kind", jsrKind).OK(t).Says(t, jsrRes)
	})

	w.Step("day2/show-prints-everything-and-finds-any-row", func() {
		ops := filepath.Join(storeDir, jsrOps)
		// D-458: a row of another folder of the store, by id
		lee.Lore(ops, "show", "--id", syncTask).OK(t).Says(t, jsrSyncTask).Never(t, "no row")
		lee.Lore(ops, "task", "show", syncTask).OK(t).Says(t, jsrSyncTask)
		// D-455, D-456: --json has everything; lists stay lists; the origin names its store
		js := lee.Lore(ops, "show", "--id", syncTask, "--json").OK(t)
		js.Says(t, `"watchers": [`, `"origin": {`, `"audit": {`, `"lineage": {`, `"reason": "written from a store folder`, `"machine": {`, `"version": `)
		if !regexp.MustCompile(`"store": \{\s*"id": "sto_[0-9a-f]{32}",\s*"name": "[^"]+/[^"]+"`).MatchString(js.Stdout) {
			t.Errorf("origin store has no name: %s", js.Stdout)
		}
		lee.Lore(ops, "show", "--id", syncTask, "--origin").OK(t).Says(t, "git        none: written from a store folder", "  lore ", "  machine ")
		lee.Lore(ops, "show", "--id", syncTask).OK(t).Never(t, "origin (written once)")
	})

	w.Step("day3/a-code-repo-reads-every-folder-once", func() {
		w.AdvanceDays(1)
		// D-458: a folder the linked repo does not attach, read-only
		lee.Lore(app, "org", "link", tlOrg, "--project", jsrSync, "--yes").OK(t)
		lee.Lore(app, "task", "list", "--store", tlOrg+"/"+jsrOps).OK(t).Says(t, jsrOpsTask).Never(t, "no store named", jsrSyncTask)
		// D-443: a store attached whole is every folder, each place once
		wt := filepath.Join(parent, "wt")
		if out, err := exec.Command("git", "init", "-q", wt).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
		lee.Lore(wt, "init", "--yes").OK(t)
		lee.Lore(wt, "store", "add", "whole", tt.store.SSHURL(), "--yes").OK(t)
		r := lee.Lore(wt, "sql", "--internal", "--query", "select source, body from rules_all order by body").OK(t)
		r.Says(t, "whole/"+jsrOps, "whole/"+jsrSync, tlOrg+"/org", "(3 rows)").Never(t, "a source needs")
	})
}
