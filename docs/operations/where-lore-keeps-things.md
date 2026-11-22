# Where lore keeps things: `lore where` and lore's home

**In short:** lore keeps its own files in one folder per machine (`~/.lore`, or the absolute folder `LORE_HOME` names; the first line of `lore where` says which and why), in a `.lore/` folder in each code repo, in a few marked blocks of files it shares with you (`.gitignore`, `.gitattributes`, `CLAUDE.md`, git hooks) and in a few git config entries. `lore where` (also spelled `lore files`) lists every one of them on this machine and in the repo you run it in, one line each, with what it is. `~/.lore/README.md` explains the home folder in plain words. Nothing is hidden: when lore writes into your repo or its git config for the first time it says what and where.

## What it is and when you want it

You opened `~/.lore` and want to know what a file is, whether you may delete it, or which store a cache file belongs to. Or you want to know what lore changed in a repo before you commit. Or a teammate asks what lore installs on their machine. `lore where` answers all three from what is actually on disk, not from a list in a document.

## List everything

Run it anywhere; inside a repo it adds that repo's files:

<!-- example: files#lore-where -->
```console
# alice, in website
$ lore where
lore home: ~/.lore (default; set LORE_HOME to an absolute folder to move it)
lore's home: ~/.lore
  README.md
      this map of lore's home, written by lore and rewritten when the layout changes
  cache
      rebuildable caches: everything here is made again from the row files when deleted
  cache/gitfast
      git answers kept between commands so an everyday command starts few git processes: each repo folder's git config with the files it came from, and the history and org checks of each store at one commit; an answer is dropped when a config file or git itself changes, and the oldest go past 256 files
  cache/machine-store.db
      the cache of state/machine.db, arranged for search
  cache/machine-store.db.stamp
      the open stamp beside a cache file (what a read needs to open it without parsing its schema; a command that writes the cache writes it, and so does the first read after lore is upgraded or reinstalled): the cache of state/machine.db, arranged for search
  cache/repo
      one cache per code repo checkout, of the rows in its .lore/data: repo/<repo folder name>-<short hash of the repo path>.db (kept here so a command never creates a file in the repo)
  cache/repo/website-<hash>.db
      a code repo's cache; used by ~/work/website
  cache/repo/website-<hash>.db.lore-dir-ignored
      a code repo's cache
  cache/repo/website-<hash>.db.stamp
      the open stamp beside a cache file (what a read needs to open it without parsing its schema; a command that writes the cache writes it, and so does the first read after lore is upgraded or reinstalled): a code repo's cache; used by ~/work/website
  cache/stores
      one cache file per store folder: stores/<store id>/<branch>/<folder>.db (`lore where` names the store and the repos that use each); a code branch that keeps its own notes (isolated: `lore branch mode`) reads a store folder through its own cache, stores/<store id>/<branch>/_layers/<repo key>/<branch key>/<folder>.db (repo key: 12 hex digits of the repo's remote; branch key: the branch name with / written %2F), while a shared branch (the default branch, dev, staging, release/*, one marked `lore branch share`) reads the folder's own cache; a layer cache is removed when that branch's notes are landed, dropped or purged, when the branch becomes shared, by `lore doctor --fix` (doctor lists a stray one), and by `lore cache prune` once no repo reads it
  cache/stores/sto_7a67…9504/main/root.db
      a store folder's cache; store notes; used by ~/work/website
  cache/stores/sto_7a67…9504/main/root.db.stamp
      the open stamp beside a cache file (what a read needs to open it without parsing its schema; a command that writes the cache writes it, and so does the first read after lore is upgraded or reinstalled): a store folder's cache; store notes; used by ~/work/website
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
  .git/config [lore.initedhere]
      this clone ran lore init or the migration, so its commit brings the repo's first render (teammates' clones keep theirs out of git status until then)
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
<!-- written by lore, layout 2026-11-22.1; lore rewrites this file when its layout changes -->
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
| `cache/` | caches, all rebuilt from the row files: `machine-store.db`, `authority/` (the store history check of each store clone lore does not own, one file per clone, so a read never writes into your own clone), one file per store folder under `stores/<store id>/<branch>/`, `combined/` and `remote/` per repo, `repos/` (which caches each repo uses); lore 0.1.17's caches (`prj_….db`, `links-<org>.json` and the `stores/<host>/…` tree) are listed as such, and `lore cache prune` removes them (a project's `prj_….db` once its repo is migrated) | safe to delete |
| `stores/<owner>/<repo>/` | a plain git clone of each store; rows are JSON files you may open and edit | a clone with no unpushed work is safe to delete; `lore store forget` removes it cleanly |
| `stores/_me/` | your personal store `me` when it is a plain folder (`file://`), not a git repo; the `_` keeps it apart from any owner folder | it holds your notes: do not delete it unless you mean to lose them |
| `render/` | private context for repos, imported by their `CLAUDE.local.md` | rewritten by `lore render` |
| `logs/hooks.log` | errors of agent hooks (capped, one older copy kept) | safe to delete |
| `run/` | the local read server's socket (`read.sock`, only you can open it) and its lock | safe to delete: the server stops and the next read starts it again |
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

A repo that holds no lore data of its own (a client repo whose notes live in a store folder, with at most `.lore/stores.yaml`) never gets a tracked file changed by lore, `lore fix` included: no `.gitignore` lines and no `.gitattributes` block. lore's local files stay out of git status through `.git/info/exclude`, and lore says so the first time. The same holds in a client repo that still has row files (set up as an own project before the org named it a client repo): lore's merge rules go to `.git/info/attributes` and its local files to `.git/info/exclude`, `lore doctor --fix`, `lore fix` and `lore sync` included, and a `.gitignore` rule hiding row files there is reported, never rewritten. In a project with lore data, an ordinary lore write (a note, a task, `lore sync`) changes no tracked file outside `.lore/` either: when the tracked `.gitignore` or `.gitattributes` lacks some of lore's lines (right after `lore migrate run`, or after an upgrade that added a line), the write keeps the same rules in the clone's own `.git/info/exclude` and `.git/info/attributes` and says so once; `lore init`, `lore doctor --fix` and `lore fix` write the tracked lines, in any clone but a client repo's, for you to commit ([Upgrading](upgrade.md)).

A repo whose `.gitignore` (or the clone's own `.git/info/exclude`, or your global excludes file) ignores `.lore/` keeps the project private to this machine: lore writes into no file the repo tracks (no `.gitignore` lines, no `.gitattributes` block, no pointer block in `CLAUDE.md` or `AGENTS.md`, no hook block in a committed hooks folder) and says so once, in one line that starts `git ignores lore data` and names the rule. A `# lore` block an older lore appended to such a repo's `.gitignore` is named by `lore doctor`, and `lore doctor --fix` takes lore's lines out. A client repo is not such a project: its `.lore/` is committed, so its pointer stays in its tools' files. `.lore/LORE.md` is still rendered, and Claude Code reads it through the pointer in `CLAUDE.local.md`, which lore excludes in `.git/info/exclude`; a tool with no such local file gets the notes from `lore context` or the agent hooks.

`lore fix` (and `lore doctor --fix`, which runs the same steps) checks again after fixing: the status it prints is the state after the fixes, and a `Fixed` list under it names what changed (`fixed` in `--json`). When the fixing pass repaired nothing and found nothing a repair names, that pass is already the state now and the second check is skipped, so `lore fix` on a project with nothing to fix takes about as long as `lore doctor`.

## Reads change nothing; writes say where

A command that only reads (`list`, `show`, `search`, `whoami`, `doctor`, `where`, …) never writes into your repo, its `.git` or a store clone's files, and never pushes. It may fetch a store, and when commits wait in a store clone it says `N local commit(s) wait to be pushed; a read never pushes: run lore sync`, once: the next reads stay quiet until the count changes or the store comes back online.

A command that writes names each row file it wrote (`lore: wrote /home/pat/code/app/.lore/data/memories/mem_….json`, or a count per folder when there are many), and every push names the store, the commit and the branch, with the commits and row files it sent as `git rev-list` and `git diff` count them: `lore: pushed store notes: commit 1a2b3c4 to branch main (3 commits, 4 row files)`. A row written to a folder of an org store is named by its folder (`(store acme/web)`), as search and the lists name it, and one push that carried several folders of a store clone is one line for the whole clone: `lore: pushed store acme (the store clone: folders org, web): …`.

A command that only reads also never sets up git: it does not install the merge driver, hooks, attributes or a clone's identity, in a code repo or a store clone. When something is missing it prints at most one line naming the command that sets it up (`lore sync`, `lore fix`, or the next write), and that command says what it wrote.

## The local read server

On Linux and macOS a read such as `lore ask`, `lore search` or a `list`/`show` is answered by a lore process that keeps running between commands, with its folder caches and lookups already open: a warm `lore ask` takes about 40-50 ms instead of about 100 ms. The first read starts it in the background (that read still runs on its own); it listens on `run/read.sock` in lore's home, a socket only you can open, in a folder only you can enter. It exits after 10 minutes without a call, an hour after it started, after a call that left it holding more than 128 MB of live memory beyond what it keeps between calls on purpose (the answers `lore ask` keeps, which hold every row's text of the stores it reads, and the git objects it has read; all of it is bounded by the store it reads, not by how many writes it has seen since it started, so on a store of several thousand rows it stays at about 150 MB in all and on a store of 100,000 rows it keeps serving with a few hundred MB), when its socket is deleted, and when the lore binary changes (an upgrade), and the next read starts it again.

Between calls it keeps each folder cache's open connection (its schema already read), and for a read that names its store (`--store`) the opened store itself; a kept store is used again only while nothing it was built from changed (`config.yaml`, `state/`, the folder caches, each store clone's HEAD, index and refs, the repo's `.lore`) and for at most 30 seconds, so a row a sync brought, a write from another process or a deleted cache shows in the very next read, exactly as without the server. A cache file replaced on disk is never read through its old connection.

It runs exactly the command you typed, in your folder, with your environment and your terminal, so the output, the exit code and the agent lore sees are the same as without it. It answers only commands that only read, one at a time, and only for the same lore binary (the same file and version, whether you start it through a link such as a package manager's `bin/lore` or through its real path; a copy is another binary): anything else, a busy server, or a server that is gone runs the command in its own process as before. A server stopped before it started the command leaves the command to run in its own process; a server stopped while it was answering never makes lore run it again (the start of the answer would show twice): the command exits 1 and says the answer may be cut short, so run it again. A read whose output pipe closes early (`lore sql … | head -c 5`) ends as it does without the server: killed by SIGPIPE, nothing more printed. Turn it off with `read_server: false` in `config.yaml`, or for one shell with `LORE_READ_SERVER=0`; it is off in CI (`CI` set) unless `LORE_READ_SERVER=1`. On Windows every command runs in its own process.

## Try it

The example section `files` lists lore's home, a store clone and a repo with `lore where`: [`files`](../../tests/docs/sections/files.sh) ([output](../../tests/docs/transcripts/files.txt)).

## Related

- [Stores as plain folders](../sync/store-clones.md)
- [Editing by hand](../sync/editing-by-hand.md)
- [Hooks and the merge driver](../sync/hooks-and-merge-driver.md)
- [Upgrade lore and fix a project](upgrade.md)
