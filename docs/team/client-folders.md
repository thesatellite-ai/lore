# Client folders from the parent folder

**In short:** an admin can make, list, remove and attach the team store's client folders from the folder that holds the client repos, even when that folder is not a git repo. `lore store folder add|list|remove` need only the setup the folder belongs to; a client repo then attaches its folder with `lore org link`, without any lore files in it.

You want this page when you run a team (an `admin` setup) and keep each client's repos under one parent directory: you make a folder in the team store for every client before any of their repos exists, and see at a glance which folders exist and which repo attaches which. Every output below is a real run (`tests/team/docs/run-examples.sh client_folders`) against a fake GitHub.

## Before you start

- An **admin setup** whose `folders:` cover the parent folder (here `~/work/clients/**`, which covers `~/work/clients` itself). From anywhere else, add `--setup <the admin setup>`.
- The org's store exists (`lore setup` makes it, or `lore store init <org> <url>`).

## Step 1 — Make the client folders from the parent folder

Tomas keeps his clients under `~/work/clients`, a plain folder. `team` is the org store of the setup this folder is in; the store's url works too. Nothing is written to any repo: the folder is created in the store, committed and pushed.

<!-- example: client-folders#from-the-parent-folder -->
```console
# tomas, in clients
$ lore store list
stores:
  NAME        URL                          FOLDER  READ_ONLY  BRANCH  PERSONAL  SOURCE  VISIBILITY  REACH       ADDRESS
  agency      git@github.com:agency/notes          false      main    false     org     internal    local_copy  
  agency/org  git@github.com:agency/notes  org     false      main    false     org     internal    local_copy  
count: 2
$ lore store folder add team client_north
✓ created folder client_north in agency (prj_7a67…9504; shown only in repos that attach it)
$ lore store folder add team client_south
✓ created folder client_south in agency (prj_2ad3…f891; shown only in repos that attach it)
$ lore store folder add git@github.com:agency/notes client_east
✓ created folder client_east in git@github.com:agency/notes (prj_162d…9666; shown only in repos that attach it)
```
<!-- /example -->

## Step 2 — List the folders

`lore store folder list` lists every store the setup sees (with no store named), or one store: each folder with its id, who created it and when, whether the repo you are in attaches it, and how many row files it holds. `--json` gives the same rows.

<!-- example: client-folders#list-the-folders -->
```console
# tomas, in clients
$ lore store folder list team
store agency folders:
  FOLDER        ID                                    CREATED BY   CREATED               ATTACHED HERE  ROWS
  client_east   prj_162d…9666  Tomas Weber  2026-11-02T10:00:20Z  no             0
  client_north  prj_7a67…9504  Tomas Weber  2026-11-02T10:00:10Z  no             0
  client_south  prj_2ad3…f891  Tomas Weber  2026-11-02T10:00:15Z  no             0
```
<!-- /example -->

## Step 3 — What needs a repo

Store names from `.lore/stores.yaml` exist only inside the repo that lists them, and `lore store init|add|remove|set-url` edit that file. Outside a repo they say so and name what works here:

<!-- example: client-folders#names-that-need-a-repo -->
```console
# tomas, in clients
$ lore store folder add golang tips
ERROR: [E_NOT_FOUND] store folder add: no store named golang here: golang would come from a repo's .lore/stores.yaml, and ~/work/clients is not inside a git repo
  hint: here name team, agency, git@github.com:agency/notes; or cd into the repo whose .lore/stores.yaml attaches golang
(exit 27)
$ lore store add golang git@github.com:agency/notes
ERROR: [E_NOT_A_STORE] store add: it edits a repo's .lore/stores.yaml, and ~/work/clients is not inside a git repo
  hint: cd into the repo that should attach the store; outside a repo, `lore store folder add team <folder>` and `lore store list` work from the setup alone (`lore store init me` makes the personal store)
(exit 19)
```
<!-- /example -->

## Step 4 — A client repo attaches its folder

Once North's repo exists, link it to its folder. `lore store folder list` run inside it shows the folder as attached:

<!-- example: client-folders#a-client-repo-attaches-one -->
```console
# tomas, in clients/north-web
$ lore org link agency --project client_north --yes
This marks github.com/client/north-web as a CLIENT repo of agency: internal notes default to github.com/agency/notes client_north/,
and the leak guards turn on for everyone in agency.
  link: project prj_54fe…28f2 at github.com/client/north-web → github.com/agency/notes@main:client_north/ (default=false)
✓ github.com/client/north-web (prj_54fe…28f2) linked to github.com/agency/notes client_north/ (pushed)
  nothing was written to this repo
$ lore store folder list team
store agency folders:
  FOLDER        ID                                    CREATED BY   CREATED               ATTACHED HERE  ROWS
  client_east   prj_162d…9666  Tomas Weber  2026-11-02T10:00:20Z  no             0
  client_north  prj_7a67…9504  Tomas Weber  2026-11-02T10:00:10Z  yes            0
  client_south  prj_2ad3…f891  Tomas Weber  2026-11-02T10:00:15Z  no             0
```
<!-- /example -->

## Step 5 — A client repo that holds no lore files

A client's repo must never hold lore files. Link it without `lore init`: the link is made for the repo's remote (with several remotes, `--remote` picks one) and kept only in the store, so nothing is written into the repo: no `.lore/`, no `.gitignore` lines, no `.gitattributes`, no git hooks. Every later command in that repo finds the link from the store by the remote; its rows go to the linked folder, and its cache lives under `~/.lore`. `lore doctor` there says so.

<!-- example: client-folders#a-client-repo-with-no-lore-files -->
```console
# tomas, in clients/south-app
$ lore org link agency --project client_south --yes
This marks github.com/client/south-app as a CLIENT repo of agency: internal notes default to github.com/agency/notes client_south/,
and the leak guards turn on for everyone in agency.
  link: project prj_05e9…4603 at github.com/client/south-app → github.com/agency/notes@main:client_south/ (default=false)
✓ github.com/client/south-app (prj_05e9…4603) linked to github.com/agency/notes client_south/ (pushed)
  nothing was written to this repo
$ lore task add 'set up the staging deploy'
✓ tsk_c0f3…b5b8 — set up the staging deploy [medium]
  filed: defaults.client_repos.task = agency (personal config) → the linked project client_south/
$ lore task list
○ tsk_c0f3…b5b8 set up the staging deploy [medium]  [agency/client_south]
$ git status --short --ignored && ls -A
.git
README.md
```
<!-- /example -->

## Step 6 — Remove a folder made by mistake

Folder names are unique across all of an org's stores, so a folder made in the wrong store blocks that name everywhere else. `lore store folder remove <store> <folder>` removes an empty folder (no rows, no repo links) after asking (`--yes` without a terminal), as one store commit, pushed per the store's push mode; `lore store folder list` stops showing it and the name is free again. A folder that holds rows or repo links is refused with the counts and the next step:

<!-- example: client-folders#remove-a-folder-made-by-mistake -->
```console
# tomas, in clients
$ lore store folder add team client_old
✓ created folder client_old in agency (prj_76bd…3f10; shown only in repos that attach it)
$ lore store folder remove team client_old --yes
✓ removed folder client_old (prj_76bd…3f10) from agency
$ lore store folder remove team client_north --yes
ERROR: [E_REFUSED] store folder remove: folder client_north of agency holds 1 repo link; nothing was removed
  hint: unlink each repo with `lore org unlink <org> --project client_north` run in that repo; or remove it with everything in it: `lore store folder remove agency client_north --purge` (the rows go to the store's trash first)
(exit 23)
```
<!-- /example -->

- Keep what is in it: move the rows with `lore move <id> --to <store>/<folder>`, and unlink each repo with `lore org unlink <org> --project <folder>` run in that repo; then remove the empty folder.
- Or remove it with everything in it: `lore store folder remove <store> <folder> --purge --confirm`. The live rows go to the store's trash first (one `bat_` batch), the repo links and the folder's id are dropped. To bring the rows back, make the folder again with `lore store folder add <store> <folder>` (it gets the same id), attach it to a repo, then `lore trash restore <bat_…>`.
- The org folder (`org_folder`, which holds the roster, teams and `lore-org.yaml`) is never removed.
- For an org store only the org's admin removes folders; for any other store, whoever can push to it. `agents.person_only` can keep coding agents out (`lore policy set agents.person_only '[store.folder.remove]'`).
- A repo whose `.lore/stores.yaml` still attaches a removed folder gets a `removed_store_folder` warning from `lore doctor`, naming the commit that removed it and `lore store remove <name>`.

## Step 7 — Move a whole repo into its folder

A repo that kept its notes in its own `.lore/data` (or a lore 0.1.17 repo, with rows on its `refs/lore/live` ref or only in its `.lore/lore.db`) moves into a store folder in one pass: `lore store adopt <store>/<folder> --from <repo>`. Every row keeps its id, its HLC and its links to other rows (parent tasks, comments, tags); `project_id` becomes the folder's project, the repo's own project row stays behind. It is one store commit and one push however many rows there are, it asks first (`--yes` without a terminal), and it runs from any folder of the setup, like the other store commands:

<!-- example: client-folders#adopt-a-whole-repo -->
```console
# tomas, in clients
$ lore store adopt team/client_east --from east-api --dry-run
would adopt 5 rows of ~/work/clients/east-api into agency/client_east (prj_162d…9666), ids and HLCs kept
  actors 1, memories 1, policies 1, task_lists 1, tasks 1
  from the repo's live ref (refs/lore/live): 2
  left in the repo: projects: 1 row (the folder is its own project)
  left in the repo: repos: 1 row (the code repo's own row; a folder links repos with lore org link)
$ lore store adopt team/client_east --from east-api --yes
✓ adopted 5 rows of ~/work/clients/east-api into agency/client_east (prj_162d…9666), ids and HLCs kept
  actors 1, memories 1, policies 1, task_lists 1, tasks 1
  from the repo's live ref (refs/lore/live): 2
  left in the repo: projects: 1 row (the folder is its own project)
  left in the repo: repos: 1 row (the code repo's own row; a folder links repos with lore org link)
$ lore store adopt team/client_east --from east-api --yes
✓ nothing to adopt: agency/client_east already holds every row of ~/work/clients/east-api (no commit)
  already there: 5 unchanged, 0 newer or deleted in the folder (kept)
  left in the repo: projects: 1 row (the folder is its own project)
  left in the repo: repos: 1 row (the code repo's own row; a folder links repos with lore org link)

# tomas, in clients/east-api
$ git status --short
```
<!-- /example -->

- The repo is not changed: run it again and it writes nothing (no commit), so it is safe to rerun after a failed push or once the repo got new notes (only rows newer than the folder's copy are written; a row edited or deleted in the folder since is kept).
- `--dry-run` counts what would move, per table, and what stays in the repo and why.
- `--remove-source` then deletes the adopted row files from the repo's `.lore/data` as an ordinary diff you review and commit (the project's `_meta.json` and project row stay). Rows that were only on a lore 0.1.17 live ref stay on that ref.
- A v0.1.17 repo needs no `lore migrate run` first: its rows are read as migrate would write them (format steps, tags folded into `tag_ids`, an agent's live stamp `…/agent` read as a stable actor).
- Moving a single row is still `lore move <id> --to <store>/<folder>`.

## An org with several stores

An org entry lists every store (`lore org refresh <org>` takes them from the store's `lore-org.yaml`). `lore store list` and `lore store folder list` show each of them once; the org's other stores are named by their url. Every store command that takes a store takes that url: `lore store folder add|list|remove <url>`, `lore store show|set|visibility|compact <url>`, and `lore org link <org> --project <folder> --store <url>`. A brand-new empty repo becomes a lore store with the first write to it: the `lore-store.yaml` marker goes into that same commit. A repo that an older lore left with a folder's `_meta.json` and no marker is adopted the same way on its next write.

## Which store commands work outside a repo

| Command | Outside a repo |
|---|---|
| `lore store list` | the setup's stores: the org's (`<org>`, `<org>/org`, each other store by its url) and `me` |
| `lore store folder add <store> <folder>` | `team`, `me`, the org's name, or the org store's url |
| `lore store folder list [store]` | same names; every store of the setup when none is named |
| `lore store folder remove <store> <folder>` | same names |
| `lore store adopt <store>/<folder> --from <repo>` | same names (without `--from`, the repo you run it in) |
| `lore store visibility`, `lore store set`, `lore store compact` | same names |
| `lore store show`, `open`, `forget`, `doctor` | always (they read this machine's clones) |
| `lore store init <name>`, `add`, `remove`, `set-url` | need a repo: they edit its `.lore/stores.yaml` (`lore store init me` works anywhere) |
| `lore org link`, `unlink`, `set`, `use` | need a git repo: they are about that repo (`lore org link` needs no `lore init`) |
| `lore org create`, `import`, `rename`, `refresh`, `lore team invite …` | always |

A command that only changes a store also leaves the repo you run it in alone: it does not touch that repo's `.gitignore`, `.gitattributes` or git hooks.
