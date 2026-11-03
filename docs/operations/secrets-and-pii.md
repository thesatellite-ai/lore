# Secrets and personal data

**In short:** lore refuses to save a note that looks like a password, key or token, because notes end up in git where everyone with the repo can read them. If you really must keep one, it stays on your machine and never goes to git. You can also add your own patterns, and a few switches turn off lore's other safety refusals when you know what you are doing.

## What it is and when you want it

A secret is anything that lets someone in: a password, an access key for a cloud account, a GitHub token, a private key file. Personal data (PII, "personally identifiable information") is anything that points at a real person, such as a customer's phone number.

lore checks the text of every note before it saves it, like a spell checker that looks for keys instead of typos. It has a built-in list of common shapes: cloud access keys, GitHub and package-registry tokens, chat-app tokens, private key blocks, `Authorization: Bearer …` headers, `password=…` pairs, web addresses with a password in them, and more. A match stops the command.

Then there is a second check: before lore writes a note into `.lore/data` (the files that get committed), it checks again. So even a note you forced in stays out of git.

## Before you start

- lore is set up in the repo.
- Keep real secrets in a password manager or your CI's secret store, and write in the note **where** the secret lives, not the secret itself ("The staging deploy key is in the team vault under staging/deploy").

## Step 1 — See lore refuse a secret

Alice (an agency developer) tries to save a deploy key as a memory. lore stops, names the kind of secret it found, and shows only the first four characters:

<!-- example: ops#secret-refused -->
```console
# alice, in website
$ lore memory add --body 'Deploy key for staging: AKIAIOSFODNN7EXAMPLE'
ERROR: [E_SECRET_DETECTED] body contains a secret (credential pattern aws_access_key, preview AKIA****)
  hint: use --allow-secrets to override (logged loud); see also ~/.lore/pii-patterns.txt
(exit 1)
```
<!-- /example -->

The example key is the public sample key from cloud documentation; it opens nothing.

## Step 2 — Keep one anyway, on your machine only

`--allow-secrets` saves the note in your local database. The second check still keeps it out of `.lore/data`, so it never reaches git:

<!-- example: ops#allow-secrets only=1,2 -->
```console
# alice, in website
$ lore memory add --allow-secrets --body 'Deploy key for staging: AKIAIOSFODNN7EXAMPLE'
✓ mem_d431…313a 
  scope: [master]
lore sync: secret-detected memories/mem_d431…313a.json: credential pattern aws_access_key found — not written to git; edit the row or set LORE_SYNC_ALLOW_SECRETS=1 for one run
$ grep -rl AKIA .lore/data || echo 'not in .lore/data'
not in .lore/data
```
<!-- /example -->

That row now lives only in `.lore/lore.db` and in your backups. When you no longer need it, delete it:

<!-- example: ops#allow-secrets only=3 -->
```console
# alice, in website
$ lore memory delete mem_d431…313a --confirm
lore sync: secret-detected memories/mem_d431…313a.json: credential pattern aws_access_key found — not written to git; edit the row or set LORE_SYNC_ALLOW_SECRETS=1 for one run
✓ mem_d431…313a deleted (hard)
```
<!-- /example -->

If a note that is meant to be shared trips the check by mistake, edit its text so it no longer looks like a secret. `LORE_SYNC_ALLOW_SECRETS=1` lets one run write such rows into `.lore/data` anyway; use it only when you are sure, because what is in git stays in git history.

## Add your own patterns

`lore pii-pattern` keeps a list of your own patterns (regular expressions: a small text language for "this shape of text", like `INV-[0-9]{6}` for "INV- and six digits"). A new pattern starts switched off; you switch it on with its id:

<!-- example: ops#pii-pattern -->
```console
# alice, in website
$ lore pii-pattern add --name invoice-number --regex 'INV-[0-9]{6}'
✓ pii-pattern invoice-number added (disabled — run `pii-pattern enable pip_c0f3…b5b8`)
$ lore pii-pattern list
[off] invoice-number  (user)
$ lore pii-pattern enable pip_c0f3…b5b8
✓ pii-pattern pip_c0f3…b5b8 enabled
$ lore pii-pattern list
[on] invoice-number  (user)
```
<!-- /example -->

`lore pii-pattern disable <id>` switches it off again. The list is kept per machine, in the database; it is not shared through git.

**Today these patterns are stored but not used.** lore's save checks only use the built-in list, and the `~/.lore/pii-patterns.txt` file named in the refusal hint is not read either. Until that is fixed, do not count on your own patterns to stop a note.

## Safety switches

lore refuses a few risky setups on its own. Each refusal has one environment variable that turns it off (an environment variable is a setting you put in front of a command or in your shell, like `LORE_ALLOW_ROOT=1 lore doctor`). Each takes the value `1`; anything else counts as off.

| Variable | What lore refuses without it | Why |
|---|---|---|
| `LORE_ALLOW_ROOT` | Running as the root (administrator) user: `E_ROOT_REFUSED`. | Files created as root can no longer be changed by your normal user, and a mistake as root can damage the whole machine. |
| `LORE_ALLOW_NETWORK_FS` | A database on a network drive or a cloud-synced folder (shared network folders, folders a sync app copies between computers): `E_NETWORK_FS`. | SQLite databases get damaged silently when two machines or a sync app touch the file. Keep the repo on a local disk; git shares the notes anyway. |
| `LORE_ALLOW_DB_PATH_OUTSIDE` | A `..` (a step up out of the folder) in the database path of a `.lore/lore.toml` pointer file: `E_BAD_PATH`. | A path that climbs out of the project can point lore at someone else's files. |
| `--allow-secrets` (a flag, not a variable) | Saving a note that looks like a secret: `E_SECRET_DETECTED`. | See Step 1. |

Use them for one command, not in your shell profile, so the refusal is back next time.

## Pitfalls

- **The check looks at shapes, not meaning.** A password written as plain words ("the password is blue-horse-42") does not match any pattern. Never rely on lore as your only guard.
- **`--allow-secrets` rows live in your backups too.** `lore backup` copies the whole database. Do not put `.lore/backups/` in a shared folder.
- **A forced row keeps a warning in `lore doctor` while it exists.** Each refused file shows as a `secret-detected` warning and makes doctor DEGRADED. Edit the text or delete the row; once the row is gone, doctor drops the warning.
- **Custom patterns do nothing yet** (see above), even when `lore pii-pattern list` shows them `[on]`.
- **Support bundles are safe to share by default:** they carry counts, not note text. See [doctor](doctor.md#send-a-support-bundle-with-a-bug-report).

## Related

- [The audit log](audit-log.md)
- [Back up, restore and repair](backup-restore-repair.md)
- [Use lore in CI](ci.md)
- [Rules, memories and decisions](../knowledge/rules-memories-decisions.md)
- [Environment variables](../reference/environment.md)
- [Operations index](README.md)
