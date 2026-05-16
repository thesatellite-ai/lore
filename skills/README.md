# lore Skill

This directory is a **Claude Skill bundle** — a reusable knowledge package that teaches Claude (Code, Desktop, or API agents) how to use `lore`.

## What's a Skill?

Anthropic Skills are folder-based packages that Claude auto-discovers and loads on demand. The format is published at [skills.anthropic.com](https://skills.anthropic.com) / supported in Claude Code and Claude Desktop.

A Skill folder always has a `SKILL.md` at the root with YAML frontmatter:

```yaml
---
name: skill-name
description: When this skill is relevant. Be specific — Claude uses this to decide when to load.
---
```

Plus optional supporting files (`COMMANDS.md`, `PLAYBOOKS.md`, scripts, …) referenced from `SKILL.md`.

## Layout

```
skill/
├── SKILL.md              ← entry point, has frontmatter, always loaded first
├── README.md             ← this file (install + maintenance)
├── COMMANDS.md           ← full CLI reference, every command + flags + JSON
├── USECASES.md           ← "I want to X" → command mapping (intent table)
├── PLAYBOOKS.md          ← 20 multi-step workflows (P1-P20)
├── SCENARIOS.md          ← 60+ concrete situations (A-O sections)
├── DECISION-TREE.md      ← which command to use when intent is ambiguous
├── GLOSSARY.md           ← terminology used across the docs + CLI
├── ERRORS.md             ← all 37 E_* codes + recovery decision tree
└── examples/
    ├── 01-bootstrap.md           ← first-time setup
    ├── 02-capture-correction.md  ← user corrects → rule add
    ├── 03-multi-repo.md          ← scoping per repo
    ├── 04-disaster-recovery.md   ← repair tier 1/2/3
    ├── 05-decision-record.md     ← ADR-style capture
    ├── 06-task-tracking.md       ← mission + tasks + sprint
    ├── 07-search-patterns.md     ← FTS5 BM25 query syntax
    ├── 08-ci-integration.md      ← render-diff in CI
    └── 09-tags-and-comments.md   ← polymorphic labels + discussions
```

## Install in Claude Code

Drop the `skill/` folder into your project — Claude Code auto-discovers it on session start. Or to make it global across all projects:

```bash
mkdir -p ~/.claude/skills
cp -r skill ~/.claude/skills/lore
```

Now every Claude Code session in any project knows about lore and can use it when relevant.

## Install in Claude Desktop (MCP-based)

Claude Desktop loads skills via the MCP filesystem server. Point it at this directory:

```jsonc
// ~/Library/Application Support/Claude/claude_desktop_config.json
{
  "mcpServers": {
    "skills": {
      "command": "npx",
      "args": ["@modelcontextprotocol/server-filesystem", "/path/to/skill"]
    }
  }
}
```

## Install in arbitrary AI agents

The folder is plain markdown — any agent that can read files can consume it. Tell the agent:

> Read `skill/SKILL.md` for an overview. When the user wants to capture project knowledge, use the commands documented in `skill/COMMANDS.md`. For multi-step flows, check `skill/PLAYBOOKS.md`. Match errors against `skill/ERRORS.md`.

## Update workflow

This skill describes the **public CLI surface** of lore. When the CLI changes:

```bash
# Regenerate the error table from the binary
lore errors list --json | jq -r '
    "| `\(.errors[].code)` | \(.errors[].description) |"
' > /tmp/errors.md
# Then paste into ERRORS.md

# Verify commands documented in SKILL.md/COMMANDS.md still exist
lore --help | grep -E "^\s+\w+"
```

## License

Same license as lore itself.
