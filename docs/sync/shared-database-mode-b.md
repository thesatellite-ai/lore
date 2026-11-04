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
$ lore store init client2 file://$HOME/stores/client2
name: client2
url: file://~/stores/client2
id: sto_c0f3…b5b8

# alice, in api
$ lore store add client2 file://$HOME/stores/client2
name: client2
url: file://~/stores/client2

# alice, in app
$ lore store list
stores:
  NAME     URL                                                                         FOLDER  READ_ONLY  BRANCH  PERSONAL
  client2  file://~/stores/client2          false              false
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
lore sync: imported 2
✓ rul_76bd…3f10 [client2]
  scope: [master] severity: must

# alice, in api
$ lore rule add --to client2 --body 'Every change comes with a test.'
lore sync: imported 2
✓ rul_d431…313a [client2]
  scope: [master] severity: must
$ lore rule add --body 'The API answers in JSON only.'
✓ rul_b4f4…a5b0 
  scope: [master] severity: must
$ lore rule list
rul_b4f4…a5b0 [must/always] The API answers in JSON only.
rul_76bd…3f10  Every screen works on a phone.  [client2]
rul_d431…313a  Every change comes with a test.  [client2]

# alice, in app
$ lore rule list
rul_76bd…3f10  Every screen works on a phone.  [client2]
rul_d431…313a  Every change comes with a test.  [client2]
$ git status --short
?? .gitattributes
?? .gitignore
?? .lore/
```
<!-- /example -->

`.lore/stores.yaml` holds a path on your machine. Commit it only if every teammate keeps the store at the same path; otherwise add it to that repo's `.gitignore`.

## Step 3 — Archive or delete a project

`lore project archive <id>` hides a project and `unarchive` brings it back. `lore project delete <id>` removes it for good; lore asks first, and without a terminal it stops with `E_NEEDS_CONFIRM` until you add `--yes`:

<!-- example: projects#archive-a-project -->
```console
# alice, in scratch
$ lore project list
lore sync: imported 2
prj_28f8…03df  scratch  https://github.com/client2/scratch
$ lore project archive prj_28f8…03df
✓ scratch archived
$ lore project list
prj_28f8…03df  scratch  https://github.com/client2/scratch [archived]
$ lore project unarchive prj_28f8…03df
✓ scratch unarchived
$ lore project delete prj_28f8…03df
ERROR: [E_NEEDS_CONFIRM] engine: run project delete?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore project delete prj_28f8…03df --yes
✓ scratch deleted
lore sync: trashed rows 1
lore sync: rows whose file disappeared are kept in `lore sync trash` (undo: `lore sync trash restore <id>`)
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
project_id: prj_4c59…6c38
$ lore rule list
ERROR: [E_UNSUPPORTED] this repo uses the retired shared-database mode (.lore/lore.yaml); run `lore migrate run`
  hint: `lore migrate run --dry-run` lists what moves: the project's rows become a folder store attached in .lore/stores.yaml, and the pointer is kept as <file>.migrated
(exit 23)
$ lore migrate run --dry-run
dry_run: true
count: 2
rows: 
steps: mode-b: memories/mem_3b62…e913 → store shared (~/.lore/stores/infra), mode-b: rules/rul_f147…5139 → store shared (~/.lore/stores/infra), mode-b: .lore/stores.yaml: add store shared file://~/.lore/stores/infra, mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
$ lore migrate run
count: 2
rows: 
steps: mode-b: memories/mem_3b62…e913 → store shared (~/.lore/stores/infra), mode-b: rules/rul_f147…5139 → store shared (~/.lore/stores/infra), mode-b: .lore/stores.yaml: add store shared file://~/.lore/stores/infra, mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
$ ls .lore; cat .lore/stores.yaml
lore-cache.db
lore-cache.db-shm
lore-cache.db-wal
lore.yaml.migrated
state
stores.yaml
stores:
    - name: shared
      url: file://~/.lore/stores/infra
$ lore rule list; lore memory list
rul_f147…5139  Terraform changes need a plan in the pull request.  [shared]
mem_3b62…e913  The staging cluster sleeps at night.  [shared]
```
<!-- /example -->

Run it in each repo that pointed at the shared database. Repos that pointed at the same project end up attached to the same store.

## Try it — what lore says outside a repo

lore works inside a git repo (any folder of it). Outside one it stops instead of guessing:

<!-- example: projects#errors -->
```console
# alice, in ~
$ lore rule list
ERROR: [E_NOT_A_STORE] git: the folder is not inside a git work tree
(exit 19)
```
<!-- /example -->

## Pitfalls

- **A folder store stays on your machine.** It is not committed anywhere, so back it up yourself, or use a git store if the notes should reach other machines or people.
- **Use a full path in the address.** `file://~/…` is not expanded; write `file://$HOME/…` so your shell fills it in.
- **`..` is refused in an old pointer's `db_path`** (`E_BAD_PATH`) when `lore migrate run` reads it, so a cloned repo cannot point lore at a file outside where you expect. `LORE_ALLOW_DB_PATH_OUTSIDE=1` allows it for one run when you really mean it; see [Environment variables](../reference/environment.md).
- **`${LORE_HOME}` in an old pointer** means `~/.lore` unless `LORE_HOME` is set; set it for the `lore migrate run` that converts such a repo if the shared database lives elsewhere.

## Related

- [Projects and repos](multi-repo-projects.md): repos inside one project, list, show, archive, delete.
- [How notes travel with git](how-notes-travel-with-git.md): the repo's own notes, committed with the code.
- [Lore for teams](../team/README.md): git stores shared across an agency and its clients.
- [Feature index › Projects and repos](../reference/features.md#projects-and-repos)
