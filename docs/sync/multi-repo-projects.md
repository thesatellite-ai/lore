# Projects and repos

**In short:** a *project* is the box all of one product's notes live in; `lore init` makes one per repo. Inside a project you can name *repos* (parts of the code, such as the shop and the help pages) and attach a note to one part only, so "Buttons are blue" applies to the shop and not to everything. This page shows how to list, add, archive and delete both, how to record a repo that moved or was forked, and how to pick the repo from the environment.

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
created 2026-11-02T10:00:00Z by alice · updated 2026-11-02T10:00:00Z by alice
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
lore: wrote ~/work/website/.lore/data/repos/rep_162d…9666.json
✓ repo shop registered (rep_162d…9666)
$ lore repo add help --display-name 'Help pages'
lore: wrote ~/work/website/.lore/data/repos/rep_54fe…28f2.json
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
lore: wrote ~/work/website/.lore/data/rules/rul_05e9…4603.json
✓ rul_05e9…4603
  scope: [master] severity: must
$ lore rule add --repo shop --body 'Buttons are blue.'
lore: wrote ~/work/website/.lore/data/rules/rul_c0f3…b5b8.json
✓ rul_c0f3…b5b8
  scope: [repo:shop] severity: must
$ lore rule list
rul_05e9…4603 [must/always] Run the tests before you push.
1 hidden (1 scoped to a repo); --all shows them
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
  hint: copy the whole id (its prefix, an underscore and 32 hex characters) from the list or show output of that kind
(exit 20)
$ lore repo archive rep_54fe…28f2
lore: wrote ~/work/website/.lore/data/repos/rep_54fe…28f2.json
✓ help archived
$ lore repo list
rep_2ad3…f891  website  https://github.com/client/website
rep_162d…9666  shop  https://github.com/client/website
rep_54fe…28f2  help [archived]
$ lore repo unarchive rep_54fe…28f2
lore: wrote ~/work/website/.lore/data/repos/rep_54fe…28f2.json
✓ help unarchived
$ git add -A && git commit -qm 'lore: repos' && git push -q origin main
```
<!-- /example -->

`lore repo delete <id>` and `lore project delete <id>` remove a row for good; lore asks first, and without a terminal they stop with `E_NEEDS_CONFIRM` until you add `--yes`. An example with projects is on [One store for several repos](shared-database-mode-b.md#step-3--archive-or-delete-a-project).

## Step 5 — The repo moved: record its new address

Every repo row keeps the address it was confirmed at, and lore uses it to recognise the repo in any clone. When the client renames the repo (or moves it to another host), update your remote as usual and then tell lore with `lore repo set-url <url>`: the new address becomes the primary, the old one is kept as history ("until today"), and the repo keeps its id, so every note stays attached. `--mirror` adds a second address (a copy on another host) without changing the primary; `--remove` drops a mirror again; `--repo <mount>` updates another repo of the project instead of this checkout's. The change is written to `.lore/data/_meta.json`; commit it:

<!-- example: projects#repo-moved -->
```console
# alice, in website
$ git remote set-url origin https://github.com/client/website-v2 && git push -q origin main
$ lore repo set-url git@github.com:client/website-v2.git
lore: wrote ~/work/website/.lore/data/repos/rep_2ad3…f891.json
lore: wrote ~/work/website/.lore/data/projects/prj_7a67…9504.json
✓ rep_2ad3…f891 primary address is now github.com/client/website-v2 (was github.com/client/website until today)
  .lore/data/_meta.json updated; commit it with your next push
$ lore repo set-url https://gitlab.com/client/website-v2 --mirror
lore: wrote ~/work/website/.lore/data/repos/rep_2ad3…f891.json
✓ rep_2ad3…f891 mirror address added: gitlab.com/client/website-v2
  .lore/data/_meta.json updated; commit it with your next push
$ lore repo list
rep_2ad3…f891  website  git@github.com:client/website-v2.git
rep_162d…9666  shop  https://github.com/client/website
rep_54fe…28f2  help
$ lore repo merge-ids --dry-run
✓ rep_2ad3…f891 has no alias ids; nothing to merge
$ git add -A && git commit -qm 'lore: repo address' && git push -q origin main
```
<!-- /example -->

`lore repo merge-ids` is the clean-up for a repo that has alias ids (other `rep_` ids recorded as the same repo): it rewrites the notes stamped with an alias id to the winning id, then clears the aliases. `--dry-run` lists what it would rewrite; here there is nothing to merge.

## Step 6 — A fork gets its own repo id

A fork starts as a copy of the original, `.lore/data` included, so at first it carries the original's repo id. `lore repo fork` gives the fork its own permanent id and records where it came from (`forked_from`; with `--split`, `split_from` for a repo split out of another one). It refuses while the clone's remote is still an address of the original, because then this clone *is* the original. Point `origin` at the fork first. `--new-project` also gives the fork its own project:

<!-- example: projects#fork -->
```console
# alice, in ~
$ git clone -q https://github.com/alice/website website-fork

# alice, in website-fork
$ git remote add upstream https://github.com/client/website-v2 && git remote set-url origin https://github.com/client/website-v2 && lore repo fork
lore repo fork: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/website-fork/.git/config; hook blocks in ~/work/website-fork/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: the command was refused, so the git set-up it made first is undone (.git/config, .git/hooks/commit-msg, .git/hooks/post-checkout, .git/hooks/post-commit, .git/hooks/post-merge, .git/hooks/post-rewrite, .git/hooks/pre-commit, .git/hooks/pre-push, .git/lore/git-wiring); the next write that goes through sets it up again
ERROR: [E_INVALID_INPUT] this clone's remote github.com/client/website-v2 is a confirmed address of rep_2ad3…f891, so it is that repo, not a fork
  hint: change the remote to the fork's address first (git remote set-url origin <fork url>)
(exit 20)
$ git remote set-url origin https://github.com/alice/website
$ lore repo fork
lore repo fork: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/website-fork/.git/config; hook blocks in ~/work/website-fork/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: wrote ~/work/website-fork/.lore/data/repos/rep_d431…313a.json
✓ new repo rep_d431…313a (website-fork), forked_from rep_2ad3…f891
✓ .lore/data/_meta.json updated; commit it with your next push
$ lore repo list
rep_2ad3…f891  website  git@github.com:client/website-v2.git
rep_162d…9666  shop  https://github.com/client/website
rep_54fe…28f2  help
rep_d431…313a  website-fork  https://github.com/alice/website
$ git status --short
 M .lore/data/_meta.json
?? .lore/data/repos/rep_d431…313a.json
```
<!-- /example -->

## Try it — pick the repo from the environment

`LORE_REPO` does what `--repo` does for every command in a shell, which is handy when a coding agent works inside one part of a monorepo. A `--repo` flag on the command line still wins. `LORE_PROJECT_ID` picks the project by its `prj_` id the same way; an id that names no project stops the command with `E_NOT_FOUND` instead of falling back to the folder's project:

<!-- example: projects#repo-from-the-environment -->
```console
# alice, in website
$ LORE_REPO=shop lore rule list
rul_05e9…4603 [must/always] Run the tests before you push.
rul_c0f3…b5b8 [must/always] Buttons are blue.
$ LORE_REPO=shop lore rule list --repo website
rul_05e9…4603 [must/always] Run the tests before you push.
1 hidden (1 scoped to a repo); --all shows them
$ LORE_PROJECT_ID=prj_7a67…9504 LORE_REPO=shop lore rule list
rul_05e9…4603 [must/always] Run the tests before you push.
rul_c0f3…b5b8 [must/always] Buttons are blue.
$ LORE_PROJECT_ID=prj_76bd…3f10 lore rule list
ERROR: [E_NOT_FOUND] rule: no project "prj_76bd…3f10"
  hint: run `lore project list` to see the projects
(exit 27)
```
<!-- /example -->

## Pitfalls

- **`repo archive` wants the id.** `lore repo archive help` fails with "not found" even though `--repo help` works elsewhere; copy the `rep_...` id from `lore repo list`. (The hint printed with that error points at branches and is not the cause here.)
- **An unknown `--repo` is an error, not an empty list.** A typo in the mount name stops the command, so you never act on the wrong set of notes by mistake.
- **A note's repo is fixed when you add it.** To move a rule, use `lore rule edit <id> --rebind-repo <mount>` or `--rebind-master` (back to the whole project).
- **Two `lore init`s on two branches make two projects.** When those branches merge, run `lore sync fix-projects`; see [Conflicts and duplicates](conflicts-and-duplicates.md#step-5--two-people-set-up-lore-on-separate-branches).
- **A repo set up with a local-path remote has no address yet.** A path (`/srv/git/website.git`) cannot identify a repo across clones, so lore records none. The first `lore sync` in a clone whose remote is a real address (a fresh clone on a new laptop) records it as the primary, silently, in the repos row and `.lore/data/_meta.json`: there is no "remote changed" notice, because nothing changed. Commit the two files with your next push.
- **`lore repo set-url` does not change your git remote.** Run `git remote set-url origin <url>` yourself; lore only records the address for its own matching.
- **Delete is for mistakes.** Archive is reversible and keeps history; delete is not.

## Related

- [One store for several repos](shared-database-mode-b.md)
- [How notes travel with git](how-notes-travel-with-git.md)
- [Lore for teams › Stores and links](../team/concepts.md#stores-and-links): an agency project linked to a client repo.
- [Feature index › Projects and repos](../reference/features.md#projects-and-repos)
