# Setup and invite errors

**In short:** each section below starts with an error code you may see from [guided setup](guided-setup.md), [team invites](invites.md) or [`lore setup fix`](../operations/doctor-all.md). It says what causes it in plain words and how to fix it. The exit status is in brackets; the full list of exit statuses is in [Error codes and exit statuses](../reference/errors.md#exit-statuses).

> **Status: in development.** These codes are being added in lore 0.1.18 on the `feat/onboarding` branch. The example output was copied from a development build on 2026-11-06.

First steps for anything odd with setups:

- `lore setup list`: every setup, and which one is active here and why.
- `lore whoami --explain`: the setup rules and the org rules, each with its outcome.
- `lore doctor --all`: every check of every setup, with fixes.

Most setup steps do not fail with an error code at all: a problem shows as a failing check (`store.read`, `github.ssh_user`, …) with its fix, on the page or in `lore setup check`. The codes below are what a command answers when it cannot go on.

## Setups

### `E_SETUP_NOT_FOUND` (exit 27)

**Cause:** `--setup <name>`, `LORE_SETUP=<name>`, or a `lore setup` command names a setup that is not in `~/.lore/config.yaml` on this machine.

```console
$ lore setup show --name personal
ERROR: [E_SETUP_NOT_FOUND] no setup named "personal"; this machine has no setups
  hint: lore setup makes one
(exit 27)
```

**Fix:** check the name with `lore setup list`. Setups are per machine: a setup made on your laptop does not exist on a server until you make it there. Unset a stale `LORE_SETUP` in your shell profile.

### `E_SETUP_AMBIGUOUS` (exit 21)

**Cause:** two setups match this folder equally (both have `~/work/**`), or the repo's org is used by two setups and neither matched by folder. lore never guesses which one you meant.

**Fix:** make one folder glob more specific (`lore setup edit --name acme-hub --folder '~/work/clients/acme-hub/**'`), or run the command with `--setup <name>`, or pin the repo with `lore setup use --name <name> --here`. `lore doctor --all` reports the same tie as the `folders.overlap` check.

### `E_SETUP_NAME_TAKEN` (exit 20)

**Cause:** a new setup was given the name of one that exists. On the page and in `lore setup check`, this shows as the failing `setup.name_free` check of the first step.

**Fix:** pick another name, or change the existing setup with `lore setup edit --name <name>`.

### `E_SETUP_ORG_MISMATCH` (exit 23)

**Cause:** the active setup belongs to org `agency`, but this repo says it belongs to another org through something stronger (`LORE_ORG`, `lore org use`, a committed `.lore/org.yaml`, a store link or a repo pattern). Writes are refused so that notes never land in the wrong org; reads still work. The join step answers the same code when this machine already has an org entry named like the invite's org but for a different store.

**Fix:** run with `--setup <a setup of that org>`, or fix the source that names the other org (`lore whoami --explain` shows which one). For a join, rename the existing entry first (`lore org rename --old agency --new agency_old`), then join again.

### `E_SETUP_NO_PERSONAL_STORE` (exit 20)

**Cause:** you wrote `--to me` in a setup that has `personal_store: none`.

```console
$ lore memory add --body 'x' --to me
ERROR: [E_SETUP_NO_PERSONAL_STORE] setup personal has no personal store (personal_store: none); write to the repo or the org store, or lore setup edit personal --personal-store <url>
  hint: --to repo, --to <org store>, or lore setup edit personal --personal-store <url>
```

**Fix:** write to the repo or the org store instead, or give the setup a personal store: `lore setup edit --name personal --personal-store git@github.com:tomas/lore-me.git` (or `inherit` for the top-level `personal.store`).

### `E_SETUP_STEP_FAILED` (exit 23)

**Cause:** `lore setup apply` (or Save on the page) was asked to save a step while one of its error-level checks still fails. The message lists the failing check ids.

**Fix:** run `lore setup check --setup <setup> --step <id>` to see each failing check with its fix, fix them, then save again.

### `E_SETUP_STEP_ORDER` (exit 23)

**Cause:** a step was saved before the steps it needs. For example, `identity` needs `kind`:

```console
$ lore setup apply --setup newone --step identity --answers '{"name":"Tomas Weber","email":"tomas@agency.example"}'
ERROR: [E_SETUP_STEP_ORDER] step identity needs kind first
(exit 23)
```

**Fix:** save the earlier steps first; `lore setup plan --kind <kind>` lists each step with what it needs.

## Invites

### `E_INVALID_INPUT` "handle @<h> is taken" on the join step

**Cause:** someone on the org's roster already has the handle you answered on the Identity step. The join had already added the org's entry on this machine with that handle when the roster refused it.

**Fix:** change the handle on the Identity step and save the join step again: the join takes the new handle into the org entry it added before and registers you with it (`lore setup apply --setup <setup> --step identity --answers '{"email":"<your email>","name":"<your name>","handle":"<another handle>"}'`, then the join step again, or the same on the setup page).

### `E_JOIN_ORG_UNKNOWN` (exit 27)

**Cause:** `lore setup join --invite <name>` names an org this machine has no entry for. A member an admin added with `lore member add` joins from a fresh machine, where the org's name means nothing yet.

```console
$ lore setup join --invite toy --no-open
ERROR: [E_JOIN_ORG_UNKNOWN] setup join: no org named "toy" on this machine yet; join with the org's store url the admin gave you
  hint: lore setup join --invite "<the org's store url>" (lore member add printed it for your admin); an outsider: paste the whole line starting with lore-invite:v1:
(exit 27)
```

**Fix:** run the command `lore member add` printed for your admin: `lore setup join --invite "<the org's store url>"` (for example `lore setup join --invite git@github.com:toyco/toy-team.git`).

### `E_INVITE_INVALID` (exit 20)

**Cause:** the text given to `lore setup join` (or pasted on the page) is not a whole lore invite: the `lore-invite:v1:` prefix is missing, or the text after it was cut or changed (a chat app may break a long line).

```console
$ lore setup join --invite lore-invite:v1:garbage
ERROR: [E_INVITE_INVALID] this is not a lore invite; copy the whole line starting with lore-invite:v1: (the text after the prefix is cut or changed)
  hint: ask the person who invited you to run lore team invite resend --ref <your-email>
(exit 20)
```

**Fix:** copy the whole line again. The inviter can print it again with `lore team invite resend --ref "<your email>"`.

### `E_INVITE_NOT_FOUND` (exit 27)

**Cause:** the invite's row is not in the team's store (it was never pushed, or the store is another one), or `lore team invite --email resend|revoke` names an id or email with no invite.

**Fix:** ask the inviter to run `lore team invite --email "<your email>"` again. As an admin, `lore team invite list` shows the invites that exist.

### `E_INVITE_EXPIRED` (exit 23)

**Cause:** the invite is past its last day (14 days after it was written, unless `--expires` said otherwise).

**Fix:** ask the inviter for a new one; as the admin, run `lore team invite --email <email>` again (an expired invite does not block a new one).

### `E_INVITE_REVOKED` (exit 23)

**Cause:** an admin revoked the invite.

```console
$ lore team invite resend --ref mira@agency.example
ERROR: [E_INVITE_REVOKED] invite inv_01a1b195c0f47163971b269b3863583a was revoked
  hint: lore team invite mira@agency.example
```

**Fix:** ask the inviter; only they can send a new invite.

### `E_INVITE_USED` (exit 23)

**Cause:** the invite was already accepted by another person. Every invite works once. Running your own join again is fine and does not answer this.

**Fix:** ask the inviter for a new invite of your own.

### `E_INVITE_EMAIL` (exit 20)

**Cause:** the invite is for another email than the one you gave in the identity step (neither your main address nor one of your other addresses is the invite's).

**Fix:** add the invite's address to the identity step (main email, or "other addresses you use"), or ask the inviter for an invite to the address you use.

## Stores

### `E_STORE_NO_ACCESS` (exit 23)

**Cause:** GitHub (or the git host) refused this setup's account or key for the store: you are not a collaborator, the repo does not exist, or the ssh key logs in as another account. A network failure is a different code (`E_STORE_UNREACHABLE`, exit 15).

**Fix:** ask the store's owner for access (for a team store, the admin can run `lore team invite --email … --grant`), accept the GitHub invitation at github.com/notifications, or check the key with `lore doctor --all --setup <name>` (`github.ssh_auth`, `github.ssh_user`).

In this build the code is registered but the setup steps report the problem as the failing `store.read` check (with the same fixes) rather than as this error.

### `E_STORE_NO_WRITE` (exit 23)

**Cause:** you can read the store but not push to it with this account.

**Fix:** ask for push (write) access, or attach the store `--read-only` if reading is all you need.

In this build the code is registered but the setup steps report the problem as the failing `store.write` check rather than as this error.

## Fixes

### `E_FIX_REFUSED` (exit 20)

**Cause:** `lore setup fix` (or the page's Run button) would not run a check's fix. Either the fix is not one lore runs (it is text for you to follow), or a value filled into its command failed the safety check: it is empty, holds a control character or a shell character, starts with `-` where a flag is not expected, or names a program other than `lore`, `gh` or `git`.

```console
$ lore setup fix --setup agency --check github.gh_account
ERROR: [E_FIX_REFUSED] fix for github.gh_account refused: fix is not runnable; do it by hand: gh is optional; it only speeds up checks and lets lore offer 'create repo'. To add: gh auth login --hostname github.com
  hint: run lore setup check to see the fix text and do it by hand
(exit 20)
```

The page and `lore doctor` do not show this error for a fix they cannot fill in: they show the fix text with `…` where a value is missing, and no command to run.

**Fix:** follow the fix text by hand. When a value was refused, correct the setup or answer it came from (a folder, a GitHub user, a setup name) with `lore setup edit`, then check again.

## Other codes you may see

| Code | When, in setup |
|---|---|
| `E_NEEDS_CONFIRM` (26) | `lore setup fix`, `lore team invite revoke` and `lore team invite --grant` without `--yes` and without a terminal; `lore setup trash purge` without `--confirm` (`--yes` never purges) |
| `E_REFUSED` (23) | a `lore team invite --email …` command while no admin setup of the org is active; add `--setup <an admin setup>` |
| `E_INVALID_INPUT` (20) | a second invite for an email that has a pending one; `--role` without `--project`; revoking an accepted invite; `lore setup use --here` outside a lore repo |
| `E_UNAUTHORIZED` (28) | the setup page link was already used, or a request without the page's session; run `lore setup` again for a new link |
| `E_CONFIG_INVALID` (20) | a `setups:` entry in `~/.lore/config.yaml` is not valid; `lore config check` lists the problems |

## Related

- [Guided setup](guided-setup.md), [Team invites](invites.md), [Checking every setup](../operations/doctor-all.md).
- [Error codes and exit statuses](../reference/errors.md) and [Troubleshooting](troubleshooting.md).
