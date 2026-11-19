//go:build journeys && !windows

package journeys

// showflags_test.go — a lead looks things up the way they name them
// (devco D-449): every show takes --id, a decision answers to the number
// its title opens with (D5), decision list filters by --status, project
// show inside a store folder needs no id, and member show names projects.

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Names of the show-flags journey (placeholders).
const (
	jsfFolder   = "billing"
	jsfDecision = "D5: Which repos are client repos"
	jsfOther    = "D51: An unrelated choice"
)

func TestJourneyShowFlags(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	var folderDir string

	tt.w.Step("day1/a-folder-with-decisions", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jsfFolder).OK(t)
		tt.lore(tlLee, "team", "add", "@web", "--members", "@lee").OK(t)
		lee.Lore(parent, "decision", "add", "--to", tlOrg+"/"+jsfFolder, "--title", jsfDecision, "--body", "client wins", "--status", "proposed").OK(t)
		lee.Lore(parent, "decision", "add", "--to", tlOrg+"/"+jsfFolder, "--title", jsfOther, "--body", "b").OK(t)
		out := lee.Lore(parent, "memory", "add", "--to", tlOrg+"/"+jsfFolder, "--body", "folder marker", "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(mem_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no mem id in: %s", out)
		}
		p := jPath(t, lee.Lore(parent, "path", m[1], "--json").OK(t))
		folderDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join("memories", m[1]+".json"))
		if filepath.Base(folderDir) != jsfFolder {
			t.Fatalf("lore path %s = %s, not in the %s folder", m[1], p.Path, jsfFolder)
		}
	})

	tt.w.Step("day1/lookups-by-the-names-people-use", func() {
		lee.Lore(folderDir, "decision", "show", "--id", "D5").OK(t).Says(t, "client wins").Never(t, "not a decision id")
		lee.Lore(folderDir, "decision", "list", "--status", "proposed").OK(t).Says(t, "D5:").Never(t, "D51")
		lee.Lore(folderDir, "project", "show").OK(t).Says(t, jsfFolder).Never(t, "missing required")
		tt.lore(tlLee, "member", "show", "--id", "@lee").OK(t).Says(t, tlLee.Name)
		tt.lore(tlLee, "member", "show", "--who", "@lee").OK(t).Says(t, tlLee.Name)
		tt.lore(tlLee, "team", "show", "--id", "@web").OK(t).Says(t, "members (1)")
		tt.lore(tlLee, "team", "show", "--handle", "@web").OK(t).Says(t, "members (1)")
		if out := tt.lore(tlLee, "member", "show", "@lee").OK(t).Out(); strings.Contains(out, "prj_") {
			t.Errorf("member show lists project ids, want names:\n%s", out)
		}
	})
}
