//go:build machines && !windows

package machines

// shape_foreign.go — machines holding files someone else wrote: other
// tools' files edited by hand (foreign_tools), a config.yaml the person
// wrote by hand (foreign_handcfg), and what v0.1.17 left in a repo and in
// ~/.lore beyond ShapeV0117 (foreign_v0117). Every name is a placeholder.

import (
	"path/filepath"
)

// Foreign shape names.
const (
	ShapeForeignTools   = "foreign_tools"
	ShapeForeignHandcfg = "foreign_handcfg"
	ShapeForeignV0117   = "foreign_v0117"
)

// RepoForeign is the repo of the foreign shapes (Machine.Repos key): a git
// repo lore never initialised, full of other tools' files.
const RepoForeign = "foreign"

// ForeignSetup is the setup the foreign shapes' config.yaml holds.
const ForeignSetup = "personal"

// Hand-written text of foreign_tools. Each body has the person's text
// above and below whatever lore block it holds; the tests check every
// byte outside lore's block is kept.
const (
	// claudeEditedBlock is an older lore pointer block whose inside the
	// person edited (an extra line, the import path changed).
	foreignClaudeTop    = "# Team rules\n\nAlways run the linter.\n\n"
	foreignClaudeBlock  = "<!-- lore:pointer:start -->\n@.lore/OLD.md\nmy own note inside the block\n<!-- lore:pointer:end -->\n"
	foreignClaudeBottom = "\n## Deploy\n\nNever deploy on a Friday.\n"
	// AGENTS.md holds a pointer block whose end marker the person deleted:
	// everything after the start marker is theirs.
	foreignAgentsTop   = "# Agents\n\nUse tabs.\n\n"
	foreignAgentsStart = "<!-- lore:pointer:start -->\n"
	foreignAgentsRest  = "Read the wiki first.\n\n## Style\n\nShort functions.\n"
	// The person's own Cursor rule, hooks and copilot file.
	foreignCursorRule    = "---\ndescription: team style\nglobs: \"**/*.go\"\nalwaysApply: false\n---\nPrefer table tests.\n"
	foreignCursorHooks   = "{\n  \"version\": 1,\n  \"hooks\": {\n    \"afterFileEdit\": [\n      {\"command\": \"./scripts/fmt.sh\"}\n    ]\n  }\n}\n"
	foreignCopilot       = "# Copilot\n\nAnswer in English.\n"
	foreignClaudeSetting = "{\n  \"model\": \"placeholder-model\",\n  \"permissions\": {\n    \"allow\": [\"Bash(go test:*)\"]\n  },\n  \"hooks\": {\n    \"PreToolUse\": [\n      {\n        \"matcher\": \"Bash\",\n        \"hooks\": [{\"type\": \"command\", \"command\": \"./scripts/guard.sh\"}]\n      }\n    ],\n    \"SessionStart\": [\n      {\n        \"hooks\": [{\"type\": \"command\", \"command\": \"echo hello\"}]\n      }\n    ]\n  },\n  \"customKey\": {\"kept\": true}\n}\n"
	foreignClaudeLocal   = "{\n  \"permissions\": {\n    \"deny\": [\"Bash(rm:*)\"]\n  }\n}\n"
	// ~/.claude/CLAUDE.md: the person's own rules for every project.
	foreignHomeClaude = "# My rules\n\n- Short replies.\n- Never push without asking.\n"
)

// foreignToolsConfig is a block-style config.yaml with one solo setup over
// ~/code/** using every tool whose files the shape holds.
var foreignToolsConfig = `identity:
    key: human:` + PersonEmail + `
    name: ` + PersonName + `
setups:
    - name: ` + ForeignSetup + `
      kind: solo
      default: true
      identity:
        key: human:` + PersonEmail + `
        name: ` + PersonName + `
      tools:
        - claude_code
        - codex
        - cursor
        - copilot
      folders:
        - ~/code/**
`

// ForeignHandConfig is foreign_handcfg's config.yaml as the person wrote
// it: comments at the top, between setups and trailing, 2-space indent in
// one setup and 4 in the other, flow lists, unknown keys at the top and
// inside a setup, quoted and unquoted strings, CRLF line endings and no
// final newline.
var ForeignHandConfig = crlf("# my lore config — edited by hand\n" +
	"# keep this header\n" +
	"identity:\n" +
	"  key: \"human:" + PersonEmail + "\"   # me\n" +
	"  name: '" + PersonName + "'\n" +
	"my_notes: keep me  # a key lore does not know\n" +
	"setups:\n" +
	"  # the main one\n" +
	"  - name: " + ForeignSetup + "\n" +
	"    kind: solo\n" +
	"    default: true\n" +
	"    folders: [~/code/**, ~/b/**]\n" +
	"    tools: [claude_code]\n" +
	"    my_setup_note: \"hand added\"\n" +
	"\n" +
	"  # side projects, 4-space indent\n" +
	"  - name: side\n" +
	"    kind: solo\n" +
	"    folders:\n" +
	"        - ~/side/**\n" +
	"# trailing comment")

// crlf turns every \n into \r\n.
func crlf(s string) string {
	out := make([]byte, 0, len(s)+len(s)/8)
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, '\r')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// Old v0.1.17 leftovers, byte for byte what 0.1.17 wrote
// (saas/cmd/cli/sync_wire.go hookLines, render.go pointerBlock): the
// lore-sync hook block next to the person's own hook line, the
// lore-sync:live ride-along block, and its CLAUDE.md pointer block.
const (
	v0117PreCommit    = "#!/bin/sh\n# the person's own pre-commit line\nmake lint\n# >>> lore-sync >>>\n( if command -v lore >/dev/null 2>&1; then if lore sync hook --help >/dev/null 2>&1; then lore sync hook pre-commit; else echo \"lore: installed lore cannot check .lore/data (no 'sync hook'); upgrade it\" >&2; fi; fi ) || exit $?\n# <<< lore-sync <<<\n"
	v0117PostCmt      = "#!/bin/sh\n# >>> lore-sync:live >>>\n( command -v lore >/dev/null 2>&1 && lore land --help >/dev/null 2>&1 && lore sync hook post-commit ) || true\n# <<< lore-sync:live <<<\n"
	v0117ClaudeTop    = "# Old project\n\nKeep it simple.\n\n"
	v0117ClaudeBlock  = "<!-- lore:pointer:start -->\n@.lore/LORE.md\n<!-- if your agent does not support @import, read .lore/LORE.md now -->\n<!-- lore:pointer:end -->\n"
	v0117ClaudeBottom = "\n## After\n\nPerson text after.\n"
)

// HooksPathRel is foreign_v0117's core.hooksPath folder (repo-relative).
const HooksPathRel = ".githooks"

func init() {
	RegisterShape(ExtraShape{Name: ShapeForeignTools, Build: func(b *builder) {
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
		b.loreHome()
		b.file(".lore/config.yaml", foreignToolsConfig, 0o600)
		repo := b.foreignRepo()
		b.file(filepath.Join(repo, "CLAUDE.md"), foreignClaudeTop+foreignClaudeBlock+foreignClaudeBottom, 0o644)
		b.file(filepath.Join(repo, "AGENTS.md"), foreignAgentsTop+foreignAgentsStart+foreignAgentsRest, 0o644)
		b.file(filepath.Join(repo, ".cursor/rules/team.mdc"), foreignCursorRule, 0o644)
		b.file(filepath.Join(repo, ".cursor/hooks.json"), foreignCursorHooks, 0o644)
		b.file(filepath.Join(repo, ".claude/settings.json"), foreignClaudeSetting, 0o644)
		b.file(filepath.Join(repo, ".claude/settings.local.json"), foreignClaudeLocal, 0o644)
		b.file(filepath.Join(repo, ".github/copilot-instructions.md"), foreignCopilot, 0o644)
		b.file(".claude/CLAUDE.md", foreignHomeClaude, 0o644)
	}})
	RegisterShape(ExtraShape{Name: ShapeForeignHandcfg, Build: func(b *builder) {
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
		b.loreHome()
		b.file(".lore/config.yaml", ForeignHandConfig, 0o600)
		b.foreignRepo()
	}})
	RegisterShape(ExtraShape{Name: ShapeForeignV0117, Build: func(b *builder) {
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
		b.loreHome()
		b.file(".lore/identity.yaml", "stable_key: human:"+OldEmail+"\ndisplay_name: Old Name\n", 0o644)
		b.file(".lore/identity.toml", "stable_key = \"human:"+OldEmail+"\"\ndisplay_name = \"Old Name\"\n", 0o644)
		b.file(".lore/lore.db", "", 0o644)
		repo := b.foreignRepo()
		// The repo's own v0.1.17 leftovers: lore.db in .lore, the hook
		// blocks in .git/hooks, a core.hooksPath folder with a block, the
		// 0.1.17 pointer block in CLAUDE.md.
		b.file(filepath.Join(repo, ".lore/lore.db"), "", 0o644)
		b.file(filepath.Join(repo, ".git/hooks/pre-commit"), v0117PreCommit, 0o755)
		b.file(filepath.Join(repo, ".git/hooks/post-commit"), v0117PostCmt, 0o755)
		b.file(filepath.Join(repo, "CLAUDE.md"), v0117ClaudeTop+v0117ClaudeBlock+v0117ClaudeBottom, 0o644)
		hp := filepath.Join(b.m.Home, "code", "foreign-hp")
		b.dir(hp, 0o755)
		b.run(hp, "/usr/bin/git", "init", "-q")
		b.run(hp, "/usr/bin/git", "config", "core.hooksPath", HooksPathRel)
		b.file(filepath.Join(hp, HooksPathRel, "pre-commit"), v0117PreCommit, 0o755)
		b.m.Repos[RepoForeignHP] = hp
	}})
}

// RepoForeignHP is foreign_v0117's repo whose hooks live in core.hooksPath.
const RepoForeignHP = "foreign-hp"

// foreignRepo makes ~/code/foreign, a git repo lore never initialised.
func (b *builder) foreignRepo() string {
	p := filepath.Join(b.m.Home, "code", RepoForeign)
	b.dir(p, 0o755)
	b.run(p, "/usr/bin/git", "init", "-q")
	b.m.Repos[RepoForeign] = p
	return p
}
