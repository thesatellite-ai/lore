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
// `--store <org>/<folder>` in a repo said "not attached"; `--store <org>`
// worked only from a plain folder (D-471), and a missing folder got write
// wording (D-472).
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
		// D-484: lists and search read the same folders as sql
		lee.Lore(wt, "rule", "list").OK(t).Says(t, jsrSync+" rule one", jsrOps+" rule one", "[whole/"+jsrOps+"]").Never(t, "(no rules)")
		lee.Lore(wt, "task", "list").OK(t).Says(t, jsrSyncTask, jsrOpsTask)
		lee.Lore(wt, "rule", "list", "--store", "whole").OK(t).Says(t, jsrSync+" rule one", jsrOps+" rule one").Never(t, "(no rules)")
		lee.Lore(wt, "search", "--query", "renew domain").OK(t).Says(t, jsrOpsTask, "whole/"+jsrOps)
		// D-485: --all-repos in one folder of the store covers every folder of it
		ops := filepath.Join(storeDir, jsrOps)
		lee.Lore(ops, "search", "--query", "rotate token").OK(t).Never(t, jsrSyncTask)
		lee.Lore(ops, "search", "--query", "rotate token", "--all-repos").OK(t).Says(t, jsrSyncTask)
	})
	w.Step("day3/sql-labels-each-place-like-search", func() {
		// D-568: `place` is the label search prints, so the linked repo's
		// rows read as <org>/<folder> in sql too; D-685: `source` is that
		// same label, one name per folder in every placement
		const q = "SELECT source, place, count(*) AS n FROM rules_all GROUP BY 1, 2 ORDER BY 1, 2"
		r := lee.Lore(app, "sql", "--internal", "--query", q).OK(t)
		lee.Lore(app, "search", "--query", "sync rule").OK(t).Says(t, "["+tlOrg+"/"+jsrSync+"]", "["+tlOrg+"/org]")
		for _, row := range []string{tlOrg + "/" + jsrSync + `\s+` + tlOrg + "/" + jsrSync + `\s+1`, tlOrg + `/org\s+` + tlOrg + `/org\s+1`} {
			if !regexp.MustCompile(`(?m)^` + row + `\s*$`).MatchString(r.Out()) {
				t.Errorf("sql source/place row %q missing:\n%s", row, r.Out())
			}
		}
		// the source column filters on the folder-qualified name (D-685)
		lee.Lore(app, "sql", "--internal", "--query", "SELECT body FROM rules_all WHERE source = '"+tlOrg+"/"+jsrSync+"'").OK(t).
			Says(t, jsrSync+" rule one", "(1 row)")
	})
	w.Step("day3/the-org-reads-the-same-everywhere", func() {
		// D-471: --store <org> reads every folder of the org from a linked
		// repo, an unattached repo under the setup's folders, a repo and a
		// folder no setup covers, and a store clone; D-472: a folder the
		// store lacks is a read's refusal, never "nothing written … --create"
		wt := filepath.Join(parent, "wt")
		other, far := filepath.Join(parent, "other"), filepath.Join(lee.Home, "far")
		for _, d := range []string{other, far} {
			if out, err := exec.Command("git", "init", "-q", d).CombinedOutput(); err != nil {
				t.Fatal(string(out))
			}
		}
		for _, d := range []string{parent, app, other, wt, far, lee.Home, storeDir, filepath.Join(storeDir, jsrOps)} {
			lee.Lore(d, "rule", "list", "--store", tlOrg).OK(t).Says(t, jsrSync+" rule one", jsrOps+" rule one", "org rule one")
			lee.Lore(d, "task", "list", "--store", tlOrg).OK(t).Says(t, jsrSyncTask, jsrOpsTask)
			lee.Lore(d, "search", "--query", "rule", "--store", tlOrg).OK(t).Says(t, "Found 3 hits")
			lee.Lore(d, "memory", "list", "--store", tlOrg).OK(t)
			lee.Lore(d, "rule", "list", "--store", tlOrg+"/"+jsrOps).OK(t).Says(t, jsrOps+" rule one").Never(t, jsrSync+" rule one")
			lee.Lore(d, "store", "folder", "list", "--store", tlOrg).OK(t).Says(t, jsrOps, jsrSync)
			lee.Lore(d, "memory", "list", "--store", tlOrg+"/nofolder").
				Fails(t, 27, "E_NOT_FOUND", "has no folder nofolder yet", "store folder list --store "+tlOrg).
				Never(t, "E_NEEDS_CONFIRM", "nothing written", "--create")
			// D-473: sql takes --store like every other read
			const q = "select source, body from rules_all order by body"
			lee.Lore(d, "sql", "--store", tlOrg, "--query", q).OK(t).
				Says(t, tlOrg+"/"+jsrOps, tlOrg+"/"+jsrSync, tlOrg+"/org", "(3 rows)").Never(t, "unknown flag")
			lee.Lore(d, "sql", "--store", tlOrg+"/"+jsrOps, "--query", q).OK(t).
				Says(t, jsrOps+" rule one").Never(t, jsrSync+" rule one")
			lee.Lore(d, "sql", "--store", tlOrg+"/nofolder", "--query", q).
				Fails(t, 27, "E_NOT_FOUND", "has no folder nofolder yet", "store folder list --store "+tlOrg)
		}
	})
}
