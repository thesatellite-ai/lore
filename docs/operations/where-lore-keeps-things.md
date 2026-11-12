# Where lore keeps things: `lore where` and lore's home

**In short:** lore keeps its own files in one folder per machine (`~/.lore`), in a `.lore/` folder in each code repo, in a few marked blocks of files it shares with you (`.gitignore`, `.gitattributes`, `CLAUDE.md`, git hooks) and in a few git config entries. `lore where` (also spelled `lore files`) lists every one of them on this machine and in the repo you run it in, one line each, with what it is. `~/.lore/README.md` explains the home folder in plain words. Nothing is hidden: when lore writes into your repo or its git config for the first time it says what and where.

## What it is and when you want it

You opened `~/.lore` and want to know what a file is, whether you may delete it, or which store a cache file belongs to. Or you want to know what lore changed in a repo before you commit. Or a teammate asks what lore installs on their machine. `lore where` answers all three from what is actually on disk, not from a list in a document.

## List everything

Run it anywhere; inside a repo it adds that repo's files:

```console
$ lore where
lore's home: /home/pat/.lore
  README.md
      this map of lore's home, written by lore and rewritten when the layout changes
  config.yaml
      this machine's lore settings: setups, orgs, stores (YAML; `lore config check` validates it)
  state/machine.db
      the machine store: rows that only mean something on this machine (agent sessions, runs), the audit chain, doctor results and small local state (usage counts, secret-check times, unsent time entries)
  cache/stores/sto_01a1…/main/root.db
      a store folder's cache; store notes; used by /home/pat/code/app
  ...

store clones:
  /home/pat/.lore/stores/acme/notes
      a git clone of store https://github.com/acme/notes.git; rows are JSON files at /home/pat/.lore/stores/acme/notes/<folder>/<table>/<id>.json (the store's own rows: /home/pat/.lore/stores/acme/notes/_store/<table>/<id>.json); …
  /home/pat/.lore/stores/acme/notes/.git/lore
      lore's own files for this clone: never committed, never in the files you browse
  ...

this repo: /home/pat/code/app
  .lore/data
      this repo's rows, one JSON file per row (.lore/data/<table>/<id>.json) and _meta.json (the project id); committed
  .gitignore
      lore's lines keeping its cache, lock and state out of git, and the rows in
  .git/config [merge.lore.driver]
      the merge driver git runs for lore's row files (lore merges them field by field)
  .git/hooks/pre-commit
      lore's marked block in the pre-commit hook (checks rows and secrets on commit, keeps the live ref in step)
  ...
```

`lore where --json` gives the same list for scripts: each entry has `area` (home, store, repo, user), `path`, `key` (a git config key or ref), `kind` (file, folder, git-config, git-ref, block), `what`, `by_hand` (what happens if you edit or delete it) and `exists`. `--all` also lists the places lore uses that do not exist on this machine yet.

Anything in `~/.lore` that lore does not recognise (a file you put there, a leftover of another program) is listed under "in lore's home but not lore's". lore never deletes it.

## lore's home, `~/.lore`

| Path | What it is | If you edit or delete it |
|---|---|---|
| `README.md` | the map of this folder, written by lore | lore writes it again |
| `config.yaml` | this machine's settings: setups, orgs, stores | you may edit it; deleting it forgets every setup, org and store on this machine |
| `identity`, `machine.json` | your actor salt and this machine's id | deleting them makes you (or this machine) look new |
| `state/` | machine-local state, never synced, not rebuildable: `machine.db` (agent sessions, runs, the audit chain, small local state), the audit logs, batch records, store clones not at their usual place | do not delete |
| `cache/` | caches, all rebuilt from the row files: `machine-store.db`, one file per store folder under `stores/<store id>/<branch>/`, `combined/` and `remote/` per repo, `repos/` (which caches each repo uses) | safe to delete |
| `stores/<owner>/<repo>/` | a plain git clone of each store; rows are JSON files you may open and edit | a clone with no unpushed work is safe to delete; `lore store forget` removes it cleanly |
| `render/` | private context for repos, imported by their `CLAUDE.local.md` | rewritten by `lore render` |
| `logs/hooks.log` | errors of agent hooks (capped, one older copy kept) | safe to delete |
| `tmp/` | scratch space for setup probes | safe to delete while no lore runs |

Cache files are named by a store id or by a hash of a repo's path, not by a name: two repos that call the same store by different names share one cache, and a moved repo gets a fresh one. `lore where` prints the store and the repos each cache file serves.

## In a store clone

A store clone under `~/.lore/stores/` is a normal git clone. lore's own files sit in its `.git/lore/` folder (the store lock, its journal, when the store was last fetched and pushed), never in the files you browse and never committed. Each project folder keeps its rows under `<folder>/<table>/<id>.json`; the store's own rows (its root folder) sit in `_store/<table>/<id>.json`, so every other top-level folder is a project or the org folder. `.git/info/attributes` holds one line telling git not to merge row files line by line; lore merges them field by field.

## In a code repo

| Path | What it is | Committed |
|---|---|---|
| `.lore/data/` | the repo's rows, one JSON file per row, and `_meta.json` (the project id) | yes |
| `.lore/lore-cache.db`, `.lore/lock`, `.lore/state/` | the search cache, the write lock, local journals | no (`.gitignore`) |
| `.lore/LORE.md`, `.lore/stores.yaml` | rendered context for agents, the stores the repo attaches | yes |
| a marked block in `.gitignore`, `.gitattributes`, `CLAUDE.md` / `AGENTS.md` / … | lore's lines; text outside the block is never touched | yes |
| `.git/config` `merge.lore.*` | the merge driver for row files | no (this clone) |
| marked blocks in `.git/hooks/*` | checks on commit and push | no (this clone) |
| `.git/lore/` | lore's state for this clone (live ref lock and log, claims) | no |
| `refs/lore/*` | live rows and claim signals shared through the remote | pushed by lore |

The first time lore writes any of these it says so, with the file and the lines or keys, for example `lore sync: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in …/.git/config; lore's merge rules in …/.gitattributes; hook blocks in …/.git/hooks (commit-msg, pre-commit, …)`.

A repo that holds no lore data of its own (a client repo whose notes live in a store folder, with at most `.lore/stores.yaml`) never gets a tracked file changed by lore, `lore doctor --fix` included: no `.gitignore` lines and no `.gitattributes` block. lore's local files stay out of git status through `.git/info/exclude`, and lore says so the first time.

`lore doctor --fix` checks again after fixing: the status it prints is the state after the fixes, and a `Fixed` list under it names what changed (`fixed` in `--json`).

## Reads change nothing; writes say where

A command that only reads (`list`, `show`, `search`, `whoami`, `doctor`, `where`, …) never writes into your repo, its `.git` or a store clone's files, and never pushes. It may fetch a store, and when commits wait in a store clone it says `N local commit(s) wait to be pushed; a read never pushes: run lore sync`.

A command that writes names each row file it wrote (`lore: wrote /home/pat/code/app/.lore/data/memories/mem_….json`, or a count per folder when there are many), and every push names the store, the commit and the branch: `lore: pushed store notes: commit 1a2b3c4 to branch main`.
