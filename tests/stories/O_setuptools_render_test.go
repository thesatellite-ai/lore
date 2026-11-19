//go:build stories && !windows

package stories

// O_setuptools_render_test.go — devco D-711 on the owner-shaped machine
// (story O): the guided setup chose five tools (the person's tools across
// all repos), then `lore org link --own` in an org repo that uses only
// Claude Code, then `lore render`. Render wrote AGENTS.md, GEMINI.md,
// .cursor/rules/lore.mdc and .github/copilot-instructions.md into the code
// repo unasked. Now it writes only CLAUDE.md's pointer, names each skipped
// tool with the command that adds it, and that command works as printed.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOSetupToolsRenderOnlyUsed(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	billing := s.Seed(s.Remote("github.com/"+oOwner+"/billing"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	five := []string{"claude_code", "codex", "cursor", "gemini_cli", "copilot"}
	for _, st := range []struct {
		step    string
		answers map[string]any
	}{
		{"kind", map[string]any{"kind": "admin", "name": oSetup}},
		{"identity", map[string]any{"name": oAdminOrg.Name, "email": oAdminOrg.Email}},
		{"github", map[string]any{"github_account": oAdminOrg.Login, "host_alias": oAlias}},
		{"personal_store", map[string]any{"personal_store": "local"}},
		{"team", map[string]any{"org": oOrg, "store_url": team.SSHURL()}},
		{"tools", map[string]any{"tools": five}},
		{"secrets", map[string]any{"backend": "none"}},
		{"folders", map[string]any{"folders": []string{"~/code/" + oOrg + "/**"}, "default": false}},
		{"this_folder", map[string]any{}},
		{"summary", map[string]any{}},
	} {
		oApply(t, ada, pc.Dir, oSetup, st.step, st.answers)
	}

	bc := ada.CloneTo(billing, ada.OrgDir("billing"))
	other := []string{"AGENTS.md", "GEMINI.md", ".cursor/rules/lore.mdc", ".github/copilot-instructions.md"}
	var add string
	s.Step("D-711/link-own-then-render-writes-claude-only", func() {
		bc.WriteFile("CLAUDE.md", "# billing notes\n")
		ada.LoreIn(bc, "init", "--target", "claude_code", "--yes").OK(t)
		ada.LoreIn(bc, "policy", "unset", "--key", "context.targets", "--yes").OK(t) // a repo from before the setting
		bc.CommitAll("lore")
		ada.LoreIn(bc, "store", "folder", "add", "team", "billing").OK(t)
		ada.LoreIn(bc, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.LoreIn(bc, "org", "link", "--org", oOrg, "--project", "billing", "--own", "--yes").OK(t)
		r := ada.LoreIn(bc, "render", "--yes").OK(t).Says(t, "CLAUDE.md (pointer present)")
		for _, f := range other {
			r.Says(t, "("+f+"): your setup lists it but this repo does not use it")
			if _, ok := bc.ReadFile(f); ok {
				t.Fatalf("D-711: render wrote %s into a repo that uses only Claude Code", f)
			}
		}
		for _, l := range strings.Split(r.Stdout, "\n") {
			if i := strings.Index(l, "add it with: "); i >= 0 && strings.Contains(l, "skipped cursor") {
				add = strings.TrimSpace(l[i+len("add it with: "):])
			}
		}
		if add != "lore render --add-target cursor" {
			t.Fatalf("printed add command = %q", add)
		}
	})

	s.Step("D-711/the-printed-command-adds-the-tool", func() {
		ada.LoreIn(bc, append(strings.Fields(strings.TrimPrefix(add, "lore ")), "--yes")...).OK(t).Says(t, "added cursor to context.targets")
		if _, ok := bc.ReadFile(filepath.Join(".cursor", "rules", "lore.mdc")); !ok {
			t.Fatal("the printed command did not write .cursor/rules/lore.mdc")
		}
		for _, f := range []string{"AGENTS.md", "GEMINI.md", ".github/copilot-instructions.md"} {
			if _, ok := bc.ReadFile(f); ok {
				t.Fatalf("D-711: %s appeared after adding only cursor", f)
			}
		}
	})
}
