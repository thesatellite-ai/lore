# Getting started

**In short:** install lore and set it up in a repo. Add a first task that your teammates see on any branch. Then connect your company's private notes repo. Each step shows the real output you will see.

This page takes you from nothing to a first live task, a first landing and a first org. The examples follow Alice (an agency developer).

## Before you start

- A git repo with a remote you can push to (here `github.com/alice/notes-app`). Live rows and landings travel through that remote.
- `git` configured with your email (`git config user.email`): with no lore config at all, that email is your identity.
- For Step 7 only: a new, empty private repo for your company's notes (the store), and the URL patterns of your company's repos and of the client repos you work in.

## Step 1 — Install or upgrade

The team features are part of the normal `lore` binary. There is nothing extra to install. Install or upgrade the same way you installed lore (the [README](../../README.md#install) lists Homebrew, the install script and Windows). Then check the version:

<!-- example: getting-started#version -->
```console
# alice, in notes-app
$ lore --version
lore version dev
```
<!-- /example -->

Every teammate who works with live rows should run the same or a newer lore. A live ref records the lowest version allowed to write to it. An older binary can only read there, and says so (`E_OLD_LORE`).

## Step 2 — Set up lore in a repo

In a repo that has a remote (here `github.com/alice/notes-app`), run `lore init` once and commit what it wrote:

<!-- example: getting-started#init -->
```console
# alice, in notes-app
$ lore init --name=notes-app
lore init: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/notes-app/.git/config; lore's merge rules in ~/work/notes-app/.gitattributes; hook blocks in ~/work/notes-app/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: wrote 4 row file(s) in ~/work/notes-app/.lore/data
lore initialized
    project_id: prj_7a67…9504
    name:       notes-app
    repo_id:    rep_2ad3…f891
· context targets: codex (AGENTS.md) (no AI tool file found; AGENTS.md is the cross-tool file, change with --target <profile>)
✓ ~/work/notes-app/AGENTS.md (pointer → .lore/LORE.md)
· saved as context.targets; change with: lore policy set --key context.targets --value <profiles> --yes
  next: lore render            to write .lore/LORE.md for those tools
$ git add -A && git commit -qm 'lore: init' && git push -q origin main
```
<!-- /example -->

`lore init` also installs lore's merge driver: a small helper git calls to combine two edits of the same note. It adds a few small git hook blocks too (scripts git runs at set moments: `pre-commit`, `commit-msg`, `post-commit`, `pre-push`, and fetch helpers). They chain into whatever hooks you already have. They do nothing when lore is not installed.

## Step 3 — See who lore thinks you are

With no config at all, there is no org. Your identity comes from your git email. In a repo with a hosted remote, the `join` line says how to join an org on this machine (a new laptop of someone who already joined needs only the store url from their admin; the org's admin, joining their own second laptop the same way, gets an admin setup there):

<!-- example: getting-started#whoami-without-config -->
```console
# alice, in notes-app
$ lore whoami
org        none  (matched by: nothing matched)
join       if this repo is your org's, join the org on this machine: lore setup join --invite <your org's store url> (the line your admin's `lore member add` printed; on a machine that already joined, `lore setup list` shows the store)
identity   alice <human:alice@example.com>   act_162d…9666  (from: git config user.email)
repo       no org  (fallback identity)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_7a67…9504
remotes    github.com/alice/notes-app
links      not checked  store links are not read yet (they arrive with `lore org link` and stores)
personal   not set
config     ~/.lore/config.yaml does not exist (no orgs configured)
```
<!-- /example -->

## Step 4 — Your first live task

Tasks are live by default. Adding one writes it to a hidden list on GitHub that is not a branch (the hidden ref `refs/lore/live`). Nothing goes to the remote by itself: `lore sync` or your next `git push` sends it (or set `live.autopush: true` to send each write at once). Your branch, your staging area and your working folder do not change:

<!-- example: getting-started#first-live-task -->
```console
# alice, in notes-app
$ lore task add --title 'fix login timeout'
lore: wrote task_lists/tlt_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/notes-app/.lore/data/task_lists/tlt_54fe…28f2.json is unchanged)
lore: wrote tasks/tsk_05e9…4603 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/notes-app/.lore/data/tasks/tsk_05e9…4603.json is unchanged)
✓ tsk_05e9…4603 — fix login timeout [medium]
  created tasklist tlt_54fe…28f2 "default" for tasks added without --tasklist
lore live: (live, kept here: `lore sync` or your next `git push` sends live writes; `lore config set --key live.autopush --value true` sends each write at once)
$ lore task list
○ tsk_05e9…4603 fix login timeout [medium]
$ git status --short
$ lore sync && git ls-remote origin
lore live: pushed waiting live writes commit 601d285 to origin refs/lore/live
· already in sync
86dbae476bd6	HEAD
86dbae476bd6	refs/heads/main
601d2851af28	refs/lore/live
$ lore live show
task_lists tlt_54fe…28f2     live      since 2026-11-02 10:00
tasks      tsk_05e9…4603     live      since 2026-11-02 10:00
2 live rows in _root (epoch 1)
```
<!-- /example -->

Look at what `git status` and `git ls-remote` show. No file changed in your checkout. After `lore sync` the remote has a `refs/lore/live` next to `main` (before it, the task was only on your machine). Every teammate sees it after their next fetch (`lore sync` fetches at once), whatever branch they are on. `lore live show` lists the rows that are live right now. The task list row is the default list lore created for the task.

## Step 5 — Your first landing

Finishing a task marks it ready to land; nothing goes to the remote by itself. `lore sync` lands it: lore writes it to `main`'s `.lore/data` with a commit of its own and pushes that to the remote, and your local `main` is not touched. Your own `git push` of `main` lands it too: the pre-push hook adds the `lore: land N rows` commit to your `main` and asks you to push again, so the push carries it:

<!-- example: getting-started#first-landing -->
```console
# alice, in notes-app
$ lore task done --id tsk_05e9…4603
lore: wrote tasks/tsk_05e9…4603 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/notes-app/.lore/data/tasks/tsk_05e9…4603.json is unchanged)
✓ tsk_05e9…4603 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
$ git fetch -q origin && git log --oneline origin/main
86dbae4 lore: init
05513aa init
$ lore live show
task_lists tlt_54fe…28f2     live      since 2026-11-02 10:00
tasks      tsk_05e9…4603     live      since 2026-11-02 10:00
2 live rows in _root (epoch 1)
$ lore sync
lore live: pushed waiting live writes commit dfd8dc8 to origin refs/lore/live
lore live: landed on main (cf563f3); leaves live once main is fetched
$ lore sync >/dev/null && lore live show
lore live: pushed waiting live writes commit df3a55c to origin refs/lore/live
0 live rows in _root (epoch 1)
```
<!-- /example -->

Right after the landing the rows are still live ("leaves live once main is fetched"). The next fetch (here a second `lore sync`) sees that `main` holds the same content and removes them from live. If your `main` is protected, `lore sync` cannot land them, and the landing rides along on your next commit instead. See [Small team in one repo](use-cases/small-team.md#when-main-is-protected).

## Step 6 — A v0.1.17 identity file moves into config.yaml

If you used lore v0.1.17, your identity may be in `~/.lore/identity.yaml`. 0.1.18 still reads it, but only while `identity:` in `~/.lore/config.yaml` is empty, and `lore identity show` warns about it. `lore migrate run` moves it into `config.yaml` and keeps the old file as `identity.yaml.migrated`:

<!-- example: getting-started#old-identity-file -->
```console
# alice, in notes-app
$ lore identity show
resolved:    human:alice@example.com
display:     Alice Doe
kind:        human
source:      ~/.lore/identity.yaml
stable:      yes
warning: ~/.lore/identity.yaml is from v0.1.17 and is read only while identity: in ~/.lore/config.yaml is unset: `lore migrate run` moves it into identity:
$ lore migrate run
lore: wrote ~/work/notes-app/.lore/data/actors/act_162d…9666.json
Upgraded 1 row
  identity: ~/.lore/identity.yaml moved into ~/.lore/config.yaml; old file kept as ~/.lore/identity.yaml.migrated
  actor_copies: refreshed 1 actor copy from their source: Alice Doe (act_162d…9666)
team lock: none (this machine's migration lock only; nothing written to the remote for it); `--team-lock` also holds refs/lore/signals on the remote while the run lasts, so a teammate cannot migrate at the same time
backup: refs/lore-backup/migrate/2026-11-02 (local only, never pushed)
branch (.lore/data): 1 files upgraded, not committed (lore never commits a code repo)
undo: lore migrate rollback
1 upgraded file in .lore/data waits for your commit: git add .lore/data && git commit -m "lore: migrate rows to format 2"

# alice, in ~
$ ls ~/.lore; cat ~/.lore/config.yaml
README.md
cache
config.yaml
config.yaml.lock
identity.yaml.migrated
machine.json
state
identity:
  key: human:alice@example.com
  name: Alice Doe
```
<!-- /example -->

The `actor_copies` line is the copy of Alice's actor row in this repo, written before the v0.1.17 identity file named her Alice Doe, brought up to date from her identity; commit `.lore/data` as the run says. In a repo outside any org, or a client repo, the run also trims every actor copy in `.lore/data` to {id, display name, kind} (an `actor_projection:` line names the keys it dropped, such as the `stable_key` lore 0.1.17 wrote), so the migrated files pass the client-repo commit check and `lore check leaks --ci`; an org's own repo keeps them.

An identity file from before v0.1.17 (`~/.lore/identity.toml`) is not read at all: set who you are with `lore identity set --identity <email>`, and `lore identity unset` removes the old file. A project's v0.1.17 shared-database pointer (`.lore/lore.yaml`, or the older `.lore/lore.toml`) is read only by `lore migrate run`, which converts the project to row files (see [Shared database (Mode B)](../sync/shared-database-mode-b.md)).

Your **identity key** is your email address with `human:` in front (`human:alice@example.com`). Lore uses it, not your name, to tell people apart. So keep it the same on every machine.

## Step 7 — A first config with an org

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
| `personal.store` | where your personal notes live (`--to me`): a git URL of your own private repo, which every machine you use attaches with `lore store init --name me --url <url>`; a folder `file://~/.lore/stores/_me` (what `lore store init --name me` with no URL makes); or the lore 0.1.17 value `local` (`~/.lore/personal`, used in these examples). See [Personal notes](use-cases/personal-notes.md) |
| `orgs[].name` | the org's name, also used in `--to agency` |
| `orgs[].identity` | who you are inside this org: `key` (`human:<your email there>`), `name`, `handle` (your `@name` on the roster) |
| `orgs[].stores` | the org's store repos; `org_folder` marks the folder that holds the roster and company rules |
| `orgs[].own` | URL patterns of the org's own repos (no client guards) |
| `orgs[].repos` | URL patterns of client repos (guards on) |

Check it. Create the store (once per company, from a new empty repo): `lore store init --name <name> --url <url>` runs inside one of the org's own repos and attaches the store there by name. Then see the result in that repo:

<!-- example: getting-started#first-config only=2,4,5,6 -->
```console
# alice, in ~
$ lore config check
✓ ~/.lore/config.yaml is valid: 1 org entry (agency)

# alice, in tools
$ lore store init --name agency --url git@github.com:agency/notes --yes
lore store init: store clone ~/.lore/stores/agency/notes: git user set to Alice Doe <alice@agency.example> (your identity in org agency)
✓ created store agency (sto_d431…313a) at git@github.com:agency/notes; recorded in .lore/stores.yaml; commit it to share
$ lore whoami
org        agency  (matched by: own pattern github.com/agency/* of agency)
identity   Alice Doe <human:alice@agency.example>   act_b4f4…a5b0  @alice  (from: org entry (config.yaml orgs[agency].identity))
repo       own repo  (no client guards)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_c0f3…b5b8
remotes    github.com/agency/tools
stores     git@github.com:agency/notes on branch main  (org folder org)
links      known  0 link(s) for this repo (stores of 1 entry scanned)
personal   me -> ~/.lore/personal
$ lore actor register
lore: committed locally to store agency/org; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/org/projects/prj_28f8…03df.json (store agency/org)
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_b4f4…a5b0.json (store agency/org)
✓ act_b4f4…a5b0 Alice Doe (@alice) registered in agency org/
```
<!-- /example -->

`lore whoami` now names the org, the rule that chose it (`own pattern`), your identity there and the store. It prints the store URL the same way without a terminal or in CI; only a credential inside a URL is shown as `<redacted>` (see [Troubleshooting](troubleshooting.md#a-url-shows-redacted)). `lore actor register` put you on the org's roster. Now teammates can write `--assign @alice`.

Repos outside the org keep your fallback identity, never a company one:

<!-- example: getting-started#first-config only=7 -->
```console
# alice, in notes-app
$ lore whoami
org        none  (matched by: nothing matched)   hint: lore org use --entry agency
identity   Alice Doe <human:alice@example.com>   act_162d…9666  (from: top-level identity (config.yaml))
repo       no org  (fallback identity)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_7a67…9504
remotes    github.com/alice/notes-app
links      known  0 link(s) for this repo (stores of 1 entry scanned)
personal   me -> ~/.lore/personal
```
<!-- /example -->

## Try it

In any repo where you ran `lore init`, add a task and list it. The task is live: after your next `lore sync` or `git push`, teammates see it on their next fetch, before any commit lands on main:

<!-- example: getting-started#first-live-task only=1,2 -->
```console
# alice, in notes-app
$ lore task add --title 'fix login timeout'
lore: wrote task_lists/tlt_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/notes-app/.lore/data/task_lists/tlt_54fe…28f2.json is unchanged)
lore: wrote tasks/tsk_05e9…4603 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/notes-app/.lore/data/tasks/tsk_05e9…4603.json is unchanged)
✓ tsk_05e9…4603 — fix login timeout [medium]
  created tasklist tlt_54fe…28f2 "default" for tasks added without --tasklist
lore live: (live, kept here: `lore sync` or your next `git push` sends live writes; `lore config set --key live.autopush --value true` sends each write at once)
$ lore task list
○ tsk_05e9…4603 fix login timeout [medium]
```
<!-- /example -->

## Pitfalls

- **Commit after `lore init`.** The new `.lore/` files belong in git like any other project file.
- **Use your email as the identity key** (`human:alice@example.com`), never a display name.
- **Run `lore config check` after every config edit.** A wrong key is reported there, before any command uses it.
- **A teammate's new row shows after your next fetch, once they have run `lore sync` or `git push`.** Run `lore sync` to fetch now. Nothing you write is pushed until you do the same.
- **Every teammate needs the same or a newer lore.** A live ref records the lowest version allowed to write to it; an older binary can only read there and answers `E_OLD_LORE` to a write.

## Related

- Working with teammates in one repo: [Small team in one repo](use-cases/small-team.md).
- Working in a client's repo: [Agency with a client repo](use-cases/agency-client-repo.md).
- Every option of `config.yaml`: [Commands reference](commands.md#config-keys).
- [Identity and stores](identity-and-stores.md): change your identity, join an org with `lore org import`.
- [Troubleshooting](troubleshooting.md): the errors you may see on the way.
