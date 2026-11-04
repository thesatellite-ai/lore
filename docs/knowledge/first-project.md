# Your first project

**In short:** lore keeps the notes your coding tools need, such as "Run the tests before you push", inside your repo. You set it up once with `lore init`, write a rule, and run `lore render`. Your coding tool then reads that rule every time it starts.

## What it is and when you want it

A coding tool (an editor assistant or an agent that writes code for you) starts every session knowing nothing about your project. It does not know that you always run the tests first, or that the client wants buttons to be blue. You end up typing the same reminders again and again.

lore is a small notebook for those reminders. Each note is a *row* (one line in a small database, like one card in a card box). lore saves the rows in your repo, so they travel with the code, and it writes the important ones into a file the tool reads when it starts. That file is `CLAUDE.md` by default; `AGENTS.md` and others work too.

This page is for one person and one repo, with no team set up. Alice (an agency developer) works on the client's website, `github.com/client/website`.

## Before you start

- lore installed: see [Install](../../README.md#install). Check it with `lore --version`.
- git installed, and a repo that you can commit to. A remote (a copy on GitHub) is useful but not needed for this page.

<!-- example: start#version -->
```console
# alice, in website
$ lore --version
lore version 0.1.0-dev
```
<!-- /example -->

## Step 1 — Set up lore in the repo

Run `lore init` once in the top folder of the repo. It creates the local database and a `.lore/data` folder that git tracks. `--name` is the project's name (without it, lore takes the name from the git remote or the folder), and `--non-interactive` skips the questions lore would otherwise ask.

<!-- example: start#init -->
```console
# alice, in website
$ lore init --non-interactive --name=website
✓ lore initialized at ~/work/website
    project_id: prj_7a67…9504
    name:       website
    origin:     https://github.com/client/website
    db:         ~/work/website/.lore/lore.db
    identity:   human:alice@agency.example (git config)
  next: lore learn-from docs   to bootstrap from existing markdown
lore sync: exported 0 rows to .lore/data — review and commit it
$ git status --short
?? .gitattributes
?? .gitignore
?? .lore/
```
<!-- /example -->

The new files are plain text. `.lore/lore.db` (the database) stays on your machine and is ignored by git. `.lore/data` holds one small file per row, and that is what you commit.

## Step 2 — Run setup once

`lore setup` builds the search index. Run it after you install or upgrade lore. It is safe to run again.

<!-- example: start#setup -->
```console
# alice, in website
$ lore setup
lore sync: imported 2
→ running ent schema migration...
→ building FTS5 search index...
✓ setup complete (registry fingerprint: 601d2850ffd047c2)
```
<!-- /example -->

## Step 3 — Write your first rule

A *rule* is a hard "always" or "never", like a sign on the office wall. Give the text with `--body`. lore prints the new row's id (a short name starting with `rul_`) and reminds you that the new file is not committed yet.

<!-- example: start#first-rule -->
```console
# alice, in website
$ lore rule add --body 'Run the tests before you push'
✓ rul_2ad3…f891 
  scope: [master] severity: must
$ lore rule list
rul_2ad3…f891 [must/always] Run the tests before you push
```
<!-- /example -->

`[must/always]` means: the rule must be followed (*severity*), and it is shown in every session (*activation*). See [Rules, memories and decisions](rules-memories-decisions.md) for the other choices.

## Step 4 — Render it for your coding tool

`lore render` writes your rules into `.lore/LORE.md` and adds a short pointer at the top of `CLAUDE.md`. The pointer is one import line: the tool opens `CLAUDE.md`, sees `@.lore/LORE.md`, and reads that file too. Anything you wrote in `CLAUDE.md` yourself stays as it was.

<!-- example: start#render -->
```console
# alice, in website
$ lore render
✓ .lore/LORE.md (12293 bytes; 1R + 0HF)
✓ ~/work/website/CLAUDE.md (pointer → .lore/LORE.md)
$ cat CLAUDE.md
<!-- lore:pointer:start -->
@.lore/LORE.md
<!-- if your agent does not support @import, read .lore/LORE.md now -->
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
86dbae4 lore: first rule
9b40ce7 init
```
<!-- /example -->

## Try it: check that everything is healthy

`lore doctor` checks the database, the git setup and who lore thinks you are. It ends with code 0 when healthy, 1 when something needs attention, and 2 when something is broken, so a script can check the result.

<!-- example: start#doctor -->
```console
# alice, in website
$ lore doctor
Status: HEALTHY
Binary: 0.1.0-dev
[db]
  ok:       true
  wal_size: 4152 bytes
[sync]
  baselined:        true
  pending exports:  0
  open conflicts:   0
  file errors:      0
  merge driver:     true
  uncommitted:      0
[identity]
  resolved: human:alice@agency.example
  source:   git config
  stable:   yes
Notes:
  - live: tracking refspec installed, not forced into refs/lore/*
  - live: last fetch 40s ago
  - live: post-commit and pre-push hook blocks installed
```
<!-- /example -->

Two more small commands help you look around. `lore tables` counts the rows in every table, and `lore completion bash` (or `zsh`, `fish`, `powershell`) prints a script that makes the Tab key complete lore commands:

<!-- example: start#tables lines=8 -->
```console
# alice, in website
$ lore tables
TABLE                 ROWS
_lore_live_rows       0
_lore_sync_conflicts  0
_lore_sync_dirty      0
_lore_sync_errors     0
_lore_sync_extra      3
_lore_sync_files      3
_lore_sync_meta       6
…
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
- <!-- ds:block id=render-pins-ez0g6hen --> **Only the strongest notes are pinned.** `lore render` pins `must` rules and `critical` or `high` hotfixes. Memories, decisions and `should` rules are found with `lore search` when the tool needs them. See [Render](render-and-agent-files.md).
- **Commit `.lore/data`.** lore tells you after each change how many files are not committed. If you never commit them, your teammates and your next clone never see the notes.

## Related

- [Rules, memories and decisions](rules-memories-decisions.md): the three kinds of note you will use most.
- [Bring in notes you already have](import-existing-notes.md): turn an existing `CLAUDE.md` or notes folder into rows.
- [Render and agent files](render-and-agent-files.md): `AGENTS.md`, other output paths, the directive block.
- [Search](search.md): find any note by its words.
- [Getting started with a team](../team/getting-started.md): what to do when more people join (shared lists, orgs and private stores).
- [Knowledge guides index](README.md)
