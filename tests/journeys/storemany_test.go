//go:build journeys && !windows

package journeys

// storemany_test.go — a company store with more folders than SQLite
// attaches at once (devco D-478). Before: every whole-store `search` and
// `sql` read from the store clone root, a plain setup folder or
// `--setup <setup>` failed with `[E_UNSUPPORTED] sqlite lens: 16 sources,
// at most 10 can be attached`, while a code repo attaching the same store
// worked (its open keeps one combined cache past the limit; the store-only
// opens kept one file per folder). A search failed only when a hit was a
// resource, whose kind name it reads through the lens.
//
//	clone root, setup folder, --setup, --store, code repo ─▶ same rows, no limit

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Names of the many-folders journey (placeholders).
const (
	jsmKind = "code_repo"
	jsmRes  = "billingrepo"
	jsmBody = "select body from rules_all order by body"
)

func TestJourneyStoreManyFolders(t *testing.T) {
	t.Parallel()
	for _, n := range []int{16, 40} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			t.Parallel()
			jsmMany(t, n)
		})
	}
}

// jsmMany builds an org store of n folders (org plus app1..app<n-1>, one
// rule each, a resource in app1) and reads it whole from every place.
func jsmMany(t *testing.T, n int) {
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	var storeDir string
	apps := make([]string, 0, n-1)
	for i := 1; i < n; i++ {
		apps = append(apps, "app"+strconv.Itoa(i))
	}

	w.Step("day1/an-org-with-many-folders", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		for _, f := range apps {
			lee.Lore(parent, "store", "folder", "add", "team", f).OK(t)
		}
		for _, f := range append([]string{"org"}, apps...) {
			lee.Lore(parent, "rule", "add", "--to", tlOrg+"/"+f, "--body", f+" rule one", "--yes").OK(t)
		}
		lee.Lore(parent, "resource", "kind", "add", "--kind", jsmKind, "--to", tlOrg+"/org", "--yes").OK(t)
		lee.Lore(parent, "resource", "add", "--kind", jsmKind, "--name", jsmRes, "--to", tlOrg+"/"+apps[0], "--yes").OK(t)
		out := lee.Lore(parent, "task", "add", "find the clone", "--to", tlOrg+"/"+apps[0], "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no task id in %s", out)
		}
		p := jPath(t, lee.Lore(parent, "path", m[1], "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join(apps[0], "tasks", m[1]+".json"))
		lee.Lore(parent, "sync").OK(t)
	})

	w.Step("day2/every-place-reads-every-folder", func() {
		rows := "(" + strconv.Itoa(n) + " rows)"
		want := []string{"org rule one", apps[n-2] + " rule one", rows}
		wt := filepath.Join(parent, "wt")
		if out, err := exec.Command("git", "init", "-q", wt).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
		lee.Lore(wt, "init", "--yes").OK(t)
		lee.Lore(wt, "store", "add", "whole", tt.store.SSHURL(), "--yes").OK(t)
		// the code repo path, which always worked: the rows every other place must give
		ref := lee.Lore(wt, "sql", "--internal", "--query", jsmBody).OK(t).Says(t, want...).Stdout
		ref, _, _ = strings.Cut(ref, "note: ") // the --internal note, not a row
		type place struct {
			dir  string
			args []string
		}
		places := []place{
			{parent, nil}, // a plain setup folder (no git)
			{lee.Home, []string{"--setup", tlLeadSetup}}, // --setup from anywhere
			{parent, []string{"--store", tlOrg}},
			{wt, []string{"--store", tlOrg}},
		}
		places = append(places, place{storeDir, nil}) // the store clone root
		for _, p := range places {
			r := lee.Lore(p.dir, append([]string{"sql", "--query", jsmBody}, p.args...)...).OK(t).Never(t, "can be attached", "E_UNSUPPORTED")
			if r.Stdout != ref {
				t.Errorf("sql in %s %v:\n%s\nwant (code repo):\n%s", p.dir, p.args, r.Stdout, ref)
			}
			// a resource hit reads its kind name through the lens
			lee.Lore(p.dir, append([]string{"search", "--query", jsmRes}, p.args...)...).OK(t).
				Says(t, jsmRes+" ("+jsmKind+")").Never(t, "can be attached", "E_UNSUPPORTED")
			lee.Lore(p.dir, append([]string{"search", "--query", apps[n-2] + " rule"}, p.args...)...).OK(t).
				Says(t, apps[n-2]+" rule one").Never(t, "can be attached")
			lee.Lore(p.dir, append([]string{"sql", "--include-deleted", "--query", "select count(*) from rules_all"}, p.args...)...).OK(t).
				Says(t, strconv.Itoa(n)).Never(t, "can be attached")
		}
	})
}
