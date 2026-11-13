# Stores as plain folders

**In short:** a store is an ordinary git repository, and lore keeps one ordinary clone of it on your machine at `~/.lore/stores/<owner>/<repo>`. Open it in a file browser or an editor and you see what the team wrote: one folder per project, one folder per kind of note inside it, one JSON file per note, and a `README.md` that explains it. Nothing is hidden in a second copy or a nested `.lore` folder; lore's own bookkeeping sits in the clone's `.git/lore/` folder, out of the files you browse. You may also clone the store yourself, anywhere, and tell lore to use that clone with `lore store use <path>`.

## What it is and when you want it

You want this page when you open `~/.lore/stores/` and wonder what each folder is, when you want to read a team's notes without lore, when you want lore to work in a clone you made yourself, or when an earlier 0.1.18 build left its clones elsewhere. To change a note by hand, see [Editing by hand](editing-by-hand.md); to change one in your editor as Markdown, see [Edit a row in your editor](../knowledge/editing-rows.md); for every file lore keeps on the machine, see [Where lore keeps things](../operations/where-lore-keeps-things.md).

## Before you start

- lore set up in a repo (`lore init`) and a git store attached to it, here `lore store init notes git@github.com:acme/notes` in Alice's checkout of `github.com/acme/website`. A team gets its stores from its org setup instead ([Lore for teams](../team/README.md)); the clone looks the same.

## Step 1 — Where the clone is

When lore first needs a git store it clones it to `~/.lore/stores/<owner>/<repo>`, named after the store's address (`git@github.com:acme/notes` → `~/.lore/stores/acme/notes`). The host and any ssh alias never appear in the path. A store pinned to another branch is its own clone at `<repo>@<branch>`; two stores whose addresses end in the same owner and repo on different hosts get `<repo>~2` for the second. Your personal store, when it is a plain folder rather than a git repo, is `~/.lore/stores/_me` (no git host allows an owner name that starts with `_`, so it never clashes with an owner folder).

The first time lore clones or writes to the store it prints what it set up, including your git identity for that clone:

<!-- example: files#a-git-store -->
```console
# alice, in website
$ lore store init notes git@github.com:acme/notes
lore store init: store clone ~/.lore/stores/acme/notes: git user set to alice <alice@agency.example> (your lore identity)
✓ created store notes (sto_7a67…9504) at git@github.com:acme/notes; recorded in .lore/stores.yaml; commit it to share
$ lore task add 'Renew the TLS certificate' --to notes --no-live
lore: pushed store notes: commit 601d285 to branch main
lore: pushed store notes: commit 86dbae4 to branch main
lore: wrote 4 row file(s) in ~/.lore/stores/acme/notes/_store (store notes)
✓ tsk_2ad3…f891 — Renew the TLS certificate [medium] → [notes]
  created tasklist tlt_162d…9666 "default" for tasks added without --tasklist
$ lore memory add --to notes --body 'The staging database is reset every Monday.'
lore: pushed store notes: commit 9b40ce7 to branch main
lore: wrote ~/.lore/stores/acme/notes/_store/memories/mem_54fe…28f2.json (store notes)
✓ mem_54fe…28f2 [notes]
  scope: [master]
```
<!-- /example -->

Every write names the files it changed, and every push names the store, the commit and the branch.

## Step 2 — What is inside

<!-- example: files#the-store-clone -->
```console
# alice, in ~
$ find ~/.lore/stores -maxdepth 3 -not -path '*/.git/*' | sort
~/.lore/stores
~/.lore/stores/acme
~/.lore/stores/acme/notes
~/.lore/stores/acme/notes/.0-needs-lore-0.1.18-or-newer
~/.lore/stores/acme/notes/.git
~/.lore/stores/acme/notes/README.md
~/.lore/stores/acme/notes/_store
~/.lore/stores/acme/notes/lore-store.yaml

# alice, in .lore/stores/acme/notes
$ ls -a; cat lore-store.yaml
.
..
.0-needs-lore-0.1.18-or-newer
.git
README.md
_store
lore-store.yaml
lore_store: 4
id: sto_7a67…9504
visibility: internal
created: "2026-11-02"
$ find _store -type f | sort
_store/actors/act_05e9…4603.json
_store/machines/mch_c0f3…b5b8.json
_store/memories/mem_54fe…28f2.json
_store/task_lists/tlt_162d…9666.json
_store/tasks/tsk_2ad3…f891.json
$ cat _store/memories/mem_54fe…28f2.json
{
  "_hlc": "2026-11-02T10:00:15.000Z/0000/act_05e9…4603",
  "_o": {
    "cmd": "memory add",
    "git": {
      "branch": "main",
      "commit": "05513aa29b78",
      "state": "branch"
    },
    "project": "prj_76bd…3f10",
    "repo": "rep_d431…313a",
    "store": "sto_7a67…9504"
  },
  "_table": "memories",
  "_v": 2,
  "_w": {
    "m": "mch_017941fe53b8eca065d80ff150a20e9fa70f8cec380c9b853a906dd8c07a7eff",
    "v": "dev"
  },
  "body": "The staging database is reset every Monday.",
  "created_at": "2026-11-02T10:00:00.000000001Z",
  "created_by": "act_05e9…4603",
  "created_by_actor_id": "act_05e9…4603",
  "id": "mem_54fe…28f2",
  "kind": "retrieved",
  "project_id": "prj_76bd…3f10",
  "source_kind": "manual",
  "trust_score": 0.5,
  "tx_at": "2026-11-02T10:00:00.000000001Z",
  "updated_at": "2026-11-02T10:00:00.000000001Z",
  "updated_by": "act_05e9…4603"
}
$ ls .git/lore
fetch-stamp
identity
lock
push_stamp
remote-tip
state
$ head -12 README.md
# A lore store
This folder is a lore store: notes, rules, decisions, tasks and the other records a team keeps with lore. Every record is one small JSON file, so you can open, read and search the store with any editor, file browser or git tool. lore wrote this file; edit or replace it as you like, and lore leaves your version alone.
## What the folders are
- `lore-store.yaml`: the store's identity (its id, who may see its rows).
- `_store/` holds the store's own records, the ones that belong to the whole store and to no project (actors, machines, store-wide rules and so on), one folder per kind of record: `_store/actors/act_….json`. Its name starts with `_`, which no project folder may, so it is never mistaken for one.
- Every other folder is a project folder (a client, a project, a team) or the org folder, each holding one folder per kind of record, one file per record: `acme/tasks/tsk_….json`, `acme/memories/mem_….json`. Folders can nest (`team/web/tasks/`). A folder may not be named like a kind of record (`tasks`, `memories`, `rules`, ...) and may not start with `_` or `.`.
- `_meta.json` in a folder holds the folder's id; `_purged.json` lists records removed for good. Leave both to lore.
- `org/` (when the store has one) holds the company's people, teams and rules, and `org/lore-org.yaml`, the company's settings.
$ git log --format='%an <%ae>  %s'
alice <alice@agency.example>  lore: memory add mem_54fe…28f2
alice <alice@agency.example>  lore: machine register mch_c0f3…b5b8
alice <alice@agency.example>  lore: task add: Renew the TLS certificate (3 rows)
alice <alice@agency.example>  lore: store init
```
<!-- /example -->

| Path in the clone | What it is |
|---|---|
| `lore-store.yaml` | the marker that makes the repo a store: its permanent id (`sto_…`), its visibility and the layout version (`lore_store: 4`). lore never treats a folder with this file as a code repo: no `.lore/data`, no folder rules, and `cd ~/.lore/stores/acme/notes/<folder> && lore task list` works on that folder |
| `README.md` | what the folders are and how to browse and edit them; lore writes it once, with its next commit in the store, and never overwrites your version. `lore doctor` notes a store without one (`store_readme`) |
| `_store/<table>/<id>.json` | the store's own rows (people and machines, notes written to the whole store). A folder name may never start with `_`, so `_store` is never a project |
| `<folder>/<table>/<id>.json` | the rows of one project or client folder (`acme/tasks/tsk_….json`), with `<folder>/_meta.json` (the folder's id and repo links). Folders can nest (`team/web/tasks/`). A folder may not be named like a kind of row (`tasks`, `memories`, …) nor start with `_` or `.` |
| `org/lore-org.yaml` | in an org's main store, the org's shared settings: its stores, repos and defaults (`lore org refresh` copies them into your config) |
| `.git/lore/` | lore's own files for this clone: the store lock, its journal, when lore last fetched and pushed, the git identity lore wrote. Never committed and never in the files you browse |

A row file is plain JSON, one field per line, so `grep`, `git log -p` and any editor read it. A code repo is different: there lore keeps its rows in `.lore/data/` to keep them apart from your code.

## Step 3 — Short names

Every store has a short name, used in lore's output, in `lore store list`, in store arguments and in `--to <store>/<folder>` targets. A store you attach yourself is called what you named it (`notes` above). An org's stores are called by their repo name without `.git` (`git@github.com:acme/lore-east.git` → `lore-east`), and an org can set another name in its `lore-org.yaml` (`stores[].name`). Two stores of one org with the same repo name are refused when the config loads, naming both, until one gets a `name:`. The org's main store keeps the org's name as its label.

## Step 4 — Use a clone of your own

You can clone the store wherever you like and point lore at it. lore records the path on this machine only (`~/.lore/state/store-clones.json`; your teammates keep their own clones), and `lore where` and `lore path` follow it. `lore store use --default <store>` goes back to lore's clone and leaves yours where it is:

<!-- example: files#your-own-clone -->
```console
# alice, in ~
$ git clone -q git@github.com:acme/notes ~/code/notes && ls ~/code/notes
README.md
_store
lore-store.yaml

# alice, in website
$ lore store use ~/code/notes --yes
lore: pushed store notes: commit df3a55c to branch main
✓ store notes now uses your clone ~/code/notes (recorded on this machine only)
  before: ~/.lore/stores/acme/notes
  lore's earlier clone ~/.lore/stores/acme/notes is no longer used and stays on disk; remove it when you no longer need it
$ lore store list
stores:
  NAME   URL                        FOLDER  READ_ONLY  BRANCH  PERSONAL  SOURCE       VISIBILITY  REACH       ADDRESS
  notes  git@github.com:acme/notes          false      main    false     stores.yaml  internal    local_copy  
count: 1
$ lore path tsk_2ad3…f891
~/code/notes/_store/tasks/tsk_2ad3…f891.json
$ lore store use --default notes --yes
✓ store notes uses lore's own clone again: ~/.lore/stores/acme/notes (the next command that needs it clones it if it is missing)
  before: ~/code/notes (your clone, left as it is)
```
<!-- /example -->

## What lore never does in a store clone

- **A read never pushes and never sets up git.** A command that only reads (`list`, `show`, `search`, `where`, `doctor`) may fetch the store and fast-forward lore's clone, but it never pushes; when commits wait, it says `N local commit(s) wait to be pushed; a read never pushes: run lore sync`. It never installs the merge driver, hooks or your clone identity either; at most it prints one line naming the command that will (`lore sync`, `lore fix`, or the next write).
- **It never touches your global git config.** The identity, merge driver and hook lore needs are written into the clone's own `.git/config`, `.git/info/attributes` and `.git/hooks/pre-commit`, each announced the first time ([Plain git in the store](editing-by-hand.md#plain-git-in-the-store)).
- **The clone's git identity is always a person.** lore writes your name and email (in an org store, your identity in that org) as the clone's `user.name` and `user.email`, never an agent's. When an agent writes for you, the agent is named in a `Lore-Agent:` trailer of lore's commit, not in its author. `lore doctor` flags a clone whose identity is not yours (`store_identity`) with the exact command, and `lore fix` corrects it.
- **It never deletes a file of yours.** Files that are not rows are reported by `lore doctor` (`store_files`) and left alone.

## Every store at once, from anywhere

Outside any repo, `lore sync` syncs every store `lore store list` shows (the org's stores and your personal store `me`), one line per store; a store that fails does not stop the others, and the command exits non-zero naming the ones that failed. `lore org refresh` without an argument refreshes the only org on the machine, else the active setup's org.

When a store's address uses an ssh alias of your own (a second GitHub account), the shared config keeps the canonical address (`git@github.com:acme/…`) and your machine maps it to your alias with an owner-scoped `access:` rule in `~/.lore/config.yaml` ([Reaching stores through your own ssh alias](../reference/config.md#reaching-stores-through-your-own-ssh-alias-access)). `lore doctor` explains a store it cannot reach with the exact fix.

## From an earlier 0.1.18 build

Earlier 0.1.18 builds kept each store as a bare clone plus a worktree under `~/.lore/clones/`, with rows under `<folder>/.lore/data/`. The first command of this lore moves them to `~/.lore/stores/<owner>/<repo>` by itself (nothing is lost: local commits and uncommitted changes are carried), gives each store one layout commit, and says what it moved. `lore doctor` then names each old clone with the exact command to remove it once everything in it is safe, and `lore fix` removes those for you. The details are in [Upgrade lore](../operations/upgrade.md#from-an-earlier-0118-build-lore-moves-your-stores-itself).

## Try it

The example section `files` runs every step on this page in a throwaway world: [`files`](../../tests/docs/sections/files.sh) ([output](../../tests/docs/transcripts/files.txt)).

## Pitfalls

- **Deleting the clone while commits wait loses them.** A clone with nothing unpushed is safe to delete (lore clones it again when needed); `lore store forget` removes it cleanly and refuses while work waits.
- **Do not put your own project files in a store.** lore leaves them alone, but your teammates get them too if you commit them; `lore doctor` lists them.
- **A folder named like a kind of row is refused.** `lore store folder add notes tasks` names the reserved names; pick another name.

## Related

- [Editing by hand](editing-by-hand.md)
- [Edit a row in your editor](../knowledge/editing-rows.md)
- [Where lore keeps things](../operations/where-lore-keeps-things.md)
- [Your stores: list, move, retire](stores.md)
- [Identity and stores](../team/identity-and-stores.md)
