# lore Skill

**In short:** this folder teaches a coding agent how to use lore: when to save a rule or a note, which command to run, and what each error means. Install it once and the agent follows it in every session.

This folder is a **skill bundle**: a set of markdown pages that teaches a coding agent how to use `lore`. It works with any agent tool that loads skills, and with any agent that can read files.

## What's a Skill?

A skill is a folder of instructions that an agent tool finds by itself and reads only when the task needs it. Several coding tools support this folder format; the format is published at [skills.anthropic.com](https://skills.anthropic.com).

A Skill folder always has a `SKILL.md` at the root with YAML frontmatter:

```yaml
---
name: skill-name
description: When this skill is relevant. Be specific — the agent tool uses this to decide when to load.
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
    ├── 04-disaster-recovery.md   ← rebuild the DB from .lore/data
    ├── 05-decision-record.md     ← save a decision and its reasons
    ├── 06-task-tracking.md       ← mission + tasks + sprint
    ├── 07-search-patterns.md     ← find a note fast (search syntax)
    ├── 08-ci-integration.md      ← render-diff in CI
    └── 09-tags-and-comments.md   ← labels and comments on any row
```

## Install in Claude Code

Put the `skill/` folder in your project and the agent tool finds it when a session starts. To use it in every project, copy it to the tool's global skills folder:

```bash
mkdir -p ~/.claude/skills
cp -r skill ~/.claude/skills/lore
```

Now every session, in any project, knows about lore and uses it when it is relevant.

## Install in Claude Desktop (MCP-based)

The desktop app loads skills through the MCP filesystem server (MCP: a standard way for an agent tool to reach outside files and services, like a plug socket). Point it at this folder:

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

## Install in any other agent tool

The folder is plain markdown, so any agent that can read files can use it. Tell the agent:

> Read `skill/SKILL.md` for an overview. When the user wants to capture project knowledge, use the commands documented in `skill/COMMANDS.md`. For multi-step flows, check `skill/PLAYBOOKS.md`. Match errors against `skill/ERRORS.md`.

## Update workflow

This skill describes the **public CLI surface** of lore. When the CLI changes:

```bash
# The error table comes from the errors block of docs/reference/cli-commands.md
# (regenerated with `lore spec docs`); copy the changed rows into ERRORS.md

# Verify commands documented in SKILL.md/COMMANDS.md still exist
lore --help | grep -E "^\s+\w+"
```

## License

Apache License 2.0, the same as the lore source (see the repository's `LICENSE`).
