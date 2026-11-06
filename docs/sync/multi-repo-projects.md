# Projects and repos

**In short:** a *project* is the box all of one product's notes live in; `lore init` makes one per repo. Inside a project you can name *repos* (parts of the code, such as the shop and the help pages) and attach a note to one part only, so "Buttons are blue" applies to the shop and not to everything. This page shows how to list, add, archive and delete both.

## What it is and when you want it

- A **project** is one notes box with a name and an id (`prj_...`). In the normal set-up there is one project per repo, created by `lore init` and stored in `.lore/data/projects`.
- A **repo** inside a project is a named part of the code (its *mount name*, like a folder label: `shop`, `help`). A note with no repo applies to the whole project; a note added with `--repo shop` applies only when you work on the shop.

You want repos when one repository holds several parts that need different notes (a monorepo), or when you want to say which remote (the online address of a repo, like `github.com/client/website`) a part comes from. You want the project commands to see what a database holds and to tidy up old projects.

If the client's product is several separate git repositories that should share one notes database on your machine, read [One store for several repos](shared-database-mode-b.md) instead.

## Before you start

- A repo set up with lore ([How notes travel with git](how-notes-travel-with-git.md)). Here Alice (an agency developer) works in the client's repo `github.com/client/website`.

## Step 1 — See the project

`lore project list` shows every project in the database; `lore project show <name-or-id>` shows one, with its repos (`--json` adds the full list):

<!-- example: projects#project-list -->
```console
# alice, in website
$ lore project list
prj_7a67…9504  website  https://github.com/client/website
$ lore project show website
Project: prj_7a67…9504
  name:    website
  origin:  https://github.com/client/website
  created: 2026-11-02 10:00:00Z
Repos:
  rep_2ad3…f891  website
```
<!-- /example -->

## Step 2 — Add repos

Give each part a short mount name. `--origin` records where its code lives and `--display-name` is a free label:

<!-- example: projects#add-repos -->
```console
# alice, in website
$ lore repo list
rep_2ad3…f891  website  https://github.com/client/website
$ lore repo add shop --origin https://github.com/client/website --display-name 'Online shop'
✓ repo shop registered (rep_162d…9666)
$ lore repo add help --display-name 'Help pages'
✓ repo help registered (rep_54fe…28f2)
$ lore repo list
rep_2ad3…f891  website  https://github.com/client/website
rep_162d…9666  shop  https://github.com/client/website
rep_54fe…28f2  help
```
<!-- /example -->

Repos are rows like any other: lore writes them to `.lore/data/repos`, and you commit them so teammates get the same names.

## Step 3 — Attach notes to one repo

Without `--repo`, a rule applies to the whole project. With `--repo shop` it applies to the shop only. `lore rule list` alone shows the project-wide rules; with `--repo shop` it shows the project-wide rules plus the shop's own:

<!-- example: projects#notes-per-repo -->
```console
# alice, in website
$ lore rule add --body 'Run the tests before you push.'
✓ rul_05e9…4603
  scope: [master] severity: must
$ lore rule add --repo shop --body 'Buttons are blue.'
✓ rul_c0f3…b5b8
  scope: [repo:shop] severity: must
$ lore rule list
rul_05e9…4603 [must/always] Run the tests before you push.
$ lore rule list --repo shop
rul_05e9…4603 [must/always] Run the tests before you push.
rul_c0f3…b5b8 [must/always] Buttons are blue.
$ lore rule list --repo blog
ERROR: [E_NOT_FOUND] wire: no repo "blog" in this project
  hint: run `lore repo list` to see the mount names
(exit 27)
```
<!-- /example -->

`--repo` is a flag every lore command takes, so the same scoping works for memories, decisions and the other kinds of rows.

## Step 4 — Archive, unarchive and delete

Archiving hides a repo or project without losing anything; unarchive brings it back. These commands take the id (`rep_...`, `prj_...`), not the mount name:

<!-- example: projects#archive-a-repo -->
```console
# alice, in website
$ lore repo archive help
ERROR: [E_INVALID_ID] repo: "help" is not a repo id (rep_…)
(exit 20)
$ lore repo archive rep_54fe…28f2
✓ help archived
$ lore repo list
rep_2ad3…f891  website  https://github.com/client/website
rep_162d…9666  shop  https://github.com/client/website
rep_54fe…28f2  help [archived]
$ lore repo unarchive rep_54fe…28f2
✓ help unarchived
$ git add -A && git commit -qm 'lore: repos' && git push -q origin main
```
<!-- /example -->

`lore repo delete <id>` and `lore project delete <id>` remove a row for good; lore asks first, and without a terminal they stop with `E_NEEDS_CONFIRM` until you add `--yes`. An example with projects is on [One store for several repos](shared-database-mode-b.md#step-3--archive-or-delete-a-project).

## Pitfalls

- **`repo archive` wants the id.** `lore repo archive help` fails with "not found" even though `--repo help` works elsewhere; copy the `rep_...` id from `lore repo list`. (The hint printed with that error points at branches and is not the cause here.)
- **An unknown `--repo` is an error, not an empty list.** A typo in the mount name stops the command, so you never act on the wrong set of notes by mistake.
- **A note's repo is fixed when you add it.** To move a rule, use `lore rule edit <id> --rebind-repo <mount>` or `--rebind-master` (back to the whole project).
- **Two `lore init`s on two branches make two projects.** When those branches merge, run `lore sync fix-projects`; see [Conflicts and duplicates](conflicts-and-duplicates.md#step-5--two-people-set-up-lore-on-separate-branches).
- **Delete is for mistakes.** Archive is reversible and keeps history; delete is not.

## Related

- [One store for several repos](shared-database-mode-b.md)
- [How notes travel with git](how-notes-travel-with-git.md)
- [Lore for teams › Stores and links](../team/concepts.md#stores-and-links): an agency project linked to a client repo.
- [Feature index › Projects and repos](../reference/features.md#projects-and-repos)
