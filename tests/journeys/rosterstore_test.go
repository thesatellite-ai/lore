//go:build journeys && !windows

package journeys

// rosterstore_test.go — a lead lists the org's teams and people from a
// plain folder (devco D-499). Before: from the home folder (no repo, no
// store clone) `team list`, `team show`, `member list` refused --store
// ("unknown flag"), and with no flag failed E_NOT_A_STORE with a git-init
// hint; only `--setup <setup>` or a cd into the store clone worked, while
// `rule list --store <org>` read the org from anywhere.
//
//	plain folder ─▶ team|member|actor reads --store <org> ─▶ the org's roster
//	plain folder ─▶ the same with no flag ─▶ hint names --store and --setup
//	code repo, store clone ─▶ the plain commands as before

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Names of the roster-store journey (placeholders).
const (
	jrsTeam = "webteam"
	jrsTask = "check the roster"
	jrsKind = "code_repo"
	jrsRes  = "billingrepo"
)

func TestJourneyRosterReadsTakeStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	var storeDir string

	w.Step("day1/a-team-and-its-people", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "team", "add", "@"+jrsTeam, "--members", "@lee", "--yes").OK(t)
		out := lee.Lore(parent, "task", "add", jrsTask, "--to", tlOrg+"/org", "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no task id in %s", out)
		}
		p := jPath(t, lee.Lore(parent, "path", m[1], "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join("org", "tasks", m[1]+".json"))
	})

	w.Step("day1/the-roster-from-a-plain-folder-with-store", func() {
		far := filepath.Join(lee.Home, "notes")
		if out, err := exec.Command("mkdir", "-p", far).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
		for _, d := range []string{lee.Home, far} {
			lee.Lore(d, "team", "list", "--store", tlOrg).OK(t).Says(t, "@"+jrsTeam).Never(t, "unknown flag")
			lee.Lore(d, "team", "show", "--handle", jrsTeam, "--store", tlOrg).OK(t).Says(t, "@"+jrsTeam, tlLee.Name)
			lee.Lore(d, "member", "list", "--store", tlOrg).OK(t).Says(t, tlLee.Email, "@"+jrsTeam)
			lee.Lore(d, "member", "show", "--who", tlLee.Email, "--store", tlOrg).OK(t).Says(t, tlLee.Name)
			lee.Lore(d, "actor", "list", "--store", tlOrg).OK(t).Says(t, tlLee.Name)
			// D-499: no flag outside every store and repo names both ways in
			for _, args := range [][]string{{"team", "list"}, {"member", "list"}} {
				lee.Lore(d, args...).Fails(t, 19, "E_NOT_A_STORE", "--store <org>", "--setup <setup>", tlOrg, tlLeadSetup).Never(t, "git init")
				lee.Lore(d, append(args, "--setup", tlLeadSetup)...).OK(t).Says(t, "@"+jrsTeam)
			}
			lee.Lore(d, "team", "list", "--store", "nosuchorg").Fails(t, 27, "E_NOT_FOUND", "names no org", "--store <org>", tlOrg)
		}
	})

	w.Step("day1/every-read-names-its-store", func() {
		// D-499 widened: `resource list --kind code_repo --store <org>/org`
		// said "unknown flag"; the resource, metadata and trash reads take
		// --store <org>[/<folder>] from any folder
		lee.Lore(parent, "resource", "kind", "add", "--kind", jrsKind, "--to", tlOrg+"/org", "--yes").OK(t)
		lee.Lore(parent, "resource", "add", "--kind", jrsKind, "--name", jrsRes, "--to", tlOrg+"/org", "--yes").OK(t)
		rid := regexp.MustCompile(`"id": "(rul_[0-9a-f]{32})"`).FindStringSubmatch(lee.Lore(parent, "rule", "add", "--body", "review every roster change", "--to", tlOrg+"/org", "--yes", "--json").OK(t).Out())
		if rid == nil {
			t.Fatal("no rule id")
		}
		for _, d := range []string{lee.Home, app, storeDir} {
			for _, s := range []string{tlOrg + "/org", tlOrg} {
				lee.Lore(d, "resource", "list", "--kind", jrsKind, "--store", s).OK(t).Says(t, jrsRes).Never(t, "unknown flag")
				lee.Lore(d, "resource", "show", "--id", jrsRes, "--store", s).OK(t).Says(t, jrsRes)
				lee.Lore(d, "resource", "kinds", "--store", s).OK(t).Says(t, jrsKind)
			}
			lee.Lore(d, "trash", "stats", "--store", tlOrg).OK(t).Never(t, "unknown flag")
			lee.Lore(d, "rule", "show", rid[1], "--store", tlOrg).OK(t).Says(t, "review every roster change").Never(t, "unknown flag")
		}
		// a name no store answers to is refused, never ignored (the read
		// does not list every store's rows)
		lee.Lore(app, "resource", "list", "--kind", jrsKind, "--store", "nosuchstore").Fails(t, 27, "E_NOT_FOUND", "nosuchstore").Never(t, jrsRes)
	})

	w.Step("day1/a-repo-and-the-store-clone-as-before", func() {
		for _, d := range []string{app, storeDir} {
			lee.Lore(d, "team", "list").OK(t).Says(t, "@"+jrsTeam)
			lee.Lore(d, "member", "list").OK(t).Says(t, tlLee.Email)
			lee.Lore(d, "team", "list", "--store", tlOrg).OK(t).Says(t, "@"+jrsTeam)
			lee.Lore(d, "member", "list", "--store", tlOrg).OK(t).Says(t, tlLee.Email)
		}
	})
}
