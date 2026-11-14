//go:build journeys && !windows

package journeys

// clientrepo_quiet_test.go — a client repo linked by its remote holds no
// lore files, and every command run there keeps it so (round 2 client2
// group). Lee links a client repo, then in it: a plan goes to the linked
// folder like a task (it used to go into the client's .lore/data or be
// refused); a comment on that task follows the task's store (it was
// refused E_LEAK_BLOCKED, the pointer judged from the repo store); render
// says a client repo gets no rendered files (it hinted `lore init`); the
// client CI guard `check leaks --ci` passes (it said "run from the project
// root"); a write refused for a secret leaves no .lore/ behind (its audit
// line went to .lore/state/audit-v2.jsonl in the client's tree).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	cqSlug   = "github.com/client-co/portal"
	cqFolder = "client_portal"
)

// cqTaskID matches the task id task add prints.
var cqTaskID = regexp.MustCompile(`(tsk_[0-9a-f]{32})`)

func TestJourneyClientRepoStaysQuiet(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")

	w.Step("day1/client-repo-commands-write-nothing-into-it", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", cqFolder).OK(t)
		portal := jPlainRepo(t, lee, w.Remote(cqSlug), filepath.Join(parent, "portal"))
		lee.LoreIn(portal, "org", "link", tlOrg, "--project", cqFolder, "--yes").OK(t)

		lee.LoreIn(portal, "plan", "add", "--title", "Q4 search revamp", "--body", "phase one indexes").OK(t).Says(t, cqFolder)
		r := lee.LoreIn(portal, "task", "add", "--title", "search page badge").OK(t).Says(t, cqFolder)
		m := cqTaskID.FindStringSubmatch(r.Stdout)
		if m == nil {
			t.Fatalf("task add printed no task id:\n%s", r)
		}
		lee.LoreIn(portal, "comment", "add", "--on-table", "tasks", "--on-id", m[1], "--body", "pushed to dev").OK(t).Says(t, cqFolder).Never(t, "E_LEAK_BLOCKED")

		lee.LoreIn(portal, "render").OK(t).Says(t, "client repo").Never(t, "lore init")
		lee.LoreIn(portal, "check", "leaks", "--ci").OK(t).Says(t, "no .lore folder").Never(t, "project root")

		key := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in the repo
		lee.LoreIn(portal, "decision", "add", "--title", "env", "--body", "the key is "+key).Fails(t, 26, "secret")

		// F-14: a comment that carries a secret the person keeps out of
		// every store (--secret-unstored) is written to the task's store
		// like any other; the client clone gets no hooks, no merge driver,
		// no .lore/
		lee.LoreIn(portal, "comment", "add", "--on-table", "tasks", "--on-id", m[1], "--body", "staging key "+key, "--secret-unstored", "--to", "acmeteam").OK(t).Never(t, "set up git for lore")
		if cfg := portal.Git("config", "--local", "--list"); strings.Contains(cfg, "merge.lore") {
			t.Errorf("comment add set lore's merge driver in the client clone:\n%s", cfg)
		}
		hook := strings.TrimSpace(portal.Git("rev-parse", "--path-format=absolute", "--git-path", "hooks/pre-commit"))
		if b, err := os.ReadFile(hook); err == nil && strings.Contains(string(b), "lore") {
			t.Errorf("comment add installed lore's hooks in the client clone (%s)", hook)
		}

		if st := portal.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("the client repo must hold nothing of lore's, git status:\n%s", st)
		}
	})
}
