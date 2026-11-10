# Setups from the command line

**In short:** everything the [guided setup page](guided-setup.md) does is also a `lore setup` subcommand. This page walks through them on one machine with two setups: list them, see which one is active and why, pick another one for a command, a shell or a repo, edit one, disable it, move it to the trash and back, save setup steps from a script, and import setups from an older lore install.

You want this page when you script a machine's setup (a dotfiles repo, a new-laptop script, a coding agent that sets lore up), when you work over ssh with no browser, or when you need to know exactly why lore picked a setup in some folder. Every output below is a real run (`tests/team/docs/run-examples.sh setup_cli`) in a throwaway home folder with a fake GitHub.

## Before you start

- lore 0.1.18 or later (`lore --version`).
- What a setup is, and its three kinds (`solo`, `member`, `admin`): see [What a setup is](guided-setup.md#what-it-is-and-when-you-want-it).
- The example machine belongs to Tomas Weber. He runs the `agency` org (an `orgs:` entry in `~/.lore/config.yaml`, store `git@github.com:agency/notes`), keeps agency work under `~/work/agency/` and his own projects under `~/work/personal/`. His `~/.lore/config.yaml` ends with two setups, written by hand here (the setup page writes the same entries):

```yaml
setups:
  - name: agency
    kind: admin
    org: agency
    default: true
    folders:
      - ~/work/agency/**
  - name: personal
    kind: solo
    folders:
      - ~/work/personal/**
    identity:
      key: human:tomas@home.example
      name: Tomas Weber
    personal_store: none
```

A setup name is lowercase letters, digits, `_` or `-`, at most 64 characters, and may not be `repo`, `me`, `team` or `company`.

## Step 1 — See the setups and the active one

`lore setup list` prints every setup with its kind, org, default flag, folders and last health, then the setup active in the current folder and the rule that picked it, then one line about the folder itself (its org, attached stores, and who rows written there are stamped with). The same command in two folders gives two answers; `lore whoami --explain` shows every rule of the choice with its outcome:

<!-- example: setup-cli#list -->
```console
# tomas, in agency/site
$ lore setup list
setups:
  NAME      KIND   ORG     DEFAULT  FOLDERS             HEALTH
  agency    admin  agency  true     ~/work/agency/**    not checked yet (lore doctor --all)
  personal  solo           false    ~/work/personal/**  not checked yet (lore doctor --all)
active: agency (folder: ~/work/agency/**)
This folder: org agency, no attached store, written as Tomas Weber <tomas@agency.example> — matches setup agency (folder ~/work/agency/**)

# tomas, in personal/dotfiles
$ lore setup list
setups:
  NAME      KIND   ORG     DEFAULT  FOLDERS             HEALTH
  agency    admin  agency  true     ~/work/agency/**    not checked yet (lore doctor --all)
  personal  solo           false    ~/work/personal/**  not checked yet (lore doctor --all)
active: personal (folder: ~/work/personal/**)
This folder: no org, no attached store, written as Tomas Weber <tomas@home.example> — matches setup personal (folder ~/work/personal/**)
$ lore whoami --explain | sed -n '/setup resolution/,$p'
setup resolution (first step that answers wins):
  ✗ 1. flag     --setup not given
  ✗ 2. env      LORE_SETUP is not set
  ✗ 3. repo     no repo_setups pin for rep_7a67…9504
  ✓ 4. folder   ~/work/personal/**
  · 5. org      
  · 6. default  
  · 7. none     
```
<!-- /example -->

The rules, first match wins: the `--setup` flag, `LORE_SETUP`, this repo's pin, the most specific folder glob, the org of the repo (when exactly one setup has it), the default setup, none. [Many setups, chosen by folder](guided-setup.md#step-4--know-which-setup-a-folder-uses) has the full table.

## Step 2 — Look at one setup

`lore setup show <name>` prints one setup's entry: keys and paths, never a secret (at most a key file path and its fingerprint).

<!-- example: setup-cli#show -->
```console
# tomas, in ~
$ lore setup show personal
setup:
  name: personal
  kind: solo
  folders: ~/work/personal/**
  identity:
    key: human:tomas@home.example
    name: Tomas Weber
  personal_store: none
```
<!-- /example -->

`--json` on `list` and `show` prints the same as JSON for scripts.

## Step 3 — Choose a setup for a command, a shell or a repo

Three ways to override the folder rule, from narrowest to widest:

- `--setup <name>` on any command, for that command only.
- `LORE_SETUP=<name>` for a shell; `lore setup use <name>` prints the line to paste and writes nothing.
- `lore setup use <name> --here` pins this repo to the setup, on this machine only (stored under `repo_setups:` in `~/.lore/config.yaml`; the repo needs a lore project id from `lore init`). `lore setup use --here --clear` removes the pin.

A name that is not a setup is refused, on a read as on a write:

<!-- example: setup-cli#choose -->
```console
# tomas, in agency/site
$ lore --setup personal whoami | head -2
org        agency  (matched by: own pattern github.com/agency/* of agency)
setup      personal  (flag: --setup personal)
$ lore setup use personal
export LORE_SETUP=personal
$ lore setup use personal --here
pinned this repo to setup personal (this machine only; lore setup use --here --clear removes the pin)
$ lore setup list | tail -2
active: personal (repo: rep_2ad3…f891)
This folder: org agency, no attached store, written as Tomas Weber <tomas@agency.example> — matches setup personal (repo rep_2ad3…f891)
$ lore setup use --here --clear
cleared this repo's setup pin; lore setup list shows the setup lore picks here now
$ lore --setup nope task add 'check the sitemap'
ERROR: [E_SETUP_NOT_FOUND] no setup named "nope"; the setups on this machine: agency, personal
  hint: lore setup list
(exit 27)
```
<!-- /example -->

## Step 4 — Edit a setup

`lore setup edit <name>` changes one setup in place. `--folder` replaces the whole folder list (repeat it for several globs), `--note` records why the setup exists (shown by `show`, `trash list` and the page's Home):

<!-- example: setup-cli#edit -->
```console
# tomas, in ~
$ lore setup edit personal --folder '~/work/personal/**' --folder '~/src/**' --note 'side projects and dotfiles'
name: personal
changed: true
$ lore setup show personal
setup:
  name: personal
  kind: solo
  folders: ~/work/personal/**, ~/src/**
  identity:
    key: human:tomas@home.example
    name: Tomas Weber
  personal_store: none
  note: side projects and dotfiles
```
<!-- /example -->

The other flags: `--github-user`, `--route ssh|https`, `--host-alias`, `--ssh-key` (the private key file or its `.pub`) or `--no-ssh-key`, `--gh-account`, `--personal-store inherit|none|<url>`, `--tool` (replaces the list), `--secrets`, `--default` / `--no-default`, `--no-note`. Two setups whose most specific globs tie (both `~/work/**`) are refused when lore has to choose between them (`E_SETUP_AMBIGUOUS`); make one glob more specific.

`lore setup rename <name> <new>` gives a setup a new name: its entry keeps everything else, the repos pinned to it stay pinned, and an unfinished setup's saved steps follow it. A name another setup holds is refused with `E_SETUP_NAME_TAKEN`. On the setup page, **Edit** on a setup's row opens its steps as "Edit setup: <name>": every Save changes that setup, a new name on the first step renames it, and a new kind opens the steps that kind asks for.

## Step 5 — Disable and enable

Disabling keeps the setup in `config.yaml` with `disabled: true` but takes it out of every match: folders, `--setup`, `LORE_SETUP`, repo pins, org and default. `lore doctor --all` skips it. A command that names it (a read or a write) is refused with the hint to enable it:

<!-- example: setup-cli#disable-and-enable -->
```console
# tomas, in ~
$ lore setup disable personal
disabled setup personal: it stays in ~/.lore/config.yaml but matches no folder, --setup or default; lore setup enable personal turns it back on

# tomas, in personal/dotfiles
$ lore --setup personal task add 'rotate the backup key'
ERROR: [E_SETUP_NOT_FOUND] setup "personal" is disabled; it matches nothing until it is enabled
  hint: lore setup enable personal
(exit 27)

# tomas, in ~
$ lore setup enable personal
enabled setup personal
```
<!-- /example -->

## Step 6 — Remove, restore and purge

`lore setup remove` moves the setup to the setup trash (`trash.setups[]` in `config.yaml`) with the time it was removed and the repos pinned to it; its stores, repos and org entry are never touched. `lore setup trash restore <name>` brings the newest trashed entry of that name back with its pins (`--as <new>` when a setup of that name exists again). `lore setup trash purge` deletes a trashed setup for good: `--dry-run` lists what is deleted and what stays, and it only deletes with `--confirm` (`--yes` never purges):

<!-- example: setup-cli#remove-and-restore -->
```console
# tomas, in personal/dotfiles
$ lore setup use personal --here
pinned this repo to setup personal (this machine only; lore setup use --here --clear removes the pin)

# tomas, in ~
$ lore setup remove personal
moved setup personal to the trash; lore setup trash restore personal brings it back
$ lore setup trash list
setups in the trash (lore setup trash restore <name> brings one back):
  personal  solo  removed 2026-11-02T10:01:25Z  — side projects and dotfiles
$ lore setup trash restore personal
restored setup personal from the trash
$ lore setup remove personal
moved setup personal to the trash; lore setup trash restore personal brings it back
$ lore setup trash purge personal --dry-run
would delete for good:
  - the trashed setup personal (removed 2026-11-02T10:01:40Z) and its unfinished-step progress
stays:
  - the personal store local in ~/.lore/config.yaml
  - every GitHub repo: lore never deletes a repository
$ lore setup trash purge personal
ERROR: [E_NEEDS_CONFIRM] deletes: the trashed setup personal (removed 2026-11-02T10:01:40Z) and its unfinished-step progress
stays: the personal store local in ~/.lore/config.yaml; every GitHub repo: lore never deletes a repository
Delete setup personal for good?
  hint: rerun with --confirm to delete it for good (--yes never purges); --dry-run shows what stays
(exit 26)
$ lore setup trash purge personal --confirm
deleted for good:
  - the trashed setup personal (removed 2026-11-02T10:01:40Z) and its unfinished-step progress
stays:
  - the personal store local in ~/.lore/config.yaml
  - every GitHub repo: lore never deletes a repository
$ lore setup trash list
the setup trash is empty
```
<!-- /example -->

Two more purge flags, `--clear-identity` and `--clear-personal-store`, also remove the machine's top-level `identity:` and `personal.store` from `config.yaml`; the store repository itself is never deleted.

## Step 7 — Save setup steps from a script

The page is a list of steps, and each step is also a command: `lore setup plan --kind <k>` lists the steps of a kind (and the saved progress of setups being made), `lore setup check <setup> --step <id> --answers '<json>'` validates one step's answers and runs its checks (it saves only the progress), and `lore setup apply` saves the step once every error-level check passes. Steps are saved in order: a step whose earlier steps are not done is `E_SETUP_STEP_ORDER`.

<!-- example: setup-cli#steps -->
```console
# tomas, in ~
$ lore setup plan --kind solo | cut -c1-52
steps:
  ID              TITLE                             
  kind            What kind of setup is this?       
  identity        Who are you?                      
  github          Which GitHub account?             
  personal_store  Where do your own notes go?       
  tools           Which coding tools do you use?    
  secrets         Where do you keep secrets?        
  folders         Which folders is this setup for?  
  this_folder     This folder                       
  summary         Summary                           
progress:
  SETUP  STEP  ANSWERS  CHECKS  STATUS  UPDATED_AT
$ lore setup apply side --step kind --answers '{"kind":"solo","name":"side"}' | head -4
step: kind
status: done
changed: true
wrote: ~/.lore/config.yaml
$ lore setup check side --step identity --answers '{"name":"Tomas Weber","email":"tomas@home.example"}' | head -2
step: identity
status: ready
$ lore setup apply side --step github --answers '{"user":"tomas-dev"}'
ERROR: [E_SETUP_STEP_ORDER] step github needs identity first
  hint: run lore setup apply side --step identity
(exit 23)
```
<!-- /example -->

Here the `kind` step creates the setup `side`, `identity` is checked but not saved, so `github` (which needs `identity`) is refused. Saving a step again with the same answers writes nothing. A `member` or `admin` setup is written to `setups:` only by its `join` or `team` step, once its org entry exists. When a check fails and has a fix lore can run, `lore setup fix <setup> --check <id> --yes` runs it after a confirmation; see [Fixes that lore can run](guided-setup.md#step-7--run-a-fix-from-the-command-line).

## Step 8 — Import from an existing install

`lore setup import-scan` reads what an older lore install left behind (`~/.lore/config.yaml`, v0.1.17 identity files, the repos lore has run in, every repo's `.lore/stores.yaml`, plus the folders you pass with `--folder`) and proposes setups; it writes nothing. `lore setup import --setups '<json list>'` writes the entries it proposes (or your own); a name that is taken is skipped and listed under `skipped`.

<!-- example: setup-cli#import -->
```console
# tomas, in ~
$ lore setup import-scan --folder ~/work
proposals:
  SETUP  FIELDS  REPOS
sources:
  KIND             PATH                                                                                                STATE    DETAIL
  config           ~/.lore/config.yaml                         found    1 org, 2 setups, 1 deleted setup
  identity_legacy  ~/.lore/identity.yaml                       missing  
  identity_backup  ~/.lore/identity.yaml.migrated              missing  
  identity_legacy  ~/.lore/identity.toml                       missing  
  identity_backup  ~/.lore/identity.toml.migrated              missing  
  machine_db       ~/.lore/state/machine.db                    found    2 known repos
  scan_folder      ~/work                                      found    3 lore repos (2 already known)
  repo_stores      ~/work/agency/site/.lore/stores.yaml        missing  
  repo_stores      ~/work/personal/dotfiles/.lore/stores.yaml  missing  
  repo_stores      ~/work/stores/.lore/stores.yaml             found    1 store
repos:
  ROOT                                                                              FROM        ORG  STORES
  ~/work/agency/site        machine.db       
  ~/work/personal/dotfiles  machine.db       
  ~/work/stores             scan             git@github.com:agency/notes
covered: agency already uses org agency, side already uses the same personal store
found:
  config: ~/.lore/config.yaml
banner: 
banner_key: 
$ lore setup import --setups '[{"name":"personal","kind":"solo"}]'
imported: personal
skipped:
  NAME  REASON
```
<!-- /example -->

The example leaves out the per-repo lines (each repo's `stores.yaml` source and the `repos:` rows), because they come in no fixed order. On this machine both setups already cover what was found, so there is nothing to propose (`covered:` says why); the import then writes a bare `personal` solo setup, which the page or `lore setup edit` completes.

## Step 9 — Join a team from an invite line

`lore setup join <invite>` parses the line an admin sent (see [Team invites](invites.md)) and opens the page on the join step; `--no-open` prints the address, `--json` prints the decoded invite and the step plan. A text that is not an invite is refused at once:

<!-- example: setup-cli#join -->
```console
# tomas, in ~
$ lore setup join lore-invite:v1:garbage
ERROR: [E_INVITE_INVALID] this is not a lore invite; copy the whole line starting with lore-invite:v1: (the text after the prefix is cut or changed)
  hint: ask the person who invited you to run lore team invite resend <your email>
(exit 20)
```
<!-- /example -->

## Try it

The fastest check of a machine's setups is `lore setup list` in each folder you work in. On Tomas's machine, inside a personal repo:

<!-- example: setup-cli#list only=2 -->
```console
# tomas, in personal/dotfiles
$ lore setup list
setups:
  NAME      KIND   ORG     DEFAULT  FOLDERS             HEALTH
  agency    admin  agency  true     ~/work/agency/**    not checked yet (lore doctor --all)
  personal  solo           false    ~/work/personal/**  not checked yet (lore doctor --all)
active: personal (folder: ~/work/personal/**)
This folder: no org, no attached store, written as Tomas Weber <tomas@home.example> — matches setup personal (folder ~/work/personal/**)
```
<!-- /example -->

Each folder should name the setup you expect on the `active:` line, with the rule you expect. If not, pin the repo (`lore setup use <name> --here`) or make a folder glob more specific (`lore setup edit <name> --folder …`), and run `lore whoami --explain` to see every rule.

## Commands not run on this page

- `lore setup aliases` and `lore setup discover` probe ssh and GitHub (`ssh -T git@github.com` per alias and key), so they cannot run against the docs' fake GitHub. They list each ssh alias of github.com and the GitHub user it logs in as, gh's logins, and (discover) the emails, keys, folders and store urls found on this machine; see [Accounts found on this machine](guided-setup.md#step-3--let-lore-find-how-you-reach-github).
- `lore setup folders` and `lore setup browse` feed the page's folder picker; `lore setup repair` is on [Checking every setup](../operations/doctor-all.md).
- `lore setup web` and bare `lore setup` open the page itself.

## Pitfalls

- **A bad `--setup` name stops every repo command.** `lore --setup nope task list` is refused with `E_SETUP_NOT_FOUND` just like `lore --setup nope task add …`, so a script with a typo fails instead of reading another setup's view. Check the name with `lore setup list` before scripting it. A folder or org tie (`E_SETUP_AMBIGUOUS`) still stops writes only.
- **`--folder` replaces the list.** Pass every glob the setup should keep, not only the new one.
- **A repo pin is per machine.** `lore setup use <name> --here` is stored in this machine's `config.yaml`; a teammate's clone of the same repo is not pinned.
- **`lore setup use <name>` without `--here` writes nothing.** It prints the `export LORE_SETUP=…` line for you to paste.
- **Purge needs `--confirm`.** `--yes` never purges, so a script that passes `--yes` everywhere cannot lose a setup by accident.
- **Steps are saved in order.** A script that applies `github` before `identity` gets `E_SETUP_STEP_ORDER`; follow the order of `lore setup plan`.

## Related

- [Guided setup](guided-setup.md): the page these commands mirror, the setup kinds, and how lore chooses a setup.
- [Team invites](invites.md): the line `lore setup join` reads.
- [Setup and invite errors](setup-errors.md): every `E_SETUP_*` and `E_INVITE_*` code with its fix.
- [Checking every setup](../operations/doctor-all.md): `lore doctor --all` and `lore setup repair`.
- [Configuration reference](../reference/config.md): the `setups:` and `repo_setups:` keys.
