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
  NAME        URL                          FOLDER  READ_ONLY  BRANCH  PERSONAL  SOURCE  VISIBILITY  REACH       ADDRESS  AUDIENCE
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

Store names from `.lore/stores.yaml` exist only inside the repo that lists them, and `lore store init|add|remove|set-url` edit that file. Outside a repo they say so and name what works here (`lore store set-url` of an org's store, `me` or a `sto_` id works anywhere: it edits no repo file):

<!-- example: client-folders#names-that-need-a-repo -->
```console
# tomas, in clients
$ lore store folder add --store golang --folder tips
ERROR: [E_NOT_FOUND] store folder add: no store named golang here: golang would come from a repo's .lore/stores.yaml, and ~/work/clients is not inside a git repo
  hint: here name team, agency, git@github.com:agency/notes; or cd into the repo whose .lore/stores.yaml attaches golang
(exit 27)
$ lore store add --name golang --url git@github.com:agency/notes
ERROR: [E_NOT_A_STORE] the stores a repo attaches (a store command edits a repo's .lore/stores.yaml) live in each code repo: ~/work/clients is not inside a code repo
  hint: cd into the code repo that should attach (or drop) the store and run it there; --store does not apply (it edits that repo's stores.yaml); outside a repo, `lore store folder add --store team --folder <folder>` and `lore store list` work from the setup alone (`lore store init --name me` makes the personal store)
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
  no tracked file of this repo was written; the leak guard hooks (commit-msg, pre-push) are in this clone's git hooks
$ lore store folder list --store team
store agency folders:
  FOLDER        ID                                    CREATED BY   CREATED               ATTACHED HERE  ROWS
  client_east   prj_162d…9666  Tomas Weber  2026-11-02T10:00:20Z  no             0
  client_north  prj_7a67…9504  Tomas Weber  2026-11-02T10:00:10Z  yes            0
  client_south  prj_2ad3…f891  Tomas Weber  2026-11-02T10:00:15Z  no             0
```
<!-- /example -->

## Step 5 — A client repo that holds no lore files

A client's repo must never hold lore files. Link it without `lore init`: the link is made for the repo's remote (with several remotes, `--remote` picks one) and kept only in the store, so nothing is written into the repo's files: no `.lore/`, no `.gitignore` lines, no `.gitattributes`. The one thing the link adds is the leak check in the clone where you run it: a `commit-msg` and a `pre-push` hook block in `.git/hooks` and `lore.clientrepo` in `.git/config` (all outside the tracked tree: `git status` and every file of the repo stay byte-identical), so a commit there that quotes an internal note is refused, and so is a push of one committed with `--no-verify`. When the repo's hooks folder is committed with the repo (`core.hooksPath` pointing at a folder of the work tree, such as `.githooks`), lore writes nothing there, not even with `lore fix --tracked-hooks`: the link and `lore doctor` warn that commits and pushes are not checked and print the two lines to add to that folder's hooks, `lore check leaks --staged --commit-msg "$1" || exit $?` in `commit-msg` and `lore sync hook pre-push "$@" || exit $?` in `pre-push`. `lore whoami` there points at `lore doctor` for those lines, never at the link. A clone linked before the pre-push block existed holds only the commit one: `lore whoami` there says `guards on for commits only`, `lore doctor` warns, and `lore doctor --fix` adds the pre-push block (in `.git/hooks` only). The checks read only the lines the commit adds or changes (the pre-push block likewise reads only the lines the pushed commits add, and after a rebase skips the lines and commit messages that remote already holds, so a force-push of a rebased branch is not refused for text already on it), so editing a file that was committed before (a hand-written `CLAUDE.md` whose old text is now a note in the store) is not refused for that old text, and a refusal names each matching line as `<file>:<line>`. A teammate's clone gets it when they join the org or run any other lore setup step from inside it (the setup installs the guard in that clone the same way, and when it cannot, for example in a clone whose hooks folder is committed with the repo, the step stops with `E_REFUSED`, the reason and the fix: the two lines above to add, or `lore doctor --fix` once the hooks folder can be written; it never goes on with the guard off), or when they run the same `lore org link` there (or `lore doctor --fix`); in a clone made another way (a plain `git clone` after the setup) the guard is off until one of those runs there, unless the machine guard is on (below): `lore doctor` notes that the guard is off (a note, not a warning: a clone the link never ran in is not a degraded machine; a block missing from a clone the guard was on in is a warning) and `lore whoami` in that clone says `client repo  (guards OFF in this clone: …)` and names the command that installs it: `lore org link --org <org> --project <folder>` in a clone no link names yet (`lore fix` has nothing to fix in a repo with no lore data), the link's own command in a clone the store links by its remote. Every later command in that repo finds the link from the store by the remote; its rows go to the linked folder, and its cache lives under `~/.lore`. `lore doctor` there says so. If someone later runs `lore init` in that repo (an own repo that should hold lore files after all), the project keeps the link's id (`prj_…` above) instead of minting a new one, and init says `the org store already links <remote>`, so the folder's notes still reach the repo. Commands run there keep it so: `lore render` writes nothing into the repo (no LORE.md, no pointer block, no import file) and says the repo is a client repo; it writes the private render outside the checkout under `~/.lore/render/`, and `--report` and `--dry-run` show exactly that, never a LORE.md. Agents read the repo's notes with `lore context --for-file <path>` or `--for-prompt <text>` (or an agent hook that runs it), which pick from the linked folder; no tool loads the private file on its own there, since nothing of lore's goes into a client repo, and `lore setup --agent-hooks` refuses there (every agent hook lives in a settings file inside the checkout); for the always-on rules, add `lore context --hook claude_code --hook-stdin --session-start` as a SessionStart hook in your user-level tool settings (outside the repo), which serves the linked folder's must rules and critical and high hotfixes at each session start, and `lore sync` there refreshes the private file; a write refused there (a secret prompt) leaves no `.lore/` behind, and a package folder set up as its own project uses the repo's link. A teammate's first command after the admin's `lore org link` fetches the org's stores once, so the new link is seen at once. In a repo the org's `repos:` pattern claims (whoami: `client repo (guards on)`), no hint ever points at `lore init`: a refused write, `lore doctor` and the setup page's "This folder" line name `lore org link --org <org> --project <folder>` (or `--to <org>/<folder>` per write, `--store <org>/<folder>` per read) instead; when you have a personal store, a knowledge read there (`lore search`, `lore rule list`) answers from it alone and says so first (`read your personal store only, not <org>'s folders: …` with the same link and `--store` steps), so an empty answer never hides that the org's folders were not read, and `lore init` itself refuses with `E_NEEDS_CONFIRM` (it would write `.lore/`, `.gitattributes`, a `# lore` block in the client's tracked `.gitignore` listing lore's local files (`.lore/lock`, `.lore/state/`, `.lore/backups/`, the in-flight `.lore/data/**/.tmp-*` files and the 0.1.17 `.lore/lore.db*` names), its pointer into the client's `CLAUDE.md`/`AGENTS.md`, and git hooks and merge drivers in the clone's `.git`) until you pass `--yes`. It asks the same in a clone whose `.lore/` came with the checkout (lore 0.1.17 data, or a teammate's init): adopting it would still write the hooks, merge drivers and the `.gitignore` block; only a clone this machine set up before passes. Once such a repo is linked, a write with no `--to` goes to the linked folder whatever format the client's own `.lore/data` is on. `lore where` lists such a clone as using its store caches, and `lore cache prune` keeps them while the clone is on the machine.

<!-- example: client-folders#a-client-repo-with-no-lore-files -->
```console
# tomas, in clients/south-app
$ lore org link --org agency --project client_south --yes
lore: leak guard installed in this clone: ~/work/clients/south-app/.git/hooks/commit-msg, ~/work/clients/south-app/.git/hooks/pre-push (commits and pushes are checked for internal notes; teammates get it when they run lore setup or the same link in their clone)
This marks github.com/client/south-app as a CLIENT repo of agency: internal notes default to github.com/agency/notes client_south/,
and the leak guards turn on for everyone in agency.
  link: project prj_05e9…4603 at github.com/client/south-app → github.com/agency/notes@main:client_south/ (default=false)
✓ github.com/client/south-app (prj_05e9…4603) linked to github.com/agency/notes client_south/ (committed in the local clone; `lore sync` sends it)
  no tracked file of this repo was written
$ lore task add --title 'set up the staging deploy'
lore: committed locally to store agency/client_south; lore sync sends it
lore: wrote 4 row file(s) in ~/.lore/stores/agency/notes/client_south (store agency/client_south)
lore: wrote ~/.lore/stores/agency/notes/client_south/_meta.json (store agency/client_south)
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

To guard every clone on a machine, also ones lore never ran in, opt in once with `lore setup guard install` (never on by default; `--dry-run` shows what it would do). It writes a dispatcher for each git hook into `~/.lore/git-hooks/` and points your global `core.hooksPath` there. Each dispatcher first runs the hook the repo would run without it: the repo's own `.git/hooks/<hook>`, or, when a global `core.hooksPath` was already set (a hook manager's folder), that folder's hook, with the same arguments and input, and stops when it fails. Then `commit-msg` and `pre-push` run lore's leak check (`lore check leaks --staged --commit-msg`, `lore sync hook pre-push`), which does nothing in a repo that is not a client repo; a hook that already runs lore's block (a lore project, a linked clone) is not checked twice. A repo with its own local `core.hooksPath` keeps it. lore's own blocks keep going where the dispatchers run them (the clone's `.git/hooks`, or the earlier global folder), so a clone stays guarded after the machine guard is gone. `lore doctor` in a client repo says the machine guard covers it. `lore setup guard remove` sets the global `core.hooksPath` back to exactly what it was (or unsets it when none was set) and deletes the folder; a global `core.hooksPath` someone changed after the install is left as it is. `push-to-checkout`, `proc-receive` and `fsmonitor-watchman` get no dispatcher (their presence alone changes what git does), so a repo's own one of those does not run while the machine guard is on.

A repo whose `.lore/` holds only the `stores.yaml` that `lore store init` or `lore store add` wrote has no lore project of its own either, so it is linked the same way, own (`--own`) or client: after `lore org link` its writes (`lore rule add`, `lore memory add`, …) go to the linked folder and no `.lore/data` is made in the repo.

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
- A note the repo deleted, keeps in its trash or purged (`lore task delete --id <id>`, `lore trash purge --id <id> --confirm`, `lore live purge --ids <id> --confirm`) is never adopted: not counted, not copied, never listed as a near-duplicate, also while the purge is not pushed yet and with no `lore live compact` first. adopt names them as `left in the repo: tasks: 2 rows (deleted, in the trash or purged: never adopted)` and leaves their deletion on the repo's live ref; only a row the folder already holds takes the deletion.
- `--remove-source` then deletes the adopted row files from the repo's `.lore/data` as an ordinary diff you review and commit (the project's `_meta.json` and project row stay). adopt prints the two git commands that commit it; lore's pre-commit hook knows those rows now live in the store folder, so it commits the removal (saying where the rows went) instead of refusing it as rows deleted by hand, and `lore doctor` shows it as a note until it is committed, with the same git command adopt printed (naming `.lore/LORE.md` too when `--move` removed it and it is not staged yet). The adopted rows also leave the repo's live ref (`refs/lore/live`, where open work waits), in one live commit the repo's next push carries to every clone, so the repo never reads one id twice once it reads the folder; a row edited on the ref while adopt ran stays there and is named (run the same command again to take it). `--commit` makes that repo commit itself, through the repo's hooks (it refuses while other files are staged).
- `--move` is the whole move of the repo you run it in, in one command: the adoption, `--remove-source` and `lore org link` of the repo to the folder (`--own` for one of the org's own repos, as org link takes it), then the one git command left, or the commit itself with `--commit`. It also removes the repo's stale `.lore/LORE.md` when lore generated it (a linked repo renders none, and a client repo's leak guard refused each commit touching it), and in a repo still on the lore 0.1.17 format its project row and `_meta.json` once every note has left, so nothing of the old format stays and nothing is migrated; it names each file it removes and why, and the commit stages them with the row files (`git -C <repo> add -A .lore/data .lore/LORE.md && …`). Everything it needs is checked before anything is written (the link's plan, other staged files), and `--dry-run` lists every step and writes nothing:

```console
$ lore store adopt --target team/client_east --move --commit --yes
```
- Every row adopt writes gets the same secret scan as any write, before anything is written. A row holding something that looks like a key refuses the whole adopt (nothing reaches the store), naming each row and its masked finding, never the value, and printing the three reruns: `--secret-unstored` (each value becomes `${secret:unstored}` plus a store-then-rotate task in the target folder's `secrets` task list, a personal store folder too), `--secret-ref "<manager>://<entry>"` (one for all, or one per finding in the order listed) or `--not-a-secret --reason "<why>"` (kept, with an allow entry in the folder). `--dry-run` shows the same refusal.
- Near-duplicates are listed, never merged: when the folder already holds a row of the same kind with the same name (its title, else its summary, else the first line of its body; case and punctuation aside) or a body sharing at least 80% of its words, adopt (with `--move`, and on `--dry-run`) lists each pair after the counts, the moved id beside the folder's id ("rul_a… ~ rul_b…: rule \"Use portless for dev URLs.\" (same title)"), the read of both (`lore show --id <id> --store <store>/<folder>`; a `--dry-run` reads the moving row where it still is: `cd <repo> && lore show --id <id> --store repo`, the cd into the repo that moves, so the line also runs as printed when `--from` named another repo; the dry run's delete of the moved row starts with the same cd) and the delete that drops one (either id, at once: a moved row's id names the folder's copy also before the repo commit is pushed). Both rows stay until you pick; `--json` lists every pair under `duplicates`.
- A v0.1.17 repo needs no `lore migrate run` first: its rows are read as migrate would write them (format steps, tags folded into `tag_ids`, an agent's live stamp `…/agent` read as a stable actor). There `lore move` refuses (it would write the repo's rows on the new format); `lore store adopt --move` takes the open rows off its live ref with the rest.
- Moving a single row is still `lore move --id <id> --to <store>/<folder>`; it lists a near-duplicate it leaves the same way (also `lore memory move`).

### After an adopt: one person, two actor ids, and empty refs

An older lore adopted a v0.1.17 repo's rows as they were. Two things can be left behind, and both are repaired in place, from the client repo linked to the folder (or any repo that attaches it):

- **The repo's own actor id for someone already on the roster.** `lore doctor` names the pair; `lore actor merge --from <old-id> --into <roster-id>` (from any folder; `--store <org>/<org_folder>` names the org store when the ids are not in the stores the folder reads) rewrites every row naming the old id, all of a store's rows as one store commit and one push (1,000 rows take seconds), lists each store or folder it edits with its count (`notes-two/client_a: 66 edited`), and marks the old id `merged_into` the kept one, also on the org roster. A row it cannot edit does not stop it: the others are written, each row left is listed with its reason and fix, the command exits non-zero, and running it again writes only what is left. History stamps (`created_by`, `updated_by`) keep the old id as they were written; reads show the kept id for it: `lore task list --created-by <roster-id>` lists the rows made under the old one.
- **`tasklist_id: ""` (or another ref holding an empty string).** lore 0.1.17 wrote `""` for "none". Reads and edits treat it as none, so every edit works; `lore doctor` names the rows (`empty_ref`) and `lore doctor --fix` writes null in them, one store commit per store. `lore store adopt` and `lore migrate run` write null from the start. To take a task off its list on purpose: `lore task edit --id <id> --clear-tasklist`.
- **`tasklist_id: "Backlog"` (a ref holding a name, not an id).** An import made by hand can leave a list's name where its id belongs. `lore store adopt` and `lore migrate run` write the id of the one list of that title in the row's project, or clear the field when there is none (or more than one), and print one `warning:` line per row naming the field, the old value and what it holds now.

## An org with several stores

An org entry lists every store (`lore org refresh --org <org>` takes them from the store's `lore-org.yaml`; without `<org>` it refreshes the only org entry on the machine, else the org of the active setup of the folder, else it names the org entries and the command to run; `lore org link` and `lore org unlink` default the same way). `lore store list` and `lore store folder list` show each of them once; the org's other stores are named by their short name, the repo name of the url without `.git` (`git@github.com:agency/lore-x.git` is `lore-x`), with the url in its own column, and each `store … folders:` block is headed by the store that holds those folders (the org's name only for the store holding the org folder), from any repo. lore-org.yaml can set another short name with `stores[].name`, and must when two stores of the org have the same repo name: lore refuses such a file and names both urls. Every store command that takes a store takes the short name or the url: `lore store folder add|list|remove lore-x`, `lore store show|set|visibility|compact lore-x`, `--to lore-x/<folder>`, and `lore org link --org <org> --project <folder> --store <url>` (the url in any spelling, an ssh host alias of github.com included). Output names the store by its short name. A brand-new empty repo becomes a lore store with the first write to it: the `lore-store.yaml` marker goes into that same commit. A repo that an older lore left with a folder's `_meta.json` and no marker is adopted the same way on its next write. `lore org store add` with a brand-new empty repo is followed by `lore org link … --store <url>`: that link is the store's first write. `lore org store add` also names the org and its main store in the added store's `lore-store.yaml` (`org:` and `org_store:`, committed in that store's clone and sent by the next `lore sync`, also with `--push`, which sends only the org file), so a new member who joins with that store's url gets the org; an admin's first write into a store that was still an empty repo when it was added (`lore store folder add`, `lore org link` or a row) writes them into its new `lore-store.yaml` in that same first commit; a store added before that gets them from an admin's next `lore sync`, which says `named org <org> and its main store in the lore-store.yaml of <stores>` once. A store for the org's admins only (servers, domains, accounts) is added with `--audience admins`: members' lore never reads it and no repo links to it ([Admins-only stores](identity-and-stores.md#admins-only-stores)).

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
| `lore store init --name <name>`, `add`, `remove`, `set-url` | need a repo: they edit its `.lore/stores.yaml` (`lore store init --name me` works anywhere, and so does `lore store set-url` of an org's store, `me` or a `sto_` id) |
| `lore org link`, `unlink`, `set`, `use` | need a git repo: they are about that repo (`lore org link` needs no `lore init`) |
| `lore org create`, `import`, `rename`, `refresh`, `lore team invite --email …` | always |
| `lore sync` | always: the setup's stores, then the waiting commits of the other stores of the same org in `~/.lore/config.yaml`, never another org's (a folder or a note written outside a repo is sent from there too; a folder in no setup sends every org's) |
| a write with `--to <store>` or `--to <store>/<folder>` (`lore rule add --to acme/org …`, `lore memory add --to me …`) | the same names: it lands in that store folder (a new folder asks first, or `--create`), from any folder, the home folder too: when the org has one setup, `--to <org>/<folder>` uses it as `--setup <setup>` would (with two setups of the org, name one with `--setup`); a write with no `--to` needs a repo, and in the home folder the hint names only the flags that command takes (`--to` for a write, `--setup` for an edit by id, `cd` for `lore context`), never `git init` |
| a write with no `--to` (`lore memory add --body …`, `lore rule add …`, `lore task add …`, every kind of note: behaviours, workflows, snapshots and the rest too) in an own repo linked to a store folder (`lore org link --org acme --project sync --own`), whatever its `.lore/data` still holds (old memories, a hotfix, actors, projects) | that linked folder, where the repo's reads come from: the row and its author's actor row land in `acme/sync`, the code repo's `.lore/data` stays as it is, and the add names the folder; `--to repo` is the one way to write the repo. Notes still in `.lore/data` are a move not yet done: `lore doctor` warns about them and prints the one command that moves them, `lore store adopt --target acme/sync --move --own --yes` (no `--own` in a client repo), which runs as printed; every add's `--help` says the default is the linked folder |
| a write that names its row by name with `--store <org>[/<folder>]` (alias `--to`: `lore resource kind rename`, `resource field rename/retype/remove`, `lore meta set/rename/edit`, `lore project archive`, `lore repo archive`) | from the home folder or any folder outside a repo, the row is found in that store (`<org>`: any folder of the org) and written where it is; with no store flag there the hint names `--store <org>[/<folder>]`. Inside a code repo a `--store` naming a store the repo attaches keeps the command to that store (`<org>`: every folder of the org the repo attaches): a row named by id or found by name in another attached store is refused with `E_NOT_FOUND` "… is not in acme/web; it is in acme/org (use --store acme/org)" and nothing is read or written there (reads by id refuse the same way); a `--store` naming one the repo does not attach is refused with `E_NOT_FOUND`, naming what the repo attaches and the same command to run from the home folder; nothing is written anywhere else |
| an edit, delete or move naming its row by id (`lore memory edit --id <id> …`, `lore task done --id <id>`, `lore task delete --id <id>`, `lore move --id <id> --to acme/ops`) | the org-store folder holding the row, or the folder of your personal store holding it (`me/<folder>`, a note written with `--to me/<folder>`): no `--to` needed, from any folder (a repo that does not attach that folder, a repo lore is not set up in, a store clone, the home folder), and a move reads the row from its folder and writes it to the one `--to` names |
| a read naming its row by id (`lore memory show --id <id>`, `lore rule show --id <id>`) in the home folder, a folder no setup covers or a repo lore is not set up in | the org-store folder holding the row, looked up in every org's stores (an id names one row anywhere), with no `--store`; the text output of every `<noun> show` (memory, rule, decision, task, hotfix, pattern, playbook …) prints the row's `[<org>/<folder>]` after its id. With `--store <org>/<folder>` naming a folder that does not hold it, the refusal says where it is (`no row <id> in acme/ops; it is in acme/sync`) and names `--store acme/sync` |
| `lore context --store <org>` or `--store <org>/<folder>` (with `--for-file`, `--topic`, `--for-prompt` …) | that org's folders, or that folder, from any folder, as the other reads |
| `lore store list` in a folder no setup covers | the stores this folder sees (often only `me`) and a hint naming `lore store list --setup <setup>` for each setup that brings org stores |
| a read with `--store <org>` (`lore task list --store acme`, `lore decision list --store acme`, `lore trash list --store acme`) | every folder of every store of the org, each row labelled `<org>/<folder>`: one command lists the whole company's open tasks or proposed decisions, and it answers the same from any folder: a repo linked to one folder of the org, a repo lore is not set up in, a store clone, the home folder or a repo no setup covers |
| `lore sql run --store <org> --query <query>`, `lore sql run --store <org>/<folder> --query <query>` | the same names: every folder of the org (each a `source` `<org>/<folder>` in the `<table>_all` views, each row once), or that folder, from any folder (a repo, a repo lore is not set up in, a store clone, the home folder); a folder the store does not have yet is `E_NOT_FOUND` as below |
| a read with `--to <store>/<folder>` or `--store <store>/<folder>` (`lore rule list --to acme/sync`, `lore search --query … --to acme/sync`, `lore actor list --to acme/org`) | the same names: it reads that store folder, with the org's `org/` folder beside it (its kinds and policy); a folder the store does not have yet is `E_NOT_FOUND` naming the store's folders and `lore store folder list --store <store>` (a read never offers `--create`) |
| a read with no store named (`lore rule list`, `lore rule show --id <id>`, `lore resource list`, `lore lint`, `lore actor list`), and `lore actor merge` | the setup's org store: the org's roster folder plus every folder of the org's stores; this holds for every kind's list, show and search (`lore playbook list`, `lore pattern list`, `lore handoff list`, `lore repo list`, `lore tag list` … with `--setup <setup>` or in a folder the setup covers), which list every project's rows there as `lore rule list` does, and for `lore context` (`--topics`, `--topic`, `--for-prompt`, `--for-file`), which reads the setup's stores as its scope; a folder no org's setup covers is refused with `E_NOT_A_STORE` naming `--to`, never `git init`; in an own repo of an org that is not linked to a folder (and has no `.lore/`), a list or search read (`lore rule list`, `lore memory list`, `lore search`, `lore trash list`, every `lore <kind> list`, `lore context`) answers from the folder holding a resource of the org that names the repo's remote, with a first line saying so: `read acme/sync: its resource web_app names this repo (github.com/acme-co/web), which is not linked to a folder; link it: lore org link --org acme --project sync --own`, and its `--json` names the folder in a top-level `"routed": "acme/sync"` field (an empty answer too; a read naming its store has none); a resource kept in another folder (the org folder) whose `project` field names a folder of the org (a text value, or a select value: the value's current name, never its `val_` id) reads that folder and suggests it (`read acme/sync: its resource web_app (in acme/org) names this repo …; link it: lore org link --org acme --project sync --own`); a write with no `--to` there is refused, and its hint names the same folder (`--to acme/sync`) and the same `lore org link --org acme --project sync --own`; with no such resource the refusal names `lore org link --org <org> --project <folder> --own`; a read naming a row by id that no store clone on this machine holds says `<id> is in no store of any setup on this machine; looked in <org>/<folder>, …` (from the home folder too, with or without `--setup`; on a machine with no store at all, no setup and no org entry, it is the `E_NOT_A_STORE` refusal every note command gives there), one whose folder holds only its tombstone (moved away, or deleted by an older lore) says `<id> was deleted from <org>/<folder> on <date>` with `lore trash list --store <org>/<folder>` (from the home folder, a linked repo or a store clone's folder alike), a purged one (its folder's `_purged.json` lists it) answers `E_PURGED` `<id> was purged from <org>/<folder> on <date>; purged rows never come back` and never offers the trash list or a restore, a trashed row of a folder the read does not attach answers `is in the trash` as an attached one does, and outside a git checkout or in a store clone no not-found hint names a branch |
| `lore ask --query <question>` or `lore search --query <words>` with no store named, in a folder no setup covers (the home folder) | your personal store, every folder of it, as `--store me` reads it, ending with `note: read your personal store (not in a repo)`; with no personal store set up it refuses with `E_NOT_A_STORE` like any other read there |
| `lore repo add --project <prj-id-or-folder> --mount-name <name> --origin <url> --yes`, `lore repo list --project …` | `--project` names the org-store folder: the repo is recorded there, by url; no checkout is needed and nothing is written to any repo's `.git` |
| `lore resource add --kind <kind> --to <store>/<folder>` | a kind defined in the org's `org/` folder (`lore resource kind add --kind <kind> --to <org>/org`) resolves, as in a linked repo |

Inside a code repo, a read naming the org or one of its folders (`lore task list --store acme`, `lore task list --store acme/ops` in a repo linked to `acme/sync`) reads the org's stores from their clones for that one command, read-only, as from a plain folder, whatever the repo attaches; a store the repo's own `.lore/stores.yaml` names that is no store of the org keeps the repo's meaning. A folder of another attached store the repo does not attach is read the same way and says so on stderr; `lore store add … --folder <folder>` attaches it for good. A row named by id is found the same way in any folder of the org's stores and of your personal store (`me/<folder>`), and in a store clone's folder in any other folder of the clone or of its org.

A command that only changes a store also leaves the repo you run it in alone: it does not touch that repo's `.gitignore`, `.gitattributes` or git hooks.
