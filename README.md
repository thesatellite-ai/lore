<p align="center">
  <img src="brand/png/lore-icon-512.png" alt="lore logo — stacked memory records orbited by a recall node" width="120" height="120">
</p>

<h1 align="center">lore</h1>

<p align="center"><strong>Give your AI coding agent a memory.</strong></p>

<p align="center">The local-first memory &amp; context layer for AI coding agents — capture rules, decisions, and gotchas once, and lore compiles them into the <code>CLAUDE.md</code> / <code>AGENTS.md</code> your agent reads every session. No cloud, no API keys, nothing leaves your machine.</p>

<p align="center">
  <a href="https://github.com/thesatellite-ai/lore/releases"><img src="https://img.shields.io/github/v/release/thesatellite-ai/lore?color=10B981&label=release" alt="Latest release"></a>
  <a href="https://github.com/thesatellite-ai/lore/blob/main/LICENSE"><img src="https://img.shields.io/github/license/thesatellite-ai/lore?color=10B981" alt="License"></a>
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-10B981" alt="Platforms: macOS, Linux, Windows">
  <img src="https://img.shields.io/badge/built%20with-Go-00ADD8" alt="Built with Go">
  <img src="https://img.shields.io/badge/data-100%25%20local-10B981" alt="100% local data">
  <a href="https://github.com/thesatellite-ai/lore/stargazers"><img src="https://img.shields.io/github/stars/thesatellite-ai/lore?style=social" alt="GitHub stars"></a>
</p>

lore is an open-source, local-first **memory and context-management CLI for AI coding agents** — Claude Code, Cursor, Windsurf, Cline, GitHub Copilot, and Codex. It captures what you and your agent learn about a project — rules, decisions, patterns, gotchas, tasks — and turns it into the `CLAUDE.md` / `AGENTS.md` your agent reads every session. So it stops forgetting context and stops repeating the same mistakes.

One command to install, nothing to run, everything stays on your machine. Ships with a Claude Code skill so the agent saves and recalls knowledge for you automatically.

- [Why lore?](#why-lore)
  - [lore vs other AI memory tools](#lore-vs-other-ai-memory-tools)
- [Install](#install)
  - [Install the skill (required)](#install-the-skill-do-this-now--required)
- [Quick start](#quick-start)
- [Concepts](#concepts)
- [Command reference](#command-reference)
  - [Project setup & ops](#project-setup--ops)
  - [Capture knowledge](#capture-knowledge)
  - [Retrieve & render](#retrieve--render)
  - [Project management](#project-management)
  - [Agent loop & automation](#agent-loop--automation)
  - [Backup, health & recovery](#backup-health--recovery)
- [Common flags](#common-flags)
- [Interactive TUI](#interactive-tui)
- [FAQ](#faq)
- [Building from source / contributing](#building-from-source--contributing)

## Why lore?

Most AI memory tools bolt a vector database onto your agent and hope it retrieves the right thing at runtime. lore takes the opposite bet: **your agent already reads `CLAUDE.md` / `AGENTS.md` at the start of every session** — so lore makes *that file* the memory. `lore render` writes your knowledge to `.lore/LORE.md` and stitches an `@import` pointer into `CLAUDE.md`, so your hand-written content is preserved and recall is just your agent reading the file it already reads. No embeddings, no API keys, no runtime retrieval lottery, and nothing leaves your machine.

- **Local-first & private** — one SQLite file under `.lore/`. No cloud, no account, no telemetry. Your project knowledge never leaves your laptop.
- **Deterministic recall** — must-follow rules and critical warnings are *pinned* into the rendered file; everything else is one `lore search` away. No embedding drift, no "why didn't it remember that?"
- **Structured, not a blob** — `rule` (with severity), `decision` (with rationale), `hotfix`, `pattern`, `playbook`, `task`, `run` — each entity is rendered the way it's meant to be read, not dumped as one undifferentiated wall of text.
- **Works with the tools you already use** — renders `CLAUDE.md`, `AGENTS.md`, or `.cursorrules` via `--target`. Claude Code, Cursor, Windsurf, Cline, Copilot, Codex.
- **Token-budget-aware** — the hybrid render pins only `must` rules + critical hotfixes; the long tail stays searchable, so your context window stays lean as knowledge grows.
- **Knowledge + work in one place** — tasks, missions, agent runs, and git-commit links live next to the knowledge they relate to.
- **Free & open source** — no seats, no usage tiers, no rug pull.

### lore vs other AI memory tools

| | **lore** | Cloud memory APIs | Vector RAG memory | Hand-written rules files |
|---|:---:|:---:|:---:|:---:|
| Runs 100% local, no account | ✅ | ❌ | ⚠️ self-host | ✅ |
| No API keys / no embedding costs | ✅ | ❌ | ❌ | ✅ |
| Deterministic recall (no vector drift) | ✅ | ❌ | ❌ | ✅ |
| Structured knowledge (rules vs decisions vs hotfixes, severity) | ✅ | ⚠️ | ❌ | ❌ |
| Renders into the file your agent already reads | ✅ | ❌ | ❌ | ✅ manual |
| Auto-capture **and** recall via an agent skill | ✅ | ⚠️ | ⚠️ | ❌ |
| Token-budget-aware context (pins only what matters) | ✅ | ❌ | ⚠️ | ❌ |
| Work tracking (tasks, runs, git links) in the same store | ✅ | ❌ | ❌ | ❌ |
| Full-text search across all knowledge | ✅ | ✅ | ✅ | ❌ |
| Free & open source | ✅ | ⚠️ | ⚠️ | ✅ |

If you want a hosted, embeddings-based agent memory, plenty of those exist. If you want **deterministic, private, structured project memory that lands in the file your agent already trusts** — that's lore.

## Install

> **lore needs two pieces, both required:**
> 1. the **`lore` binary** (the actual CLI/engine), and
> 2. the **Claude skill** (teaches Claude Code *when/how* to call `lore`).
>
> The Homebrew and script installers below install **both**. If you install
> the binary manually, install the skill separately (see
> [Install the skill](#install-the-skill)). The skill without the binary does
> nothing; the binary without the skill works but Claude won't use it
> automatically.

### Homebrew (macOS / Linux)

```sh
brew install thesatellite-ai/tap/lore
```

### Script (macOS / Linux)

```sh
curl -sL https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.sh | sh
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.ps1 | iex
```

Or grab a tarball from
[Releases](https://github.com/thesatellite-ai/lore/releases) and put `lore` on
your `PATH` (the `skills/` folder in the archive is the skill bundle).

### Install the skill (do this now — required)

lore needs **both** the binary *and* the Claude skill. The skill teaches Claude
Code **when and how** to call `lore` (capture on corrections/decisions/"remember
this", retrieve before answering, keep `CLAUDE.md` current). Without it the
binary works but Claude won't use it automatically.

The Homebrew and `curl … | sh` / PowerShell installers **already install the
skill** to `~/.claude/skills/lore` — you're done, just **restart Claude Code**.

Installed the binary some other way? Add the skill with one of:

```sh
npx skills add thesatellite-ai/lore          # via skills.sh
```

```sh
mkdir -p ~/.claude/skills/lore               # manual: from a tarball/checkout
cp -R skills/. ~/.claude/skills/lore/
```

Then **restart Claude Code** so it loads.

## Quick start

```sh
cd your-project

lore init                # create the local lore project
lore setup               # build the search index
lore directive install   # add the agent-directive block to CLAUDE.md / AGENTS.md

# capture knowledge
lore memory add --body="use Tailwind v4, not v3"
lore rule add --body="never force-push main"
lore decision add --title="Bundler" --body="Vite over Webpack: faster dev server"

# retrieve it
lore search "tailwind"            # search across all knowledge
lore render                      # compile knowledge → .lore/LORE.md + @import pointer in CLAUDE.md

lore tui                         # browse everything interactively
```

## Concepts

- **Project** — a local database under `.lore/` per repo (created by `lore init`).
- **Entities** — typed knowledge records: `memory`, `rule`, `decision`,
  `pattern`, `hotfix`, `snapshot`, `playbook`, `prompt`, `task`, `mission`,
  `plan`, `reminder`, `handoff`, `incident`, `techdoc`, and more.
- **Scope** — knowledge is scoped to the whole project (`master`) or a specific
  `--repo`. `add` persists `repo_id`; `list` and `search` filter by it with
  identical semantics (`--repo`, `--all-repos`, `--master-only`, `--no-inherit`).
  Real per-repo scoping — no body prefix-tag convention needed.
  Re-scope an existing row with `edit --rebind-repo=<mount>` /
  `edit --rebind-master`. Bare `--repo` on `edit` is context-only (never
  mutates scope; warns loudly). For audited body+scope change use
  `add --supersedes=<old_id> --repo=<mount>`.
- **Render** — `lore render` compiles the relevant scoped knowledge into a
  generated file (default `.lore/LORE.md`) and stitches an idempotent `@import`
  pointer into your agent file (`CLAUDE.md` by default, or `AGENTS.md` /
  `.cursorrules` via `--target`) — so your hand-written `CLAUDE.md` content is
  never clobbered. Use `--no-pointer` to write only the generated file, or
  `--out` to change its path.

### The common verb pattern

Almost every entity supports the same sub-verbs, so once you know one you know
them all:

```sh
lore <entity> add      --body="…"      # create (some take --title too)
lore <entity> list                     # list active rows
lore <entity> show     <id>            # full detail
lore <entity> edit     <id> --body="…" # update only the fields you pass
lore <entity> search   "query"         # search within that entity
lore <entity> archive  <id>            # soft-delete (reversible: unarchive)
lore <entity> delete   <id>            # HARD delete (no undo — prefer archive)
```

Bodies can be passed with `--body="…"` or piped: `echo "text" | lore memory add`.

## Command reference

`lore --help` lists everything; `lore <command> --help` shows every flag for
that command. Grouped by what you're trying to do:

### Project setup & ops

| Command | Use case |
|---|---|
| `lore init [path]` | Create a new project (`.lore/lore.db`, schema, gitignore, Project row). `--name`, `--non-interactive` |
| `lore setup` | One-time post-install/upgrade migrations (builds the search index). Run once per project after install/upgrade |
| `lore directive install` | Inject the lore agent-directive block into `CLAUDE.md` / `AGENTS.md` so agents know to use lore (`remove` to undo, `show` to print it) |
| `lore identity` | Manage the persisted human/agent identity at `~/.lore/identity.toml` |
| `lore config` | Get/set DB-level config keys (`dbconfig` table) |
| `lore version` | Version, schema version, build info (`--json`) |
| `lore doctor` | Health checks; exit 0 healthy / 1 degraded / 2 broken |

### Capture knowledge

| Command | Use case |
|---|---|
| `lore memory add` | Free-form learned knowledge ("use X not Y"). `--kind core\|retrieved\|episodic\|procedural\|archival` |
| `lore rule add` | Hard constraints the agent must follow ("never force-push main") |
| `lore decision add` | Architectural decision records (title + rationale) |
| `lore pattern add` | Reusable code/design patterns |
| `lore hotfix add` | Loud recurring warnings surfaced prominently in rendered context |
| `lore playbook add` | Step-by-step procedures ("how we cut a release") |
| `lore prompt add` | Saved prompt templates |
| `lore snapshot add` | Point-in-time knowledge capture |
| `lore handoff add` | Session/agent handoff notes |
| `lore incident add` | Incident records / postmortems |
| `lore techdoc add` | References to external documentation |
| `lore comment` | Attach comments to any entity (`add / list / search / delete`) |
| `lore tag` | Create tags and bind them to entities (`add / list / attach / detach`) |

The knowledge entities above (`memory` … `techdoc`) all support
`list / show / edit / search / archive / unarchive` (see
[the common verb pattern](#the-common-verb-pattern)); `comment` and `tag` use
the narrower verb sets shown beside them. Use
`lore <entity> add --supersedes <id>` for an audited body change.

### Retrieve & render

| Command | Use case |
|---|---|
| `lore search "<query>"` | **The retrieval hammer** — one search across every entity, best-first ranked. `--limit`, `--all-repos`, `--json`, `--include-archived` |
| `lore <entity> search "<q>"` | Scope search to a single entity type |
| `lore search status` | Per-entity search-index counts + health |
| `lore search rebuild` | Rebuild the search index from source rows |
| `lore render` | Compile scoped knowledge → `.lore/LORE.md` and stitch an `@import` pointer into the agent file. `--out`, `--target AGENTS.md`, `--no-pointer`, `--dry-run`, `--repo`, `--project` |
| `lore why-context` | Show the last rendered context (exactly what the agent saw) |
| `lore commit-show <sha>` | Show every entity linked to a git commit |

### Project management

| Command | Use case |
|---|---|
| `lore project` | Manage projects (the top-level container) |
| `lore repo` | Manage repos within a project (per-repo scoping) |
| `lore task` | Discrete work items: `add / list / triage / someday / deferred / show / edit / start / done / cancel / search`. Two axes beyond `status`: `commitment` (`accepted`/`proposed`/`someday` — agents must set it on `add`, no default) and `deferred_until` (snooze; auto-resurfaces). Default views show only committed, active, non-deferred tasks. |
| `lore tasklist` / `lore task-view` | Group tasks; saved task-list filter views |
| `lore mission` | Containers that group related tasks |
| `lore plan` | Plans |
| `lore reminder` | Time-based reminders |
| `lore workflow` / `lore workspace` | Workflow and workspace records |

### Agent loop & automation

| Command | Use case |
|---|---|
| `lore run` | Log + inspect agent runs: `start / step / end / cancel / replay / show / list` |
| `lore link add --commit=<sha> --entity=<id>` | Link a git commit to any entity (task/run/mission/decision/…). Also `list` / `remove` |
| `lore learn-from <source>` | Bootstrap knowledge from existing markdown/docs |
| `lore learn` | Manage background-learning staging: `from / list / promote / reject` |
| `lore external-source` | Register sources for `learn-from`: `add / enable / disable / list` (disabled by default) |
| `lore bench` | Benchmark engine — define, run, and report on eval tasks: `eval / run / report / result / grader` |
| `lore directive` | Install/remove/show the agent-directive block |
| `lore skill` | Meta-tools for the Claude skill bundle (`compile` — compress the canonical bundle via LLM) |
| `lore session` / `lore querylog` | Inspect sessions and the query log |
| `lore actor` | Inspect actors (humans, agents, hooks, plugins) |
| `lore behaviour` / `lore suggestion` | Behaviours and suggestions |
| `lore pii-pattern` | Custom PII/secret detection patterns (capture refuses secrets by default) |
| `lore plugin` | Manage the trusted-plugin allowlist |

### Backup, health & recovery

| Command | Use case |
|---|---|
| `lore backup` | Online backup of the project DB |
| `lore restore <file>` | Restore the DB from a backup |
| `lore repair` | Recover from a corrupted DB |
| `lore doctor` | Health checks (DB integrity, FTS drift, schema version) |
| `lore tables` | Every data table + total record count. Sortable (`--sort=name\|count[:asc\|desc]`), filterable (`--filter=`), `--json`. Also a TUI screen: `lore tui --kind=tables` |
| `lore support-bundle` | Produce a sanitized incident-report bundle |
| `lore snapshot` | Point-in-time knowledge captures (logical, not file backup) |

## Common flags

These work across most commands:

| Flag | Meaning |
|---|---|
| `--json` | Machine-readable JSON envelope (use this from scripts/agents) |
| `--repo <mount\|rep_id>` | Scope to one repo instead of project-master |
| `--project <id\|name>` | Operate on a specific project |
| `--db <path>` | Override the DB path (default: cwd's `.lore/lore.db`; also `LORE_DB`) |
| `--read-only` | Skip lock acquisition; refuse writes (safe for inspection) |
| `--include-archived` / `--archived` | Include soft-deleted rows |
| `--color auto\|always\|never` | Color output control |

Env: `LORE_DB`, `LORE_PROJECT_ID`, `LORE_REPO`, `LORE_HOME` mirror the flags.

## Interactive TUI

`lore tui` opens a full terminal UI to browse, filter, view, and edit every
entity in the DB — vim-style keys, fuzzy search, live theme toggle.

![lore TUI — main list view](docs/screenshots/main.jpg)

| | |
|---|---|
| ![lore TUI — entity detail view](docs/screenshots/view.jpg) | ![lore TUI — edit form](docs/screenshots/edit.jpg) |
| ![lore TUI — actions menu](docs/screenshots/actions.jpg) | ![lore TUI — quick help](docs/screenshots/quick-help.jpg) |

## FAQ

**Which AI coding agents does lore support?**
Any agent that reads a project-instructions file. lore renders `CLAUDE.md` (Claude Code), `AGENTS.md` (Cursor, Codex, and others), or `.cursorrules` via `lore render --target`. Works alongside Claude Code, Cursor, Windsurf, Cline, GitHub Copilot, and OpenAI Codex.

**Does lore send my code or knowledge to the cloud?**
No. Everything lives in a local SQLite database under `.lore/`. No account, no network calls, no telemetry — your project memory never leaves your machine.

**Does it use an LLM, embeddings, or an API key?**
No. Retrieval is SQLite FTS5 full-text search — fast, deterministic, and free. There's no vector database and no embedding bill.

**How is this different from just writing `CLAUDE.md` by hand?**
lore keeps knowledge structured (rules vs decisions vs hotfixes, with severity), deduplicated, scoped per-repo, searchable, and versioned — then regenerates `.lore/LORE.md` deterministically and `@import`s it from `CLAUDE.md`, leaving your hand-written content untouched. Hand-written instruction files rot, contradict each other, and quietly blow your token budget.

**Won't a large memory bloat my context window?**
No. The hybrid render pins only `must`-severity rules and critical hotfixes into the file; everything else surfaces on demand via `lore search`. Context stays small even as the knowledge base grows.

**Can my team share project memory?**
Yes — commit the generated `.lore/LORE.md` (and the `CLAUDE.md` / `AGENTS.md` that `@import`s it) to git. Each developer keeps their own local `.lore` database, and the rendered context travels with the repo.

**Is capture and recall really automatic?**
With the bundled Claude skill, the agent captures decisions, rules, and corrections and recalls relevant knowledge on its own. You can also drive everything by hand with the CLI.

**Is lore free and open source?**
Yes — free, open source, no seats or usage tiers.

## Building from source / contributing

See **[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)** (build, release, Homebrew tap)
and **[CONTRIBUTING.md](CONTRIBUTING.md)**.

<sub><strong>lore</strong> — open-source, local-first memory &amp; context management for AI coding agents. Persistent <code>CLAUDE.md</code> / <code>AGENTS.md</code> memory for Claude Code, Cursor, Windsurf, Cline, GitHub Copilot, and Codex. No cloud, no API keys, no embeddings.</sub>
