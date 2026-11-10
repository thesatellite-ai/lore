<p align="center">
  <img src="brand/png/lore-icon-512.png" alt="lore logo — stacked memory records orbited by a recall node" width="120" height="120">
</p>

<h1 align="center">lore</h1>

<p align="center"><strong>Give your coding agent a memory.</strong></p>

<p align="center">The local-first memory &amp; context layer for coding agents — capture rules, decisions, and gotchas once, and lore compiles them into the <code>CLAUDE.md</code> / <code>AGENTS.md</code> your agent reads every session. No cloud, no API keys, nothing leaves your machine.</p>

<p align="center">
  <a href="https://github.com/thesatellite-ai/lore/releases"><img src="https://img.shields.io/github/v/release/thesatellite-ai/lore?color=10B981&label=release" alt="Latest release"></a>
  <a href="https://github.com/thesatellite-ai/lore/blob/main/LICENSE"><img src="https://img.shields.io/github/license/thesatellite-ai/lore?color=10B981" alt="License"></a>
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-10B981" alt="Platforms: macOS, Linux, Windows">
  <img src="https://img.shields.io/badge/built%20with-Go-00ADD8" alt="Built with Go">
  <img src="https://img.shields.io/badge/data-100%25%20local-10B981" alt="100% local data">
  <a href="https://github.com/thesatellite-ai/lore/stargazers"><img src="https://img.shields.io/github/stars/thesatellite-ai/lore?style=social" alt="GitHub stars"></a>
</p>

**In short:** lore is a small command-line tool that remembers what your project knows: rules like "Run the tests before you push", decisions like "We use Stripe for payments", and tasks like "Send the invoice reminder before Friday's demo". It saves them as small files in your git repo and writes the important ones into the file your coding agent reads every time it starts, so the agent stops forgetting. Nothing goes to a cloud service.

lore is an open-source, local-first **memory and context-management CLI for coding agents** such as Cursor, Windsurf, Cline, GitHub Copilot and Codex. It captures what you and your agent learn about a project — rules, decisions, patterns, gotchas, tasks — and turns it into the `CLAUDE.md` / `AGENTS.md` your agent reads every session. So it stops forgetting context and stops repeating the same mistakes.

One command to install, nothing to run, everything stays on your machine. It ships with a skill (a short instruction file your coding agent loads) so the agent saves and recalls knowledge for you automatically.

- [Documentation](#documentation)
- [Why lore?](#why-lore)
  - [lore vs other agent memory tools](#lore-vs-other-agent-memory-tools)
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
  - [Health & recovery](#health--recovery)
  - [Sharing through git](#sharing-through-git)
  - [Teams, stores and agents](#teams-stores-and-agents)
- [Team sync through git](#team-sync-through-git)
  - [Merges and pull requests](#merges-and-pull-requests)
- [Common flags](#common-flags)
- [Interactive TUI](#interactive-tui)
- [FAQ](#faq)
- [Building from source / contributing](#building-from-source--contributing)
- [License](#license)

## Documentation

![How lore is built: callers at the top, then surface, wire, ops, engine, store and adapters, and the .lore/data row files in git at the bottom; a command goes down and is committed and pushed, and teammates' rows come back up as the context file coding agents read](docs/assets/architecture.svg)

- **[Learning path](docs/README.md#learning-path):** thirteen steps from your first rule to a team with a client, private notes and several agents, each with a guide and a real example.
- **[Start from your goal](docs/README.md#start-from-your-goal):** "I want to…" and the page that does it.
- **[Decision guides](docs/choosing/README.md):** notes in each repo or in one database, with the code or live, how to lay out stores, which org and identity setup, which file your coding tool reads, and when to use agents, claims and gates.
- **[Feature index](docs/reference/features.md):** every feature with its code, its guide and a runnable example.
- **[Reference](docs/reference/README.md):** every command, flag, config key, environment variable and error code.

## Why lore?

Most agent memory tools bolt a vector database (a search engine that matches by rough meaning, not by exact words) onto your agent and hope it finds the right thing when it is needed. lore takes the opposite bet: **your agent already reads `CLAUDE.md` / `AGENTS.md` at the start of every session** — so lore makes *that file* the memory. `lore render` writes your knowledge to `.lore/LORE.md` and adds an `@import` pointer (one line that says "read that file too") to `CLAUDE.md`, so your hand-written content is kept and recall is just your agent reading the file it already reads. No embeddings, no API keys, no runtime retrieval lottery, and nothing leaves your machine.

- **Local-first & private** — a local cache under `.lore/` plus plain JSON files you commit yourself. No cloud, no account, no telemetry. Nothing leaves your laptop unless you `git push` it.
- **Shared through git, per branch** — every shared row (one note) is one small JSON file under `.lore/data/`. Knowledge learned on a feature branch stays on that branch and reaches `main` when the PR merges, exactly like code. No server, no S3, no sync service.
- **Deterministic recall** — must-follow rules and critical warnings are *pinned* into the rendered file; everything else is one `lore search` away. No embedding drift, no "why didn't it remember that?"
- **Structured, not a blob** — `rule` (with severity), `decision` (with rationale), `hotfix`, `pattern`, `playbook`, `task`, `run` — each entity is rendered the way it's meant to be read, not dumped as one undifferentiated wall of text.
- **Works with the tools you already use** — renders `CLAUDE.md`, `AGENTS.md`, or `.cursorrules` via `--target`, for Cursor, Windsurf, Cline, Copilot, Codex and other coding agents.
- **Token-budget-aware** — the hybrid render pins only `must` rules + critical hotfixes; the long tail stays searchable, so your context window stays lean as knowledge grows.
- **Knowledge + work in one place** — tasks, missions, agent runs, and git-commit links live next to the knowledge they relate to.
- **Free & open source** — no seats, no usage tiers, no rug pull.

### lore vs other agent memory tools

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
> 2. the **lore skill** (teaches your coding agent *when/how* to call `lore`).
>
> The Homebrew and script installers below install **both**. If you install the binary manually, install the skill separately (see [Install the skill](#install-the-skill-do-this-now--required)). The skill without the binary does nothing; the binary without the skill works but your agent won't use it automatically.

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

Or grab a tarball from [Releases](https://github.com/thesatellite-ai/lore/releases) and put `lore` on your `PATH` (the `skills/` folder in the archive is the skill bundle).

### Install the skill (do this now — required)

lore needs **both** the binary *and* the lore skill. The skill teaches your coding agent **when and how** to call `lore` (save a note when you correct it, make a decision or say "remember this"; look things up before answering; keep `CLAUDE.md` current). Without it the binary works but your agent won't use it automatically.

The Homebrew and `curl … | sh` / PowerShell installers **already install the skill** to `~/.claude/skills/lore` — you're done, just **restart your coding agent**.

Installed the binary some other way? Add the skill with one of:

```sh
npx skills add thesatellite-ai/lore          # via skills.sh
```

```sh
mkdir -p ~/.claude/skills/lore               # manual: from a tarball/checkout
cp -R skills/. ~/.claude/skills/lore/
```

Then **restart your coding agent** so it loads.

## Quick start

```sh
cd your-project

lore init                # create the local lore project
lore directive install   # add the agent-directive block to CLAUDE.md / AGENTS.md

# capture knowledge
lore memory add --body="The client replies faster on WhatsApp than email"
lore rule add --body="Run the tests before you push"
lore decision add --title="Payments" --body="We use Stripe for payments"

# retrieve it
lore search "whatsapp"           # search across all knowledge
lore render                      # compile knowledge → .lore/LORE.md + @import pointer in CLAUDE.md

lore tui                         # browse everything (read-only)

# share it with your team: the knowledge is plain files under .lore/data/
git add .lore/data .gitattributes && git commit -m "lore knowledge"
```

## Concepts

- **Project** — one per repo (created by `lore init`): every note is a small JSON file under `.lore/data/` that you commit with the code, so it travels with it; a local cache under `.lore/` is rebuilt from those files whenever it is missing (see [Team sync through git](#team-sync-through-git)).
- **Entities** — typed knowledge records: `memory`, `rule`, `decision`, `pattern`, `hotfix`, `snapshot`, `playbook`, `prompt`, `task`, `mission`, `plan`, `reminder`, `handoff`, `incident`, `techdoc`, and more.
- **Scope** — knowledge is scoped to the whole project (`master`) or a specific `--repo`. `add` persists `repo_id`; `list` and `search` filter by it with identical semantics (`--repo`, `--all-repos`, `--master-only`, `--no-inherit`). Real per-repo scoping — no body prefix-tag convention needed. Re-scope an existing row with `edit --rebind-repo=<mount>` / `edit --rebind-master`. Bare `--repo` on `edit` is context-only (never mutates scope; warns loudly). For audited body+scope change use `add --supersedes=<old_id> --repo=<mount>`.
- **Render** — `lore render` compiles a small **core** of the project's knowledge into a generated file (default `.lore/LORE.md`), kept inside a byte budget: must rules, critical and high hotfixes, the agent's operating rules and an index of topics. It stitches an idempotent `@import` pointer into the file your coding tool reads (`CLAUDE.md`, `AGENTS.md`, `.cursor/rules/lore.mdc`, `GEMINI.md`, … chosen with `--target`; otherwise `lore init` picks the tools of the setup that applies to the folder, else the tool files the repo already has, else `AGENTS.md`), so your hand-written content is never clobbered. Use `--no-pointer` to write only the generated file, or `--out` to change its path.
- **Agent workflow** — the agent reads the core (it is already loaded through the pointer file), runs `lore context --for-file <path>` before it touches a path (the rules, hotfixes and notes for that file and its folders), and `lore search "<keywords>"` for everything else. `lore setup --agent-hooks` makes Claude Code, Codex and Gemini CLI fetch the per-file context by themselves.

### The common verb pattern

Almost every entity supports the same sub-verbs, so once you know one you know them all:

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

`lore --help` lists everything; `lore <command> --help` shows every flag for that command. Grouped by what you're trying to do:

### Project setup & ops

| Command | Use case |
|---|---|
| `lore init [path]` | Create a new project (its rows in `.lore/data`, `.lore/LORE.md`, the tool pointer file, `.gitignore` lines). `--name`, `--non-interactive` |
| `lore setup` | Optional: set up this machine step by step (`lore setup plan`, `check`, `apply`), check every setup with `lore doctor --all`, and `--agent-hooks` installs the coding tools' session and context hooks. Nothing needs to run after an install or upgrade (the cache and search index rebuild themselves); `lore migrate run` is the one step for v0.1.17 data |
| `lore directive install` | Inject the lore agent-directive block into `CLAUDE.md` / `AGENTS.md` so agents know to use lore (`show` to print it; delete the block to remove it) |
| `lore identity` | Manage your identity: `identity:` in `~/.lore/config.yaml`, and one per org with `lore identity set --org <org>` |
| `lore whoami` | The org this repo resolves to and why, your identity, and what loads here |
| `lore config check` | Validate `~/.lore/config.yaml` (orgs, stores, patterns, durations) and say where each problem is |
| `lore config get` / `set` / `list` | Read and change keys of `~/.lore/config.yaml` (a dotted path such as `live.autopush`; keeps comments and order) |
| `lore org use` / `link` / `create` / `import` / `rename` / `set` | Choose and set up which org a repo belongs to |
| `lore cache prune` | Remove cache files of stores no repo on this machine attaches any more (lists them and asks first; `--dry-run`) |
| `lore upgrade` | Install the newest release, or the project's pinned version (`--check` only reports) |
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

The knowledge entities above (`memory` … `techdoc`) all support `list / show / edit / search / archive / unarchive` (see [the common verb pattern](#the-common-verb-pattern)); `comment` and `tag` use the narrower verb sets shown beside them. Use `lore <entity> add --supersedes <id>` for an audited body change.

### Retrieve & render

| Command | Use case |
|---|---|
| `lore search "<query>"` | **The retrieval hammer** — one search across every entity, best-first ranked. `--limit`, `--all-repos`, `--json`, `--include-archived` |
| `lore <entity> search "<q>"` | Scope search to a single entity type |
| `lore search status` | Per-entity search-index counts + health |
| `lore search rebuild` | Rebuild the search index from source rows |
| `lore context --for-file <path>` | What applies to one file before the agent touches it; also `--for-task <id>`, `--topic <t>`, `--topics`, `--for-prompt "<text>"` |
| `lore render` | Compile the core → `.lore/LORE.md` and stitch an `@import` pointer into the agent file. `--out`, `--target <profile>`, `--no-pointer`, `--dry-run`, `--check`, `--report` |
| `lore why-context` | Show the last rendered context (exactly what the agent saw) |
| `lore commit show <sha>` | Show every entity linked to a git commit |

### Project management

| Command | Use case |
|---|---|
| `lore project` | Manage projects (the top-level container) |
| `lore repo` | Manage repos within a project (per-repo scoping) |
| `lore task` | Discrete work items: `add / list / triage / someday / deferred / show / edit / start / done / cancel / search`. Two axes beyond `status`: `commitment` (`accepted`/`proposed`/`someday` — agents must set it on `add`, no default) and `deferred_until` (snooze; auto-resurfaces). Default views show only committed, active, non-deferred tasks. |
| `lore tasklist` / `lore task view` | Group tasks; saved task-list filter views |
| `lore mission` | Containers that group related tasks |
| `lore plan` | Plans |
| `lore reminder` | Time-based reminders |
| `lore workflow` / `lore workspace` | Workflow and workspace records |

### Agent loop & automation

| Command | Use case |
|---|---|
| `lore run` | Time one try at a task: `start / end / cancel`; the span lands on the task's time |
| `lore link add --commit=<sha> --entity=<id>` | Link a git commit to any entity (task/run/mission/decision/…). Also `list` / `remove` |
| `lore external-source` | Register outside sources: `add / enable / disable / list` (disabled by default) |
| `lore directive` | Install or show the agent-directive block |
| `lore skill` | Meta-tools for the lore skill bundle (`compile` — shorten the main bundle with a model) |
| `lore session` | Inspect agent sessions on this machine |
| `lore actor` | Inspect actors (humans, agents, hooks, plugins) |
| `lore behaviour` / `lore suggestion` | Behaviours and suggestions |
| `lore secrets rule` | Your own secret patterns (capture refuses secrets by default) |

### Health & recovery

| Command | Use case |
|---|---|
| `lore doctor` | Health checks (the local cache, attached stores, sync state, team versions); `--fix` repairs what it safely can |
| `lore audit verify` / `lore audit log` | Hash-chained log of every change lore makes on this machine; `verify` flags edited or deleted entries |
| `lore search rebuild` | Rebuild the search index from `.lore/data`; a damaged cache (`.lore/lore-cache.db`) is rebuilt by the next command after you delete it |
| `lore support-bundle` | Produce a sanitized incident-report bundle |
| `lore snapshot` | Point-in-time knowledge captures (logical, not file backup) |

### Sharing through git

A sync pass runs automatically before and after every command; these subcommands expose it. `lore sync`, `status`, `conflicts`, `peek` and `install-action` accept `--json`.

| Command | Use case |
|---|---|
| `lore sync` | Run a pass now: import what git changed, export what the DB changed |
| `lore sync status` | Baseline, pending exports, open conflicts, broken files, uncommitted files |
| `lore sync conflicts` / `lore sync resolve <n> --take kept\|other` | Versions kept aside when both sides edited the same row, and how to settle them |
| `lore trash list --local` | Rows removed because their file disappeared (revert, `git clean`, stash); the file comes back with `git checkout` |
| `lore sync peek <ref> [table]` | Read another branch's rows without checking it out (`origin/feature/x`) |
| `lore sync promote <id> --to <branch>` | Commit a row onto another local branch (an urgent rule onto `main`) — never pushes |
| `lore sync merge-rows <table> --keep <id> --drop <id>` | Fold a duplicate row into another: references move to the kept row, the dropped one goes to the trash |
| `lore trash purge --archived-before 90d --confirm` | Durably remove archived rows; records them in `_purged.json` so old clones never resurrect them |
| `lore sync fix-projects` | Collapse duplicate project ids after two people bootstrapped in parallel |
| `lore sync install-action` | Opt in to a GitHub workflow that settles lore-data conflicts in pull requests; runs only by hand unless `--auto`; `--branch`, `--dry-run`; delete the file to remove it |
| `lore sync ci-merge --base origin/main` | Merge a base branch in when only lore data conflicts and lore settles it all (what that workflow runs; never pushes) |

### Teams, stores and agents

Lore works for a whole team, several repos, several companies and a crew of coding agents, with git as the only infrastructure:

- **Tasks reach teammates at once, on any branch.** Live rows (notes every teammate sees at once) are shared on one hidden list on GitHub that is not a branch (git calls it a ref: `refs/lore/live`, one per repo). They land on `main` by themselves when finished, or ride along with your next push when `main` is protected.
- **Internal notes stay internal.** Notes a client must never read live in a store: a separate notes-only repo linked to the client repo, like a locked filing cabinet next to it. Leak checks stop internal text from reaching the client's commits and pull requests.
- **Several repos, one product; one person, several companies.** Repos link to one shared project; each repo loads exactly one org (one company), with its own identity, roster (the list of people and agents) and personal notes.
- **Agents do not step on each other.** `lore task next` claims a task: the agent puts its name on it, like writing your name on a whiteboard job. The claim has a lease, so it runs out after a set time unless the agent keeps saying it is alive, and it is kept on a second hidden ref (`refs/lore/signals`). A crashed agent's task is picked up when its lease runs out, and people approve gates (steps that wait for a person to say yes).

```bash
lore task add "Send the invoice reminder before Friday's demo" --assign @bob  # live: Bob sees it on his next command
lore org link agency --project client                     # once: tie a client repo to an internal project
lore memory add --body "The client replies faster on WhatsApp than email"  # in a client repo: goes to the store, not the repo
lore task next --mission msn_01a1…                        # an agent takes the next ready task
lore hold claim full-suite --ttl 30m                      # one heavy test run at a time (lore hold release full-suite)
```

Read on in [docs/team](docs/team/README.md): [overview](docs/team/overview.md), [concepts](docs/team/concepts.md), [getting started](docs/team/getting-started.md), [use cases](docs/team/README.md#use-cases) (small team, agency and client repo, group of repos, contractor, two companies, personal notes, agents on one mission), [how it works](docs/team/how-it-works.md), [agents](docs/team/agents.md), [commands reference](docs/team/commands.md) and [troubleshooting](docs/team/troubleshooting.md). The design and every rule: [LORE_TEAM_SPEC.md](LORE_TEAM_SPEC.md).

## Team sync through git

<!-- ds:block id=sync-datanames-dgnn2vn8 -->
lore keeps a fast local cache in `.lore/lore-cache.db` (gitignored) and writes every shared row as one canonical JSON file under `.lore/data/<table>/<id>.json`, which you commit. Git then does everything else: branches, merges, history, review, access control.

```
.lore/
  lore-cache.db                    gitignored — local cache, rebuilt from data/ any time
  data/                            COMMITTED — the shared knowledge
    _meta.json                     pins the project id for every clone
    rules/rul_0192….json
    memories/mem_0192….json
    tasks/tsk_0192….json
  LORE.md                          committed — generated agent context
```

**How it flows.** `lore rule add …` writes the row and its file in the same command. You commit the file with your code; when the PR merges, the rule is on `main`. A teammate's `git pull` brings the file; their next lore command (or the post-merge hook) imports it into their cache. Switching branches switches the knowledge: rows that exist only on another branch disappear from the cache and come back when you switch back.

<!-- ds:block id=sync-syncedtables-fa4jung7 -->
<!-- ds:block id=sync-localtables-ukz59e32 -->
<!-- ds:block id=sync-volatile-f7q2cp2w -->
**What syncs.** Knowledge and work tracking (rules, decisions, memories, patterns, hotfixes, playbooks, prompts, tasks, missions, plans, tags, comments, …). Machine-local data stays local: runs, the audit trail, the search index and usage counters.

<!-- ds:block id=sync-mergedriver-config-cthptudb -->
<!-- ds:block id=sync-strategyfor-db9rgsqn -->
**Merges.** One file per row means two people adding knowledge never conflict. Editing *different fields* of the same row merges cleanly through lore's git merge driver; editing the *same text* on two branches is a real conflict that a person settles. List fields that are sets (labels, approvers, team members, project links) merge both sides' additions and never conflict, and an edit beats a delete. Details, and what happens in pull requests, in [Merges and pull requests](#merges-and-pull-requests).

**The files are the truth.** Every lore command writes the row file and updates the cache in the same step; `.lore/lore-cache.db` is only a cache, rebuilt from `.lore/data` at any time, so never edit it by hand. A row whose file disappears goes to `lore trash list --local` first, and its file is still in git, so a `git clean` or a stash is always recoverable.

**Existing projects.** A repo with v0.1.17 data is converted once with `lore migrate run`: it keeps a local backup ref, and `lore migrate rollback` undoes it. `--dry-run` lists what it would change first.

**Fresh clones.** `git clone` brings `.lore/data` but no database; the first lore command (or `lore init`) builds the local cache from the files.

**Secrets.** A row matching a credential pattern is never written to `.lore/data` (it stays local and is reported); the `commit-msg` hook block re-scans the staged files.

<!-- ds:block id=sync-env-pe9vvf3f -->
**Switches.** `LORE_SYNC=0` turns live rows off (like `LORE_LIVE=0`) and stops lore updating the version pin in `.github/workflows/lore-sync-merge.yml`; rows are still written to `.lore/data`, because since 0.1.18 the files are where lore keeps them. `LORE_SYNC_GIT=0` keeps the files but never touches git config, `.gitattributes` or hooks. `LORE_SYNC_QUIET=1` silences progress lines. `LORE_SYNC_ALLOW_SECRETS=1` is ignored since 0.1.18 (a warning says so).

<!-- ds:block id=sync-hooklines-g87bgvsj -->
**Hooks.** lore chains a small marked block into the hooks directory git already uses (`.git/hooks`, or the one a hook manager such as husky owns via `core.hooksPath`); it never repoints `core.hooksPath` and never replaces your scripts. Every block is a no-op when lore is not installed, and the pre-commit block skips with an "upgrade it" note on a lore too old to have `sync hook`, so committed hooks do not block a teammate who has not upgraded; the one exception is a lore older than 0.1.18 when `.lore/data` already holds 0.1.18 rows, which the pre-commit block refuses with an "install lore 0.1.18 or newer" note, because that lore cannot read them; and in a client repo (one a lore check has marked with `git config lore.clientrepo true`) the `commit-msg` block refuses the commit when no lore on `PATH` can check it for internal notes, instead of letting it through unchecked. The blocks are POSIX `sh`; on Windows, Git for Windows runs hooks and merge drivers through its bundled shell, so `lore.exe` only needs to be on `PATH`. Several lore projects in one repo (a monorepo) each get their own block. The `commit-msg` block adds a `Lore-Task:` trailer for the task you hold (so a squash merge is still recognised as finishing it) and runs the leak check (internal text pasted into a client repo is refused, see `lore check leaks`). In a client repo the `pre-push` block runs the same leak check on every commit the push sends, so a `git commit --no-verify` is still caught (`git push --no-verify` skips every local check). The `post-commit` and `pre-push` blocks carry finished live rows onto a protected `main` (a `lore: land N rows` commit that rides along with your push); they are one block per repo and skip quietly on a lore too old to have `lore land`.

### Merges and pull requests

**What a merge driver is, and why lore has one.** When two branches change the same file, git combines them line by line, as plain text. A row file is JSON, and every edit also changes its `updated_at` line, so two people editing *different* fields of the same row (one the body, one the status) would always collide on that line and leave broken JSON to fix by hand. A merge driver is a program git calls instead for files `.gitattributes` assigns to it. lore's driver (`lore merge-driver`, assigned to `.lore/data/**/*.json`) reads the original, ours and theirs as JSON and merges them field by field:

| Field changed | Result |
|---|---|
| on one side only | that side's value |
| on both sides, to the same value | that value |
| `updated_at` | the later of the two |
| a choice, number, yes/no, date or reference (status, priority, severity, due date, `*_id`), on both sides differently | the side with the newer `updated_at`; the merge prints which fields that decided |
| body, title, any other text or JSON value, on both sides differently | a real conflict: markers go inside that one value, everything else still merges |

A second driver, `lore-keep-ours`, handles `.lore/LORE.md`: it keeps your copy, and lore re-renders the file from the merged data on the next command. git only runs a driver the clone has configured; lore configures both in each clone automatically (`.git/config`), the same way it installs its hooks. In a folder that is not a git repository there is nothing to configure, and lore leaves git alone.

**Settling a real conflict.** `lore <entity> show <id>` displays both versions; pick one (or combine them) with `lore <entity> edit <id> --body "…"`, which rewrites the file without markers, then commit. The pre-commit hook refuses to commit a file that still has markers. Markers that break the JSON itself only appear when a merge ran where lore's driver was not configured: abort it, run any lore command (which configures the driver), and merge again.

**Pull requests.** Most pull requests never need the driver: new rules, memories and tasks are new files with unique ids, and editing a row nobody else touched is an ordinary change, so GitHub merges them like any other file. When the same row was edited on the branch *and* on `main`, GitHub reports a conflict, because its merge button never runs custom drivers. Two ways to clear it:

1. **Locally** (always works): on the branch, `git pull origin main`. lore's driver merges the row there; if both sides changed the same text, settle it as above. Push, and the pull request merges normally.
2. **Automatically**, with the optional workflow:

```bash
lore sync install-action            # writes .github/workflows/lore-sync-merge.yml (runs only when started from the Actions tab) — commit it
lore sync install-action --auto     # same, but it also runs by itself when lore data changes (spends Actions time)
lore sync install-action --dry-run  # print it, write nothing
rm .github/workflows/lore-sync-merge.yml   # remove it (commit the deletion)
```

Installed with `--auto`, the workflow runs when lore data changes on a pull request or on the default branch (`--branch` to choose others); without it, it runs only when started by hand. Re-running `install-action` without `--auto` or `--manual` keeps the triggers the file already has, and the automatic version updates below never change them. For each open pull request it runs `lore sync ci-merge --base origin/<base>`, which merges the base branch in with lore's driver and commits, but only when GitHub would report a conflict, every conflict is in `.lore/data` or `.lore/LORE.md`, and the driver settles all of them; then it pushes that merge to the pull request branch. A code conflict, or the same text edited on both sides, is left alone and listed in the run summary for a person. Things to know:

- It is opt-in, and runs only by hand unless installed with `--auto`, because it uses Actions time, which is billed on private repositories. Automatic runs happen only when lore data changes, and usually finish in under a minute.
- It downloads a pinned lore release (checksum-verified) and keeps that pin current by itself: when a developer runs a newer lore release in the repository, their next lore command rewrites the workflow to that release and prints `updated .github/workflows/lore-sync-merge.yml to lore vX — commit it`. It never downgrades (a teammate on an older lore leaves it alone), never acts from a development build, and never touches a file edited by hand (a checksum line in the file detects that; re-run `install-action` to take it over again). To freeze the version instead, install with `--lore-version vX.Y.Z` (or `latest`), which is never changed.
- A lore project in a subfolder gets its own file, `lore-sync-merge-<folder>.yml`, so several projects in one repository never overwrite each other.
- Pushes made with the default `GITHUB_TOKEN` do not start other workflows, so required checks will not re-run on its merge commit. Add a repository secret `LORE_SYNC_TOKEN` (a token with contents write access) and the workflow uses it instead.
- Pull requests from forks are skipped: no token in the base repository can push to a fork.
- `lore sync ci-merge --base origin/main --dry-run` shows locally what the workflow would do for the current branch.

**Other hosts.** Nothing here is GitHub-specific except the optional workflow: the driver, hooks and attributes are plain git, so GitLab, Bitbucket and self-hosted remotes behave the same, including their web merge buttons ignoring custom drivers (merge locally, or run `lore sync ci-merge` in that host's CI).

The full design, every edge case and the measured numbers are in [LORE_SYNC_SPEC.md](LORE_SYNC_SPEC.md).

## Common flags

These work across most commands:

| Flag | Meaning |
|---|---|
| `--json` | Machine-readable JSON envelope (use this from scripts/agents) |
| `--repo <mount\|rep_id>` | Scope to one repo instead of project-master |
| `--project <id\|name>` | Operate on a specific project |
| `--db <path>` | Refused on row commands (`E_UNSUPPORTED`): their rows are files in the repository, so run the command inside it (`LORE_DB` is refused the same way) |
| `--read-only` | Skip lock acquisition; refuse writes (safe for inspection) |
| `--archived` (lists) / `--include-archived` (search) | Include archived rows |
| `--color auto\|always\|never` | Colour on every command; `auto` (default) colours only a terminal and follows `NO_COLOR` / `FORCE_COLOR` |
| `--trace` | Print every git command lore runs for live rows and every fetch, push and ls-remote to a store, with timing and the host reached (`LORE_TRACE=1`) |

Env: `LORE_PROJECT_ID`, `LORE_REPO`, `LORE_READ_ONLY=1`, `LORE_TRACE=1` mirror the flags.

Safety: lore refuses to run as root (`LORE_ALLOW_ROOT=1` overrides) and refuses a project or DB inside iCloud/Dropbox/OneDrive/Google Drive or on NFS/SMB, where the local cache can break silently (`LORE_ALLOW_NETWORK_FS=1` overrides — share knowledge through git instead).

## Interactive TUI

`lore tui` is a read-only browser in the terminal: a screen of every table with its row count, then one page of rows per table (type `/text` to filter, `n`/`p` to page), then every field of one row (sensitive values masked). `lore tui --kind decision` opens on one table first. It reads every attached store and changes nothing; add and edit rows with the `lore <kind> add|edit` commands. The full-screen editing TUI of v0.1.17 is gone in 0.1.18.

## FAQ

**Which coding agents does lore support?** Any agent that reads a project-instructions file. lore renders `CLAUDE.md`, `AGENTS.md` (Cursor, Codex, and others), or `.cursorrules` via `lore render --target`. Works alongside Cursor, Windsurf, Cline, GitHub Copilot, Codex and other coding agents.

**Does lore send my code or knowledge to the cloud?** No. Everything lives under `.lore/` in your repo: a local cache plus JSON files. No account, no network calls, no telemetry — knowledge only leaves your machine when you `git push` it yourself.

**Does it use a model, embeddings, or an API key?** No. Search is local full-text search (it matches the words you type) — fast, always the same answer, and free. There's no vector database and no embedding bill.

**How is this different from just writing `CLAUDE.md` by hand?** lore keeps knowledge structured (rules vs decisions vs hotfixes, with severity), deduplicated, scoped per-repo, searchable, and versioned — then regenerates `.lore/LORE.md` deterministically and `@import`s it from `CLAUDE.md`, leaving your hand-written content untouched. Hand-written instruction files rot, contradict each other, and quietly blow your token budget.

**Won't a large memory bloat my context window?** No. The hybrid render pins only `must`-severity rules and critical hotfixes into the file; everything else surfaces on demand via `lore search`. Context stays small even as the knowledge base grows.

**Can my team share project memory?** Yes — through git. Every shared row lives as a JSON file under `.lore/data/`; commit it with your code and it follows branches, merges with PRs, and reaches teammates on `git pull`. Each developer's local cache (`.lore/lore-cache.db`) is rebuilt from those files. See [Team sync through git](#team-sync-through-git).

**What happens when two people edit the same rule on different branches?** Different fields merge automatically through lore's merge driver. The same text edited twice is a real conflict: git stops, the conflict markers sit inside the JSON value, and lore shows both versions but will not let you commit the file until you pick one with `lore <entity> edit`.

**I accidentally ran `git clean` / reverted a commit — is the knowledge gone?** No. Rows whose file disappears are copied to `lore trash list --local` before they leave the cache, and the file itself is still in git history: `git checkout HEAD -- <file>` (or the commit before the one that deleted it) brings it back.

**Is capture and recall really automatic?** With the bundled lore skill, the agent captures decisions, rules, and corrections and recalls relevant knowledge on its own. You can also drive everything by hand with the CLI.

**Is lore free and open source?** Yes — free and open source under the Apache License 2.0 from 0.1.18, with no seats or usage tiers. See [License](#license).

## Building from source / contributing

See **[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)** (build, release, Homebrew tap) and **[CONTRIBUTING.md](CONTRIBUTING.md)**.

## License

lore is licensed under the [Apache License 2.0](LICENSE) from release 0.1.18 (see [NOTICE](NOTICE)). Releases v0.1.17 and earlier remain under the PolyForm Perimeter License 1.0.0, the license they were published under.

<sub><strong>lore</strong> — open-source, local-first memory &amp; context management for coding agents. Persistent <code>CLAUDE.md</code> / <code>AGENTS.md</code> memory for Cursor, Windsurf, Cline, GitHub Copilot, Codex and other coding agents. No cloud, no API keys, no embeddings.</sub>
