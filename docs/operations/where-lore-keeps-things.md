# Where lore keeps things: `lore where` and lore's home

**In short:** lore keeps its own files in one folder per machine (`~/.lore`), in a `.lore/` folder in each code repo, in a few marked blocks of files it shares with you (`.gitignore`, `.gitattributes`, `CLAUDE.md`, git hooks) and in a few git config entries. `lore where` (also spelled `lore files`) lists every one of them on this machine and in the repo you run it in, one line each, with what it is. `~/.lore/README.md` explains the home folder in plain words. Nothing is hidden: when lore writes into your repo or its git config for the first time it says what and where.

## What it is and when you want it

You opened `~/.lore` and want to know what a file is, whether you may delete it, or which store a cache file belongs to. Or you want to know what lore changed in a repo before you commit. Or a teammate asks what lore installs on their machine. `lore where` answers all three from what is actually on disk, not from a list in a document.

## List everything

Run it anywhere; inside a repo it adds that repo's files:

<!-- example: files#lore-where -->
```console
# alice, in website
$ lore where
lore's home: ~/.lore
  README.md
      this map of lore's home, written by lore and rewritten when the layout changes
  cache
      rebuildable caches: everything here is made again from the row files when deleted
  cache/machine-store.db
      the cache of state/machine.db, arranged for search
  cache/repo
      one cache per code repo checkout, of the rows in its .lore/data: repo/<repo folder name>-<short hash of the repo path>.db (kept here so a command never creates a file in the repo)
  cache/repo/website-<hash>.db
      a code repo's cache; used by ~/work/website
  cache/stores
      one cache file per store folder: stores/<store id>/<branch>/<folder>.db (`lore where` names the store and the repos that use each)
  cache/stores/sto_7a67…9504/main/root.db
      a store folder's cache; store notes; used by ~/work/website
  cache/uses
      one record per repo of the cache files it uses: uses/<hash of the repo path>.json (`lore cache prune` keeps what a repo still uses)
  cache/uses/05513aa29b7881f12bc94eb06be8acf0d03eaed2c5b80873f8e40d2805d7e0ce.json
      a repo's cache use record
  machine.json
      this machine's id, kept so it stays the same between runs
  state
      machine-local state: never synced, and not rebuildable
  state/audit
      the audit log of writes, one .jsonl file per store
  state/machine.db
      the machine store: rows that only mean something on this machine (agent sessions, runs), the audit chain, doctor results and small local state (usage counts, secret-check times, unsent time entries)
  stores
      the stores lore keeps on this machine: stores/<owner>/<repo>/ is a plain git clone of each store (rows are JSON files you may open and edit; its .git/config holds lore's merge driver and your git identity for that store, user.name and user.email, so your own commits there carry it), stores/_me/ the personal store when it is a plain folder (the _ keeps it apart from any owner folder); `lore where` lists each one
store clones:
  ~/.lore/stores/acme/notes (store git@github.com:acme/notes)
      a git clone of store git@github.com:acme/notes; rows are JSON files at ~/.lore/stores/acme/notes/<folder>/<table>/<id>.json (the store's own rows: ~/.lore/stores/acme/notes/_store/<table>/<id>.json); README.md at its top says how to browse and edit it; .0-needs-lore-0.1.18-or-newer there is for lore 0.1.17, which names it when it refuses the store (it says to upgrade)
  ~/.lore/stores/acme/notes/.git/lore (store git@github.com:acme/notes)
      lore's own files for this clone: never committed, never in the files you browse
  ~/.lore/stores/acme/notes/.git/lore/fetch-stamp (store git@github.com:acme/notes)
      when lore last fetched the remote
  ~/.lore/stores/acme/notes/.git/lore/identity (store git@github.com:acme/notes)
      the user.email lore wrote into this clone's .git/config, so lore keeps only its own value current and never changes one you set
  ~/.lore/stores/acme/notes/.git/lore/lock (store git@github.com:acme/notes)
      the store lock (holds no data)
  ~/.lore/stores/acme/notes/.git/lore/remote-tip (store git@github.com:acme/notes)
      the store branch's tip on the remote when lore last saw it, so a store repo deleted and made again is noticed, never refilled
  ~/.lore/stores/acme/notes/.git/lore/state (store git@github.com:acme/notes)
      the store lock's journal of audit records not yet acknowledged
  ~/.lore/stores/acme/notes/.git/config [user.name] (store git@github.com:acme/notes)
      who your own git commits in this store clone are by (alice): your identity for this store, set by lore when it cloned or first opened the clone (`lore doctor` checks it)
  ~/.lore/stores/acme/notes/.git/config [user.email] (store git@github.com:acme/notes)
      who your own git commits in this store clone are by (alice@agency.example): your identity for this store, set by lore when it cloned or first opened the clone (`lore doctor` checks it)
this repo: ~/work/website
  .lore
      lore's folder in this repo
  .lore/data
      this repo's rows, one JSON file per row (.lore/data/<table>/<id>.json) and _meta.json (the project id and format: the lore 0.1.18 format (format 2)); committed
  .lore/lock
      the lock lore takes while it writes rows here; not committed
  .lore/state
      local state of this checkout (the lock's audit journal, a migration's journal); not committed
  .lore/stores.yaml
      the stores this repo attaches (committed)
  AGENTS.md
      lore's marked block pointing codex at .lore/LORE.md; the rest of the file is yours
  .gitignore
      lore's lines keeping its cache, lock and state out of git, and the rows in
  .gitattributes
      lore's block: row files merge with lore's merge driver and show collapsed in diffs
  .git/config [remote.origin.fetch]
      fetches lore's live refs with every git fetch
  .git/config [merge.lore.name]
      the merge driver git runs for lore's row files (lore merges them field by field)
  .git/config [merge.lore.driver]
      the merge driver git runs for lore's row files (lore merges them field by field)
  .git/config [merge.lore-keep-ours.name]
      the merge driver git runs for lore's row files (lore merges them field by field)
  .git/config [merge.lore-keep-ours.driver]
      the merge driver git runs for lore's row files (lore merges them field by field)
  .git/hooks/commit-msg
      lore's marked block in the commit-msg hook (checks rows and secrets on commit, keeps the live ref in step)
  .git/hooks/post-checkout
      lore's marked block in the post-checkout hook (checks rows and secrets on commit, keeps the live ref in step)
  .git/hooks/post-commit
      lore's marked block in the post-commit hook (checks rows and secrets on commit, keeps the live ref in step)
  .git/hooks/post-merge
      lore's marked block in the post-merge hook (checks rows and secrets on commit, keeps the live ref in step)
  .git/hooks/post-rewrite
      lore's marked block in the post-rewrite hook (checks rows and secrets on commit, keeps the live ref in step)
  .git/hooks/pre-commit
      lore's marked block in the pre-commit hook (checks rows and secrets on commit, keeps the live ref in step)
  .git/hooks/pre-push
      lore's marked block in the pre-push hook (checks rows and secrets on commit, keeps the live ref in step)
  .git/lore
      lore's own files for this repo: never committed
  .git/lore/fetch-stamp
      when lore last fetched the remote
  .git/lore/git-wiring
      a fingerprint of lore's git wiring (merge driver, hooks), so it is checked only when something changed
  .git/lore/live.lock
      the lock of the live ref
  ~/.lore/cache/repo/website-<hash>.db
      this repo's search cache, kept under the lore home so no command creates a file in the repo; rebuilt from the rows
elsewhere for you:
  /usr/local/bin/lore
      the lore program itself
what each one is and what happens if you edit or delete it: ~/.lore/README.md; `lore where --json` lists the same
```
<!-- /example -->

`lore where --json` gives the same list for scripts: each entry has `area` (home, store, repo, user), `path`, `key` (a git config key or ref), `kind` (file, folder, git-config, git-ref, block), `what`, `by_hand` (what happens if you edit or delete it) and `exists`. `--all` also lists the places lore uses that do not exist on this machine yet.

Anything in `~/.lore` that lore does not recognise (a file you put there, a leftover of another program) is listed under "in lore's home but not lore's". lore never deletes it.

## lore's home, `~/.lore`

`~/.lore/README.md` is the same map in plain words, written by lore and rewritten when the layout changes:

<!-- example: files#the-home-folder -->
```console
# alice, in ~
$ ls ~/.lore
README.md
cache
machine.json
state
stores
$ head -20 ~/.lore/README.md
<!-- written by lore, layout 2026-11-12.4; lore rewrites this file when its layout changes -->
# lore's home on this machine
This folder is where lore keeps what belongs to this machine: its settings, the stores it cloned, caches and small state files. Your notes themselves live in stores (git repositories or folders) and in your code repos' .lore/data folders; nothing here is synced by itself.
`lore where` lists every file lore keeps on this machine and in the repo you run it in, one line each, with the store and repo each cache file belongs to (`lore where --json` for scripts). It also names anything in this folder lore does not recognise; lore never deletes such a file.
Store cache files are named by a store id, not by a store name: two repos that call the same store by different names share one cache. A code repo's own cache is named by its folder name and a short hash of its path (cache/repo/<name>-<hash>.db), so two checkouts get two files and a moved or renamed repo folder gets a fresh one. `lore where` shows which store and repo each one serves.
| path | what it is | if you edit or delete it |
|---|---|---|
| `README.md` | this map of lore's home, written by lore and rewritten when the layout changes | safe to delete: lore writes it again |
| `config.yaml` | this machine's lore settings: setups, orgs, stores (YAML; `lore config check` validates it) | you may edit it; deleting it forgets every setup, org and store on this machine (the stores themselves are not touched) |
| `config.yaml.lock` | the lock lore takes while it rewrites config.yaml | holds no data; safe to delete while no lore command runs |
| `identity` | this machine's secret salt for your actor id (never synced) | deleting it makes lore treat you as a new person on this machine |
| `machine.json` | this machine's id, kept so it stays the same between runs | deleting it makes lore work the id out again; where it was random, this machine then shows up as a new one |
| `api-token` | the token clients of `lore api` send | deleting it makes `lore api` make a new one; clients then need the new token |
| `state/` | machine-local state: never synced, and not rebuildable | do not delete the folder; the files below say what each one holds |
| `state/machine.db` | the machine store: rows that only mean something on this machine (agent sessions, runs), the audit chain, doctor results and small local state (usage counts, secret-check times, unsent time entries) | do not edit it by hand; deleting it loses those rows and the audit chain |
| `state/store-clones.json` | store clones that are not at their usual place: your own clone (`lore store use`) or a second store with the same name | deleting it makes lore use its own clones under stores/ again (your clone stays where it is) |
```
<!-- /example -->

| Path | What it is | If you edit or delete it |
|---|---|---|
| `README.md` | the map of this folder, written by lore | lore writes it again |
| `config.yaml` | this machine's settings: setups, orgs, stores | you may edit it; deleting it forgets every setup, org and store on this machine |
| `identity`, `machine.json` | your actor salt and this machine's id | deleting them makes you (or this machine) look new |
| `state/` | machine-local state, never synced, not rebuildable: `machine.db` (agent sessions, runs, the audit chain, small local state), the audit logs, batch records, store clones not at their usual place | do not delete |
| `cache/` | caches, all rebuilt from the row files: `machine-store.db`, one file per store folder under `stores/<store id>/<branch>/`, `combined/` and `remote/` per repo, `repos/` (which caches each repo uses); lore 0.1.17's caches (`prj_….db`, `links-<org>.json` and the `stores/<host>/…` tree) are listed as such, and `lore cache prune` removes them (a project's `prj_….db` once its repo is migrated) | safe to delete |
| `stores/<owner>/<repo>/` | a plain git clone of each store; rows are JSON files you may open and edit | a clone with no unpushed work is safe to delete; `lore store forget` removes it cleanly |
| `stores/_me/` | your personal store `me` when it is a plain folder (`file://`), not a git repo; the `_` keeps it apart from any owner folder | it holds your notes: do not delete it unless you mean to lose them |
| `render/` | private context for repos, imported by their `CLAUDE.local.md` | rewritten by `lore render` |
| `logs/hooks.log` | errors of agent hooks (capped, one older copy kept) | safe to delete |
| `tmp/` | scratch space for setup probes | safe to delete while no lore runs |

Cache files are named by a store id or by a hash of a repo's path, not by a name: two repos that call the same store by different names share one cache, and a moved repo gets a fresh one. `lore where` prints the store and the repos each cache file serves.

## In a store clone

[Stores as plain folders](../sync/store-clones.md) walks through a clone. A store clone under `~/.lore/stores/` is a normal git clone. lore's own files sit in its `.git/lore/` folder (the store lock, its journal, when the store was last fetched and pushed), never in the files you browse and never committed. Each project folder keeps its rows under `<folder>/<table>/<id>.json`; the store's own rows (its root folder) sit in `_store/<table>/<id>.json`, so every other top-level folder is a project or the org folder. `.git/info/attributes` holds one line (`*.json merge=lore`) telling git to merge row files with lore's merge driver, field by field, never line by line; `.git/config` holds that driver and your git identity for the store, and `.git/hooks/pre-commit` lore's check ([Plain git in the store](../sync/editing-by-hand.md#plain-git-in-the-store)).

## In a code repo

| Path | What it is | Committed |
|---|---|---|
| `.lore/data/` | the repo's rows, one JSON file per row, and `_meta.json` (the project id) | yes |
| `.lore/lock`, `.lore/state/` | the write lock, local journals (the repo's search cache is in lore's home, `~/.lore/cache/repo/`, never here) | no (`.gitignore`) |
| `.lore/data/**/.tmp-*` | a row file still being written; a write killed half way (an agent session stopped) leaves one behind, and the next write here removes it | no (`.gitignore`, line `.lore/data/**/.tmp-*`) |
| `.lore/lore.db`, `.lore/backups/` | lore 0.1.17's database of this checkout and its backups; `lore fix` copies the rows and edits only the database held into `.lore/data`, then removes both once every row they hold is in a store here | no; `lore fix` removes them |
| `.lore/LORE.md`, `.lore/stores.yaml` | rendered context for agents, the stores the repo attaches | yes |
| a marked block in `.gitignore`, `.gitattributes`, `CLAUDE.md` / `AGENTS.md` / … | lore's lines; text outside the block is never touched | yes |
| `.git/config` `merge.lore.*` | the merge driver for row files | no (this clone) |
| marked blocks in `.git/hooks/*` | checks on commit and push | no (this clone) |
| `.git/lore/` | lore's state for this clone (live ref lock and log, claims) | no |
| `refs/lore/*` | live rows and claim signals shared through the remote | pushed by lore |

The first time lore writes any of these it says so, with the file and the lines or keys, for example `lore sync: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in …/.git/config; lore's merge rules in …/.gitattributes; hook blocks in …/.git/hooks (commit-msg, pre-commit, …)`.

A repo that holds no lore data of its own (a client repo whose notes live in a store folder, with at most `.lore/stores.yaml`) never gets a tracked file changed by lore, `lore fix` included: no `.gitignore` lines and no `.gitattributes` block. lore's local files stay out of git status through `.git/info/exclude`, and lore says so the first time. In a project someone migrated with `lore migrate run`, only the clone that ran it (git config `lore.migratedhere`) adds lore's lines to the tracked `.gitignore` and `.gitattributes` on its next write; other clones keep the same rules in `.git/info/exclude` and `.git/info/attributes` until that commit reaches them, and `lore doctor --fix` or `lore fix` writes the tracked lines anywhere ([Upgrading](upgrade.md)).

`lore fix` (and `lore doctor --fix`, which runs the same steps) checks again after fixing: the status it prints is the state after the fixes, and a `Fixed` list under it names what changed (`fixed` in `--json`).

## Reads change nothing; writes say where

A command that only reads (`list`, `show`, `search`, `whoami`, `doctor`, `where`, …) never writes into your repo, its `.git` or a store clone's files, and never pushes. It may fetch a store, and when commits wait in a store clone it says `N local commit(s) wait to be pushed; a read never pushes: run lore sync`, once: the next reads stay quiet until the count changes or the store comes back online.

A command that writes names each row file it wrote (`lore: wrote /home/pat/code/app/.lore/data/memories/mem_….json`, or a count per folder when there are many), and every push names the store, the commit and the branch: `lore: pushed store notes: commit 1a2b3c4 to branch main`.

A command that only reads also never sets up git: it does not install the merge driver, hooks, attributes or a clone's identity, in a code repo or a store clone. When something is missing it prints at most one line naming the command that sets it up (`lore sync`, `lore fix`, or the next write), and that command says what it wrote.

## Try it

The example section `files` lists lore's home, a store clone and a repo with `lore where`: [`files`](../../tests/docs/sections/files.sh) ([output](../../tests/docs/transcripts/files.txt)).

## Related

- [Stores as plain folders](../sync/store-clones.md)
- [Editing by hand](../sync/editing-by-hand.md)
- [Hooks and the merge driver](../sync/hooks-and-merge-driver.md)
- [Upgrade lore and fix a project](upgrade.md)
