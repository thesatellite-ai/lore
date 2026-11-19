//go:build journeys && !windows

package journeys

// storefolder_context_test.go — a lead asks `lore context` from inside a
// folder of the company store clone (devco D-479). Before: the folder was
// opened twice, as the repo store and again as its org name, so
// `context --for-prompt` printed every rule and memory twice (the same id
// twice in a row) and `--for-file` printed memories twice; a linked code
// repo had no doubles.
//
//	store clone/<folder> ─▶ context, render, search, lists: each row once

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Names of the store-folder context journey (placeholders).
const (
	jscFolder = "app1"
	jscRule   = "run app1 locally with the dev profile"
	jscMemory = "app1 runs locally on port 7400"
	jscOrgMem = "every app runs locally behind the proxy"
)

// jscRowID matches a row id lore prints.
var jscRowID = regexp.MustCompile(`\b(?:rul|mem)_[0-9a-f]{12,32}\b`)

// jscOnce fails when a row id appears more than once in out (each row once).
func jscOnce(t *testing.T, what, out string) {
	t.Helper()
	seen := map[string]int{}
	for _, id := range jscRowID.FindAllString(out, -1) {
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("%s: %s printed %d times:\n%s", what, id, n, out)
		}
	}
	if len(seen) == 0 {
		t.Errorf("%s: no row ids in:\n%s", what, out)
	}
}

func TestJourneyStoreFolderContextOnce(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	var storeDir string

	w.Step("day1/a-store-with-a-folder-and-the-org", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jscFolder).OK(t)
		lee.Lore(parent, "rule", "add", "--to", tlOrg+"/"+jscFolder, "--body", jscRule, "--yes").OK(t)
		lee.Lore(parent, "memory", "add", "--to", tlOrg+"/"+jscFolder, "--body", jscMemory, "--yes").OK(t)
		lee.Lore(parent, "memory", "add", "--to", tlOrg+"/org", "--body", jscOrgMem, "--yes").OK(t)
		out := lee.Lore(parent, "rule", "list", "--store", tlOrg+"/"+jscFolder, "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(rul_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no rule id in %s", out)
		}
		p := jPath(t, lee.Lore(parent, "path", m[1], "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join(jscFolder, "rules", m[1]+".json"))
		lee.Lore(parent, "sync").OK(t)
	})

	w.Step("day2/context-in-the-folder-prints-each-row-once", func() {
		w.AdvanceDays(1)
		dir := filepath.Join(storeDir, jscFolder)
		r := lee.Lore(dir, "context", "--for-prompt", "run app1 locally").OK(t)
		r.Says(t, "locally")
		jscOnce(t, "context --for-prompt", r.Out())
		f := lee.Lore(dir, "context", "--for-file", "apps/app1/main.go").OK(t)
		jscOnce(t, "context --for-file", f.Out())
		// the same class: lists, search and render in the folder
		jscOnce(t, "rule list", lee.Lore(dir, "rule", "list").OK(t).Out())
		jscOnce(t, "memory list", lee.Lore(dir, "memory", "list").OK(t).Out())
		s := lee.Lore(dir, "search", "--query", "locally").OK(t)
		jscOnce(t, "search", s.Out())
	})
}
