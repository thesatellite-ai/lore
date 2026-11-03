# Getting started

**In short:** install lore and set it up in a repo. Add a first task that your teammates see at once. Then connect your company's private notes repo. Each step shows the real output you will see.

This page takes you from nothing to a first live task, a first landing and a first org. The examples follow Alice (an agency developer).

## 1. Install or upgrade

The team features are part of the normal `lore` binary. There is nothing extra to install. Install or upgrade the same way you installed lore (the [README](../../README.md#install) lists Homebrew, the install script and Windows). Then check the version:

<!-- example: getting-started#version -->
```console
# alice, in notes-app
$ lore --version
lore version 0.1.0-dev
```
<!-- /example -->

Every teammate who works with live rows should run the same or a newer lore. A live ref records the lowest version allowed to write to it. An older binary can only read there, and says so (`E_OLD_LORE`).

## 2. Set up lore in a repo

In a repo that has a remote (here `github.com/alice/notes-app`), run `lore init` once and commit what it wrote:

<!-- example: getting-started#init -->
```console
# alice, in notes-app
$ lore init --non-interactive --name=notes-app
✓ lore initialized at ~/work/notes-app
    project_id: prj_7a67…9504
    name:       notes-app
    origin:     https://github.com/alice/notes-app
    db:         ~/work/notes-app/.lore/lore.db
    identity:   human:alice@example.com (git config)
  next: lore learn-from docs   to bootstrap from existing markdown
lore sync: exported 2 rows to .lore/data — review and commit it
$ git add -A && git commit -qm 'lore: init' && git push -q origin main
```
<!-- /example -->

`lore init` also installs lore's merge driver: a small helper git calls to combine two edits of the same note. It adds a few small git hook blocks too (scripts git runs at set moments: `pre-commit`, `commit-msg`, `post-commit`, `pre-push`, and fetch helpers). They chain into whatever hooks you already have. They do nothing when lore is not installed.

## 3. See who lore thinks you are

With no config at all, there is no org. Your identity comes from your git email:

<!-- example: getting-started#whoami-without-config -->
```console
# alice, in notes-app
$ lore whoami
org        none  (matched by: nothing matched)
identity   alice <human:alice@example.com>   act_2ad3…f891  (from: git config)
repo       no org  (fallback identity, personal everywhere/ notes only)
project    prj_7a67…9504
remotes    github.com/alice/notes-app
links      not checked  store links are not read yet (they arrive with `lore org link` and stores)
personal   local: everywhere/
config     ~/.lore/config.yaml does not exist (no orgs configured)
```
<!-- /example -->

## 4. Your first live task

Tasks are live by default. Adding one writes it to a hidden list on GitHub that is not a branch (the hidden ref `refs/lore/live`), and pushes it at once. Your branch, your staging area and your working folder do not change:

<!-- example: getting-started#first-live-task -->
```console
# alice, in notes-app
$ lore task add 'fix login timeout'
✓ tsk_162d…9666 — fix login timeout [medium]
lore live: (live, pushed)
$ lore task list
○ tsk_162d…9666 fix login timeout [medium]
$ git status --short
$ git ls-remote origin
601d2850ffd0	HEAD
601d2850ffd0	refs/heads/main
86dbae476bd6	refs/lore/live
$ lore live show
task_lists tlt_54fe…28f2     live      since 2026-11-02 10:00
tasks      tsk_162d…9666     live      since 2026-11-02 10:00
2 live rows in _root (epoch 1)
```
<!-- /example -->

Look at what `git status` and `git ls-remote` show. No file changed in your checkout. The remote now has a `refs/lore/live` next to `main`. Every teammate's next lore command fetches it, whatever branch they are on. `lore live show` lists the rows that are live right now. The task list row is the default list lore created for the task.

## 5. Your first landing

Finishing a task lands it. Lore writes it to `main`'s `.lore/data` with a commit of its own and pushes that straight to the remote. Your local `main` is not touched:

<!-- example: getting-started#first-landing -->
```console
# alice, in notes-app
$ lore task done tsk_162d…9666
✓ tsk_162d…9666 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed)
lore live: landed on main (9b40ce7); leaves live once main is fetched
$ git fetch -q origin && git log --oneline origin/main
9b40ce7 lore: land 2 rows (_root)
601d285 lore: init
05513aa init
$ lore live show
task_lists tlt_54fe…28f2     live      since 2026-11-02 10:00
tasks      tsk_162d…9666     live      since 2026-11-02 10:00
2 live rows in _root (epoch 1)
$ lore sync
· already in sync
lore live: 2 row(s) left live (main holds them)
$ lore live show
0 live rows in _root (epoch 1)
```
<!-- /example -->

Right after the landing the rows are still live ("leaves live once main is fetched"). The next command that fetches (here `lore sync`) sees that `main` holds the same content. It then removes them from live. If your `main` is protected, the landing rides along on your next push instead. See [Small team in one repo](use-cases/small-team.md#when-main-is-protected).

## 6. Old TOML identity files move to YAML by themselves

If you used lore before, your identity may be in `~/.lore/identity.toml`. The first command of the new version moves it to `identity.yaml`. It keeps the old file:

<!-- example: getting-started#toml-migration -->
```console
# alice, in notes-app
$ lore whoami
lore: migrated ~/.lore/identity.toml → identity.yaml (old file kept as identity.toml.migrated)
org        none  (matched by: nothing matched)
identity   Alice Doe <human:alice@example.com>   act_2ad3…f891  (from: identity.yaml)
repo       no org  (fallback identity, personal everywhere/ notes only)
project    prj_7a67…9504
remotes    github.com/alice/notes-app
links      not checked  store links are not read yet (they arrive with `lore org link` and stores)
personal   local: everywhere/
config     ~/.lore/config.yaml does not exist (no orgs configured)

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

Your **identity key** is your email address with `human:` in front (`human:alice@example.com`). Lore uses it, not your name, to tell people apart. So keep it the same on every machine.

## 7. A first config with an org

Everything that involves a company is set in `~/.lore/config.yaml`. That means stores (separate private repos for the company's notes), a roster (the list of people and agents in the company) and client guards (checks that keep internal notes out of the client's repo). Here is a minimal config for someone at the agency. It also has a fallback identity for personal repos:

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
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    own:   [github.com/agency/*]
    repos: [github.com/client/*]
```
<!-- /example -->

| Key | What it says |
|---|---|
| `identity` | who you are in repos that match no org |
| `personal.store` | where your personal notes live: `local` (`~/.lore/personal`) or a git URL of your own private repo |
| `orgs[].name` | the org's name, also used in `--to agency` |
| `orgs[].identity` | who you are inside this org: `key` (`human:<your email there>`), `name`, `handle` (your `@name` on the roster) |
| `orgs[].stores` | the org's store repos; `org_folder` marks the folder that holds the roster and company rules |
| `orgs[].own` | URL patterns of the org's own repos (no client guards) |
| `orgs[].repos` | URL patterns of client repos (guards on) |

Check it. Create the store (once per company, in a new empty repo). Then see the result in one of the org's own repos:

<!-- example: getting-started#first-config only=2,3,5,6 -->
```console
# alice, in ~
$ lore config check
✓ ~/.lore/config.yaml is valid: 1 org entry (agency)
$ lore store init git@github.com:agency/notes
✓ wrote lore-store.yaml to github.com/agency/notes (new branch main, first commit) and pushed

# alice, in tools
$ lore whoami
org        agency  (matched by: own pattern github.com/agency/* of agency)
identity   Alice Doe <human:alice@agency.example>   act_c0f3…b5b8  @alice  (from: org entry (config.yaml))
repo       own repo  (no client guards)
project    prj_05e9…4603
remotes    github.com/agency/tools
stores     git@github.com:agency/notes on branch main  (org folder org)
links      known  0 link(s) for this repo (stores of 1 entry scanned)
personal   local: agency/, everywhere/
$ lore actor register
✓ act_c0f3…b5b8 Alice Doe (@alice) registered in agency org/
lore: [agency/org] 3 file(s) pushed
```
<!-- /example -->

`lore whoami` now names the org, the rule that chose it (`own pattern`), your identity there and the store. Run without a terminal (a script, an agent's tool call) or with a CI variable set, it prints `<redacted>` in place of store URLs and internal folders, so they never reach a log someone else reads; see [Troubleshooting](troubleshooting.md#whoami-prints-redacted). `lore actor register` put you on the org's roster. Now teammates can write `--assign @alice`.

Repos outside the org keep your fallback identity, never a company one:

<!-- example: getting-started#first-config only=7 -->
```console
# alice, in notes-app
$ lore whoami
org        none  (matched by: nothing matched)   hint: lore org use agency
identity   Alice Doe <human:alice@example.com>   act_2ad3…f891  (from: top-level identity (config.yaml))
repo       no org  (fallback identity, personal everywhere/ notes only)
project    prj_7a67…9504
remotes    github.com/alice/notes-app
links      known  0 link(s) for this repo (stores of 1 entry scanned)
personal   local: everywhere/
```
<!-- /example -->

## Next

- Working with teammates in one repo: [Small team in one repo](use-cases/small-team.md).
- Working in a client's repo: [Agency with a client repo](use-cases/agency-client-repo.md).
- Every option of `config.yaml`: [Commands reference](commands.md#config-keys).

## Pitfalls

- **Commit after `lore init`.** The new `.lore/` files belong in git like any other project file.
- **Use your email as the identity key** (`human:alice@example.com`), never a display name.
- **Run `lore config check` after every config edit.** A wrong key is reported there, before any command uses it.
- **A teammate's new row may take up to 60 seconds to show.** Run `lore sync` to fetch now.

## Related

- [Identity and stores](identity-and-stores.md): change your identity, join an org with `lore org import`.
- [Troubleshooting](troubleshooting.md): the errors you may see on the way.
