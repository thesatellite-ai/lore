# Client folders from the parent folder

**In short:** an admin can make, list, remove and attach the team store's client folders from the folder that holds the client repos, even when that folder is not a git repo. `lore store folder add|list|remove` need only the setup the folder belongs to; a client repo then attaches its folder with `lore org link`, without any lore files in it.

You want this page when you run a team (an `admin` setup) and keep each client's repos under one parent directory: you make a folder in the team store for every client before any of their repos exists, and see at a glance which folders exist and which repo attaches which. Every output below is a real run (`tests/team/docs/run-examples.sh client_folders`) against a fake GitHub.

## Before you start

- An **admin setup** whose `folders:` cover the parent folder (here `~/work/clients/**`, which covers `~/work/clients` itself). From anywhere else, add `--setup <the admin setup>`.
- The org's store exists (`lore setup` makes it, or `lore store init --name <org> --url <url>`).

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
$ lore store folder add --store team --folder client_north
committed locally to store agency; lore sync sends it
✓ created folder client_north in agency (prj_7a67…9504; shown only in repos that attach it)
$ lore store folder add --store team --folder client_south
committed locally to store agency; lore sync sends it
✓ created folder client_south in agency (prj_2ad3…f891; shown only in repos that attach it)
$ lore store folder add --store git@github.com:agency/notes --folder client_east
committed locally to store agency; lore sync sends it
✓ created folder client_east in agency (prj_162d…9666; shown only in repos that attach it)
```
<!-- /example -->

## Step 2 — List the folders

`lore store folder list` lists every store the setup sees (with no store named), or one store: each folder with its id, who created it and when, whether the repo you are in attaches it, and how many row files it holds. `--json` gives the same rows.

<!-- example: client-folders#list-the-folders -->
```console
# tomas, in clients
$ lore store folder list --store team
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
$ lore store folder add --store golang --folder tips
ERROR: [E_NOT_FOUND] store folder add: no store named golang here: golang would come from a repo's .lore/stores.yaml, and ~/work/clients is not inside a git repo
  hint: here name team, agency, git@github.com:agency/notes; or cd into the repo whose .lore/stores.yaml attaches golang
(exit 27)
$ lore store add --name golang --url git@github.com:agency/notes
ERROR: [E_NOT_A_STORE] store add: it edits a repo's .lore/stores.yaml, and ~/work/clients is not inside a git repo
  hint: cd into the repo that should attach the store; outside a repo, `lore store folder add --store team --folder <folder>` and `lore store list` work from the setup alone (`lore store init --name me` makes the personal store)
(exit 19)
```
<!-- /example -->

## Step 4 — A client repo attaches its folder

Once North's repo exists, link it to its folder. `lore store folder list` run inside it shows the folder as attached:

<!-- example: client-folders#a-client-repo-attaches-one -->
```console
# tomas, in clients/north-web
$ lore org link --org agency --project client_north --yes
This marks github.com/client/north-web as a CLIENT repo of agency: internal notes default to github.com/agency/notes client_north/,
and the leak guards turn on for everyone in agency.
  link: project prj_54fe…28f2 at github.com/client/north-web → github.com/agency/notes@main:client_north/ (default=false)
✓ github.com/client/north-web (prj_54fe…28f2) linked to github.com/agency/notes client_north/ (committed in the local clone; `lore sync` sends it)
  nothing was written to this repo
$ lore store folder list --store team
store agency folders:
  FOLDER        ID                                    CREATED BY   CREATED               ATTACHED HERE  ROWS
  client_east   prj_162d…9666  Tomas Weber  2026-11-02T10:00:20Z  no             0
  client_north  prj_7a67…9504  Tomas Weber  2026-11-02T10:00:10Z  yes            0
  client_south  prj_2ad3…f891  Tomas Weber  2026-11-02T10:00:15Z  no             0
```
<!-- /example -->

## Step 5 — A client repo that holds no lore files

A client's repo must never hold lore files. Link it without `lore init`: the link is made for the repo's remote (with several remotes, `--remote` picks one) and kept only in the store, so nothing is written into the repo: no `.lore/`, no `.gitignore` lines, no `.gitattributes`, no git hooks. Every later command in that repo finds the link from the store by the remote; its rows go to the linked folder, and its cache lives under `~/.lore`. `lore doctor` there says so. If someone later runs `lore init` in that repo (an own repo that should hold lore files after all), the project keeps the link's id (`prj_…` above) instead of minting a new one, and init says `the org store already links <remote>`, so the folder's notes still reach the repo. Commands run there keep it so: `lore render` writes nothing and says the repo is a client repo (agents read its notes with `lore context --for-file <path>` or the agent hook), a write refused there (a secret prompt) leaves no `.lore/` behind, and a package folder set up as its own project uses the repo's link. A teammate's first command after the admin's `lore org link` fetches the org's stores once, so the new link is seen at once.

<!-- example: client-folders#a-client-repo-with-no-lore-files -->
```console
# tomas, in clients/south-app
$ lore org link --org agency --project client_south --yes
This marks github.com/client/south-app as a CLIENT repo of agency: internal notes default to github.com/agency/notes client_south/,
and the leak guards turn on for everyone in agency.
  link: project prj_05e9…4603 at github.com/client/south-app → github.com/agency/notes@main:client_south/ (default=false)
✓ github.com/client/south-app (prj_05e9…4603) linked to github.com/agency/notes client_south/ (committed in the local clone; `lore sync` sends it)
  nothing was written to this repo
$ lore task add --title 'set up the staging deploy'
lore: committed locally to store agency; lore sync sends it
lore: wrote 4 row file(s) in ~/.lore/stores/agency/notes/client_south (store agency)
✓ tsk_c0f3…b5b8 — set up the staging deploy [medium]
  filed: defaults.client_repos.task = agency (personal config) → the linked project client_south/
  created tasklist tlt_76bd…3f10 "default" for tasks added without --tasklist
$ lore task list
○ tsk_c0f3…b5b8 set up the staging deploy [medium]  [agency/client_south]
$ git status --short --ignored && ls -A
.git
README.md
```
<!-- /example -->

## Step 6 — Remove a folder made by mistake

Folder names are unique across all of an org's stores, so a folder made in the wrong store blocks that name everywhere else. `lore store folder remove --store <store> --folder <folder>` removes an empty folder (no rows, no repo links) after asking (`--yes` without a terminal), as one store commit, pushed per the store's push mode; `lore store folder list` stops showing it and the name is free again. A folder that holds rows or repo links is refused with the counts and the next step:

<!-- example: client-folders#remove-a-folder-made-by-mistake -->
```console
# tomas, in clients
$ lore store folder add --store team --folder client_old
committed locally to store agency; lore sync sends it
✓ created folder client_old in agency (prj_d431…313a; shown only in repos that attach it)
$ lore store folder remove --store team --folder client_old --yes
✓ removed folder client_old (prj_d431…313a) from agency
  committed in this machine's copy of the store; push manual sends it with the store's next push (`lore sync` sends it now)
$ lore store folder remove --store team --folder client_north --yes
ERROR: [E_REFUSED] store folder remove: folder client_north of agency holds 1 repo link; nothing was removed
  hint: unlink each repo with `lore org unlink --org <org> --project client_north` run in that repo; or remove it with everything in it: `lore store folder remove --store agency --folder client_north --purge` (the rows go to the store's trash first)
(exit 23)
```
<!-- /example -->

- Keep what is in it: move the rows with `lore move --id <id> --to <store>/<folder>`, and unlink each repo with `lore org unlink --org <org> --project <folder>` run in that repo; then remove the empty folder.
- Or remove it with everything in it: `lore store folder remove --store <store> --folder <folder> --purge --confirm`. The live rows go to the store's trash first (one `bat_` batch), the repo links and the folder's id are dropped. To bring the rows back, make the folder again with `lore store folder add --store <store> --folder <folder>` (it gets the same id), attach it to a repo, then `lore trash restore --id <bat_…>`.
- The org folder (`org_folder`, which holds the roster, teams and `lore-org.yaml`) is never removed.
- For an org store only the org's admin removes folders; for any other store, whoever can push to it. `agents.person_only` can keep coding agents out (`lore policy set --key agents.person_only --value '[store.folder.remove]'`).
- A repo whose `.lore/stores.yaml` still attaches a removed folder gets a `removed_store_folder` warning from `lore doctor`, naming the commit that removed it and `lore store remove --name <name>`.

## Step 7 — Move a whole repo into its folder

A repo that kept its notes in its own `.lore/data` (or a lore 0.1.17 repo, with rows on its `refs/lore/live` ref or only in its `.lore/lore.db`) moves into a store folder in one pass: `lore store adopt --target <store>/<folder> --from <repo>`. Every row keeps its id, its HLC and its links to other rows (parent tasks, comments, tags); `project_id` becomes the folder's project, the repo's own project row stays behind. It is one store commit and one push however many rows there are, it asks first (`--yes` without a terminal), and it runs from any folder of the setup, like the other store commands:

<!-- example: client-folders#adopt-a-whole-repo -->
```console
# tomas, in clients
$ lore store adopt --target team/client_east --from east-api --dry-run
would adopt 5 rows of ~/work/clients/east-api into agency/client_east (prj_162d…9666), ids and HLCs kept
  actors 1, memories 1, policies 1, task_lists 1, tasks 1
  from the repo's live ref (refs/lore/live): 2
  left in the repo: projects: 1 row (the folder is its own project)
  left in the repo: repos: 1 row (the code repo's own row; a folder links repos with lore org link)
$ lore store adopt --target team/client_east --from east-api --yes
✓ adopted 5 rows of ~/work/clients/east-api into agency/client_east (prj_162d…9666), ids and HLCs kept
  actors 1, memories 1, policies 1, task_lists 1, tasks 1
  from the repo's live ref (refs/lore/live): 2
  left in the repo: projects: 1 row (the folder is its own project)
  left in the repo: repos: 1 row (the code repo's own row; a folder links repos with lore org link)
  committed in this machine's copy of the store; push manual sends it with the store's next push (`lore sync` sends it now)
$ lore store adopt --target team/client_east --from east-api --yes
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
- Moving a single row is still `lore move --id <id> --to <store>/<folder>`.

### After an adopt: one person, two actor ids, and empty refs

An older lore adopted a v0.1.17 repo's rows as they were. Two things can be left behind, and both are repaired in place, from the client repo linked to the folder (or any repo that attaches it):

- **The repo's own actor id for someone already on the roster.** `lore doctor` names the pair; `lore actor merge --from <old-id> --into <roster-id>` rewrites every row naming the old id, all of a store's rows as one store commit and one push (1,000 rows take seconds), lists each store or folder it edits with its count (`notes-two/client_a: 66 edited`), and marks the old id `merged_into` the kept one, also on the org roster. A row it cannot edit does not stop it: the others are written, each row left is listed with its reason and fix, the command exits non-zero, and running it again writes only what is left. History stamps (`created_by`, `updated_by`) keep the old id as they were written; reads show the kept id for it: `lore task list --created-by <roster-id>` lists the rows made under the old one.
- **`tasklist_id: ""` (or another ref holding an empty string).** lore 0.1.17 wrote `""` for "none". Reads and edits treat it as none, so every edit works; `lore doctor` names the rows (`empty_ref`) and `lore doctor --fix` writes null in them, one store commit per store. `lore store adopt` and `lore migrate run` write null from the start. To take a task off its list on purpose: `lore task edit --id <id> --clear-tasklist`.

## An org with several stores

An org entry lists every store (`lore org refresh --org <org>` takes them from the store's `lore-org.yaml`; without `<org>` it refreshes the only org entry on the machine, else the org of the active setup of the folder, else it names the org entries and the command to run; `lore org link` and `lore org unlink` default the same way). `lore store list` and `lore store folder list` show each of them once; the org's other stores are named by their short name, the repo name of the url without `.git` (`git@github.com:agency/lore-x.git` is `lore-x`), with the url in its own column, and each `store … folders:` block is headed by the store that holds those folders (the org's name only for the store holding the org folder), from any repo. lore-org.yaml can set another short name with `stores[].name`, and must when two stores of the org have the same repo name: lore refuses such a file and names both urls. Every store command that takes a store takes the short name or the url: `lore store folder add|list|remove lore-x`, `lore store show|set|visibility|compact lore-x`, `--to lore-x/<folder>`, and `lore org link --org <org> --project <folder> --store <url>`. Output names the store by its short name. A brand-new empty repo becomes a lore store with the first write to it: the `lore-store.yaml` marker goes into that same commit. A repo that an older lore left with a folder's `_meta.json` and no marker is adopted the same way on its next write. `lore org store add` with a brand-new empty repo is followed by `lore org link … --store <url>`: that link is the store's first write.

A list or search with `--store <store>` reads that store and every folder of it: `lore memory list --store acme` lists the notes of `acme/org` and of each `acme/<project>` folder this repo reads; `--store acme/org` reads that one folder.

## Which store commands work outside a repo

| Command | Outside a repo |
|---|---|
| `lore store list` | the setup's stores: the org's (`<org>`, `<org>/org`, each other store by its short name) and `me` |
| `lore store folder add --store <store> --folder <folder>` | `team`, `me`, the org's name, an org store's short name, or its url |
| `lore store folder list [--store store]` | same names; every store of the setup when none is named |
| `lore store folder remove --store <store> --folder <folder>` | same names |
| `lore store adopt --target <store>/<folder> --from <repo>` | same names (without `--from`, the repo you run it in) |
| `lore store visibility`, `lore store set`, `lore store compact` | same names |
| `lore store show`, `open`, `forget`, `doctor` | always (they read this machine's clones) |
| `lore store init --name <name>`, `add`, `remove`, `set-url` | need a repo: they edit its `.lore/stores.yaml` (`lore store init --name me` works anywhere) |
| `lore org link`, `unlink`, `set`, `use` | need a git repo: they are about that repo (`lore org link` needs no `lore init`) |
| `lore org create`, `import`, `rename`, `refresh`, `lore team invite --email …` | always |
| a write with `--to <store>` or `--to <store>/<folder>` (`lore rule add --to acme/org …`, `lore memory add --to me …`) | the same names: it lands in that store folder (a new folder asks first, or `--create`); a write with no `--to` needs a repo, and in the home folder the hint names `--to` instead of `git init` |

A command that only changes a store also leaves the repo you run it in alone: it does not touch that repo's `.gitignore`, `.gitattributes` or git hooks.
