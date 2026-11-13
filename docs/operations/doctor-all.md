# Checking every setup: `lore doctor --all` and `lore setup repair`

**In short:** `lore doctor --all` checks every [setup](../team/guided-setup.md) on this machine at once: your identity, the GitHub account each setup logs in as, each store you read and write, the repos you work in, your coding tools and your folders. Each problem comes with the command or step that fixes it. `lore setup repair` runs the same checks for one setup and lists (or runs) the fixes.

> **New in lore 0.1.18.** The output on this page is a real run (`tests/docs/run-examples.sh doctor-all`) in a throwaway home folder: a fake `ssh` answers as GitHub would for one account, and `gh` has no login, so nothing reaches the network.

## What it is and when you want it

Plain [`lore doctor`](doctor.md) checks the repo you are in: hooks, cache, sync. It cannot tell you that the ssh key your agency setup uses now logs in as your personal GitHub account, or that a folder one of your setups points at was deleted. `lore doctor --all` checks the setups themselves, the ones the [guided setup](../team/guided-setup.md) and [`lore setup` commands](../team/setup-cli.md) write to `~/.lore/config.yaml`.

You want it after you set up a new machine, after you change `~/.ssh/config` or log in to `gh` as someone else, when a push to a store suddenly fails, and before you ask a teammate why lore "does not work". It writes nothing.

## Before you start

- lore 0.1.18 or later, with at least one setup (`lore setup list` shows them).
- `git` and `ssh` on PATH. `gh` is optional: without it, or without a gh login, the checks that ask GitHub's API (is the store private, does gh have a login for the account) are a note (`ℹ`) or a warning and never fail. Only a setup whose git route is https with the account's gh login needs gh, and there a missing login is a check that could not run.
- The example machine belongs to Tomas Weber. He runs the `agency` org (store `git@github.com:agency/notes`) with an `admin` setup for `~/work/agency/**`, and keeps his own projects in a `solo` setup for `~/work/personal/**` with no personal store:

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

## Step 1 — Check every setup

Run it anywhere; inside a repo, the normal `lore doctor` lines for that repo follow the setup blocks.

<!-- example: doctor-all#run-it -->
```console
# tomas, in agency/site
$ lore doctor --all
setup agency (admin of agency, default) ─ ~/work/agency/**
  ⚠ identity    tomas@agency.example · roster @tomas · employee since 2026-11-02
  ⚠ github      tomas-dev via github.com
  – store me    read – write – private –
  ℹ store agency   read ✓ write ✓ private ℹ
  – secrets     none
  ⚠ identity.actor_copies  Tomas Weber (act_7a67…9504) in repo has "tomas", agency/org says "Tomas Weber"
      fix: lore actor refresh
  ⚠ github.gh_account  gh has no login for tomas-dev
      fix: gh auth login --hostname github.com
  ℹ store.private  agency: gh is not logged in, so lore cannot check whether agency/notes is private
      fix: Optional: install gh and run gh auth login so lore can check that agency/notes is private
setup personal (solo) ─ ~/work/personal/**
  ⚠ identity    tomas@home.example
  – github      no GitHub account in this setup (lore setup apply personal --step github)
  – secrets     none
  ⚠ identity.actor_copies  Tomas Weber (act_7a67…9504) in repo has "tomas", agency/org says "Tomas Weber"
      fix: lore actor refresh
  ⚠ folders.exist  not a folder: ~/work/personal
      fix: ~/work/personal is not a folder on this machine; create it, or change the glob with lore setup edit personal --folder <glob>
This repo, warnings:
  - actor_copies: actor Tomas Weber (act_7a67…9504): the copy in repo has display_name "tomas", its source (agency/org) says "Tomas Weber"; fix: lore actor refresh
  - org_admin_missing: Tomas Weber (act_7a67…9504) created org agency but has no org role (member list shows "-"); the org's creator is its admin; fix: lore doctor --fix (or: lore member edit act_7a67…9504 --role admin)
2 setups · 23 ok · 6 warnings · 0 errors → degraded (exit 1)
Context:
  - context: .lore/LORE.md not rendered yet (lore render)
(exit 1)
```
<!-- /example -->

Each setup gets one block: a line per group (identity, GitHub, each store, secrets, tools), then every check that is not ok with its fix, then, when a check failed, the `lore setup repair` line for that setup. The marks are `✓` ok, `⚠` a warning, `✗` a failed check, `?` a check that could not run (a timeout, a missing program), `–` nothing checked (every check skipped: no personal store, no secrets backend, no GitHub account in the setup) and `ℹ` a note that changes nothing in the status (here `store.private`: there is no `gh` login to ask GitHub with, so lore cannot tell whether the store is private, and the fix says how to let it check). The last line counts every result and gives the overall status and exit code.

| Flag | Effect |
|---|---|
| `--all` | check every setup on this machine |
| `--setup <name>` | with `--all`, check only that setup; without `--all`, plain `lore doctor` shows the active setup's block |
| `--fresh` | probe again instead of reusing results from the last minute |
| `--json` | the full report as JSON ([Step 4](#step-4--read-the-report-as-json)) |

Results of ssh, git and `gh` checks are kept for 60 seconds, so the setup page and a doctor run right after it do not probe twice; `--fresh` skips that. Each probe stops after `doctor.probe_timeout_s` (default 20 seconds, at most 300), at most four run at once, and a probe that times out counts as "could not run", never as broken.

To find which alias logs in as which account, `github.key_matches` asks every `Host` alias in `~/.ssh/config` at once, each with its own 3-second limit, and reuses those answers for a minute. An alias that did not answer in 3 seconds is named in the result ("did not answer in 3s"); one still being asked when the check had to answer is listed as "still asking", and running the check again fills it in. A personal store that is a local folder (`file://…`, not a git repo) gets its own read, write and marker checks: lore lists the folder, creates and removes one temporary file, and looks for `lore-store.yaml`, instead of the git probes.

## Step 2 — Check one setup

`--setup <name>` limits the run to one setup, which is quicker and keeps the output short:

<!-- example: doctor-all#one-setup -->
```console
# tomas, in agency/site
$ lore doctor --all --setup personal
setup personal (solo) ─ ~/work/personal/**
  ⚠ identity    tomas@home.example
  – github      no GitHub account in this setup (lore setup apply personal --step github)
  – secrets     none
  ⚠ identity.actor_copies  Tomas Weber (act_7a67…9504) in repo has "tomas", agency/org says "Tomas Weber"
      fix: lore actor refresh
  ⚠ folders.exist  not a folder: ~/work/personal
      fix: ~/work/personal is not a folder on this machine; create it, or change the glob with lore setup edit personal --folder <glob>
This repo, warnings:
  - actor_copies: actor Tomas Weber (act_7a67…9504): the copy in repo has display_name "tomas", its source (agency/org) says "Tomas Weber"; fix: lore actor refresh
  - org_admin_missing: Tomas Weber (act_7a67…9504) created org agency but has no org role (member list shows "-"); the org's creator is its admin; fix: lore doctor --fix (or: lore member edit act_7a67…9504 --role admin)
1 setup · 7 ok · 4 warnings · 0 errors → degraded (exit 1)
Context:
  - context: .lore/LORE.md not rendered yet (lore render)
(exit 1)
```
<!-- /example -->

The only warning is a folder glob whose folder does not exist on this machine (`folders.exist`). It is a warning, not an error: the setup still works in every folder that does exist, and the fix names both ways out (create the folder, or change the glob with `lore setup edit`).

## Step 3 — Read a broken result

Here Tomas's `~/.ssh/config` was changed so that `github.com` now offers his colleague's key: ssh to GitHub logs in as `mira-dev`, not as the setup's `tomas-dev`. Because the earlier results are cached for a minute, he adds `--fresh`:

<!-- example: doctor-all#wrong-account -->
```console
# tomas, in agency/site
$ lore doctor --all --setup agency --fresh
setup agency (admin of agency, default) ─ ~/work/agency/**
  ⚠ identity    tomas@agency.example · roster @tomas · employee since 2026-11-02
  ✗ github      tomas-dev via github.com
  – store me    read – write – private –
  ℹ store agency   read ✓ write ✓ private ℹ
  – secrets     none
  ⚠ identity.actor_copies  Tomas Weber (act_7a67…9504) in repo has "tomas", agency/org says "Tomas Weber"
      fix: lore actor refresh
  ✗ github.ssh_user  github.com logs in as mira-dev
      fix: github.com logs in as mira-dev, not tomas-dev. In ~/.ssh/config point Host github.com at tomas-dev's key (IdentityFile <that key>, IdentitiesOnly yes), or pick the mira-dev account
  ✗ github.key_matches  github.com logs in as mira-dev, but this setup's account is tomas-dev
      fix: Nothing lore can use logs in as tomas-dev. Run the GitHub step again so lore finds a route (an ssh alias that logs in as tomas-dev, or https with tomas-dev's gh login: gh auth login), or add a Host block for tomas-dev's key to ~/.ssh/config
  ⚠ github.gh_account  gh has no login for tomas-dev
      fix: gh auth login --hostname github.com
  ℹ store.private  agency: gh is not logged in, so lore cannot check whether agency/notes is private
      fix: Optional: install gh and run gh auth login so lore can check that agency/notes is private
      repair: lore setup repair agency
This repo, warnings:
  - actor_copies: actor Tomas Weber (act_7a67…9504): the copy in repo has display_name "tomas", its source (agency/org) says "Tomas Weber"; fix: lore actor refresh
  - org_admin_missing: Tomas Weber (act_7a67…9504) created org agency but has no org role (member list shows "-"); the org's creator is its admin; fix: lore doctor --fix (or: lore member edit act_7a67…9504 --role admin)
1 setup · 15 ok · 4 warnings · 2 errors → broken (exit 2)
Context:
  - context: .lore/LORE.md not rendered yet (lore render)
(exit 2)
```
<!-- /example -->

Two error-level checks fail: `github.ssh_user` (the key logs in as someone else) and `github.key_matches` (no route lore can use logs in as the setup's account). Every git call to the agency store would push as the wrong person, so the setup is broken and doctor exits 2. Neither fix is something lore runs for you: lore never edits `~/.ssh/config` and never reads or copies a key.

| Exit | Status | When |
|---|---|---|
| 0 | healthy | every check is ok or skipped |
| 1 | degraded | a warning, a warning-level failure, or a check that could not run (a timeout, a missing program) |
| 2 | broken | an error-level check failed |

A check that could not run makes the result degraded, never broken, because the thing it tests may be fine.

## Step 4 — Read the report as JSON

`lore doctor --all --json` prints the usual doctor report with three more keys, and its `exit_status` follows the table above. A script that only needs the status and the problems can read those keys; here every check that is neither ok nor skipped is printed:

<!-- example: doctor-all#json -->
```console
# tomas, in agency/site
$ lore doctor --all --setup agency --json | python3 -c 'import json,sys; r=json.load(sys.stdin); print(r["status"], r["exit_status"], r["counts"]); [print(c["check"], c["status"], c["level"], c.get("target","-"), "|", c.get("detail","")) for s in r["setups"] for c in s["checks"] if c["status"] not in ("ok", "skip")]'
broken 2 {'ok': 15, 'warn': 4, 'fail': 2, 'skip': 12, 'error': 0}
identity.actor_copies warn warn - | Tomas Weber (act_7a67…9504) in repo has "tomas", agency/org says "Tomas Weber"
github.ssh_user fail error - | github.com logs in as mira-dev
github.key_matches fail error - | github.com logs in as mira-dev, but this setup's account is tomas-dev
github.gh_account warn warn - | gh has no login for tomas-dev
store.private warn info store agency | gh is not logged in, so lore cannot check whether agency/notes is private
```
<!-- /example -->

| Key | Meaning |
|---|---|
| `setups[]` | one entry per setup: `name`, `kind`, `org`, `default`, `folders`, its own `status` (`healthy`, `degraded` or `broken`) and its `checks` |
| `setups[].checks[]` | `check` (an id from [Every check](#every-check)), `status` (`ok`, `warn`, `fail`, `skip`, or `error` when the check could not run), `level` (`error` or `warn`), `target` (the store, tool or repo, for checks run per item), `detail`, `fix` (`text`, `command`, `runnable`, `outward`), `checked_at`, `took_ms` |
| `machine.checks[]` | checks across setups, such as `folders.overlap` |
| `counts` | results by status: every setup's checks, the machine checks (plain `lore doctor` counts the active setup's checks) and `findings`; `skip` counts the checks that do not apply to this setup (no store url yet, no key file recorded, …) |
| `findings` | every warning and error of the report as a result (`check`, `status`, `level`, `detail`, `fix`, `target` `repo <key>`): a stale `.lore/LORE.md`, a gitignored `.lore/data`, unpushed store work. `status` is the worst of `setups[].checks`, `machine.checks` and `findings`, so it is never worse than what the report lists. A finding whose fix is one plain lore command runs with `lore doctor fix --finding <key>` (it asks first; `--yes` skips the question) |

`lore doctor last` prints each setup's last recorded results and when they were taken without running a check (the setup page's Doctor opens on it).

## Step 5 — List the steps to redo with `lore setup repair`

`lore setup repair --name <name>` runs that setup's checks fresh, groups the failing ones by the setup step they belong to, and opens the setup page on only those steps (the passing steps are shown as done and not touched). Without a name it repairs the active setup. `--dry-run` only lists them, and `--json` prints the same list for a script:

<!-- example: doctor-all#repair only=1,2 -->
```console
# tomas, in agency/site
$ lore setup repair --name agency --dry-run
agency: 1 step to redo
  github
    ✗ github.ssh_user  github.com logs in as mira-dev
      fix: github.com logs in as mira-dev, not tomas-dev. In ~/.ssh/config point Host github.com at tomas-dev's key (IdentityFile <that key>, IdentitiesOnly yes), or pick the mira-dev account
    ✗ github.key_matches  github.com logs in as mira-dev, but this setup's account is tomas-dev
      fix: Nothing lore can use logs in as tomas-dev. Run the GitHub step again so lore finds a route (an ssh alias that logs in as tomas-dev, or https with tomas-dev's gh login: gh auth login), or add a Host block for tomas-dev's key to ~/.ssh/config
(exit 1)
$ lore setup repair --name agency --json | python3 -c 'import json,sys; r=json.load(sys.stdin); [print(s["name"], st["step"], [c["check"] for c in st["checks"]]) for s in r["setups"] for st in s["steps"]]'
agency github ['github.ssh_user', 'github.key_matches']
```
<!-- /example -->

| Flag | Effect |
|---|---|
| `--dry-run` | only list the failing steps and their fixes |
| `--yes` | run the fixes that are runnable and do not change anything outside this machine (such as `lore render`, `lore skill install`, `lore setup --agent-hooks`), then check again; fixes that touch GitHub stay listed for you to confirm |
| `--all` | repair every setup with a failure, one after the other |
| `--json` | print `{"setups":[{"name":…,"steps":[{"step":…,"checks":[…]}]}]}` instead of opening the page |

It exits 1 while any step still has a failing check, and 0 when everything passes. Only failed checks count: warnings and checks that could not run are not repaired, so a setup that doctor calls degraded can still answer "nothing to repair".

To run one fix yourself, `lore setup fix --setup <setup> --check <id> --yes` runs that check's fix command when it is marked runnable; a fix that offers a choice (a linked skill folder: `replace` or `update`) runs only the one named with `--choice <id>`; a fix lore cannot run is refused with its text, so you can do it by hand ([Fixes that lore can run](../team/guided-setup.md#step-7--run-a-fix-from-the-command-line)). In the prebuilt binaries, **Repair** on the guided setup page does the same from Home or Doctor.

## Which repos it checks

lore records each repo it runs in on this machine. `--all` checks those under each setup's folders, plus the repo you are in. On a machine where lore has not recorded any repo yet, it looks for folders holding `.lore/` under each setup's folders instead, down to `doctor.scan_depth` levels (default 3, at most 10).

Run outside a git repo (in your home folder, say), `lore doctor --all` skips the repo checks and says so in one line, `This repo: not inside a git repo, so the repo checks were skipped (N); run lore doctor in a repo to check it`. Those skips do not change the exit status.

## Every check

Each check has a level: an **error** blocks saving its setup step and makes doctor exit 2; a **warn** is reported and makes doctor exit 1. An **info** check is shown for reading only and never changes the exit code. Two checks change level with the store: `store.private` is an error for a team's org store and a warning for your personal store, and `store.write` is a warning for a store attached `--read-only`. When `gh` is missing or has no login on an ssh route, `store.private` is info: lore cannot ask, and nothing is known to be wrong.

| Check | Level | What it checks | Fix it points to |
|---|---|---|---|
| `setup.config` | error | the setup's entry in `~/.lore/config.yaml` is valid | `lore config check` lists the problems; edit the file (shown, not run) |
| `setup.org_entry` | error | a member or admin setup's org entry exists | `lore setup join --invite <invite>` again, or `lore setup remove --name <name>` |
| `setup.name_free` | error | no other setup has this name (setup page only) | pick another name, or `lore setup edit --name <name>` |
| `identity.email_format` | error | the email is a valid address (for a member or admin setup, the org entry's identity email, `orgs[<org>].identity`) | type the address you use for this work |
| `identity.invite_email` | error | a member's email is the invite's | use the invite's address, or ask for a new invite |
| `identity.git_email` | warn | git commits here carry this email (for a member or admin setup, the org entry's identity email) | `git config --global user.email <email>` (shown, never run) |
| `identity.roster` | warn | your email is on the team roster, read from the org store that holds it (the rows `lore actor show` reads); the identity line shows `roster @<handle>` | finish the join step, or `lore actor contact add --actor …` |
| `identity.period` | warn | you are an active team member, not marked as left: the identity line shows the open period (`employee since 2025-03-01`) | ask an admin to run `lore actor rejoin` |
| `identity.top_level` | warn | no old top-level `identity:` is left beside a solo setup's identity (the setup is the one source) | `lore identity migrate --keep setup --setup <name>` (Run it) |
| `identity.top_level_keep` | warn | only when the top-level `identity:` and the setup's differ: the other choice | `lore identity migrate --keep top --setup <name>` (Run it) |
| `github.ssh_key` | error | the key file the setup records exists, private key or `.pub` (skipped when none is recorded) | `ssh-keygen -t ed25519 …`, then add its `.pub` on GitHub |
| `github.ssh_auth` | error | GitHub accepts the key through the host alias | add the key at github.com/settings/keys as the right account |
| `github.ssh_user` | error | the key logs in as the setup's GitHub user | point the alias at the right key in `~/.ssh/config` (`IdentitiesOnly yes`) |
| `github.alias_config` | warn | the ssh alias offers only this key | add `IdentitiesOnly yes` under the `Host` block |
| `github.gh_account` | warn | `gh` has a login for the account (gh is optional) | `gh auth login --hostname github.com` |
| `github.gh_matches` | info | which account is `gh`'s active one; informational only, never a warning: lore runs `gh` as the setup's own account, so the active account only matters for `gh` you run by hand (skipped when gh is missing or has no active account) | none needed; for `gh` by hand, `gh auth switch --user <user>` |
| `github.key_matches` | error | the setup's route logs in as its GitHub account: for ssh the alias (and any recorded key, whose fingerprint still matches the one saved), for https a gh login for the account | an alias that does log in as that account: `lore setup edit --name <setup> --route ssh --host-alias <alias> --ssh-key <key>.pub` (runnable) when its key has a `.pub`, else `--route ssh --host-alias <alias> --no-ssh-key`; for https, `gh auth login` |
| `github.identity_email` | warn | the setup's identity email (for a member or admin setup, its org entry's identity email) is one of the GitHub account's emails, read with that account's own gh token; skipped with "could not read" when the token lacks the `user:email` scope | pick the account whose emails include it, add the email at github.com/settings/emails, or change the identity email |
| `store.url` | error | the store url is valid (an ssh url; a token in a url is never stored) | use a url like `git@github.com:you/lore-me.git` |
| `store.read` | error | you can read the store | `gh repo create <owner>/<repo> --private` (runnable, asks), ask for access, or check the network |
| `store.write` | error | you can write the store (a dry-run push) | ask the owner for write access, or attach it `--read-only` |
| `store.marker` | warn | the repo is already a lore store | saving the step runs `lore store init` |
| `store.private` | error / warn | the repo is private | `gh repo edit <owner>/<repo> --visibility private` (runnable, asks) |
| `invite.parse` | error | the invite text is a lore invite | copy the whole line starting with `lore-invite:v1:` |
| `invite.found` | error | the invite row is in the team's store | ask the inviter to invite you again |
| `invite.status` | error | the invite is still pending | ask the inviter for a new one |
| `invite.email` | error | your email is the invite's | use that address, or ask for a new invite |
| `invite.pending` | warn | (admin) no pending invite is waiting | `lore team invite resend --ref <id>` (runnable) or `revoke` |
| `invite.github_access` | warn | (admin) invited people can reach the store on GitHub | `gh api -X PUT repos/…/collaborators/<user> -f permission=push` (runnable, asks) |
| `org.free` | error | (team step) the store is new, empty or already a lore store (an existing team store is attached) | give a new or empty private repo, or your team's existing lore store |
| `org.name_free` | error | (team step) no org entry of that name exists here | pick another name, or `lore org rename` |
| `live.mode` | warn | with autopush on, the repo's remote accepts a dry-run push of lore's live rows | get push access, or set `live.autopush: false` on the setup |
| `tools.skill_installed` | warn | the lore skill is installed for each tool | `lore skill install --tool <tool>` (runnable) |
| `tools.skill_version` | warn | the installed skill matches this lore's version; with no version line, its files equal this lore's skill | `lore skill install --tool <tool>` (runnable); for a linked skill folder, two choices, each runnable: `--link replace` or `--link update` (`lore setup fix --setup <setup> --check tools.skill_version --choice replace\|update`) |
| `tools.skill_modified` | warn | the installed skill was not edited by hand (read from the same compare as `tools.skill_version`, so the two never contradict) | reinstall (keeps a `SKILL.md.bak-<date>` copy) |
| `tools.pointer_file` | warn | the tool reads `.lore/LORE.md` in this repo | `lore render --add-target <tool>` in the repo (runnable): adds the tool to `context.targets` and writes its file; a plain `lore render` wires only `context.targets` |
| `tools.hooks` | warn | the session hooks are installed | `lore setup --agent-hooks --yes` (runnable) |
| `secrets.backend_present` | warn | the setup's secret manager is on PATH | install it; references to it will not resolve on this machine |
| `folders.exist` | warn | each folder glob points at a folder that exists | create it, or `lore setup edit --name <name> --folder …` |
| `folders.overlap` | error | no other setup ties on a folder | make one glob more specific, or run with `--setup` |
| `folders.shadow` | warn | no other setup takes part of a folder with a more specific glob | informational: that part stays with the other setup |
| `folders.reachable` | warn | lore can pick the setup by itself: it has a folder, it is the default, or it is a team setup picked by its org | add a folder, or `lore setup edit --name <name> --default` (shown, never run: it takes the default from the other setup) |
| `repo.init` | warn | lore is set up in the repo | `lore init` (runnable) |
| `repo.org` | error | the repo's org matches the setup's org | run with `--setup <a setup of that org>`, or fix where the repo's org comes from |
| `repo.stores` | error | the stores in `.lore/stores.yaml` are readable with this setup's key | ask the store's owner for access, or check the key |
| `duplicate_actors` | warn / info | (repo, also in plain `lore doctor`) no two human actors share a name without one merged into the other, as an email change before 0.1.18 left them; a warning when the repo has an org, a note without one (both actors stay, nothing is lost) | `lore actor contact add --actor <keep> --type email --value "<old email>" --identity --merge` (asks first; no row is rewritten, reports follow `merged_into`); without an org, none until the repo has one |
| `duplicate_repos` | warn | (repo, also in plain `lore doctor`) no two repos rows describe one repository (same project and a shared address or mount name), as two machines upgrading one 0.1.17 repo before either pushed left them before repo ids were derived from the project and the address | `lore fix` (or `lore doctor --fix`) keeps the id `_meta.json` names, adds the other rows' addresses to it, re-points the rows that name them and moves them to the trash (`merged into <keep>`) |

"Runnable" fixes can be run by `lore setup fix` or `lore setup repair --yes`; the ones marked "asks" change something on GitHub and always need an explicit yes. Only `lore`, `gh` and `git` are ever run, from a fixed list, never a shell string.

## Try it

Tomas points `github.com` back at his own key and asks for the repair list again; the setup has nothing left to redo:

<!-- example: doctor-all#repair only=3 -->
```console
# tomas, in agency/site
$ lore setup repair --name agency --dry-run
agency: nothing to repair
```
<!-- /example -->

## Pitfalls

- **Results are cached for a minute.** Right after you change `~/.ssh/config`, a `gh` login or a store's access, run `lore doctor --all --fresh`, or you may read the old answer. `lore setup repair` always checks fresh and has no `--fresh` flag (it answers `E_INVALID_INPUT unknown flag: --fresh`).
- **Degraded is not broken.** A missing `gh`, a timeout or a store whose visibility could not be read makes the run exit 1, never 2. A script that treats exit 1 as failure fails on every machine without `gh`.
- **`lore setup repair` ignores warnings.** "nothing to repair" means no error-level check failed; `lore doctor --all` can still list warnings for the same setup.
- **The ssh checks are only as good as `~/.ssh/config`.** lore tests what ssh does with the alias, never the key file's content, so a key that GitHub accepts for another account shows up only as `github.ssh_user` / `github.key_matches`, which is why both are errors.
- **A disabled setup is skipped.** `lore doctor --all` prints one line for it (`setup old-client is disabled: skipped (lore setup enable old-client)`) and checks nothing else; a setup in the trash is not listed at all.

## Related

- [Guided setup](../team/guided-setup.md): what a setup is, the steps the checks belong to, and how lore picks the active setup.
- [Setups from the command line](../team/setup-cli.md): every `lore setup` subcommand with real output.
- [Check lore's health with `lore doctor`](doctor.md): the single-repo health check and its other checks.
- [Setup and invite errors](../team/setup-errors.md).
