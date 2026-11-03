# One database for several repos

**In short:** normally each repo has its own notes database inside it. When a client's product is split over several repos (here client 2's `app`, `api` and `infra`), you can instead keep one database file on your machine and put a tiny pointer file in each repo that says "my notes live over there". lore calls this *Mode B*. It is personal to your machine: the notes are not committed to git and do not reach your teammates.

## What it is and when you want it

lore finds its database in one of two ways:

- **Mode A (the normal one):** `.lore/lore.db` inside the repo, kept in step with the committed `.lore/data` files ([How notes travel with git](how-notes-travel-with-git.md)). This is what `lore init` sets up.
- **Mode B (shared database):** `.lore/lore.yaml` inside the repo, a two-line pointer to a database file somewhere else (for example `~/.lore/client2.db`) and to one project in it. Several repos can point at the same file, each at its own project.

Think of Mode B as one filing cabinet at your desk with a drawer per repo, instead of a small box inside each repo. You want it when you work alone across several repos of one product and want one place for your notes on it, or for repos where you cannot or do not want to commit `.lore/data`.

You do not want it to share notes with a team: in Mode B lore does not write `.lore/data`, so nothing travels with git. For shared notes, use Mode A, or the agency's private store (a separate private repo that only the agency can open, like a locked filing cabinet next to the client's repo) described in [Lore for teams](../team/README.md).

## Before you start

- lore installed. Each repo folder must not have a `.lore` folder yet (`shared-init` refuses a folder that already has one).
- Pick where the shared file lives. `${HOME}` and `${LORE_HOME}` (lore's own folder, `~/.lore` by default) are replaced with the real folder when lore reads the pointer, so the same pointer works for any user name.

## Step 1 — Point each repo at the shared database

Alice (an agency developer) has client 2's three repos in `~/work`. In each one she runs `lore project shared-init` with the same `--db`. The first run creates the database; each run adds a project and writes the pointer:

<!-- example: projects#shared-init -->
```console
# alice, in app
$ lore project shared-init --db '${HOME}/.lore/client2.db' --name app
✓ registered project "app"
    project_id: prj_c0f3…b5b8
    db:         ~/.lore/client2.db
    pointer:    ~/work/app/.lore/lore.yaml
$ cat .lore/lore.yaml
db_path: ${HOME}/.lore/client2.db
project_id: prj_c0f3…b5b8

# alice, in api
$ lore project shared-init --db '${HOME}/.lore/client2.db' --name api
✓ registered project "api"
    project_id: prj_76bd…3f10
    db:         ~/.lore/client2.db
    pointer:    ~/work/api/.lore/lore.yaml

# alice, in infra
$ lore project shared-init --db '${HOME}/.lore/client2.db' --name infra
✓ registered project "infra"
    project_id: prj_d431…313a
    db:         ~/.lore/client2.db
    pointer:    ~/work/infra/.lore/lore.yaml

# alice, in ~
$ lore project shared-list --db ~/.lore/client2.db
api                            prj_76bd…3f10  last_active=2026-11-02
app                            prj_c0f3…b5b8  last_active=2026-11-02
infra                          prj_d431…313a  last_active=2026-11-02
```
<!-- /example -->

`lore project shared-list --db <file>` lists every project in a shared database.

## Step 2 — Write notes as usual

In each folder, lore commands work as in any repo. Each repo sees its own project's notes; git sees only the new `.lore` folder with the pointer in it:

<!-- example: projects#shared-notes -->
```console
# alice, in app
$ lore rule add --body 'Every screen works on a phone.'
✓ rul_b4f4…a5b0 
  scope: [master] severity: must

# alice, in api
$ lore rule add --body 'Every change comes with a test.'
✓ rul_28f8…03df 
  scope: [master] severity: must
$ lore rule list
rul_28f8…03df [must/always] Every change comes with a test.

# alice, in app
$ lore rule list
rul_b4f4…a5b0 [must/always] Every screen works on a phone.
$ git status --short
?? .lore/
```
<!-- /example -->

Commit `.lore/lore.yaml` only if every teammate keeps the database at the same path; otherwise add `.lore/` to that repo's `.gitignore`.

## Step 3 — Choose the database and project yourself

lore decides which database and project to use in this order: a flag (`--db`, `--project`, `--repo`), then an environment variable (`LORE_DB`, `LORE_PROJECT_ID`, `LORE_REPO`), then the pointer `.lore/lore.yaml`, then a local `.lore/lore.db`. It only looks in the current folder, never in parent folders. So from anywhere you can name the database and the project; and inside a pointer folder, `LORE_PROJECT_ID` reads another project's notes:

<!-- example: projects#pick-the-database only=1,2,3 -->
```console
# alice, in ~
$ lore rule list --db ~/.lore/client2.db --project api
rul_28f8…03df [must/always] Every change comes with a test.
$ LORE_DB=~/.lore/client2.db lore rule list --project app
rul_b4f4…a5b0 [must/always] Every screen works on a phone.

# alice, in infra
$ LORE_PROJECT_ID=$(grep project_id ../api/.lore/lore.yaml | cut -d' ' -f2) lore rule list
rul_28f8…03df [must/always] Every change comes with a test.
```
<!-- /example -->

## Step 4 — Archive or delete a project

`lore project archive <id>` hides a project and `unarchive` brings it back. `lore project delete <id>` removes it for good and refuses without `--confirm`:

<!-- example: projects#archive-a-project -->
```console
# alice, in ~
$ mkdir -p scratch && cd scratch && lore project shared-init --db '${HOME}/.lore/client2.db' --name scratch
✓ registered project "scratch"
    project_id: prj_4c59…6c38
    db:         ~/.lore/client2.db
    pointer:    ~/work/scratch/.lore/lore.yaml

# alice, in scratch
$ lore project archive prj_4c59…6c38
✓ scratch archived

# alice, in ~
$ lore project shared-list --db ~/.lore/client2.db
api                            prj_76bd…3f10  last_active=2026-11-02
app                            prj_c0f3…b5b8  last_active=2026-11-02
infra                          prj_d431…313a  last_active=2026-11-02
scratch                        prj_4c59…6c38  last_active=2026-11-02

# alice, in scratch
$ lore project unarchive prj_4c59…6c38
✓ scratch unarchived
$ lore project delete prj_4c59…6c38
ERROR: [E_INVALID_INPUT] --confirm required to hard-delete
  hint: prefer `lore project archive prj_4c59…6c38` for soft-delete (reversible); pass --confirm if you really mean hard-delete
(exit 1)
$ lore project delete prj_4c59…6c38 --confirm
✓ scratch deleted (hard)

# alice, in ~
$ rm -rf scratch && lore project shared-list --db ~/.lore/client2.db
api                            prj_76bd…3f10  last_active=2026-11-02
app                            prj_c0f3…b5b8  last_active=2026-11-02
infra                          prj_d431…313a  last_active=2026-11-02
```
<!-- /example -->

## Try it — what lore says when it cannot tell

Outside a project folder lore stops instead of guessing. A folder with both a local database and a pointer is refused too, because lore cannot tell which you meant. Remove the one you do not need:

<!-- example: projects#errors -->
```console
# alice, in ~
$ lore rule list
ERROR: [E_NOT_PROJECT_ROOT] current directory is not a lore project (no .lore/lore.db or lore.yaml)
  hint: run `lore init` here, or cd into a project root
(exit 1)

# alice, in app
$ touch .lore/lore.db && lore rule list
ERROR: [E_AMBIGUOUS_PROJECT] both .lore/lore.db AND a .lore/lore.yaml (or lore.toml) pointer present
  hint: delete the one you don't need (Mode A uses lore.db; Mode B uses lore.yaml)
(exit 1)
$ rm .lore/lore.db
$ cd .lore && lore rule list
ERROR: [E_NOT_PROJECT_ROOT] current directory is not a lore project (no .lore/lore.db or lore.yaml)
  hint: run `lore init` here, or cd into a project root
(exit 1)
```
<!-- /example -->

## Pitfalls

- **No sync in Mode B.** There is no `.lore/data`, so branches, pull requests and teammates never see these notes, and `lore sync` has nothing to do. Back up the shared file yourself.
- **Both files in one folder is an error** (`E_AMBIGUOUS_PROJECT`). `shared-init` refuses a folder that has a `.lore` folder, but copying a `lore.db` in later causes this.
- **A database with several projects needs a project.** `--db file` from outside a pointer folder must come with `--project <name-or-id>` (error `E_AMBIGUOUS_PROJECT` otherwise). Today `LORE_PROJECT_ID` is not read in that case, only `--project`: use the flag.
- **`..` is refused in `db_path`.** A pointer whose path climbs out with `..` is refused (`E_BAD_PATH`), so a repo you cloned cannot point lore at a file outside where you expect. `LORE_ALLOW_DB_PATH_OUTSIDE=1` allows it when you really mean it.
- **Older pointer files are converted.** A repo set up with an older lore has `.lore/lore.toml`. lore converts it to `lore.yaml` the first time it reads it and keeps the old file as `lore.toml.migrated`:

<!-- example: projects#old-pointer-file -->
```console
# alice, in infra
$ mv .lore/lore.yaml ~/pointer.keep; printf 'db_path = "${HOME}/.lore/client2.db"\nproject_id = "%s"\n' $(grep project_id ~/pointer.keep | cut -d' ' -f2) > .lore/lore.toml; rm ~/pointer.keep; ls .lore
lore.toml
$ lore rule list
lore: migrated ~/work/infra/.lore/lore.toml → lore.yaml (old file kept as lore.toml.migrated)
(no rules)
$ ls .lore
lore.toml.migrated
lore.yaml
```
<!-- /example -->

- **`shared-list` does not mark archived projects.** An archived project still appears in the list with no marker, so keep a note of what you archived.

## Related

- [Projects and repos](multi-repo-projects.md): repos inside one project, list, show, archive, delete.
- [How notes travel with git](how-notes-travel-with-git.md): Mode A, the default.
- [Lore for teams](../team/README.md): sharing notes across an agency and its clients.
- [Feature index › Projects and repos](../reference/features.md#projects-and-repos)
