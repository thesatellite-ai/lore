# Guided setup: `lore setup` and many setups on one machine

**In short:** a *setup* is one way you work on this machine: who you are, which GitHub account you use, where your own notes go, which team you belong to, and which folders it is for. You can have several (say, one for your own projects and one for a team), and lore picks the right one from the folder you are in. `lore setup` walks you through making one, step by step, with checks; every step is also a command, so a script or an agent can do the same.

> **New in lore 0.1.18.** The guided page in the browser ships in the prebuilt lore binaries (GitHub Releases, Homebrew, the install scripts). Everything on this page works in a build from source too, through the `lore setup` commands. The output below is a real run (`tests/team/docs/run-examples.sh guided_setup`) in a throwaway home folder with a fake GitHub: a fake `ssh` answers as GitHub would, and `gh` has no login, so nothing reaches the network.

## What it is and when you want it

Before setups, lore had one identity and one personal store per machine. That breaks the day you work for a client under one GitHub account and on your own projects under another: notes land in the wrong store, commits carry the wrong email, and pushes use the wrong key. A setup ties those choices to folders. Setups live only in `~/.lore/config.yaml` under `setups:`; they are never written to a store or a repo, and they never hold a secret (at most a key file path such as `~/.ssh/id_ed25519` and its fingerprint, never the key itself).

There are three kinds:

| Kind | On the page | What it is for |
|---|---|---|
| `solo` | Just me | your own projects; the setup carries its own identity (name and email) |
| `member` | Join a team | a team someone invited you to; your identity is the one in that org's entry (`orgs:` in `~/.lore/config.yaml`) |
| `admin` | Start a team | a team you run: lore creates the team's store and org and makes you its first member; only an admin setup can send [invites](invites.md) |

A setup name is lowercase letters, digits, `_` or `-`, at most 64 characters, and may not be `repo`, `me`, `team` or `company` (those already mean something to `--to`).

You want this page when you set lore up on a new machine, when you work for more than one team or client, when you join a team from an invite, or when lore wrote something as the wrong person and you need to know why. For every `lore setup` subcommand with its output, see [Setups from the command line](setup-cli.md).

## Before you start

- lore 0.1.18 or later (`lore version`).
- git, and an ssh key that GitHub accepts, for any setup that uses GitHub. `gh` is optional: with it lore can also check that a repo is private and offer to create one.
- The example machine belongs to Tomas Weber (`@tomas`, who runs the `agency` team, store `git@github.com:agency/notes`); Mira Patel (`@mira`) is a developer he invites. Tomas has two setups, written by hand here (the setup page writes the same entries):

```yaml
setups:
  - name: agency
    kind: admin
    org: agency
    default: true
    folders:
      - ~/work/agency/**
    github:
      user: tomas-dev
      host_alias: github.com
  - name: personal
    kind: solo
    folders:
      - ~/work/personal/**
    identity:
      key: human:tomas@home.example
      name: Tomas Weber
    personal_store: none
```

## Step 1 — See the steps a setup has

A setup is made one step at a time. Each step asks a few questions, runs its checks as you answer, and saves only when no error-level check fails. `lore setup plan --kind solo|member|admin` lists the steps for a kind (add `--json` for the questions and checks of each):

<!-- example: guided-setup#plan only=1 -->
```console
# tomas, in ~
$ lore setup plan --kind member | cut -c1-52
steps:
  ID              TITLE                             
  kind            What kind of setup is this?       
  identity        Who are you?                      
  github          Which GitHub account?             
  personal_store  Where do your own notes go?       
  join            Join your team                    
  tools           Which coding tools do you use?    
  secrets         Where do you keep secrets?        
  folders         Which folders is this setup for?  
  this_folder     This folder                       
  summary         Summary                           
progress:
  SETUP  STEP  ANSWERS  CHECKS  STATUS  UPDATED_AT
```
<!-- /example -->

| Step id | Title | Kinds | What it saves |
|---|---|---|---|
| `kind` | What kind of setup is this? | all | the setup's kind and name; for `solo`, whether it is the default |
| `identity` | Who are you? | all | your name, email, other addresses and handle (a `member` setup is prefilled with the invite's email) |
| `github` | Which GitHub account? | all | the GitHub user you pick, and the route lore found for it: `route: ssh` with the host alias (`github.com` or a `Host` block of yours) and the key's fingerprint (plus the key path when a `.pub` sits beside it or you gave one), or `route: https` (the account's gh login) |
| `personal_store` | Where do your own notes go? | all | your personal store (`--to me`): create a new private repo, use an existing one, a local folder (`file://~/.lore/stores/_me`), the same as your other setups, or none |
| `join` | Join your team | member | the invite you were sent; joins the org and its roster |
| `team` | Start your team | admin | the team name, a new or empty private repo for its store, the team's own repo patterns and client repo patterns |
| `tools` | Which coding tools do you use? | all, optional | the tool profiles (`claude_code`, `codex`, …); lore installs its skill for each ([skill install](../knowledge/skill-install.md)) |
| `secrets` | Where do you keep secrets? | all, optional | the default secret manager for new secret references (lore only asks it for its version) |
| `folders` | Which folders is this setup for? | all, optional | folder globs and whether this is the default setup |
| `this_folder` | This folder | all, optional | nothing; shown only when you started inside a git repo: which setup this repo uses and whether lore is set up in it |
| `summary` | Summary | all | nothing; every step and every warning with its fix |

Steps that do not apply to a kind are skipped. A re-check reuses the results of a check of the same answers from the last 2 minutes and runs only the checks that are missing, failed or older. Every check and its fix is listed in [Checking every setup](../operations/doctor-all.md#every-check).

## Step 2 — Open the guided setup

On a terminal, bare `lore setup` (or `lore onboard`, or `lore setup web`) starts a small local server and opens its page in your browser. In the prebuilt binaries that is the guided page; in a build from source it is a plain page that lists the commands that do the same work ([A build without the setup page](#a-build-without-the-setup-page)).

| Flag | Effect |
|---|---|
| `--no-open` | print the address instead of opening a browser (copy it into a browser on this machine) |
| `--port <n>` | use this local port instead of a free one |
| `--web` | open the page even when stdout is not a terminal |
| `--schema-only` | only update the local database schema, never open the page (`lore upgrade` uses it) |

When stdout is not a terminal (a script or CI) and `--web` is not given, `lore setup` only updates the local database schema, as it always did, and prints nothing:

<!-- example: guided-setup#plan only=2 -->
```console
# tomas, in ~
$ lore setup < /dev/null; echo "exit $?"
exit 0
```
<!-- /example -->

The page stops when you press Close on it, when you press Ctrl-C in the terminal, or after `setup.idle_minutes` (default 30) without a request.

## Step 3 — Let lore find how you reach GitHub

On the GitHub step you pick only the account. lore then finds how git reaches GitHub as that account:

1. an ssh route that already logs in as the account: a `Host` block of `~/.ssh/config` whose `HostName` is github.com, or plain `github.com` with ssh's own key; a named alias wins over plain `github.com`. lore records the alias and the key's fingerprint from `ssh-keygen -lf` (on the `.pub` when there is one, else on the key file itself; a `.pub` is never required), so `lore doctor --all` sees when the key changes;
2. otherwise https with the account's gh login: every git call gets the token from `gh auth token --user <account>` through a credential helper given on that call only; the token stays in memory and is never written to git config, a url or a file;
3. otherwise the step says what to do: log in with `gh auth login`, or add a `Host` block for the account's key.

Store reads and writes of the setup use the same route (`-c url.<alias>.insteadOf` for an alias, `GIT_SSH_COMMAND -i <key>` for a forced key, the https rewrite and helper for https). lore never edits `~/.ssh/config` and never reads or copies a key.

`lore setup aliases` shows what lore sees: each `Host` alias of github.com, its key file, and the GitHub user `ssh -T` greets it as, plus gh's logins (`--keys` also tests every key file alone, `--fresh` asks again). Tomas's `~/.ssh/config` has plain `github.com` and a `gh-agency` alias; his key files are not on this example machine, so ssh uses its own default key for both:

<!-- example: guided-setup#aliases -->
```console
# tomas, in ~
$ lore setup aliases
aliases:
  ALIAS       IDENTITY_FILE      KEY_MISSING  KEY_NEARBY  PUB_KEY  FINGERPRINT  NO_PUB  IDENTITIES_ONLY  LOGS_IN_AS  STATUS  DETAIL
  github.com  ~/.ssh/id_ed25519  true                                           false   false            tomas-dev   ok      
  gh-agency   ~/.ssh/id_agency   true                                           false   true             tomas-dev   ok      
gh:
  USER  ACTIVE
route:
  line: 
```
<!-- /example -->

`lore setup discover` prints everything the page offers as suggestions: the GitHub accounts, emails, aliases, keys, folders and store urls found on this machine, each with where it came from (`--json` for the whole shape, `--fresh` to probe again). It only reads: gh's logins (`hosts.yml`), the GitHub API with that login's own gh token (`/user`, `/user/emails`, `/user/repos`), `git config --global` and its `includeIf` files, the `Host` blocks of `~/.ssh/config` (`ssh -T` through each alias, and with each key file alone with no agent; never a prompt, 5 seconds each), and the setups and orgs already in `~/.lore/config.yaml`. A gh token is held in memory for those calls only: it is never saved, logged or shown. When a token lacks the `user:email` scope, lore offers the public and noreply addresses and the `gh auth refresh -h github.com -s user:email` line to copy.

## Step 4 — Know which setup a folder uses

`lore setup` and `lore setup list` end with one line about the folder you are in: its org, the stores it attaches, who rows written there are stamped with, and which setup it uses and why. In Tomas's agency repo, the folder glob decides; in a repo of his own under `~/work/lab` (no org, no glob), only the default setup is left, and it is for another org, so lore proposes a new solo setup made from that repo:

<!-- example: guided-setup#this-folder -->
```console
# tomas, in agency/site
$ lore setup list | tail -1
This folder: org agency, no attached store, written as Tomas Weber <tomas@agency.example> — matches setup agency (folder ~/work/agency/**)

# tomas, in lab
$ lore setup list | tail -2
active: agency (default: true)
This folder: no org, no attached store, written as Tomas Weber <tomas@agency.example> — no setup matches (the default setup agency is for org agency, this repo is no org); lore setup offers a solo setup lab from this repo
```
<!-- /example -->

Outside a git repo the line is `This folder: not a git repo`, and in a git repo without `.lore/` it says that `lore init` sets it up. Nothing in the repo is changed by these lines.

For every command, lore picks the active setup with the first rule that answers:

| # | Rule | Set with |
|---|---|---|
| 1 | the `--setup <name>` flag | any command, for that command only (`X-Lore-Setup` on `lore api`) |
| 2 | `LORE_SETUP=<name>` | this shell; `lore setup use <name>` prints the line to paste |
| 3 | this repo's pin | `lore setup use <name> --here`, stored for this machine only |
| 4 | folder match | the git top folder (else the current folder) against every setup's `folders`; the most specific glob wins |
| 5 | org match | the org this repo resolves to, when exactly one setup has that org |
| 6 | the default setup | `default: true` (one setup at most) |
| 7 | none | lore behaves as it did before setups existed |

- **Most specific** means the glob with the most plain folder names before its first wildcard: `~/work/clients/acme-hub/**` beats `~/work/**`. Symlinks are resolved on both sides first.
- **A tie** (two setups with `~/work/**`, or two setups of one org matching by org) is refused with `E_SETUP_AMBIGUOUS`; lore never guesses. Make one glob more specific with `lore setup edit`, or run with `--setup`.
- **An unknown name** in `--setup` or `LORE_SETUP` is refused with `E_SETUP_NOT_FOUND`, for reads and writes alike.
- **Most specific** means the glob with the most plain folder names before its first wildcard: `~/work/clients/acme-hub/**` beats `~/work/**`. Symlinks are resolved on both sides first.
- `lore whoami --explain` prints every rule with its outcome ([Setups from the command line, Step 1](setup-cli.md#step-1--see-the-setups-and-the-active-one) shows it).

A **tie** (two setups with the same glob, or two setups of one org matching by org) is refused with `E_SETUP_AMBIGUOUS`: lore never guesses. A tie stops writes only; reads still answer. `lore setup edit --folder` refuses to make one, but a hand-edited `config.yaml` can still hold one. Here both setups were given `~/work/**` by hand:

<!-- example: guided-setup#tie only=3,4 -->
```console
# tomas, in lab
$ lore task add 'renew the certificate'
ERROR: [E_SETUP_AMBIGUOUS] ~/work/lab matches setups agency and personal equally; make one folder glob more specific (lore setup edit) or run with --setup
  hint: lore setup edit agency --folder <a more specific glob>, or --setup agency
(exit 21)
$ lore task list
(no tasks)
```
<!-- /example -->

Make one glob more specific with `lore setup edit`, or run with `--setup`. An **unknown name** in `--setup` or `LORE_SETUP` (and the name of a disabled setup) is refused with `E_SETUP_NOT_FOUND` for every repo command, reads as well as writes, so a typo never shows another setup's view; `lore whoami` prints its lines and then fails with the same error:

<!-- example: guided-setup#unknown-name -->
```console
# tomas, in agency/site
$ lore --setup nope task add 'check the sitemap'
ERROR: [E_SETUP_NOT_FOUND] no setup named "nope"; the setups on this machine: agency, personal
  hint: lore setup list
(exit 27)
$ lore --setup nope task list
ERROR: [E_SETUP_NOT_FOUND] no setup named "nope"; the setups on this machine: agency, personal
  hint: lore setup list
(exit 27)
```
<!-- /example -->

## Step 5 — Know what the active setup changes

- **Identity:** a `solo` setup's identity is used for what you write; a `member` or `admin` setup uses the identity of its org entry.
- **Org:** a `member` or `admin` setup's org is used when nothing stronger names one (`LORE_ORG`, `lore org use`, a committed `.lore/org.yaml`, a store link or a repo pattern). If one of those names another org, writes are refused with `E_SETUP_ORG_MISMATCH`; reads still work. The default setup never refuses: it is a fallback, not a claim about the folder.
- **Personal store:** a store url replaces `personal.store` for this setup (a repo picked on the setup page is always recorded on that setup and never written to `personal.store`; "Same as my other setups" needs an existing `personal.store`); `none` makes `--to me` fail:

<!-- example: guided-setup#no-personal-store -->
```console
# tomas, in agency/site
$ lore --setup personal memory add --to me --body 'Renew the domain in March.'
ERROR: [E_SETUP_NO_PERSONAL_STORE] setup personal has no personal store (personal_store: none); write to the repo or the org store, or lore setup edit personal --personal-store <url>
  hint: --to repo, --to <org store>, or lore setup edit personal --personal-store <url>
(exit 20)
```
<!-- /example -->

- **GitHub:** lore's own git calls to stores use the setup's route ([Step 3](#step-3--let-lore-find-how-you-reach-github)), so each setup reaches GitHub with its own key or login.

## Step 6 — Join a team from an invite

If an admin added you ([Adding your staff](members.md)), run `lore setup join <org>` (or the org's store url): lore finds you on the roster by your email and needs no line. Otherwise an admin sends you one line ([Team invites](invites.md)). `lore setup join <line>` opens the page on the join flow, with the kind set to "Join a team" and the email prefilled from the invite; `--no-open` prints the address instead, and `--json` prints the decoded invite and the step plan without opening anything. Mira joins with the line Tomas sent (kept in `$INVITE` here):

<!-- example: guided-setup#invite-and-join -->
```console
# tomas, in agency/site
$ lore team invite mira@agency.example
lore: pushed store agency/org: commit 601d285 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/invites/inv_7a67…9504.json (store agency/org)
✓ invite inv_7a67…9504 written to store agency (expires 2026-11-16)
Send this to mira@agency.example (it holds no secret):
  lore setup join lore-invite:v1:eyJ2IjoxLCJpZCI6Imludl8w…

# mira, in ~
$ lore setup join "$INVITE" --json | python3 -m json.tool | sed -n '/"invite": {/,/}/p'
    "invite": {
        "v": 1,
        "id": "inv_7a67…9504",
        "org": "agency",
        "store": "git@github.com:agency/notes",
        "org_folder": "org",
        "email": "mira@agency.example",
        "by": "tomas@agency.example",
        "expires": "2026-11-16"
    },
```
<!-- /example -->

The invite holds no secret and grants nothing: it tells lore where the team's store is and which invite row to look for. Whether you can read and write that store is GitHub's decision; the roles, projects and kind of stint come from the invite row inside the store, so editing the line cannot give anyone more. The join step checks that the invite parses, that you can read the store, that the invite is there and still open, that your email is the invite's, and that you can write the store; then it imports the org entry, records your emails and GitHub user, opens your stint, adds your project memberships and marks the invite accepted. A text that is not an invite is refused at once:

<!-- example: setup-cli#join -->
```console
# tomas, in ~
$ lore setup join lore-invite:v1:garbage
ERROR: [E_INVITE_INVALID] this is not a lore invite; copy the whole line starting with lore-invite:v1: (the text after the prefix is cut or changed)
  hint: ask the person who invited you to run lore team invite resend <your email>
(exit 20)
```
<!-- /example -->

## Step 7 — Run a fix from the command line

Some checks come with a command that fixes them (`lore render`, `lore skill install --tool claude_code`, `gh repo edit agency/notes --visibility private`, …). `lore setup fix <setup> --check <id> --yes` runs it only when the fix is marked runnable: lore rebuilds the command from its own list of fixes, fills in the values, and checks every value again before it runs (a value that could act as a flag or a shell word is refused with `E_FIX_REFUSED`). Only `lore`, `gh` and `git` are ever run, and a fix that changes something outside your machine, such as a GitHub repo or its collaborators, always asks, even under `lore setup repair --yes`. A fix that offers a choice, such as a skill folder that is a symbolic link (replace the link, or write into its target), runs only the command named with `--choice`. A fix lore cannot run is refused with its text, so you can do it by hand:

<!-- example: guided-setup#fix -->
```console
# tomas, in agency/site
$ lore setup fix agency --check github.gh_account
ERROR: [E_FIX_REFUSED] fix for github.gh_account refused: fix is not runnable; do it by hand: gh is optional; it only speeds up checks and lets lore offer 'create repo'. To add: gh auth login --hostname github.com
  hint: run lore setup check to see the fix text and do it by hand
(exit 20)
```
<!-- /example -->

`lore setup repair` runs every check of a setup and lists the steps to redo ([Checking every setup](../operations/doctor-all.md#step-5--list-the-steps-to-redo-with-lore-setup-repair)).

## Manage setups from the command line

Everything the page does is also a command, so scripts and agents can do it too. [Setups from the command line](setup-cli.md) runs each of these with real output.

| Command | What it does |
|---|---|
| `lore setup list` | every setup with kind, org, default, folders and last health, and which one is active here and why |
| `lore setup show <name>` | one setup's entry: keys and paths, never a secret |
| `lore setup edit <name> [flags]` | change `--folder` (replaces the list; repeat it), `--github-user`, `--route ssh\|https`, `--host-alias`, `--ssh-key` (a key file: the private key or its `.pub`) or `--no-ssh-key` (use the alias's own key), `--gh-account`, `--personal-store inherit\|none\|<url>`, `--tool` (replaces the list), `--secrets`, `--default` / `--no-default`, `--note "<why this setup exists>"` / `--no-note` |
| `lore setup rename <name> <new>` | give the setup a new name; its repo pins and saved steps follow it. Refused with `E_SETUP_NAME_TAKEN` when another setup has that name |
| `lore setup use <name>` | print `export LORE_SETUP=<name>` for this shell (it writes nothing) |
| `lore setup use <name> --here` | pin this repo to the setup on this machine; `lore setup use --here --clear` removes the pin. The repo needs a lore project id (`lore init`) |
| `lore setup disable <name>` / `enable <name>` | keep the setup but take it out of every match, or put it back ([below](#disable-remove-and-restore-a-setup)) |
| `lore setup remove <name>` | move the setup to the setup trash with its repo pins; restorable. Its stores, repos and org entry stay as they are |
| `lore setup trash list` | the setups in the trash, newest first |
| `lore setup trash restore <name> [--as <new>]` | bring the newest trashed setup of that name back, with its repo pins |
| `lore setup trash purge <name> [--dry-run] [--clear-identity] [--clear-personal-store] --confirm` | delete a trashed setup for good; `--dry-run` lists what is deleted and what stays. Without `--confirm` it asks (`E_NEEDS_CONFIRM`, exit 26), and `--yes` never purges |
| `lore setup plan [--kind k]` | the steps for a kind, and the saved progress of setups being made |
| `lore setup check <setup> --step <id> --answers '<json>'` | validate one step's answers and run its checks; saves only the progress |
| `lore setup apply <setup> --step <id> [--answers '<json>']` | save one step (its one write) once every error-level check passes; without `--answers` it uses the saved ones |
| `lore setup fix <setup> --check <id> [--target t] [--choice id] --yes` | run a failing check's fix command, after a confirmation; `--choice` picks one command of a fix that offers a choice (a linked skill folder: `replace` or `update`) |
| `lore setup repair [name] [--all] [--dry-run] [--yes]` | run a setup's checks fresh and list the steps to redo ([details](../operations/doctor-all.md#step-5--list-the-steps-to-redo-with-lore-setup-repair)) |
| `lore setup discover [--fresh]` | the GitHub accounts, emails, ssh aliases, keys, folders and store urls found on this machine ([Step 3](#step-3--let-lore-find-how-you-reach-github)) |
| `lore setup aliases [--keys] [--fresh]` | each ssh alias of github.com and the user it logs in as, plus gh's logins; `--keys` tests every key alone |
| `lore setup folders [--json]` | folder suggestions for a setup: where `lore setup` was started, its git repo and parent, and the gitconfig `includeIf` folders |
| `lore setup browse [path] [--json]` | the subfolders of a folder under your home folder (names only), for picking a setup's folders |
| `lore setup import-scan [--folder f]... [--depth n]` | read an existing lore install and propose setups; writes nothing ([below](#import-from-an-existing-lore-install)) |
| `lore setup import --setups '<json>'` | write setup entries as `import-scan` proposes them; a taken name is skipped |
| `lore setup import-dismiss <banner_key>` | hide the setup page's import banner for the values it names (kept in `~/.lore/state/import-dismissed.json`) |

Steps must be saved in order: a step whose earlier steps are not done is refused with `E_SETUP_STEP_ORDER`, and a step with a failing error-level check with `E_SETUP_STEP_FAILED`. Saving a step again with the same answers writes nothing. A `member` or `admin` setup is written to `setups:` only by its `join` or `team` step, once its org entry exists; until then its answers are kept as progress.

### Disable, remove and restore a setup

Each state is a line in `~/.lore/config.yaml`, so it shows in `git diff` of a dotfiles repo and can be edited by hand:

| State | In `config.yaml` | Matches folders, `--setup`, `LORE_SETUP`, repo pins, org, default | `lore doctor --all` |
|---|---|---|---|
| enabled | `setups[]` entry | yes | checked |
| disabled | `setups[]` entry with `disabled: true` and `disabled_at` | no; `--setup <name>` and `LORE_SETUP` naming it are refused (reads and writes) with `E_SETUP_NOT_FOUND` and the hint `lore setup enable <name>` | skipped with one line: `setup old-client is disabled: skipped (lore setup enable old-client)` |
| in the trash | `trash.setups[]` entry with `removed_at` and `repo_pins` | no | not listed |

Disable is like commenting a line out: nothing is lost and `lore setup enable <name>` puts it back. Remove works like deleting a row: the entry moves to the setup trash with the time it was removed and the repos that were pinned to it; its unfinished-step progress stays, so a restored setup resumes where it was. `lore setup trash restore <name>` brings the newest trashed entry of that name back with its pins (a pin that points at another setup since is left alone); when a setup of that name exists again, restore it under another name with `--as <new>`. A restored default stays the default only when no other setup is the default now. `lore setup trash purge` deletes for good; `--clear-identity` and `--clear-personal-store` also remove the machine's top-level `identity:` and `personal.store` from `config.yaml`, while the store repository itself is never deleted, and neither are orgs or any other repo. [Setups from the command line, Step 6](setup-cli.md#step-6--remove-restore-and-purge) shows the whole cycle.

Each setup can carry a **note**, your own line on why it exists (at most 500 characters): `lore setup edit <name> --note "Client Acme until March"`. `lore setup show` and `lore setup trash list` print it.

### Import from an existing lore install

If this machine already has lore set up the old way (an `identity:` and `orgs:` in `~/.lore/config.yaml`, a v0.1.17 `identity.yaml` or `identity.toml`, or repos lore has run in), `lore setup import-scan` proposes setups from it: one solo setup from your identity and personal store, and one member setup per org entry. Each proposed value names the file and line it came from, and a setup that already covers a proposal (same org, or a solo setup with the same personal store) is left out, including a disabled setup and a setup in the trash: the way back for those is `lore setup enable` or `lore setup trash restore`, never a second copy. `lore setup import --setups '<json list>'` writes the picked ones; a name that is taken is skipped and the others still import. [Setups from the command line, Step 8](setup-cli.md#step-8--import-from-an-existing-install) shows both.

What the scan reads, and it writes nothing (a test hashes every file before and after a scan):

| Source | What it gives |
|---|---|
| `~/.lore/config.yaml` | the identity, `personal.store`, `orgs:`, `repo_orgs:` and existing `setups:` |
| `~/.lore/identity.yaml`, `identity.toml` (and their `.migrated` copies) | an identity from v0.1.17 or older |
| `~/.lore/state/machine.db` (read from a private copy) | the repos lore has run in on this machine |
| folders you add with **Scan folders** | folders holding `.lore/`, up to 4 levels deep (`--depth`, at most 8); `node_modules`, `vendor`, `.git` and hidden folders are skipped; at most 200 repos |
| each repo's `.lore/stores.yaml` | the stores the repo attaches |

## Settings

| Key in `~/.lore/config.yaml` | Default | Meaning |
|---|---|---|
| `setups` | none | the list of setups (above) |
| `repo_setups` | none | repo id → setup name, written by `lore setup use --here`; this machine only |
| `setup.idle_minutes` | 30 | the page stops after this many minutes without a request (1 to 1440) |
| `setup.open_browser` | true | `false`: print the page address only |
| `doctor.scan_depth` | 3 | how deep `lore doctor --all` looks for repos under a setup's folders while lore has not yet recorded any repo (at most 10) |
| `doctor.probe_timeout_s` | 20 | time limit for one ssh or git check, in seconds (at most 300) |

Environment: `LORE_SETUP=<name>`. Flag: `--setup <name>` on every command.

## How the local page is kept safe

The page listens on `127.0.0.1` only, on a random port, and the address carries a one-time token in its `#` part (which a browser never sends to a server); the first visit trades it for a session cookie, so a second use of the link is refused, and every change also needs a per-session CSRF value, the page's own `Origin` and `Host`. The page can call only a short list of setup, invite, skill, doctor and whoami operations (everything else answers 404), no operation it can call returns a token or private key, and it never runs a shell string: a fix is a fixed command rebuilt and checked on the server. The session ends when the page server stops.

## A build without the setup page

The guided page is one of the proprietary components of the prebuilt lore binaries (GitHub Releases, Homebrew, the install scripts); the Apache-2.0 source in this repository builds without it. In such a build `lore setup` still opens a local page with the same safety rules, but it is a plain page that says the guided page is not in this build and lists the commands that do the same work: `lore setup plan`, `lore setup check`, `lore setup apply` and `lore doctor --all`. Every step on this page can be done with those commands ([Manage setups from the command line](#manage-setups-from-the-command-line)).

## Try it

Run a write under a setup name that does not exist; it is refused before anything is written, and `lore setup list` names the setups there are:

<!-- example: guided-setup#unknown-name only=1 -->
```console
# tomas, in agency/site
$ lore --setup nope task add 'check the sitemap'
ERROR: [E_SETUP_NOT_FOUND] no setup named "nope"; the setups on this machine: agency, personal
  hint: lore setup list
(exit 27)
```
<!-- /example -->

## Pitfalls

- **Setups are per machine.** Nothing about them reaches your team or your other machines; make them again on each machine (or keep `~/.lore/config.yaml` in a dotfiles repo).
- **A tie stops writes, not reads; a bad name stops both.** With two setups tied on a folder, `lore task list` still answers and only the write fails, so a read does not prove the folder picks one setup; `lore whoami --explain` does. A `--setup` or `LORE_SETUP` name that is unknown or disabled is refused for reads too (`lore --setup nope task list` fails with `E_SETUP_NOT_FOUND`).
- **Two setups with the same glob is an error, not a preference.** Give the narrower setup a narrower glob.
- **The default setup answers last, wherever you are.** In a repo no glob covers, the default is active (`active: agency (default: …)`) even when it belongs to another org, and the This folder line says "no setup matches". Give such repos their own setup (the line offers one) or a glob, so their notes are not written as the default's identity.
- **`lore setup use --here` needs a lore project id.** Run `lore init` in the repo first.
- **`personal_store: none` is strict.** Every `--to me` write fails in that setup; that is the point of choosing it.
- **A script's `lore setup` opens nothing.** Without a terminal (and without `--web`) it only updates the database schema and exits 0.

## Related

- [Setups from the command line](setup-cli.md): every `lore setup` subcommand with real output.
- [Team invites](invites.md): sending the line a teammate joins with.
- [Checking every setup](../operations/doctor-all.md): `lore doctor --all`, every check, and `lore setup repair`.
- [Skill install](../knowledge/skill-install.md): keeping the lore skill current in each coding tool.
- [Setup and invite errors](setup-errors.md): every `E_SETUP_*`, `E_INVITE_*` and fix error with its cause and fix.
- [Identity and stores](identity-and-stores.md) and [Team profiles](profiles.md).
