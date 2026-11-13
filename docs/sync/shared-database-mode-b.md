# One store for several repos

**In short:** when a client's product is split over several repos (here client 2's `app`, `api` and `infra`), you can keep the notes they share in one place: a **store**, a folder (or a private git repo) that holds lore notes and nothing else. Each repo attaches it once with `lore store add`, and every note written `--to` it shows up in all of them. Older lore did this with a shared database file and a pointer in each repo ("Mode B"); that mode is gone, and `lore migrate run` turns such a repo into one that uses a store.

## What it is and when you want it

Each repo keeps its own notes in `.lore/data`, committed with the code ([How notes travel with git](how-notes-travel-with-git.md)). A store is a second place lore reads from and writes to:

- **A folder store** (`file://…`) lives on your machine. It is personal: nothing in it is committed to the client's repos, and nobody else sees it.
- **A git store** (a private repo of its own) travels between machines and people through git; see [Lore for teams](../team/README.md).

Think of it as one filing cabinet next to your desk that every repo of the product can open. You want it when you work across several repos of one product and want one place for the notes that apply to all of them, or for notes that must not be committed into the client's repos.

A note goes to the store only when you say so with `--to <store>`; everything else still goes to the repo's own `.lore/data`.

## Before you start

- lore installed, and each repo set up with `lore init`.
- Pick where the store folder lives. Write the address as a full path: `file://$HOME/stores/client2` (your shell fills in `$HOME`).

## Step 1 — Make the store and attach it in each repo

Alice (an agency developer) has client 2's repos in `~/work`. In the first repo she runs `lore store init`: it makes the folder a store and attaches it to that repo. In each other repo, `lore store add` attaches the same store:

<!-- example: projects#one-store -->
```console
# alice, in app
$ lore store init --name client2 --url file://$HOME/stores/client2
✓ created store client2 (sto_b4f4…a5b0) at file://~/stores/client2; recorded in .lore/stores.yaml; commit it to share

# alice, in api
$ lore store add --name client2 --url file://$HOME/stores/client2
✓ attached store client2 (whole store, folder backend, internal) in .lore/stores.yaml; commit it to share

# alice, in app
$ lore store list
stores:
  NAME     URL                                                                         FOLDER  READ_ONLY  BRANCH  PERSONAL  SOURCE       VISIBILITY  REACH  ADDRESS
  client2  file://~/stores/client2          false              false     stores.yaml  internal    ok     
count: 1
```
<!-- /example -->

The attachment is recorded in the repo's `.lore/stores.yaml`.

## Step 2 — Write notes to the store

`--to client2` writes a note to the store; without it, the note stays in the repo. `lore rule list` shows both, and marks the store's notes with `[client2]`:

<!-- example: projects#shared-notes -->
```console
# alice, in app
$ lore rule add --to client2 --body 'Every screen works on a phone.'
lore: wrote ~/stores/client2/_store/actors/act_28f8…03df.json (store client2)
lore: wrote ~/stores/client2/_store/rules/rul_4c59…6c38.json (store client2)
lore: wrote ~/stores/client2/_store/machines/mch_3b62…e913.json (store client2)
✓ rul_4c59…6c38 [client2]
  scope: [master] severity: must

# alice, in api
$ lore rule add --to client2 --body 'Every change comes with a test.'
lore: wrote ~/stores/client2/_store/rules/rul_f147…5139.json (store client2)
✓ rul_f147…5139 [client2]
  scope: [master] severity: must
$ lore rule add --body 'The API answers in JSON only.'
lore: wrote ~/work/api/.lore/data/rules/rul_2537…6bbb.json
✓ rul_2537…6bbb
  scope: [master] severity: must
$ lore rule list
rul_2537…6bbb [must/always] The API answers in JSON only.
rul_4c59…6c38 [must/always] Every screen works on a phone. [client2]
rul_f147…5139 [must/always] Every change comes with a test. [client2]

# alice, in app
$ lore rule list
rul_4c59…6c38 [must/always] Every screen works on a phone. [client2]
rul_f147…5139 [must/always] Every change comes with a test. [client2]
$ git status --short
?? .gitattributes
?? .gitignore
?? .lore/
?? AGENTS.md
```
<!-- /example -->

`.lore/stores.yaml` holds a path on your machine. Commit it only if every teammate keeps the store at the same path; otherwise add it to that repo's `.gitignore`.

## Step 3 — Archive or delete a project

`lore project archive --id <id>` hides a project and `unarchive` brings it back. `lore project delete --id <id>` removes it for good; lore asks first, and without a terminal it stops with `E_NEEDS_CONFIRM` until you add `--yes`:

<!-- example: projects#archive-a-project -->
```console
# alice, in scratch
$ lore project list
prj_b69e…5ed7  scratch  https://github.com/client2/scratch
$ lore project archive --id prj_b69e…5ed7
lore: wrote ~/work/scratch/.lore/data/projects/prj_b69e…5ed7.json
✓ scratch archived
$ lore project list
prj_b69e…5ed7  scratch  https://github.com/client2/scratch [archived]
$ lore project unarchive --id prj_b69e…5ed7
lore: wrote ~/work/scratch/.lore/data/projects/prj_b69e…5ed7.json
✓ scratch unarchived
$ lore project delete --id prj_b69e…5ed7
ERROR: [E_NEEDS_CONFIRM] project delete prj_b69e…5ed7?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore project delete --id prj_b69e…5ed7 --yes
lore: wrote ~/work/scratch/.lore/data/repos/rep_0aae…a61f.json
lore: wrote ~/work/scratch/.lore/data/projects/prj_b69e…5ed7.json
✓ scratch (and this repo's row) moved to the trash (batch bat_6a86…ed8c; restore: lore trash restore --target bat_6a86…ed8c)
$ lore project list
(no projects)
```
<!-- /example -->

## Step 4 — Convert a repo that used a shared database

A repo set up with an older lore may still hold a two-line pointer file, `.lore/lore.yaml` (or the even older `.lore/lore.toml`), that names a shared database file and one project in it. lore no longer reads that file directly: every command stops and names `lore migrate run`. `--dry-run` lists what moves; the real run copies the project's notes into a folder store beside the shared database (`stores/<project>`), attaches it as `shared` and keeps the pointer as `lore.yaml.migrated`:

<!-- example: projects#convert-a-shared-database -->
```console
# alice, in infra
$ cat .lore/lore.yaml
db_path: ${HOME}/.lore/client2.db
project_id: prj_4f89…b64b
$ lore rule list
lore rule list: git is not set up for lore's row files in this clone: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/infra/.git/config; hook blocks in ~/work/infra/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
ERROR: [E_UNSUPPORTED] this repo uses the retired shared-database mode (.lore/lore.yaml); run lore fix
  hint: `lore fix --dry-run` lists what moves: the project's rows become a folder store attached in .lore/stores.yaml, and the pointer is kept as <file>.migrated
(exit 23)
$ lore migrate run --dry-run
Would upgrade 2 rows
  mode-b: memories/mem_77cb…76c4 → store shared (~/.lore/stores/infra)
  mode-b: rules/rul_d191…45c2 → store shared (~/.lore/stores/infra)
  mode-b: .lore/stores.yaml: add store shared file://~/.lore/stores/infra
  mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
Nothing written (dry run).
$ lore migrate run
lore: added .lore/lore.db, .lore/lore.db-shm, .lore/lore.db-wal, .lore/lore.db.broken.*, .lore/state/, .lore/backups/, .lore/lock to this clone's .git/info/exclude (never committed) so lore's local files stay out of git status; this repo holds no lore data, so lore changes none of its tracked files (`lore where` lists everything lore keeps here)
Upgraded 2 rows
  mode-b: memories/mem_77cb…76c4 → store shared (~/.lore/stores/infra)
  mode-b: rules/rul_d191…45c2 → store shared (~/.lore/stores/infra)
  mode-b: .lore/stores.yaml: add store shared file://~/.lore/stores/infra
  mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
team lock: none (this machine's migration lock only; nothing written to the remote for it); `--team-lock` also holds refs/lore/signals on the remote while the run lasts, so a teammate cannot migrate at the same time
backup: refs/lore-backup/migrate/2026-11-02 (local only, never pushed)
undo: none for a shared-database conversion (`lore migrate rollback` refuses it); the shared lore.db was not changed
$ ls .lore; cat .lore/stores.yaml
lore.yaml.migrated
state
stores.yaml
stores:
    - name: shared
      url: file://~/.lore/stores/infra
$ lore rule list; lore memory list
rul_d191…45c2 [must/always] Terraform changes need a plan in the pull request. [shared]
mem_77cb…76c4 [retrieved] The staging cluster sleeps at night. [shared]
```
<!-- /example -->

Run it in each repo that pointed at the shared database. Repos that pointed at the same project end up attached to the same store.

## Step 5 — Check the migration, undo it, or declare a format feature

`lore migrate status` shows, for every place lore writes (the repo and each attached store), how many row files are still in the v0.1.17 format (`V1`), in the 0.1.18 format (`V2`) or newer than this lore knows (`NEWER`), the format features the repo has declared, the last migration with its backup ref, and the migration lock. In `infra` it shows the conversion from Step 4 and that it cannot be rolled back (`lore migrate rollback` there answers `E_UNSUPPORTED` and changes nothing; the way back to v0.1.17 is renaming `.lore/lore.yaml.migrated` back to `.lore/lore.yaml` and removing the `shared` entry from `.lore/stores.yaml`); in `app`, which never ran a migration, `lore migrate rollback` has nothing to undo. `lore migrate unlock` releases this machine's lock after a migration crashed (`--team` clears a stale team lock and asks first). `lore migrate enable --feature <feature>` declares a format feature in `.lore/data/_meta.json` before its first use, so an older lore on a teammate's machine stops with a clear "upgrade" message instead of misreading the files; running it twice changes nothing:

<!-- example: projects#migration-status -->
```console
# alice, in infra
$ lore migrate status
writes: 2
places:
  PLACE   V1  V2  NEWER  TOTAL
  repo    0   0   0      0
  shared  0   2   0      2
journal: last migration 2026-11-02 converted the shared-database mode (.lore/lore.yaml; backup refs/lore-backup/migrate/2026-11-02); it cannot be rolled back
lock: free

# alice, in app
$ lore migrate status
format: the lore 0.1.18 format (format 2)
writes: 2
places:
  PLACE    V1  V2  NEWER  TOTAL
  repo     0   4   0      4
  client2  0   4   0      4
features: hlc, policy
min_lore_version: 0.1.18
lock: free
$ lore migrate rollback
ERROR: [E_NOT_FOUND] migrate: no migration to roll back on this machine
  hint: lore migrate status
(exit 27)
$ lore migrate unlock
no migration lock on this machine
$ lore migrate enable --feature meta
feature: meta
added: true
features: hlc, meta, policy
min_lore_version: 0.1.18
$ lore migrate enable --feature meta
feature: meta
features: hlc, meta, policy
min_lore_version: 0.1.18
$ lore migrate status | grep -E '^(features|min_lore_version)'
features: hlc, meta, policy
min_lore_version: 0.1.18
```
<!-- /example -->

`lore migrate rollback` undoes the last migration (any but a shared-database conversion): before you push, it restores the files from the backup ref (`refs/lore-backup/migrate/<date>`); after a push it writes new commits that put the old content back. When enabling a feature would raise `min_lore_version` (the oldest lore that can read the repo), `enable` asks first and needs `--yes` without a terminal: everyone on the team must have upgraded before that.

## Try it — what lore says outside a repo

lore works inside a git repo (any folder of it). Outside one it stops instead of guessing:

<!-- example: projects#errors -->
```console
# alice, in ~
$ lore rule list
ERROR: [E_NOT_A_STORE] git: the folder is not inside a git work tree
  hint: cd into a git checkout, or run git init here first
(exit 19)
```
<!-- /example -->

## Pitfalls

- **A shared-database conversion cannot be rolled back.** `lore migrate rollback` after it answers `E_UNSUPPORTED` and changes nothing (the shared `lore.db` itself was never changed). Preview with `--dry-run` first, and keep the old database file until you are sure.
- **A folder store stays on your machine.** It is not committed anywhere, so back it up yourself, or use a git store if the notes should reach other machines or people.
- **Use a full path in the address.** `file://~/…` is not expanded; write `file://$HOME/…` so your shell fills it in.
- **`..` is refused in an old pointer's `db_path`** (`E_BAD_PATH`) when `lore migrate run` reads it, so a cloned repo cannot point lore at a file outside where you expect. `LORE_ALLOW_DB_PATH_OUTSIDE=1` allows it for one run when you really mean it; see [Environment variables](../reference/environment.md).
- **`${LORE_HOME}` in an old pointer** means `~/.lore` unless `LORE_HOME` is set; set it for the `lore migrate run` that converts such a repo if the shared database lives elsewhere.

## Related

- [Projects and repos](multi-repo-projects.md): repos inside one project, list, show, archive, delete.
- [How notes travel with git](how-notes-travel-with-git.md): the repo's own notes, committed with the code.
- [Lore for teams](../team/README.md): git stores shared across an agency and its clients.
- [Feature index › Projects and repos](../reference/features.md#projects-and-repos)
