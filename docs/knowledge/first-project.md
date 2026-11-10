# Your first project

**In short:** lore keeps the notes your coding tools need, such as "Run the tests before you push", inside your repo. You set it up once with `lore init`, write a rule, and run `lore render`. Your coding tool then reads that rule every time it starts.

## What it is and when you want it

A coding tool (an editor assistant or an agent that writes code for you) starts every session knowing nothing about your project. It does not know that you always run the tests first, or that the client wants buttons to be blue. You end up typing the same reminders again and again.

lore is a small notebook for those reminders. Each note is a *row* (one line in a small database, like one card in a card box). lore saves the rows in your repo, so they travel with the code, and it writes the important ones into a file the tool reads when it starts. No tool is the default: lore points the tools your setup picked at it (see `lore setup`), else the tools your repo already uses (`CLAUDE.md`, `GEMINI.md`, a Cursor rule, ...), and `AGENTS.md`, the file several tools read, when it finds none.

This page is for one person and one repo, with no team set up. Alice (an agency developer) works on the client's website, `github.com/client/website`.

## Before you start

- lore installed: see [Install](../../README.md#install). Check it with `lore --version`.
- git installed, and a repo that you can commit to. A remote (a copy on GitHub) is useful but not needed for this page.

<!-- example: start#version -->
```console
# alice, in website
$ lore --version
lore version dev
```
<!-- /example -->

`lore version --json` prints the same version with the schema numbers a script or a bug report needs (`binary_version` is `dev` for a build from source; a release prints its tag):

<!-- example: start#version-json -->
```console
# alice, in website
$ lore version --json
{
  "schema_version": 1,
  "binary_version": "dev",
  "db_schema_version": 1,
  "bundle_format_version": 1,
  "plugin_protocol_version": 1,
  "mcp_tool_version": 1
}
```
<!-- /example -->

## Step 1 — Set up lore in the repo

Run `lore init` once in the top folder of the repo. It creates a `.lore/data` folder that git tracks and a local cache built from it. `--name` is the project's name (without it, lore takes the name from the git remote or the folder), and `--non-interactive` skips the questions lore would otherwise ask.

<!-- example: start#init -->
```console
# alice, in website
$ lore init --non-interactive --name=website
lore initialized
    project_id: prj_7a67…9504
    name:       website
    repo_id:    rep_2ad3…f891
· context targets: codex (AGENTS.md) (no AI tool file found; AGENTS.md is the cross-tool file, change with --target <profile>)
✓ ~/work/website/AGENTS.md (pointer → .lore/LORE.md)
· saved as context.targets; change with: lore policy set context.targets <profiles> --yes
  next: lore render            to write .lore/LORE.md for those tools
$ git status --short
?? .gitattributes
?? .gitignore
?? .lore/
?? AGENTS.md
```
<!-- /example -->

The new files are plain text. The local cache (`.lore/lore-cache.db`) stays on your machine and is ignored by git. `.lore/data` holds one small file per row, and that is what you commit.

## Step 2 — Nothing else to set up

There is no setup step. lore builds its local cache (`.lore/lore-cache.db`, the search index included) from the files in `.lore/data` whenever it needs to, and keeps it in step after you upgrade lore, so a new version needs no command either. Two optional extras: `lore setup --agent-hooks` installs session and context hooks for the coding tools set up here, and `lore migrate run` is the one step for notes written by lore v0.1.17.

Plain `lore setup` opens the guided setup page (your setups, identity, GitHub, stores, tools and folders; see [Guided setup](../team/guided-setup.md)); you do not need it for one repo. `lore setup --schema-only` is the one step `lore upgrade` runs after it swaps the binary: it brings the local cache's schema up to date and prints nothing when there is nothing to do. You can run it yourself after installing a new version by hand:

<!-- example: start#schema-only -->
```console
# alice, in website
$ lore setup --schema-only
```
<!-- /example -->

## Step 3 — Write your first rule

A *rule* is a hard "always" or "never", like a sign on the office wall. Give the text with `--body`. lore prints the new row's id (a short name starting with `rul_`) and reminds you that the new file is not committed yet.

<!-- example: start#first-rule -->
```console
# alice, in website
$ lore rule add --body 'Run the tests before you push'
✓ rul_162d…9666
  scope: [master] severity: must
$ lore rule list
rul_162d…9666 [must/always] Run the tests before you push
```
<!-- /example -->

`[must/always]` means: the rule must be followed (*severity*), and it is shown in every session (*activation*). See [Rules, memories and decisions](rules-memories-decisions.md) for the other choices.

## Step 4 — Render it for your coding tool

`lore render` writes your rules into `.lore/LORE.md`. This repo had no coding tool file, so `lore init` already put a short pointer at the top of `AGENTS.md`: one sentence telling the tool to read `.lore/LORE.md`. In a repo with a `CLAUDE.md` (or another tool's file) the pointer goes there instead, as an import line for tools that follow imports. Anything you wrote in that file yourself stays as it was; see [Render and agent files](render-and-agent-files.md) for the full list of tools.

<!-- example: start#render -->
```console
# alice, in website
$ lore render
✓ .lore/LORE.md (2554 bytes, budget 16000 codex; 1 full + 0 line rules, 0 + 0 hotfixes, 1 topics)
✓ .lore/LORE.full.md (344 bytes, every pinned entry in full; not imported)
= ~/work/website/AGENTS.md (pointer present)
$ cat AGENTS.md
<!-- lore:pointer:start -->
Read `.lore/LORE.md` (at the repository root) before answering: it holds this project's rules and pinned warnings, generated by `lore render`.
<!-- lore:pointer:end -->
```
<!-- /example -->

`1R + 0HF` means one rule and zero hotfixes went into the file. A *hotfix* is a short-lived warning about a known problem, like a "wet floor" sign.

## Step 5 — Commit

Commit the rows, the rendered file and the pointer like any other change. Anyone who clones the repo gets the same rules.

<!-- example: start#commit -->
```console
# alice, in website
$ git add -A && git commit -qm 'lore: first rule' && git log --oneline
601d285 lore: first rule
86dbae4 init
```
<!-- /example -->

## Try it: check that everything is healthy

`lore doctor` checks the database, the git setup and who lore thinks you are. It ends with code 0 when healthy, 1 when something needs attention, and 2 when something is broken, so a script can check the result.

<!-- example: start#doctor -->
```console
# alice, in website
$ lore doctor
Status: HEALTHY
Binary: dev
[db]
  ok:       true
  wal_size: 0 bytes
[sync]
  in git repo:      true
  tracked files:    6
  open conflicts:   0
  file errors:      0
  merge driver:     true
  data gitignored:  false
  uncommitted:      0
[identity]
  resolved: human:alice@agency.example
  source:   git config user.email
  stable:   yes
Notes:
  - live: tracking refspec installed, not forced into refs/lore/*
  - live: last fetch 40s ago
  - live: post-commit and pre-push hook blocks installed
  - Team versions (newest save per writer and machine):
  -   alice on (code repo, no machine id): lore dev (last save 2026-11-02)
  -   alice on devbox: lore dev (last save 2026-11-02)
Context:
  - context: .lore/LORE.md 2554 of 16000 bytes (15%, profile codex); warn at 80%
```
<!-- /example -->

Two more small commands help you look around. `lore sql` runs one read-only query over your notes (here: how many rules and memories there are), and `lore completion bash` (or `zsh`, `fish`, `powershell`) prints a script that makes the Tab key complete lore commands:

<!-- example: start#sql -->
```console
# alice, in website
$ lore sql "SELECT 'rules' AS kind, count(*) AS n FROM rules UNION ALL SELECT 'memories', count(*) FROM memories"
kind      n
rules     1
memories  0
(2 rows)
```
<!-- /example -->

`lore sql` reads every table of the repo, one row kind per table (`rules`, `memories`, `decisions`, `tasks` and the rest), with sensitive values masked. `--format csv` or `--format json` gives output a script can read, `--include-deleted` also reads rows in the trash, and `--timeout` stops a slow query (10 seconds by default). It never writes: anything but one `SELECT`, `WITH` or `VALUES` statement is refused with `E_SQL_REFUSED`, because a change must go through a lore command so it reaches git. In a repo with stores, each `<table>_all` view (`memories_all`, `rules_all` and the rest) starts with a `source` column naming the store; two `.lore/stores.yaml` entries on one place keep both names there, so a query over both sees each row twice (see [Your stores › Pitfalls](../sync/stores.md#pitfalls)).

<!-- example: start#sql-more -->
```console
# alice, in website
$ lore sql 'SELECT id, body, severity FROM rules' --format json
[
  {"id": "rul_162d…9666", "body": "Run the tests before you push", "severity": "must"}
]
$ lore sql 'DELETE FROM rules'
ERROR: [E_SQL_REFUSED] only one SELECT, WITH or VALUES statement (optionally after EXPLAIN [QUERY PLAN]) is allowed
  hint: lore sql is read-only; change data with lore commands so it reaches git
(exit 20)
```
<!-- /example -->

<!-- example: start#completion -->
```console
# alice, in website
$ lore completion bash | head -3
# bash completion V2 for lore                                 -*- shell-script -*-
__lore_debug()
```
<!-- /example -->

To turn completion on for every new shell, add `source <(lore completion bash)` to your `~/.bashrc`.

## Pitfalls

- **Run lore in the top folder of the repo.** In a folder without `.lore/lore.db`, every command stops with `E_NOT_PROJECT_ROOT` and the hint "run `lore init` here, or cd into a project root".
- **Render after you change rules.** The coding tool reads `.lore/LORE.md`, not the database. A new rule shows up only after the next `lore render`. Running it twice is safe: the second run says `(unchanged)` and `(pointer present)`.
- <!-- ds:block id=render-pins-buj9dujv --> **Only the strongest notes are pinned.** `lore render` pins `must` rules and `critical` or `high` hotfixes. Memories, decisions and `should` rules are found with `lore search` when the tool needs them. See [Render](render-and-agent-files.md).
- **Commit `.lore/data`.** lore tells you after each change how many files are not committed. If you never commit them, your teammates and your next clone never see the notes.

## Related

- [Rules, memories and decisions](rules-memories-decisions.md): the three kinds of note you will use most.
- [Bring in notes you already have](import-existing-notes.md): turn an existing `CLAUDE.md` or notes folder into rows.
- [Render and agent files](render-and-agent-files.md): `AGENTS.md`, other output paths, the directive block.
- [Search](search.md): find any note by its words.
- [Getting started with a team](../team/getting-started.md): what to do when more people join (shared lists, orgs and private stores).
- [Knowledge guides index](README.md)
