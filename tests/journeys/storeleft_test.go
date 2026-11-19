//go:build journeys && !windows

package journeys

// storeleft_test.go — the reads left without --store after D-499 take it
// too (devco D-506, owner: every read takes --store). Before: `row path
// --id <id> --store <org>`, `policy show --store <org>`, `secrets rules
// --store <org>`, `repo list --store <org>` and the rest said "unknown
// flag: --store" from every folder.
//
//	plain folder, home, code repo, store clone ─▶ <read> --store <org>[/<folder>] ─▶ that store's rows
//	code repo ─▶ <read> --store <name no store answers to> ─▶ E_NOT_FOUND, never every store

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Names of the store-left journey (placeholders).
const jslTask = "read me from anywhere"

func TestJourneyLeftReadsTakeStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	far := filepath.Join(lee.Home, "notes")
	var storeDir, id string

	w.Step("day1/a-task-in-the-org-folder", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		out := lee.Lore(parent, "task", "add", jslTask, "--to", tlOrg+"/org", "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no task id in %s", out)
		}
		id = m[1]
		p := jPath(t, lee.Lore(parent, "path", id, "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join("org", "tasks", id+".json"))
		if out, err := exec.Command("mkdir", "-p", far).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	})

	w.Step("day1/every-left-read-names-its-store", func() {
		for _, d := range []string{lee.Home, far, app, storeDir} {
			for _, s := range []string{tlOrg + "/org", tlOrg} {
				// row family: the row of the org folder, by id
				lee.Lore(d, "row", "path", "--id", id, "--store", s).OK(t).Says(t, id+".json").Never(t, "unknown flag")
				lee.Lore(d, "row", "markdown", "--id", id, "--store", s).OK(t).Says(t, jslTask)
				// settings family
				lee.Lore(d, "policy", "show", "--store", s).OK(t).Never(t, "unknown flag")
				lee.Lore(d, "secrets", "rules", "--store", s).OK(t).Never(t, "unknown flag")
				lee.Lore(d, "secrets", "list", "--store", s).OK(t).Never(t, "unknown flag")
				lee.Lore(d, "secrets", "allow", "list", "--store", s).OK(t).Never(t, "unknown flag")
				// project family: a store root with no project row is D-505's
				// (worker mx505); here only the flag is taken and routed
				for _, args := range [][]string{{"repo", "list"}, {"project", "show"}, {"link", "list"}, {"commit", "show", "--id", "3f2c9ab"}} {
					lee.Lore(d, append(args, "--store", s)...).Never(t, "unknown flag", "E_INVALID_INPUT")
				}
			}
		}
		// a name no store answers to is refused, never ignored
		lee.Lore(app, "row", "path", "--id", id, "--store", "nosuchstore").Fails(t, 27, "E_NOT_FOUND", "nosuchstore").Never(t, id+".json")
		lee.Lore(app, "policy", "show", "--store", "nosuchstore").Fails(t, 27, "E_NOT_FOUND", "nosuchstore")
	})

	w.Step("day1/the-store-clone-as-before", func() {
		// the plain commands with no flag read the clone's own store
		lee.Lore(storeDir, "row", "path", "--id", id).OK(t).Says(t, id+".json")
		lee.Lore(storeDir, "policy", "show").OK(t)
		lee.Lore(storeDir, "secrets", "rules").OK(t)
	})
}
