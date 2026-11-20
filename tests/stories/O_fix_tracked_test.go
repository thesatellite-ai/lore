//go:build stories && !windows

package stories

// O_fix_tracked_test.go — devco:D-744 on the owner-shaped machine (story
// O): in an own repo linked to its org folder whose committed
// .claude/settings.json held only lore's agent hooks (an older lore wrote
// them there), `lore doctor --fix` removed that tracked file (D-743) and
// its output named only .gitattributes for a commit. Every tracked file
// the run changes is named in a commit line, and committing exactly what
// the lines name leaves nothing outside .lore/.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOFixNamesTrackedFiles(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	app := s.Seed(s.Remote("github.com/"+oOwner+"/hooks-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ac := ada.CloneTo(app, ada.OrgDir("hooks-app"))
	const folder = "hooks_app"
	const settings = ".claude/settings.json"

	s.Step("D-744/linked-repo-with-lore-hooks-in-the-team-settings", func() {
		ada.Lore(ac.Dir, "init", "--non-interactive", "--name=hooks-app").OK(t)
		ada.Lore(ac.Dir, "memory", "add", "--body", "the queue drains at noon", "--to", "repo").OK(t)
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(ac.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(ac.Dir, "org", "link", "--org", oOrg, "--project", folder, "--own", "--yes").OK(t)
		ada.Lore(ac.Dir, "doctor", "--fix") // DEGRADED: the linked repo still holds its own note
		if err := os.MkdirAll(filepath.Join(ac.Dir, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		old := `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "lore agent start --tool claude_code --hook-stdin"}]}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(ac.Dir, filepath.FromSlash(settings)), []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
		ac.Git("add", "-A")
		ac.Git("commit", "-q", "-m", "lore as an older version set it up")
	})

	s.Step("D-744/doctor-fix-names-every-tracked-file", func() {
		r := ada.Lore(ac.Dir, "doctor", "--fix")
		out := r.Out()
		var changed []string
		for _, p := range strings.Split(ac.Git("diff", "--name-only", "HEAD"), "\n") {
			if p = strings.TrimSpace(p); p != "" && !strings.HasPrefix(p, ".lore/") {
				changed = append(changed, p)
			}
		}
		if !strings.Contains(strings.Join(changed, " "), settings) {
			t.Fatalf("doctor --fix left %s as it was (changed %v):\n%s", settings, changed, out)
		}
		var adds []string
		for _, l := range strings.Split(out, "\n") {
			if _, cmd, ok := strings.Cut(l, "git add "); ok {
				files, _, _ := strings.Cut(cmd, " && ")
				adds = append(adds, strings.Fields(strings.TrimPrefix(files, "-- "))...)
			}
		}
		for _, p := range changed {
			named := false
			for _, a := range adds {
				if a == p || strings.HasPrefix(p, strings.TrimSuffix(a, "/")+"/") {
					named = true
				}
			}
			if !named {
				t.Fatalf("doctor --fix changed the tracked %s and no commit line names it:\n%s", p, out)
			}
		}
		ac.Git(append([]string{"add", "--"}, adds...)...)
		ac.Git("commit", "-q", "-m", "lore: fix")
		for _, l := range strings.Split(ac.Git("status", "--porcelain", "--untracked-files=all"), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, ".lore/") {
				t.Fatalf("after committing what doctor --fix named, git status lists %q", l)
			}
		}
	})
}
