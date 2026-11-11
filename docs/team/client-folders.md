# Client folders from the parent folder

**In short:** an admin can make, list and attach the team store's client folders from the folder that holds the client repos, even when that folder is not a git repo. `lore store folder add team <folder>` and `lore store folder list` need only the setup the folder belongs to; a client repo then attaches its folder with `lore org link`.

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
  NAME        URL         FOLDER  READ_ONLY  BRANCH  PERSONAL  SOURCE  VISIBILITY  REACH       ADDRESS
  agency      <redacted>          false      main    false     org     internal    local_copy  
  agency/org  <redacted>  org     false      main    false     org     internal    local_copy  
count: 2
$ lore store folder add team client_north
✓ created folder client_north in agency (prj_7a67…9504; shown only in repos that attach it)
$ lore store folder add team client_south
✓ created folder client_south in agency (prj_2ad3…f891; shown only in repos that attach it)
$ lore store folder add git@github.com:agency/notes client_east
✓ created folder client_east in agency (prj_162d…9666; shown only in repos that attach it)
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
✓ github.com/client/north-web (prj_54fe…28f2, github.com/client/north-web) linked to github.com/agency/notes client_north/ (pushed)
  nothing was written to this repo
$ lore store folder list team
store agency folders:
  FOLDER        ID                                    CREATED BY   CREATED               ATTACHED HERE  ROWS
  client_east   prj_162d…9666  Tomas Weber  2026-11-02T10:00:20Z  no             0
  client_north  prj_7a67…9504  Tomas Weber  2026-11-02T10:00:10Z  yes            0
  client_south  prj_2ad3…f891  Tomas Weber  2026-11-02T10:00:15Z  no             0
```
<!-- /example -->

## Which store commands work outside a repo

| Command | Outside a repo |
|---|---|
| `lore store list` | the setup's stores: the org's (`<org>`, `<org>/org`) and `me` |
| `lore store folder add <store> <folder>` | `team`, `me`, the org's name, or the org store's url |
| `lore store folder list [store]` | same names; every store of the setup when none is named |
| `lore store visibility`, `lore store set`, `lore store compact` | same names |
| `lore store show`, `open`, `forget`, `doctor` | always (they read this machine's clones) |
| `lore store init <name>`, `add`, `remove`, `set-url` | need a repo: they edit its `.lore/stores.yaml` (`lore store init me` works anywhere) |
| `lore org link`, `unlink`, `set`, `use` | need a repo: they are about that repo |
| `lore org create`, `import`, `rename`, `refresh`, `lore team invite …` | always |

A command that only changes a store also leaves the repo you run it in alone: it does not touch that repo's `.gitignore`, `.gitattributes` or git hooks.
