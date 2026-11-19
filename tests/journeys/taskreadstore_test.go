//go:build journeys && !windows

package journeys

// taskreadstore_test.go — a lead reads the org's tasks from any folder
// (devco D-507, the task part of D-499). Before: from a plain folder,
// a code repo or a store clone `task checklist list`, `task deferred`,
// `task export`, `task history`, `task recur show`, `task someday`,
// `task template list|show`, `task time show`, `task triage` and
// `task view list|show` refused --store ("unknown flag"), and `task show
// --store <org>` (an alias of --from) failed E_NOT_A_STORE from a plain
// folder, while `task list --store <org>` read the org from anywhere.
//
//	plain folder | code repo | store clone ─▶ task reads --store <org>[/org] ─▶ the org's rows
//	any folder ─▶ --store <no such store> ─▶ refused, never every store's rows

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Names of the task-read-store journey (placeholders).
const (
	jtsTask     = "ship the billing export"
	jtsProposed = "try a faster cache"
	jtsSomeday  = "rewrite the docs site"
	jtsDeferred = "renew the certificate"
	jtsItem     = "stream rows"
	jtsTemplate = "release_steps"
	jtsView     = "lead_urgent"
)

func TestJourneyTaskReadsTakeStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	org := tlOrg + "/org"
	idOf := func(out string) string {
		m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no task id in %s", out)
		}
		return m[1]
	}
	var id, storeDir string

	w.Step("day1/the-org-has-tasks", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		id = idOf(lee.Lore(parent, "task", "add", jtsTask, "--recur", "every week", "--priority", "urgent", "--to", org, "--json").OK(t).Out())
		lee.Lore(parent, "task", "add", jtsProposed, "--commitment", "proposed", "--to", org).OK(t)
		lee.Lore(parent, "task", "add", jtsSomeday, "--commitment", "someday", "--to", org).OK(t)
		lee.Lore(parent, "task", "add", jtsDeferred, "--defer-until", "in 9d", "--to", org).OK(t)
		lee.Lore(parent, "task", "checklist", "add", "--id", id, "--text", jtsItem).OK(t)
		lee.Lore(parent, "task", "time", "log", "--id", id, "--duration", "1h30m").OK(t)
		lee.Lore(parent, "task", "template", "add", "--name", jtsTemplate, "--from", id, "--to", org, "--yes").OK(t)
		lee.Lore(parent, "task", "view", "add", "--name", jtsView, "--where", "priority>=high", "--to", org).OK(t)
		p := jPath(t, lee.Lore(parent, "path", id, "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join("org", "tasks", id+".json"))
	})

	w.Step("day1/every-task-read-names-its-store", func() {
		for _, d := range []string{lee.Home, app, storeDir} {
			for _, s := range []string{org, tlOrg} {
				lee.Lore(d, "task", "show", "--id", id, "--store", s).OK(t).Says(t, jtsTask).Never(t, "E_NOT_A_STORE")
				lee.Lore(d, "task", "show", "--id", id, "--from", s).OK(t).Says(t, jtsTask)
				lee.Lore(d, "task", "checklist", "list", "--id", id, "--store", s).OK(t).Says(t, jtsItem).Never(t, "unknown flag")
				lee.Lore(d, "task", "history", "--id", id, "--store", s).OK(t).Never(t, "unknown flag")
				lee.Lore(d, "task", "recur", "show", "--id", id, "--store", s).OK(t).Says(t, "every").Never(t, "unknown flag")
				lee.Lore(d, "task", "time", "show", "--id", id, "--store", s).OK(t).Says(t, "1h 30m").Never(t, "unknown flag")
				lee.Lore(d, "task", "triage", "--store", s).OK(t).Says(t, jtsProposed).Never(t, "unknown flag")
				lee.Lore(d, "task", "someday", "--store", s).OK(t).Says(t, jtsSomeday).Never(t, "unknown flag")
				lee.Lore(d, "task", "deferred", "--store", s).OK(t).Says(t, jtsDeferred).Never(t, "unknown flag")
				lee.Lore(d, "task", "export", "--format", "csv", "--store", s).OK(t).Says(t, jtsTask).Never(t, "unknown flag")
				lee.Lore(d, "task", "template", "list", "--store", s).OK(t).Says(t, jtsTemplate).Never(t, "unknown flag")
				lee.Lore(d, "task", "template", "show", "--name", jtsTemplate, "--store", s).OK(t).Says(t, jtsTask).Never(t, "unknown flag")
				lee.Lore(d, "task", "view", "list", "--store", s).OK(t).Says(t, jtsView).Never(t, "unknown flag")
				lee.Lore(d, "task", "view", "show", "--name", jtsView, "--store", s).OK(t).Says(t, jtsView).Never(t, "unknown flag")
			}
		}
		// a name no store answers to is refused, never ignored
		for _, d := range []string{lee.Home, app} {
			lee.Lore(d, "task", "triage", "--store", "nosuchstore").Fails(t, 27, "E_NOT_FOUND", "nosuchstore").Never(t, jtsProposed)
			lee.Lore(d, "task", "show", "--id", id, "--store", "nosuchstore").Fails(t, 27, "E_NOT_FOUND", "nosuchstore").Never(t, jtsTask)
		}
	})
}
