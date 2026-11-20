//go:build stories && !windows

package stories

// O_agenthooks_bare_test.go — devco D-743 on the owner-shaped machine
// (story O): an own repo whose committed .claude/settings.json holds only
// the agent hooks an older lore wrote there. `lore doctor --fix` moved
// them into .claude/settings.local.json and left a bare {} file to commit.
// The cells: that file is removed (its deletion is the one change), and in
// a repo whose committed settings.json also holds the team's own keys the
// file stays byte for byte, and a second doctor --fix changes nothing.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestStoryOAgentHooksNoBareSettingsFile(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	const start = `{"hooks":[{"type":"command","command":"lore agent start --tool claude_code --hook-stdin"}]}`
	const end = `{"hooks":[{"type":"command","command":"lore agent end --tool claude_code","timeout":30}]}`
	loreOnly := "{\n  \"hooks\": {\n    \"SessionStart\": [" + start + "],\n    \"SessionEnd\": [" + end + "]\n  }\n}\n"
	mixed := "{\n  \"permissions\": {\"allow\": [\"Bash(make test)\"]},\n  \"hooks\": {\"SessionStart\": [" + start + "]}\n}\n"
	for name, settings := range map[string]string{"hooksonly": loreOnly, "hooksmixed": mixed} {
		s.Seed(s.Remote("github.com/"+oOwner+"/"+name), func(dir string) {
			PlainRepo(t, dir)
			writeFile(t, filepath.Join(dir, ".claude", "settings.json"), settings)
		})
	}

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	only := ada.CloneTo(s.Remote("github.com/"+oOwner+"/hooksonly"), ada.OrgDir("hooksonly"))
	oSetupFlow(t, ada, only.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	both := ada.CloneTo(s.Remote("github.com/"+oOwner+"/hooksmixed"), ada.OrgDir("hooksmixed"))
	s.Step("D-743/init-committed", func() {
		for _, c := range []*teamharness.Clone{only, both} {
			ada.Lore(c.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/"+filepath.Base(c.Dir), "--yes").OK(t)
			ada.Lore(c.Dir, "init", "--yes").OK(t)
			c.Git("add", "-A")
			c.TryGit("commit", "-q", "--no-verify", "-m", "lore").OK(t)
		}
	})

	settingsStatus := func(dir string) string {
		var out []string
		for _, l := range strings.Split(only.Git("-C", dir, "status", "--porcelain", "--untracked-files=all", "--", ".claude"), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}

	s.Step("D-743/lore-only-settings-file-removed-not-left-bare", func() {
		ada.Lore(only.Dir, "doctor", "--fix").Says(t, "agent hooks")
		if b, err := os.ReadFile(filepath.Join(only.Dir, ".claude", "settings.json")); !os.IsNotExist(err) {
			t.Fatalf("doctor --fix left .claude/settings.json as %q (%v), want it removed", b, err)
		}
		if st := settingsStatus(only.Dir); st != "D .claude/settings.json" {
			t.Fatalf("git status of .claude: %q, want only the deletion of settings.json", st)
		}
		if b, err := os.ReadFile(filepath.Join(only.Dir, ".claude", "settings.local.json")); err != nil || !strings.Contains(string(b), "lore agent start --tool claude_code") {
			t.Fatalf("the hooks are not in settings.local.json: %q %v", b, err)
		}
	})

	s.Step("D-743/settings-file-with-team-keys-left-untouched", func() {
		ada.Lore(both.Dir, "doctor", "--fix")
		if b, err := os.ReadFile(filepath.Join(both.Dir, ".claude", "settings.json")); err != nil || string(b) != mixed {
			t.Fatalf("doctor --fix rewrote the team's .claude/settings.json to %q (%v)", b, err)
		}
		if st := settingsStatus(both.Dir); st != "" {
			t.Fatalf("git status of .claude: %q, want clean", st)
		}
		again := ada.Lore(both.Dir, "doctor", "--fix").Out()
		for _, l := range strings.Split(again, "\n") {
			if strings.Contains(l, "agent hooks (") && !strings.HasSuffix(strings.TrimSpace(l), ": current") {
				t.Fatalf("a second doctor --fix still changes agent hooks: %q", l)
			}
		}
	})
}
