//go:build stories && !windows

package stories

// O_pinned_asof_test.go — devco:D-952 (bug hunt H-211) on the owner-shaped
// machine (story O): the platform repo keeps its notes in a folder of the
// org store, so no LORE.md carries its pinned rules and the SessionStart
// answer and the private file are where an agent gets them. Neither said
// when those rows were taken, so a session started in the morning acted in
// the afternoon on a pinned hotfix the admin had retired since. Both now
// say "Pinned rows as of <time>" (the newest change among the rows) and
// name `lore show --id <id>`, which prints the row's current text there.

import (
	"regexp"
	"strings"
	"testing"
)

func TestStoryOPinnedRowsSayWhenTaken(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	asOf := regexp.MustCompile("_Pinned rows as of \\d{4}-\\d{2}-\\d{2} \\d{2}:\\d{2} UTC; a row's current text: `lore show --id ")
	var hotfix string

	s.Step("D-952/a-pinned-hotfix-in-the-linked-store-folder", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "billing").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "billing", "--own", "--yes").OK(t)
		hotfix = oAddedID(t, ada.Lore(pc.Dir, "hotfix", "add", "--title", "Refund retries double-pay", "--severity", "critical",
			"--body", "Do not retry the refund call until the idempotency key lands.", "--json").OK(t))
		if hotfix == "" {
			t.Fatal("hotfix add --json printed no id")
		}
	})

	s.Step("D-952/session-start-and-the-private-file-say-when-taken", func() {
		out := ada.LoreStdinIn(pc, `{"hook_event_name":"SessionStart","session_id":"sess-o"}`,
			"context", "--hook", "claude_code", "--hook-stdin", "--session-start").OK(t).Stdout
		if !strings.Contains(out, "Refund retries double-pay") || len(asOf.FindAllString(out, -1)) != 1 {
			t.Fatalf("the SessionStart answer does not say once when its pinned rows were taken:\n%s", out)
		}
		r := ada.Lore(pc.Dir, "render", "--yes", "--dry-run").OK(t)
		if !asOf.MatchString(r.Stdout) {
			t.Fatalf("the private file does not say when its pinned rows were taken:\n%s", r.Stdout)
		}
		// the printed command works where printed
		ada.Lore(pc.Dir, "show", "--id", hotfix).OK(t).Says(t, "Do not retry the refund call")
	})
}
