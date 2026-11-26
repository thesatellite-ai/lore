# Stores as plain folders

**In short:** a store is an ordinary git repository, and lore keeps one ordinary clone of it on your machine at `~/.lore/stores/<owner>/<repo>`. Open it in a file browser or an editor and you see what the team wrote: one folder per project, one folder per kind of note inside it, one JSON file per note, and a `README.md` that explains it. Nothing is hidden in a second copy or a nested `.lore` folder; lore's own bookkeeping sits in the clone's `.git/lore/` folder, out of the files you browse. You may also clone the store yourself, anywhere, and tell lore to use that clone with `lore store use --path <path>`.

## What it is and when you want it

You want this page when you open `~/.lore/stores/` and wonder what each folder is, when you want to read a team's notes without lore, when you want lore to work in a clone you made yourself, or when an earlier 0.1.18 build left its clones elsewhere. To change a note by hand, see [Editing by hand](editing-by-hand.md); to change one in your editor as Markdown, see [Edit a row in your editor](../knowledge/editing-rows.md); for every file lore keeps on the machine, see [Where lore keeps things](../operations/where-lore-keeps-things.md).

## Before you start

- lore set up in a repo (`lore init`) and a git store attached to it, here `lore store init --name notes --url git@github.com:acme/notes` in Alice's checkout of `github.com/acme/website`. A team gets its stores from its org setup instead ([Lore for teams](../team/README.md)); the clone looks the same.

## Step 1 — Where the clone is

When lore first needs a git store it clones it to `~/.lore/stores/<owner>/<repo>`, named after the store's address (`git@github.com:acme/notes` → `~/.lore/stores/acme/notes`). The host and any ssh alias never appear in the path. A store pinned to another branch is its own clone at `<repo>@<branch>`; two stores whose addresses end in the same owner and repo on different hosts get `<repo>~2` for the second. Your personal store, when it is a plain folder rather than a git repo, is `~/.lore/stores/_me` (no git host allows an owner name that starts with `_`, so it never clashes with an owner folder).

A store is one store however its address is spelled: `git@github.com:acme/notes.git`, `https://github.com/acme/notes` and `git@work-gh:acme/notes.git`, where your `~/.ssh/config` has `Host work-gh` with `HostName github.com`, all name the same store. lore reads the ssh config file (no ssh process, no network) to tell what an alias stands for, so a clone whose origin uses your alias is still your org's store: reads at its top label rows with the org's name, and a read in one of its folders lists that folder and the org's rows. A HostName that is only another route to the same host (`github.com` → `ssh.github.com`, for port 443) is not taken for an alias, and an alias whose HostName is a provider's port-443 route (`ssh.github.com`, `altssh.gitlab.com`, `altssh.bitbucket.org`) stands for the provider itself (`github.com`, `gitlab.com`, `bitbucket.org`), so the shared config gets the provider's address; the same holds for an address typed on the route host itself (`git@ssh.github.com:acme/notes.git`). An alias host your ssh config does not name, such as one git reaches through a `url.<base>.insteadOf` rule, counts as the same store when it is a single name with no dot and the repository path is the same (also when your ssh config sends the store's host to its port-443 route): the same rule decides which clone folder lore uses, which org a clone belongs to and which org folder a read in it lists.

The first time lore clones or writes to the store it prints what it set up, including your git identity for that clone:

<!-- example: files#a-git-store -->
```console
# alice, in website
$ lore store init --name notes --url git@github.com:acme/notes
lore store init: store clone ~/.lore/stores/acme/notes: git user set to alice <alice@agency.example> (your lore identity)
✓ created store notes (sto_7a67…9504) on git@github.com:acme/notes: pushed its first commit (the store marker); recorded in .lore/stores.yaml; commit it to share
$ lore task add --title 'Renew the TLS certificate' --to notes --no-live
lore: committed locally to store notes; lore sync sends it
lore: wrote 4 row file(s) in ~/.lore/stores/acme/notes/_store (store notes)
✓ tsk_2ad3…f891 — Renew the TLS certificate [medium] → [notes]
  created tasklist tlt_162d…9666 "default" for tasks added without --tasklist
$ lore memory add --to notes --body 'The staging database is reset every Monday.'
lore: committed locally to store notes; lore sync sends it
lore: wrote ~/.lore/stores/acme/notes/_store/memories/mem_54fe…28f2.json (store notes)
✓ mem_54fe…28f2 [notes]
  scope: [project]
```
<!-- /example -->

Every write names the files it changed, and every push names the store, the commit and the branch. In a store clone the lines name exactly the files the write's commits hold: a row written again with the same content is not named, a file committed beside the rows (a folder's `_meta.json` on its first write) is, and a file a move took away reads `lore: removed <file>`.

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
      "commit": "601d2850ffd0",
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
maint-stamp
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
alice <alice@agency.example>  lore: task add tsk_2ad3…f891 (2 rows + actor act_05e9…4603)
alice <alice@agency.example>  lore: store init
```
<!-- /example -->

| Path in the clone | What it is |
|---|---|
| `lore-store.yaml` | the marker that makes the repo a store: its permanent id (`sto_…`), its visibility and the layout version (`lore_store: 4`). lore never treats a folder with this file as a code repo: no `.lore/data`, no folder rules, `lore init`, a writing `lore render` (`--dry-run` previews the folder) and `lore org link`/`unlink` refuse there and write nothing ("this is the clone of store acme (folder web) …; lore init is for code repos: nothing written"), `lore setup list` says "This folder: the clone of store acme (folder web), a lore store, not a code repo", and `cd ~/.lore/stores/acme/notes/<folder> && lore task list` works on that folder. At the clone's top a read (`task list`, `rule list`, `decision list`, `memory list`, `search`, `sql`) covers every folder of the store, each row labelled `<org>/<folder>`; inside a folder `--store <org>` covers every folder of the org's stores, `--all-repos` every folder of this store, `--store <org>/<folder>` or `--project <folder>` another folder. `task edit --assign @handle` in a folder finds the org's roster, and `project member list` there lists that folder's project; `lore show --id <id>` (and every command naming a row by id) finds the row in any folder of the store or the org, read for that one command; `project list` gives each folder's row count; `task next` ranks the store's tasks (inside a folder that folder's, at the top every folder's; from anywhere `lore task next --store <org>` or `--setup <setup>`, and from the home folder plain `lore task next` when the machine has one org setup), and `task claim`, `release` and `heartbeat` work on its signals ref; `inbox` and `mission status` need a code repo linked to the folder (`lore org link --org <org> --project <folder>`) |
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
lore-store.yaml

# alice, in website
$ lore sync
lore: pushed store notes: commit 05513aa to branch main (4 commits, 5 row files)
store notes: pushed (sent 4, received 0, merged 0, refused 0); rows: 5 sent
$ lore store use --path ~/code/notes --yes
✓ store notes now uses your clone ~/code/notes (recorded on this machine only)
  before: ~/.lore/stores/acme/notes
  lore's earlier clone ~/.lore/stores/acme/notes is no longer used and stays on disk; remove it when you no longer need it
$ lore store list
stores:
  NAME   URL                        FOLDER  READ_ONLY  BRANCH  PERSONAL  SOURCE       VISIBILITY  REACH       ADDRESS  AUDIENCE
  notes  git@github.com:acme/notes          false      main    false     stores.yaml  internal    local_copy           
count: 1
$ lore path --id tsk_2ad3…f891
~/code/notes/_store/tasks/tsk_2ad3…f891.json
$ lore store use --default notes --yes
✓ store notes uses lore's own clone again: ~/.lore/stores/acme/notes (the next command that needs it clones it if it is missing)
  before: ~/code/notes (your clone, left as it is)
```
<!-- /example -->

## What lore never does in a store clone

- **A read never pushes and never sets up git.** A command that only reads (`list`, `show`, `search`, `where`, `doctor`) may fetch the store and fast-forward lore's clone, but it never pushes; when commits wait, it says `N local commit(s) wait to be pushed; a read never pushes: run lore sync`. It never installs the merge driver, hooks or your clone identity either; at most it prints one line naming the command that will (`lore sync`, `lore fix`, or the next write).
- **A read in a new folder writes nothing.** A folder's project row is made by the first write in it, in that write's commit (`lore: memory add mem_… (+ actor act_…, project prj_…)`; its own `lore: project add prj_…` commit when the write names no project). Every read before it (a list, `lore search`, `lore context`, `lore sql`, `lore whoami`, `lore doctor`) and every preview (`--dry-run` of any command, `lore render --check`, `--report`, `--trend`) leaves the clone exactly as it was: no commit, no file, no push.
- **A read at the clone's root reads every folder.** The root of a store clone holds every folder's rows and is no project's folder, so a list or search there with no `--project` (`lore pattern list`, `lore tag list`, `lore repo list`, `lore link list`, the other kinds too) shows every folder's rows, as `lore memory list` does, and never makes a project row (`lore repo list` there, and under `--setup` from any folder, lists the repos recorded in every folder of the org's stores). `lore project show` with no name refuses there, saying the folder is the root of a store, and names the way out (`lore project show --id <name>`, `--store <store>`, `lore setup check --setup <name>`).
- **It never touches your global git config.** The identity, merge driver and hook lore needs are written into the clone's own `.git/config`, `.git/info/attributes` and `.git/hooks/pre-commit`, each announced the first time ([Plain git in the store](editing-by-hand.md#plain-git-in-the-store)).
- **The clone's git identity is always a person.** lore writes your name and email (in an org store, your identity in that org) as the clone's `user.name` and `user.email`, never an agent's. When an agent writes for you, lore's commit names only you (author, committer and message); the agent is credited on the rows it writes. `lore doctor` warns (`agent_commits`) about commits an older lore signed as an agent and prints how to rewrite them. `lore doctor` flags a clone whose identity is not yours (`store_identity`) with the exact command, and `lore fix` corrects it.
- **It never deletes a file of yours.** Files that are not rows are reported by `lore doctor` (`store_files`) and left alone.

## Lore keeps the clone packed

Each write is one commit, and git stores every new commit, folder tree and row as its own small file until something packs them; git's own automatic clean-up waits for thousands of those files, so a busy store's clone used to grow to many times the size of its rows. Lore now packs its clones itself:

- **At most once a day, in the background.** After any command, when a clone's last pass is older than `store.maintenance_every` (default `24h`), lore starts one `lore store maintain --all --due` in the background and the command you ran ends as usual. The pass packs the loose objects (and rolls small packs into bigger ones), removes unreachable objects older than two weeks, and writes git's commit-graph with changed-path filters, which makes per-row history (`lore task history --id <id> --git`) and lore's checks of who changed what faster. A clone lore just made is packed already; its first pass is a day later, and a small clone with nothing to pack is only marked as done.
- **It never makes a command wait.** A second pass on the same clone skips at once; the pass never waits for the store lock (when a command holds it, the pass still packs and leaves the ref tidy-up for the next day). It changes no row, no branch and nothing on the remote, and runs with one pack thread.
- **Git's own auto clean-up is off in the clone** (`gc.auto=0`, `maintenance.auto=false`), so git never starts one in the middle of a lore write.
- **Run it now:** `lore store maintain --all` packs every clone and prints each one's size before and after; `lore store maintain --store <name>` packs one (a store name, its url, or its folder under `~/.lore/stores`, such as `acme/notes`). `lore doctor` shows a `store_maintenance` note for a clone whose `.git` is over twice the size of its packs (its size, packs, loose objects and last pass), with that command.
- **Turn it off** for this machine with `store: {maintenance: off}` in `~/.lore/config.yaml` ([config](../reference/config.md)); a person's own clone (`lore store use`) is packed only when named with `--store`.

## Blobless clones (opt-in)

A store's clone holds every version of every row since the store began. With `store: {clone_filter: blob:none}` in `~/.lore/config.yaml` ([config](../reference/config.md)), every store clone this machine makes from then on is **blobless**: it holds every commit and folder tree (so ancestry, merges and lore's checks of who changed what work as before) but only the row files it checks out; an older version is fetched from the store's remote the first time a read needs it. It is never a shallow clone (`--depth`), which would break that history.

- **New history comes complete.** Every later fetch brings new commits with all their files, so a blobless clone only ever lacks versions older than the clone itself.
- **Missing versions are fetched in one request**, never one at a time: lore lists what a read lacks and asks the remote for all of it at once.
- **Offline it says so.** With `LORE_OFFLINE=1` (or no network), a read that needs an old version the clone lacks, such as `lore task history --id <id> --git`, stops with `E_OFFLINE` and names `lore store prefetch --store <name>`; it never prints fewer versions. Reads of the current rows (`show`, `list`, `search`, `ask`) never need the network.
- **Before going offline,** `lore store prefetch --store <name>` fetches every old version the clone lacks, in one request; a full clone has nothing to fetch.
- **Existing clones stay as they are.** A full clone cannot drop its old versions in place; `lore store reclone --store <name> --filter blob:none` makes it again blobless, and `--filter none` makes a blobless one full again. The new clone takes over lore's state of the old one (its saved check of who changed what, the remote tip it saw, its claims), and the old one is kept under `~/.lore/cache/replaced-clones/` for a week, then the daily pass removes it. A clone with commits the remote does not have yet is refused: `lore sync` first.
- **The first read of a new blobless clone still fetches most old versions.** Lore checks every commit of the store's history for who wrote what when it first reads a new clone, and that check reads every version of every row, so today it fetches them (in one request) and the clone ends up about as big as a full one. A clone made by `lore store reclone` keeps the old clone's saved check and skips that. This is why the setting is off by default; it becomes the default once that first check no longer reads old versions.
- **Moving a store** (`lore store set-url`) first fetches every old version into a blobless clone, since the store's new address starts empty and the old one is the only place they live; offline it changes nothing.
- **A server without partial clone support** sends everything; lore notices and the clone is a full one.
- **Every machine of a team should run lore 0.1.54 or later** before anyone turns this on: an older lore read a version a blobless clone lacks as no such version.

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
- **A folder named like a kind of row is refused.** `lore store folder add --store notes --folder tasks` names the reserved names; pick another name.
- **A folder another store of the org already holds is refused.** Folder names are unique across an org's stores (as `lore org link` refuses a duplicate): `lore store folder add --store <store> --folder acme` on one of the org's stores when the org's client store already has `acme/` answers `E_INVALID_INPUT` naming that store; write there with `--to <that store>/acme`, move the folder over keeping its name with `lore move --from <client-store>/acme --to <store>/acme --dry-run` (the hint prints it), or pick another name.

## Related

- [Editing by hand](editing-by-hand.md)
- [Edit a row in your editor](../knowledge/editing-rows.md)
- [Where lore keeps things](../operations/where-lore-keeps-things.md)
- [Your stores: list, move, retire](stores.md)
- [Identity and stores](../team/identity-and-stores.md)
