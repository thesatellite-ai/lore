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

Alice (an agency developer) tries to save a deploy key as a memory. lore stops, names the kind of secret it found, shows only the first characters, and asks what to do. At a terminal a menu offers: redact it with a reference, keep the note without the value (unstored), mark it not a secret (a typed reason is required), or cancel; `--yes` alone does not pick one. Without a terminal (a script, an agent) the question becomes `E_NEEDS_CONFIRM`; with `--json` the error carries `findings[]` (masked) and `next.add_args_one_of`, the three flag sets you can add:

<!-- example: ops#secret-refused -->
```console
# alice, in website
$ lore memory add --body 'Deploy key for staging: AKIAIOSFODNN7EXAMPLE'
ERROR: [E_NEEDS_CONFIRM] engine: body looks like a secret (aws_access_key) at line 1: AKIAI…: redact it with a reference, mark it unstored, mark it not a secret, or cancel?
  hint: re-run your own command with one of: --secret-ref "<manager>://<entry>" | --secret-unstored | --not-a-secret --reason "<why>" --yes
(exit 26)
```
<!-- /example -->

The example key is the public sample key from cloud documentation; it opens nothing.

## Step 2 — Say it is not a secret

When the match is not a real secret (a sample value like this one, a test string), `--not-a-secret --reason "<why>" --yes` answers the question and stores the row as not a secret (a bare `--yes` does not). For a real secret use `--secret-ref "<manager>://<entry>"` (the note keeps a reference to where it lives) or `--secret-unstored` (the note is kept, the value is left out). Its file is written to `.lore/data`, but lore's commit hook checks every file again and refuses to commit one that holds a credential pattern, so a real key does not reach git by accident:

<!-- example: ops#allow-secrets only=1,2 -->
```console
# alice, in website
$ lore memory add --not-a-secret --reason 'AWS documentation example key' --yes --body 'Deploy key for staging: AKIAIOSFODNN7EXAMPLE'
✓ mem_28f8…03df
  scope: [master]
$ grep -rl AKIA .lore/data || echo 'not in .lore/data'
.lore/data/memories/mem_28f8…03df.json
```
<!-- /example -->

Delete the row (or edit the key out) before you commit:

<!-- example: ops#allow-secrets only=3 -->
```console
# alice, in website
$ lore memory delete mem_28f8…03df --yes
✓ mem_28f8…03df moved to the trash (restore: lore trash restore mem_28f8…03df)
lore sync: trashed rows 1
lore sync: rows whose file disappeared are kept in `lore trash --local` (undo: `lore trash restore --local <id>`)
```
<!-- /example -->

If a note that is meant to be shared trips the check by mistake, edit its text so it no longer looks like a secret. `LORE_SYNC_ALLOW_SECRETS=1` is ignored since 0.1.18 (it prints a warning); keep a false positive with `--not-a-secret --reason "<why>" --yes`, and remember that what is in git stays in git history.

## Add your own patterns

`lore pii-pattern` keeps a list of your own patterns (regular expressions: a small text language for "this shape of text", like `INV-[0-9]{6}` for "INV- and six digits"). A new pattern starts switched off; you switch it on with its id:

<!-- example: ops#pii-pattern -->
```console
# alice, in website
$ lore secrets rule add invoice-number --regex 'INV-[0-9]{6}' --description 'our invoice numbers' --yes
✓ invoice-number added in this repo (srl_…0f72)
$ lore secrets rules
RULE	STATE	LEVEL	WHERE	NOTE
invoice-number	on	repo	this repo	our invoice numbers
$ lore memory add --body 'Invoice INV-123456 is still open'
ERROR: [E_NEEDS_CONFIRM] engine: body looks like a secret (invoice-number) at line 1: IN…: redact it with a reference, mark it unstored, mark it not a secret, or cancel?
  hint: re-run your own command with one of: --secret-ref "<manager>://<entry>" | --secret-unstored | --not-a-secret --reason "<why>" --yes
(exit 26)
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
| `LORE_ALLOW_DB_PATH_OUTSIDE` | A `..` (a step up out of the folder) in the database path of a `.lore/lore.yaml` pointer file: `E_BAD_PATH`. | A path that climbs out of the project can point lore at someone else's files. |
| `--secret-ref`, `--secret-unstored`, `--not-a-secret --reason … --yes` (flags, not variables) | Saving a note that looks like a secret: `E_NEEDS_CONFIRM` until you add one of them; a bare `--yes` is not an answer. | See Step 2. |

Use them for one command, not in your shell profile, so the refusal is back next time.

A repo in a `Dropbox` folder, refused and then let through for one command:

<!-- example: environment#network-drive -->
```console
# alice, in Dropbox/website
$ lore rule list
ERROR: [E_NETWORK_FS] rowfs: the store is inside a "dropbox" folder (cloud sync)
  hint: move the store to a local folder, or set LORE_ALLOW_NETWORK_FS=1 to accept the risk
(exit 23)
$ LORE_ALLOW_NETWORK_FS=1 lore rule list
(no rules)
```
<!-- /example -->

A pointer file whose `db_path` climbs out with `..`, refused and then let through:

<!-- example: environment#db-path-outside -->
```console
# alice, in blog
$ cat .lore/lore.yaml
db_path: ../client.db
project_id: prj_d431…313a
$ lore migrate run --dry-run
ERROR: [E_INTERNAL] internal error
(exit 1)
$ LORE_ALLOW_DB_PATH_OUTSIDE=1 lore migrate run
count: 2
rows: 
steps: mode-b: repos/rep_b4f4…a5b0 → store shared (~/work/stores/blog), mode-b: rules/rul_28f8…03df → store shared (~/work/stores/blog), mode-b: .lore/stores.yaml: add store shared file://~/work/stores/blog, mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
journal: team lock: this repo has no remote origin; nobody to race, none taken, backup: refs/lore-backup/migrate/2026-11-02 (local only, never pushed), undo: lore migrate rollback, team lock released
backup_ref: refs/lore-backup/migrate/2026-11-02
$ lore rule list
rul_28f8…03df  Posts go out on Tuesdays  [shared]
```
<!-- /example -->

## Pitfalls

- **The check looks at shapes, not meaning.** A password written as plain words ("the password is blue-horse-42") does not match any pattern. Never rely on lore as your only guard.
- **Rows confirmed with `--yes` live in your backups too.** `lore backup` copies the whole database. Do not put `.lore/backups/` in a shared folder.
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
