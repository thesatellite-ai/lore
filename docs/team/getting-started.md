# Getting started

This page takes you from nothing to a first live task, a first landing and a first org, with the output you will see at each step.

## 1. Install or upgrade

The team features are part of the normal `lore` binary; there is nothing extra to install. Install or upgrade the same way you installed lore (the [README](../../README.md#install) lists Homebrew, the install script and Windows), then check the version:

<!-- example: getting-started#version -->
```console
# alice, in notes-app
$ lore --version
lore version 0.1.0-dev
```
<!-- /example -->

Every teammate who works with live rows should run the same or a newer lore. A live ref records the lowest version allowed to write to it; an older binary stays read-only there and says so (`E_OLD_LORE`).

## 2. Set up lore in a repo

In a repo that has a remote (here `github.com/alice/notes-app`), run `lore init` once and commit what it wrote:

<!-- example: getting-started#init -->
```console
# alice, in notes-app
$ lore init --non-interactive --name=notes-app
✓ lore initialized at /home/alice/work/notes-app
    project_id: prj_01a1…3c7b
    name:       notes-app
    origin:     https://github.com/alice/notes-app
    db:         /home/alice/work/notes-app/.lore/lore.db
    identity:   human:alice@example.com (git config)
  next: lore learn-from docs   to bootstrap from existing markdown
lore sync: exported 2 rows to .lore/data — review and commit it
$ git add -A && git commit -qm 'lore: init' && git push -q origin main
```
<!-- /example -->

`lore init` also installs lore's merge driver and a few small git hook blocks (`pre-commit`, `commit-msg`, `post-commit`, `pre-push`, and fetch helpers). They chain into whatever hooks you already have and do nothing when lore is not installed.

## 3. See who lore thinks you are

With no config at all, there is no org, and your identity comes from your git email:

<!-- example: getting-started#whoami-without-config -->
```console
# alice, in notes-app
$ lore whoami
org        none  (matched by: nothing matched)
identity   alice <human:alice@example.com>   act_3686…c40e  (from: git config)
repo       no org  (fallback identity, personal everywhere/ notes only)
project    prj_01a1…3c7b
remotes    github.com/alice/notes-app
links      not checked  store links are not read yet (they arrive with `lore org link` and stores)
personal   local: everywhere/
config     /home/alice/.lore/config.yaml does not exist (no orgs configured)
```
<!-- /example -->

## 4. Your first live task

Tasks are live by default. Adding one writes it to the hidden ref `refs/lore/live` and pushes that ref at once; your branch, your staging area and your working folder do not change:

<!-- example: getting-started#first-live-task -->
```console
# alice, in notes-app
$ lore task add 'fix login timeout'
✓ tsk_01a1…d4bd tsk_01a1…d4bd — fix login timeout [medium]
lore live: (live, pushed)
$ lore task list
○ tsk_01a1…d4bd fix login timeout [medium]
$ git status --short
$ git ls-remote origin
b5b3ee63c227	HEAD
b5b3ee63c227	refs/heads/main
36b5c6c22cfe	refs/lore/live
$ lore live show
task_lists tlt_01a1…0ec9     live      since 2026-11-02 18:41
tasks      tsk_01a1…d4bd     live      since 2026-11-02 18:41
2 live rows in _root (epoch 1)
```
<!-- /example -->

Note what `git status` and `git ls-remote` show: no file changed in your checkout, and the remote now has a `refs/lore/live` next to `main`. Every teammate's next lore command fetches it, whatever branch they are on. `lore live show` lists the rows that are live right now (the task list row is the default list lore created for the task).

## 5. Your first landing

Finishing a task lands it: lore writes it to `main`'s `.lore/data` with a commit of its own, pushed straight to the remote, without touching your local `main`:

<!-- example: getting-started#first-landing -->
```console
# alice, in notes-app
$ lore task done tsk_01a1…d4bd
✓ tsk_01a1…d4bd done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed)
lore live: landed on main (7217f21); leaves live once main is fetched
$ git fetch -q origin && git log --oneline origin/main
7217f21 lore: land 2 rows (_root)
b5b3ee6 lore: init
3c0a2dd init
$ lore live show
task_lists tlt_01a1…0ec9     live      since 2026-11-02 18:41
tasks      tsk_01a1…d4bd     live      since 2026-11-02 18:41
2 live rows in _root (epoch 1)
$ lore sync
· already in sync
lore live: 2 row(s) left live (main holds them)
$ lore live show
0 live rows in _root (epoch 1)
```
<!-- /example -->

Right after the landing the rows are still live ("leaves live once main is fetched"). The next command that fetches (here `lore sync`) sees that `main` holds the same content and removes them from live. If your `main` is protected, the landing rides along on your next push instead; see [Small team in one repo](use-cases/small-team.md#when-main-is-protected).

## 6. Old TOML identity files move to YAML by themselves

If you used lore before, your identity may be in `~/.lore/identity.toml`. The first command of the new version moves it to `identity.yaml` and keeps the old file:

<!-- example: getting-started#toml-migration -->
```console
# alice, in notes-app
$ lore whoami
lore: migrated /home/alice/.lore/identity.toml → identity.yaml (old file kept as identity.toml.migrated)
org        none  (matched by: nothing matched)
identity   Alice Doe <human:alice@example.com>   act_3686…c40e  (from: identity.yaml)
repo       no org  (fallback identity, personal everywhere/ notes only)
project    prj_01a1…3c7b
remotes    github.com/alice/notes-app
links      not checked  store links are not read yet (they arrive with `lore org link` and stores)
personal   local: everywhere/
config     /home/alice/.lore/config.yaml does not exist (no orgs configured)

# alice, in ~
$ ls ~/.lore; cat ~/.lore/identity.yaml
cache
identity.toml.migrated
identity.yaml
display_name: Alice Doe
stable_key: human:alice@example.com
```
<!-- /example -->

Nothing else is needed. The same happens to a project's shared-database pointer, `.lore/lore.toml`, which becomes `.lore/lore.yaml` (commit the rename if it is tracked).

## 7. A first config with an org

Everything that involves a company (stores, a roster, client guards) is set in `~/.lore/config.yaml`. A minimal one for someone working at Northwind, with a fallback identity for personal repos:

<!-- example: getting-started#first-config only=1 -->
```console
# alice, in ~
$ cat ~/.lore/config.yaml
identity:
  key: human:alice@example.com
  name: Alice Doe
personal:
  store: local
orgs:
  - name: northwind
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - url: git@github.com:northwind/lore
        org_folder: org
    own:   [github.com/northwind/*]
    repos: [github.com/acme/*]
```
<!-- /example -->

| Key | What it says |
|---|---|
| `identity` | who you are in repos that match no org |
| `personal.store` | where your personal notes live: `local` (`~/.lore/personal`) or a git URL of your own private repo |
| `orgs[].name` | the org's name, also used in `--to northwind` |
| `orgs[].identity` | who you are inside this org: `key` (`human:<your email there>`), `name`, `handle` (your `@name` on the roster) |
| `orgs[].stores` | the org's store repos; `org_folder` marks the folder that holds the roster and company rules |
| `orgs[].own` | URL patterns of the org's own repos (no client guards) |
| `orgs[].repos` | URL patterns of client repos (guards on) |

Check it, create the store (once per company, in a new empty repo), and see the result in one of the org's own repos:

<!-- example: getting-started#first-config only=2,3,5,6 -->
```console
# alice, in ~
$ lore config check
✓ /home/alice/.lore/config.yaml is valid: 1 org entry (northwind)
$ lore store init git@github.com:northwind/lore
✓ wrote lore-store.yaml to github.com/northwind/lore (new branch main, first commit) and pushed

# alice, in tools
$ lore whoami
org        northwind  (matched by: own pattern github.com/northwind/* of northwind)
identity   Alice Doe <human:alice@northwind.example>   act_0263…5705  @alice  (from: org entry (config.yaml))
repo       own repo  (no client guards)
project    prj_01a1…b47e
remotes    github.com/northwind/tools
stores     git@github.com:northwind/lore on branch main  (org folder org)
links      known  0 link(s) for this repo (stores of 1 entry scanned)
personal   local: northwind/, everywhere/
$ lore actor register
✓ act_0263…5705 Alice Doe (@alice) registered in northwind org/
lore: [northwind/org] 3 file(s) pushed
```
<!-- /example -->

`lore whoami` now names the org, the rule that chose it (`own pattern`), your identity there and the store. `lore actor register` put you on the org's roster, so teammates can write `--assign @alice`.

Repos outside the org keep your fallback identity, never a company one:

<!-- example: getting-started#first-config only=7 -->
```console
# alice, in notes-app
$ lore whoami
org        none  (matched by: nothing matched)   hint: lore org use northwind
identity   Alice Doe <human:alice@example.com>   act_3686…c40e  (from: top-level identity (config.yaml))
repo       no org  (fallback identity, personal everywhere/ notes only)
project    prj_01a1…3c7b
remotes    github.com/alice/notes-app
links      known  0 link(s) for this repo (stores of 1 entry scanned)
personal   local: everywhere/
```
<!-- /example -->

## Next

- Working with teammates in one repo: [Small team in one repo](use-cases/small-team.md).
- Working in a client's repo: [Agency with a client repo](use-cases/agency-client-repo.md).
- Every option of `config.yaml`: [Commands reference](commands.md#config-keys).
