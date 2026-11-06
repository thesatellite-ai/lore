# Error codes and exit statuses

**In short:** when a lore command fails, it prints a short code in square brackets, such as `[E_READ_ONLY]`, then what went wrong and a hint line with what to do next. The code never changes between versions, so scripts can check it. A few codes also have their own exit status (the number a program returns when it ends), so an agent can tell "nothing to do" from "someone else got there first" without reading the text.

## How an error looks

The first line starts with `ERROR:` and the code. The line under it, starting with `hint:`, says what to do. Both go to the error output (stderr), and the command ends with a non-zero exit status:

<!-- example: reference#an-error -->
```console
# alice, in ~
$ lore rule list
ERROR: [E_NOT_A_STORE] git: the folder is not inside a git work tree
(exit 19)
```
<!-- /example -->

To see the exit status yourself, print `$?` right after the command. Here a task would wait on itself through another task, which lore refuses with `E_CYCLE` and exit status 19:

<!-- example: reference#exit-status only=3 -->
```console
# alice, in website
$ lore task edit tsk_7a67…9504 --after tsk_2ad3…f891; echo "exit status: $?"
ERROR: [E_CYCLE] dependency cycle: tsk_7a67…9504 → tsk_2ad3…f891 → tsk_7a67…9504
  hint: drop one --after edge; a task may not wait on itself, even through others
exit status: 19
```
<!-- /example -->

The [full list](#the-full-list) below links every code with its exit status.

## Exit statuses

<!-- ds:block id=team-exit-codes-vt2j5m98 -->
| Exit status | Meaning |
|---|---|
| 0 | the command worked |
| 1 | something unexpected failed: `E_INTERNAL` |
| 10 | `E_NO_WORK`: `lore task next` found nothing ready to do |
| 11 | `E_CONTENDED`: there was work, but another agent claimed it first every time; wait a moment and try again |
| 12 | `E_LOST_CLAIM`: your claim on the task ran out or was taken back; stop working on it |
| 13 | `E_CANCELLED`: a person cancelled the task while you held it; stop |
| 14 | `E_REASSIGNED`: a person gave the task to someone else while you held it; stop |
| 15 | `E_OFFLINE`, `E_STORE_UNREACHABLE`, `E_SECRET_UNAVAILABLE`: something that must be reached at once could not be; nothing was queued |
| 16 | `E_AMBIGUOUS_ID`: the id exists in two places; name one with `--from` |
| 17 | `E_ORG_AMBIGUOUS` or `E_ORG_DISAGREES`: lore cannot tell which org this repo belongs to |
| 18 | `E_LINK_UNKNOWN`: lore cannot tell whether this is a client repo |
| 19 | a guard refused on purpose (`E_NOT_A_STORE`, `E_LEAK_BLOCKED`, `E_CYCLE`, `E_OLD_LORE`, …); read the message |
| 20 | bad input: a value, flag combination or config lore cannot use, including a mistyped flag or command (`E_INVALID_INPUT`, `E_CONFIG_INVALID`, …) |
| 21 | a name matches more than one definition (`E_META_AMBIGUOUS`, `E_RESOURCE_AMBIGUOUS`) |
| 22 | a kind or key definition is missing or out of date (`E_KIND_UNKNOWN`, `E_META_STALE`, …) |
| 23 | lore refused in this state (`E_READ_ONLY`, `E_DB_CORRUPT`, `E_HAS_DEPENDENTS`, …) |
| 24 | a migration is locked or would lose data (`E_MIGRATE_LOCKED`, `E_MIGRATE_ROLLBACK_LOSES`) |
| 25 | an upgrade problem (`E_SCHEMA_OLD`, `E_UPGRADE_PINNED`, …) |
| 26 | `E_NEEDS_CONFIRM`: ask the person, then run again with `--yes` (or `--confirm` where the command says so); for a secret warning, with one of the choices it names ([below](#when-a-write-looks-like-it-holds-a-secret)) |
| 27 | `E_NOT_FOUND`: no row, key or project with that name or id |

These numbers are a promise: they are never renumbered, only added to. Two commands have their own statuses on top: `lore doctor` exits 0 when healthy, 1 when something needs attention and 2 when something is broken, and lore's git merge helper exits 1 when it leaves a conflict for a person to merge.

## Every code

"Not raised yet" means the code is registered (so its name and number are fixed) but no command produces it in this version.

### Database and storage

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_DB_LOCKED` | 23 | another lore process held a store's database for too long | wait for the other command to finish, then run yours again |
| `E_DB_CORRUPT` | 23 | the local cache holds a record lore cannot read (a cache file that cannot be opened at all is moved aside and rebuilt by itself) | delete `.lore/lore-cache.db`: the next command rebuilds it from `.lore/data` ([how](../operations/backup-restore-repair.md)) |
| `E_DB_NOT_FOUND` | 23 | no database at the configured path. Not raised yet | check `--db` / `LORE_DB`, or run `lore init` |
| `E_SCHEMA_VERSION_MISMATCH` | 23 | a newer lore changed this database's layout. Not raised yet | upgrade lore |
| `E_MIGRATION_INCOMPLETE` | 23 | an upgrade of the database layout did not finish; `lore doctor` reports it | run `lore setup` (safe to repeat); for the repo cache, delete `.lore/lore-cache.db` and the next command rebuilds it |
| `E_NETWORK_FS` | 23 | the database sits on a network or cloud-synced drive, where it can break silently | move the project to a local folder (git still shares the notes), or set `LORE_ALLOW_NETWORK_FS=1` |
| `E_INODE_MISMATCH` | 23 | the database file was replaced while lore had it open. Not raised yet | check what else touches the file |
| `E_DISK_FULL` | 23 | the disk is full; nothing was changed | free some space and run the command again |
| `E_READ_ONLY_FS` | 23 | the database is on a drive that cannot be written. Not raised yet | move the project to a writable folder |

### Project and identity

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_NOT_PROJECT_ROOT` | 23 | this folder has no `.lore/lore.db` and no `.lore/lore.yaml`; lore never looks in parent folders | `cd` into the project's top folder, or run `lore init` |
| `E_AMBIGUOUS_PROJECT` | 23 | `.lore/` holds both a database and a pointer file, so lore cannot tell which to use | delete the one you do not want |
| `E_PROJECT_NOT_FOUND` | 27 | no project with that id in this database | check `--project` / `LORE_PROJECT_ID`; `lore project list` shows them |
| `E_PROJECT_NAME_COLLISION` | 23 | several projects share that name | give the project id instead of the name |
| `E_REPO_NOT_FOUND` | 27 | no repo with that name or id in this project | `lore repo list` shows them |
| `E_MOUNT_NAME_TAKEN` | 23 | another repo of this project already uses that name | pick another name |
| `E_IDENTITY_FAILED` | 23 | lore could not work out who you are, for example an agent that is not on the roster | start the agent with `lore agent start --label <name>`, or set your org identity |
| `E_BAD_PATH` | 20 | a path has `..` or control characters in it | give a plain path |
| `E_ALREADY_INITIALIZED` | 23 | this folder already has a `.lore/` folder; lore will not overwrite it | use the existing project |

### Input checks

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_INVALID_ID` | 20 | an id does not have the right shape. Not raised yet | copy the id from `list` output |
| `E_INVALID_INPUT` | 20 | a flag or value is not allowed (the most common error) | read the message; it names the value |
| `E_EMPTY_BODY` | 20 | the text is empty after trimming spaces | give some text |
| `E_INVALID_UTF8` | 1 | the text is not valid UTF-8. Not raised yet | save the text as UTF-8 |
| `E_BODY_TOO_LARGE` | 20 | the text is too long. Not raised yet | shorten it |
| `E_INVALID_IDENTIFIER` | 20 | a project or repo name has a character that is not allowed | use letters, digits, `-` and `_` |

### Security

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_SECRET_DETECTED` | 23 | the text looks like it holds a password or key, so lore will not store it | remove the secret; in 0.1.18 a write asks first instead (`E_NEEDS_CONFIRM`, [below](#when-a-write-looks-like-it-holds-a-secret)); a bare `--yes` is not enough: say `--not-a-secret --reason "<why>" --yes`, `--secret-ref` or `--secret-unstored` |
| `E_ROOT_REFUSED` | 23 | lore will not run as the root user | run as your normal user, or set `LORE_ALLOW_ROOT=1` |
| `E_SYMLINK_DB` | 23 | `.lore/lore.db` is a symbolic link (a shortcut to another file) | delete the link; the next lore command rebuilds the database from `.lore/data` |
| `E_SYMLINK_LOOP` | 23 | a folder walk went round in a loop of links. Not raised yet | remove the looping link |
| `E_UID_MISMATCH` | 23 | lore runs as a different user than the owner of your home folder. Not raised yet | run without `sudo` |

### Locks and a busy database

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_LOCK_HELD` | 23 | another lore process holds the project lock. Not raised yet | wait and run the command again |
| `E_READ_ONLY` | 23 | the command wants to write, but read-only mode is on | drop `--read-only` and unset `LORE_READ_ONLY` |
| `E_BUSY_TIMEOUT` | 23 | the database stayed busy too long. Not raised yet | wait and run the command again |

### Audit and sync

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_AUDIT_CHAIN_BROKEN` | 23 | `lore audit verify` found an audit entry that was edited or deleted | `lore audit log` shows the entries; `lore <kind> show <id>` the rows named |
| `E_SYNC_DATA_IGNORED` | 23 | `lore sync` found row files under `.lore/data` that git still ignores, so teammates would never get those notes; nothing was pushed | remove the line that ignores them from `.gitignore` |

lore adds a line to `.gitignore` that takes `.lore/data` back in when it can. When a rule it cannot override, such as a `.gitignore` inside `.lore/data`, still hides row files, `lore sync` stops with `E_SYNC_DATA_IGNORED` (exit 23) and pushes nothing.

### General

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_NOT_FOUND` | 27 | no row with that id | check the id; it may have landed on main while your branch is older |
| `E_INTERNAL` | 1 | something failed inside lore | run `lore support-bundle` and file a bug report with it |
| `E_NOT_IMPLEMENTED` | 23 | this option exists but this build cannot do it yet | read the message; it says what to use instead |
| `E_UNSUPPORTED` | 23 | the command does not work here, for example outside a git repo | run it inside a git repo |

### Teams and agents

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_NO_WORK` | 10 | `lore task next` found no ready task; the message says why each one is blocked | wait, or unblock a task |
| `E_CONTENDED` | 11 | ready work exists, but every claim lost the race to another agent | wait a moment and try again |
| `E_LOST_CLAIM` | 12 | your claim is no longer on the server (it ran out or was taken back) | stop working on this task |
| `E_CANCELLED` | 13 | a person cancelled the task while you held it | stop working on this task |
| `E_REASSIGNED` | 14 | a person reassigned the task while you held it | stop working on this task |
| `E_OFFLINE` | 15 | a coordination write could not be confirmed by the server; nothing was queued | check the network and try again |
| `E_AMBIGUOUS_ID` | 16 | the id exists in two loaded places. Not raised yet | name one with `--from` |
| `E_ORG_AMBIGUOUS` | 17 | two org entries match this repo equally well | choose one with `lore org use <entry>` |
| `E_ORG_DISAGREES` | 17 | `LORE_ORG` or `lore org use` names another org than the store's link for this repo | fix the choice, or pass `--force-org` |
| `E_LINK_UNKNOWN` | 18 | the store cannot be reached and no saved link list exists, so lore cannot tell whether this is a client repo | pass `--to`, or try again online (`lore store doctor`) |
| `E_NOT_A_STORE` | 19 | a repo under `stores:` has no `lore-store.yaml`, or holds files that are not lore notes | make a store with `lore store init` in a new, empty repo |
| `E_LEAK_BLOCKED` | 19 | the write would put internal notes where the client can read them | see `lore check leaks --explain` |
| `E_CYCLE` | 19 | the `--after` link would make tasks wait on each other in a circle | drop one `--after` link |
| `E_OLD_LORE` | 19 | this lore is older than the live rows here allow; reading still works | upgrade lore |
| `E_CONFIG_INVALID` | 20 | `~/.lore/config.yaml` has a mistake | run `lore config check`; it lists every problem |

Two of them for real: `lore task next` in a repo with no open task, and a write while `LORE_ORG` names another org than the repo's store link:

<!-- example: reference#no-work -->
```console
# alice, in landing
$ lore task next
ERROR: [E_NO_WORK] nothing ready: no open task matches
(exit 10)
```
<!-- /example -->

<!-- example: reference#force-org only=1 -->
```console
# alice, in website
$ LORE_ORG=freelance lore memory add --body 'The client pays invoices in 30 days'
ERROR: [E_ORG_DISAGREES] LORE_ORG chose freelance, but this repo is linked in agency
  hint: name the org the write goes to with --to <org>, or fix the choice with `lore org use` (or unset LORE_ORG)
(exit 17)
```
<!-- /example -->

## When a write looks like it holds a secret

With `secrets.on_detect: ask` (the default), a write whose text looks like it holds a password or key is not stored. lore answers `E_NEEDS_CONFIRM` (exit 26) and lists every match, one finding per matched value. Run your own command again with one of three choices:

| Add to your command | What lore stores |
|---|---|
| `--secret-ref "<manager>://<entry>"` | a reference to where the value really lives, instead of the value |
| `--secret-unstored` | a `${secret:unstored}` placeholder, instead of the value |
| `--not-a-secret --reason "<why>" --yes` | the text as it is; the finding and your reason are saved as an allow entry (`lore secrets allow list`), so it is not reported again |

A bare `--yes` never stores a detected secret: it answers `E_NEEDS_CONFIRM` again. On a terminal lore asks the same thing as a menu, one question per finding.

With `--json`, the error carries the findings and the three choices as argument lists, so an agent can ask the person and then add the chosen flags to its own command. `findings[]` shows where each match is and which rule matched, never the value; `next.add_args_one_of` holds only the flags to add (there is no `command` or `args` key, because lore never repeats your text). The real output shows the first few characters of a match in `masked`; here they are replaced with `<redacted>`, and the JSON writes `<` and `>` as `\u003c` and `\u003e`:

```json
{
  "error": {
    "code": "E_NEEDS_CONFIRM",
    "message": "engine: body looks like a secret (aws_access_key) at line 1: <redacted>: redact it with a reference, mark it unstored, mark it not a secret, or cancel?",
    "hint": "re-run your own command with one of: --secret-ref \"<manager>://<entry>\" | --secret-unstored | --not-a-secret --reason \"<why>\" --yes",
    "exit": 26,
    "class": "needs_confirmation",
    "retryable": true,
    "needs_person": true,
    "next": {
      "add_args_one_of": [
        ["--secret-ref", "<manager>://<entry>"],
        ["--secret-unstored"],
        ["--not-a-secret", "--reason", "<why>", "--yes"]
      ]
    },
    "question": "body looks like a secret (aws_access_key) at line 1: <redacted>: redact it with a reference, mark it unstored, mark it not a secret, or cancel?",
    "findings": [
      {"n": 1, "rule": "aws_access_key", "field": "body", "line": 1, "masked": "<redacted>"}
    ]
  }
}
```

`lore lint` reports the same kind of match in rows already stored as a `secret_found` finding.

## The full list

Every code with its exit status, class and HTTP status is in the generated [command and error reference](cli-commands.md#errors), written by `lore spec docs` from the code, so it always matches the program.

## Pitfalls

- Match on the code, never on the message text. The message wording may change between versions; the code does not.
- With `--json`, an error prints as one JSON object with the code, exit status and class. A mistyped flag or command is `E_INVALID_INPUT` with exit status 20:

<!-- example: reference#errors-with-json -->
```console
# alice, in ~
$ lore rule list --json
{
  "error": {
    "code": "E_NOT_A_STORE",
    "message": "git: the folder is not inside a git work tree",
    "exit": 19,
    "class": "guard",
    "retryable": false,
    "needs_person": false
  }
}
(exit 19)

# alice, in website
$ lore rule list --colour never
ERROR: [E_INVALID_INPUT] unknown flag: --colour
(exit 20)
```
<!-- /example -->

- `lore doctor` exits 1 for "needs attention" without printing an `E_` code. Check its exit status, or use `lore doctor --json`.

## Related

- [Environment variables](environment.md): `LORE_READ_ONLY`, `LORE_ALLOW_ROOT`, `LORE_ALLOW_NETWORK_FS` and the others named above.
- [Config files](config.md): what `lore config check` refuses.
- [Team troubleshooting](../team/troubleshooting.md): longer fixes for the team codes.
- [Command index](commands.md).
