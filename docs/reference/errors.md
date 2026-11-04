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

`lore errors list` prints every code with its one-line description, and `lore errors list --json` prints the same list for scripts:

<!-- example: reference#errors-list -->
```console
# alice, in website
$ lore errors list | head -4
E_DB_LOCKED                      another lore process is holding the DB write lock
E_DB_CORRUPT                     DB file failed quick_check; run `lore repair`
E_DB_NOT_FOUND                   DB file does not exist at the configured path
E_SCHEMA_VERSION_MISMATCH        DB was migrated by a newer lore; upgrade or use a different DB
$ lore errors list --json | head -9
{
  "schema_version": 1,
  "count": 53,
  "errors": [
    {
      "code": "E_DB_LOCKED",
      "description": "another lore process is holding the DB write lock"
    },
    {
```
<!-- /example -->

## Exit statuses

<!-- ds:block id=team-exit-codes-vt2j5m98 -->
| Exit status | Meaning |
|---|---|
| 0 | the command worked |
| 1 | it failed; every code without its own number below, and errors that carry no code (for example a mistyped flag) |
| 10 | `E_NO_WORK`: `lore task next` found nothing ready to do |
| 11 | `E_CONTENDED`: there was work, but another agent claimed it first every time; wait a moment and try again |
| 12 | `E_LOST_CLAIM`: your claim on the task ran out or was taken back; stop working on it |
| 13 | `E_CANCELLED`: a person cancelled the task while you held it; stop |
| 14 | `E_REASSIGNED`: a person gave the task to someone else while you held it; stop |
| 15 | `E_OFFLINE`: a write that must reach the server at once (claim, renew, release, done, approve, hold) could not be confirmed; nothing was queued |
| 16 | `E_AMBIGUOUS_ID`: the id exists in two places; name one with `--from` |
| 17 | `E_ORG_AMBIGUOUS` or `E_ORG_DISAGREES`: lore cannot tell which org this repo belongs to |
| 18 | `E_LINK_UNKNOWN`: lore cannot tell whether this is a client repo |
| 19 | `E_NOT_A_STORE`, `E_LEAK_BLOCKED`, `E_CYCLE` or `E_OLD_LORE`: lore refused on purpose; read the message |

These numbers are a promise: they are never renumbered, only added to. Two commands have their own statuses on top: `lore doctor` exits 0 when healthy, 1 when something needs attention and 2 when something is broken, and lore's git merge helper exits 1 when it leaves a conflict for a person to merge.

## Every code

"Not raised yet" means the code is registered (so its name and number are fixed) but no command produces it in this version.

### Database and storage

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_DB_LOCKED` | 1 | another lore process held a store's database for too long | wait for the other command to finish, then run yours again |
| `E_DB_CORRUPT` | 1 | the database file failed its health check | run `lore repair`, or `lore restore` from a backup |
| `E_DB_NOT_FOUND` | 1 | no database at the configured path. Not raised yet | check `--db` / `LORE_DB`, or run `lore init` |
| `E_SCHEMA_VERSION_MISMATCH` | 1 | a newer lore changed this database's layout. Not raised yet | upgrade lore |
| `E_MIGRATION_INCOMPLETE` | 1 | an upgrade of the database layout did not finish; `lore doctor` reports it | run `lore setup`, or `lore repair --tier=2` to go back to a backup |
| `E_NETWORK_FS` | 1 | the database sits on a network or cloud-synced drive, where it can break silently | move the project to a local folder (git still shares the notes), or set `LORE_ALLOW_NETWORK_FS=1` |
| `E_INODE_MISMATCH` | 1 | the database file was replaced while lore had it open. Not raised yet | check what else touches the file |
| `E_DISK_FULL` | 1 | the disk is full; nothing was changed | free some space and run the command again |
| `E_READ_ONLY_FS` | 1 | the database is on a drive that cannot be written. Not raised yet | move the project to a writable folder |

### Project and identity

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_NOT_PROJECT_ROOT` | 1 | this folder has no `.lore/lore.db` and no `.lore/lore.yaml`; lore never looks in parent folders | `cd` into the project's top folder, or run `lore init` |
| `E_AMBIGUOUS_PROJECT` | 1 | `.lore/` holds both a database and a pointer file, so lore cannot tell which to use | delete the one you do not want |
| `E_PROJECT_NOT_FOUND` | 1 | no project with that id in this database | check `--project` / `LORE_PROJECT_ID`; `lore project list` shows them |
| `E_PROJECT_NAME_COLLISION` | 1 | several projects share that name | give the project id instead of the name |
| `E_REPO_NOT_FOUND` | 1 | no repo with that name or id in this project | `lore repo list` shows them |
| `E_MOUNT_NAME_TAKEN` | 1 | another repo of this project already uses that name | pick another name |
| `E_IDENTITY_FAILED` | 1 | lore could not work out who you are, for example an agent that is not on the roster | start the agent with `lore agent start --label <name>`, or set your org identity |
| `E_BAD_PATH` | 1 | a path has `..` or control characters in it | give a plain path |
| `E_ALREADY_INITIALIZED` | 1 | this folder already has a `.lore/` folder; lore will not overwrite it | use the existing project |

### Input checks

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_INVALID_ID` | 1 | an id does not have the right shape. Not raised yet | copy the id from `list` output |
| `E_INVALID_INPUT` | 1 | a flag or value is not allowed (the most common error) | read the message; it names the value |
| `E_EMPTY_BODY` | 1 | the text is empty after trimming spaces | give some text |
| `E_INVALID_UTF8` | 1 | the text is not valid UTF-8. Not raised yet | save the text as UTF-8 |
| `E_BODY_TOO_LARGE` | 1 | the text is too long. Not raised yet | shorten it |
| `E_INVALID_IDENTIFIER` | 1 | a project or repo name has a character that is not allowed | use letters, digits, `-` and `_` |

### Security

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_SECRET_DETECTED` | 1 | the text looks like it holds a password or key, so lore will not store it | remove the secret; in 0.1.18 a write asks first instead (`E_NEEDS_CONFIRM`), and `--yes` stores it as not a secret when you are sure |
| `E_ROOT_REFUSED` | 1 | lore will not run as the root user | run as your normal user, or set `LORE_ALLOW_ROOT=1` |
| `E_SYMLINK_DB` | 1 | `.lore/lore.db` is a symbolic link (a shortcut to another file) | delete the link and run `lore init` again, or restore from a backup |
| `E_SYMLINK_LOOP` | 1 | a folder walk went round in a loop of links. Not raised yet | remove the looping link |
| `E_UID_MISMATCH` | 1 | lore runs as a different user than the owner of your home folder. Not raised yet | run without `sudo` |

### Locks and a busy database

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_LOCK_HELD` | 1 | another lore process holds the project lock. Not raised yet | wait and run the command again |
| `E_READ_ONLY` | 1 | the command wants to write, but read-only mode is on | drop `--read-only` and unset `LORE_READ_ONLY` |
| `E_BUSY_TIMEOUT` | 1 | the database stayed busy too long. Not raised yet | wait and run the command again |

### Audit and sync

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_AUDIT_CHAIN_BROKEN` | 1 | `lore audit verify` found an audit entry that was edited or deleted | restore `lore.db` from a backup to look into it |
| `E_SYNC_DATA_IGNORED` | 1 | git ignores `.lore/data`, so teammates would never get your notes | remove the line that ignores it from `.gitignore` |

lore adds a line to `.gitignore` that takes `.lore/data` back in when it can. A rule it cannot override, such as a `.gitignore` inside `.lore/data`, stops it with `E_SYNC_DATA_IGNORED`:

<!-- example: reference#sync-data-ignored only=2 -->
```console
# alice, in website
$ lore sync install-git
lore sync: git ignores lore data, so teammates will not receive it:
  .lore/data/tasks/sample.json (ignored by .lore/data/tasks/.gitignore:1: *.json)
change that rule so .lore/data/ is not excluded (a negation cannot re-include files inside an ignored folder)
ERROR: [E_SYNC_DATA_IGNORED] lore sync: git ignores lore data, so teammates will not receive it:
  .lore/data/tasks/sample.json (ignored by .lore/data/tasks/.gitignore:1: *.json)
change that rule so .lore/data/ is not excluded (a negation cannot re-include files inside an ignored folder)
(exit 1)
```
<!-- /example -->

### General

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_NOT_FOUND` | 1 | no row with that id | check the id; it may have landed on main while your branch is older |
| `E_INTERNAL` | 1 | something failed inside lore | run `lore support-bundle` and file a bug report with it |
| `E_NOT_IMPLEMENTED` | 1 | this option exists but this build cannot do it yet | read the message; it says what to use instead |
| `E_UNSUPPORTED` | 1 | the command does not work here, for example outside a git repo | run it inside a git repo |

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
| `E_CONFIG_INVALID` | 1 | `~/.lore/config.yaml` has a mistake | run `lore config check`; it lists every problem |

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
  hint: pass --force-org to write anyway, or fix the choice with `lore org use`
(exit 17)
```
<!-- /example -->

## The full list, as lore prints it

This table is made from `lore errors list` and the exit table in the code, so it always matches the program.

<!-- errors -->
| Code | Exit status | Meaning |
|---|---|---|
| `E_DB_LOCKED` | 1 | another lore process is holding the DB write lock |
| `E_DB_CORRUPT` | 1 | DB file failed quick_check; run `lore repair` |
| `E_DB_NOT_FOUND` | 1 | DB file does not exist at the configured path |
| `E_SCHEMA_VERSION_MISMATCH` | 1 | DB was migrated by a newer lore; upgrade or use a different DB |
| `E_MIGRATION_INCOMPLETE` | 1 | a schema migration did not finish; run `lore setup` |
| `E_NETWORK_FS` | 1 | DB path or store is on a network/cloud-sync filesystem (silent corruption risk); set LORE_ALLOW_NETWORK_FS=1 to override |
| `E_INODE_MISMATCH` | 1 | DB file inode changed since last open; verify external mutation |
| `E_DISK_FULL` | 1 | no space left on device for write or backup |
| `E_READ_ONLY_FS` | 1 | DB path is on a read-only filesystem |
| `E_NOT_PROJECT_ROOT` | 1 | current directory is not a lore project (no .lore/lore.db or lore.yaml) |
| `E_AMBIGUOUS_PROJECT` | 1 | both .lore/lore.db AND a .lore/lore.yaml (or lore.toml) pointer present (Mode A vs Mode B ambiguity) |
| `E_PROJECT_NOT_FOUND` | 1 | project_id not registered in this DB |
| `E_PROJECT_NAME_COLLISION` | 1 | multiple projects share that name; specify by opaque ID |
| `E_REPO_NOT_FOUND` | 1 | repo mount_name or rep_id not found in current project |
| `E_MOUNT_NAME_TAKEN` | 1 | another repo in this project already uses that mount_name |
| `E_IDENTITY_FAILED` | 1 | could not resolve actor identity; falling back to ephemeral |
| `E_BAD_PATH` | 1 | path contains traversal sequence or control characters |
| `E_ALREADY_INITIALIZED` | 1 | this directory already contains a .lore/ — refusing to overwrite |
| `E_INVALID_ID` | 1 | id failed format validation (expected <prefix>_<32-hex-uuidv7>) |
| `E_INVALID_INPUT` | 1 | input value failed validation |
| `E_EMPTY_BODY` | 1 | body is empty after normalization |
| `E_INVALID_UTF8` | 1 | input is not valid UTF-8 |
| `E_BODY_TOO_LARGE` | 1 | body exceeds maximum allowed size |
| `E_INVALID_IDENTIFIER` | 1 | identifier contains disallowed character |
| `E_SECRET_DETECTED` | 1 | input contains a credential pattern; refusing to store. When secrets.on_detect is ask, rerun with --yes once a person confirms it is not a secret (recorded in secret_allows) |
| `E_ROOT_REFUSED` | 1 | lore refuses to run as root; set LORE_ALLOW_ROOT=1 to override |
| `E_SYMLINK_DB` | 1 | DB path is a symlink; refusing for safety. Delete the symlink and re-init, or restore lore.db from a backup |
| `E_SYMLINK_LOOP` | 1 | filesystem walk encountered a symlink loop |
| `E_UID_MISMATCH` | 1 | EUID does not match HOME owner (sudo PRESERVE_ENV detected); refusing |
| `E_LOCK_HELD` | 1 | another lore process is holding the project flock |
| `E_READ_ONLY` | 1 | command requires write access but read-only mode is active |
| `E_BUSY_TIMEOUT` | 1 | DB busy timeout exceeded |
| `E_AUDIT_CHAIN_BROKEN` | 1 | audit log hash chain integrity verification failed |
| `E_SYNC_DATA_IGNORED` | 1 | git ignores files under .lore/data, so teammates would not receive them; re-include the folder in .gitignore |
| `E_NOT_FOUND` | 1 | requested entity does not exist |
| `E_INTERNAL` | 1 | internal error; please file a bug report with `lore support-bundle` |
| `E_NOT_IMPLEMENTED` | 1 | feature not yet implemented (deferred to v0.2 or v1.0+) |
| `E_UNSUPPORTED` | 1 | operation not supported in current mode |
| `E_NO_WORK` | 10 | `lore task next` found no ready task; the message lists why each candidate is blocked |
| `E_CONTENDED` | 11 | ready work exists but every claim attempt lost a race; retry after a short wait |
| `E_LOST_CLAIM` | 12 | your claim token is no longer on the signals ref (lease expired or reclaimed); stop work on this task |
| `E_CANCELLED` | 13 | a human cancelled the task while you held it; stop work on this task |
| `E_REASSIGNED` | 14 | a human reassigned the task while you held it; stop work on this task |
| `E_OFFLINE` | 15 | a coordination write (claim, renew, release, done, approve, hold) could not be confirmed by the remote; nothing was queued |
| `E_AMBIGUOUS_ID` | 16 | the id exists in two loaded sources; name one with --from |
| `E_ORG_AMBIGUOUS` | 17 | two org entries match this repo at the same rule; choose one with `lore org use <entry>` |
| `E_ORG_DISAGREES` | 17 | LORE_ORG or `lore org use` names a different org than the store link; pass --force-org to override |
| `E_LINK_UNKNOWN` | 18 | lore cannot tell whether this repo is a client repo (store unreachable, no cached link index); pass --to or retry online |
| `E_NOT_A_STORE` | 19 | the repo is not a lore store (no lore-store.yaml marker, or it holds non-lore files) |
| `E_LEAK_BLOCKED` | 19 | the write would put internal content where the client can read it; see `lore check leaks --explain` |
| `E_CYCLE` | 19 | the dependency would create a cycle between tasks |
| `E_OLD_LORE` | 19 | this lore binary is below the live ref's min_lore_version; upgrade lore (reads still work) |
| `E_CONFIG_INVALID` | 1 | ~/.lore/config.yaml failed validation; run `lore config check` |
<!-- /errors -->

## Pitfalls

- Match on the code, never on the message text. The message wording may change between versions; the code does not.
- `--json` changes a command's normal output, not its errors: errors still print as text on stderr. A mistyped flag exits 1 with `ERROR: unknown flag` and no code:

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
Error: unknown flag: --colour
Run 'lore rule list --help' for usage.
(exit 1)
```
<!-- /example -->

- `lore doctor` exits 1 for "needs attention" without printing an `E_` code. Check its exit status, or use `lore doctor --json`.

## Related

- [Environment variables](environment.md): `LORE_READ_ONLY`, `LORE_ALLOW_ROOT`, `LORE_ALLOW_NETWORK_FS` and the others named above.
- [Config files](config.md): what `lore config check` refuses.
- [Team troubleshooting](../team/troubleshooting.md): longer fixes for the team codes.
- [Command index](commands.md).
