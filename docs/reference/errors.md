# Error codes and exit statuses

**In short:** when a lore command fails, it prints a short code in square brackets, such as `[E_READ_ONLY]`, then what went wrong and a hint line with what to do next. The code never changes between versions, so scripts can check it. A few codes also have their own exit status (the number a program returns when it ends), so an agent can tell "nothing to do" from "someone else got there first" without reading the text.

## How an error looks

The first line starts with `ERROR:` and the code. The line under it, starting with `hint:`, says what to do. Both go to the error output (stderr), and the command ends with a non-zero exit status:

<!-- example: reference#an-error -->
```console
# alice, in ~
$ lore rule list
ERROR: [E_NOT_A_STORE] git: the folder is not inside a git work tree
  hint: cd into a git checkout, or run git init here first
(exit 19)
```
<!-- /example -->

To see the exit status yourself, print `$?` right after the command. Here a task would wait on itself through another task, which lore refuses with `E_CYCLE` and exit status 19:

<!-- example: reference#exit-status only=3 -->
```console
# alice, in website
$ lore task edit --id tsk_2ad3…f891 --after tsk_162d…9666; echo "exit status: $?"
ERROR: [E_CYCLE] dependency cycle: tsk_2ad3…f891 → tsk_162d…9666 → tsk_2ad3…f891
  hint: drop one --after edge; a task may not wait on itself, even through others
exit status: 19
```
<!-- /example -->

The [full list](#the-full-list) below links every code with its exit status.

## Exit statuses

<!-- ds:block id=team-exit-codes-eqpyx54h -->
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
| 24 | a migration is locked or would lose data, or the project is on an older format (`E_MIGRATE_LOCKED`, `E_MIGRATE_ROLLBACK_LOSES`, `E_FORMAT_OLD`) |
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
| `E_DB_CORRUPT` | 23 | the local cache holds a record lore cannot read (a cache file that cannot be opened at all is moved aside and rebuilt by itself) | delete the repo's cache file (`~/.lore/cache/repo/<repo folder>-<hash>.db`; `lore where` names it): the next command rebuilds it from `.lore/data` ([how](../operations/backup-restore-repair.md)) |
| `E_DB_NOT_FOUND` | 23 | a store's cache file could not be opened for a search across stores | run the command again (lore rebuilds a cache from the row files); `lore doctor` checks the caches |
| `E_SCHEMA_VERSION_MISMATCH` | 23 | a newer lore changed this database's layout. Not raised yet | upgrade lore |
| `E_MIGRATION_INCOMPLETE` | 23 | a `lore migrate run` of v0.1.17 data did not finish; `lore doctor` reports it | run `lore migrate run` again (it resumes where it stopped), or `lore migrate rollback` to undo it; for the repo cache, delete its file under `~/.lore/cache/repo/` (`lore where` names it) and the next command rebuilds it |
| `E_NETWORK_FS` | 23 | the database sits on a network or cloud-synced drive, where it can break silently | move the project to a local folder (git still shares the notes), or set `LORE_ALLOW_NETWORK_FS=1` |
| `E_INODE_MISMATCH` | 23 | the database file was replaced while lore had it open. Not raised yet | check what else touches the file |
| `E_DISK_FULL` | 23 | the disk is full; nothing was changed | free some space or files on that disk (`df -h`, `df -i`) and run the command again |
| `E_READ_ONLY_FS` | 23 | the database is on a drive that cannot be written. Not raised yet | move the project to a writable folder |

### Project and identity

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_NOT_PROJECT_ROOT` | 23 | this folder has no lore project (no `.lore/data` and no `.lore/lore.yaml`) | `cd` into the repo, or run `lore init` |
| `E_AMBIGUOUS_PROJECT` | 23 | `.lore/` holds both a database and a pointer file, so lore cannot tell which to use | delete the one you do not want |
| `E_PROJECT_NOT_FOUND` | 27 | no project with that id in this database | check `--project` / `LORE_PROJECT_ID`; `lore project list` shows them |
| `E_PROJECT_NAME_COLLISION` | 23 | several projects share that name | give the project id instead of the name |
| `E_REPO_NOT_FOUND` | 27 | no repo with that name or id in this project | `lore repo list` shows them |
| `E_MOUNT_NAME_TAKEN` | 23 | another repo of this project already uses that name | pick another name |
| `E_IDENTITY_FAILED` | 23 | lore could not work out who you are, for example an agent that is not on the roster | start the agent with `lore agent start --label <name>`, or set your org identity |
| `E_BAD_PATH` | 20 | a path has `..` or control characters in it | give a plain path |
| `E_ALREADY_INITIALIZED` | 23 | this folder already has a `.lore/` folder; lore will not overwrite it | nothing to do: use the existing project; `lore doctor` checks it |

### Input checks

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_INVALID_ID` | 20 | an id does not have the right shape (a typo, an id cut short); lore never reports a malformed id as `E_NOT_FOUND` | copy the id from `list` output |
| `E_INVALID_INPUT` | 20 | a flag or value is not allowed (the most common error); also "the agent … has no person behind it" when an agent actor whose roster row names no owner changes the roster (a person's own coding agent acts for them and passes) | read the message; it names the value. For no person behind it: the person runs it, or `lore agent start --label <l> --owner @<person>` |
| `E_EMPTY_BODY` | 20 | the text is empty after trimming spaces | give some text |
| `E_INVALID_UTF8` | 1 | the text is not valid UTF-8 | save the text as UTF-8 |
| `E_BODY_TOO_LARGE` | 20 | a row file is larger than 4 MB | shorten the text, or keep the long part in a file and link to it |
| `E_INVALID_IDENTIFIER` | 20 | a project or repo name has a character that is not allowed | use letters, digits, `-` and `_` |

### Security

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_SECRET_DETECTED` | 23 | the text looks like it holds a password or key, so lore will not store it | remove the secret; in 0.1.18 a write asks first instead (`E_NEEDS_CONFIRM`, [below](#when-a-write-looks-like-it-holds-a-secret)); a bare `--yes` is not enough: say `--not-a-secret --reason "<why>" --yes`, `--secret-ref` or `--secret-unstored` |
| `E_ROOT_REFUSED` | 23 | lore will not run as the root user | run as your normal user, or set `LORE_ALLOW_ROOT=1` |
| `E_SYMLINK_DB` | 23 | a file lore writes is a symbolic link (a shortcut to another file): the repo cache under `~/.lore/cache/repo/` or its side files, the machine store or local state under `~/.lore`, the store lock `.lore/lock`, or an audit file | delete the link; the next lore command rebuilds the cache from `.lore/data` |
| `E_SYMLINK_LOOP` | 23 | a chain of symbolic links loops, or is longer than 40 links: a tool's skill folder (`lore skill status`, `lore skill install`) or a file render writes | fix or remove the link the message names |
| `E_UID_MISMATCH` | 23 | lore runs as a different user than the owner of your home folder. Not raised yet | run without `sudo` |

### Locks and a busy database

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_LOCK_HELD` | 23 | another lore process holds a lock lore needs (for example another `lore upgrade` still running) | wait and run the command again |
| `E_READ_ONLY` | 23 | the command wants to write, but read-only mode is on | drop `--read-only` and unset `LORE_READ_ONLY` |
| `E_BUSY_TIMEOUT` | 23 | other writers kept moving the store branch, or the database stayed busy too long | wait and run the command again |

### Audit and sync

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_AUDIT_CHAIN_BROKEN` | 23 | `lore audit verify` found an audit entry that was edited or deleted | `lore audit log` shows the entries; `lore <kind> show <id>` the rows named |
| `E_SYNC_DATA_IGNORED` | 23 | `lore sync` found row files under `.lore/data` that git still ignores, so teammates would never get those notes; nothing was pushed | remove the line that ignores them from `.gitignore` |
| `E_PUSH_AGAIN` | 23 | lore's pre-push hook added a commit to the branch you pushed (rows that finished in live land with your push), so this `git push` stopped once; nothing you typed was wrong | run the same `git push` again: it sends the branch with that commit (a script that pushes once retries on this code) |

lore adds a line to `.gitignore` that takes `.lore/data` back in when it can. When a rule it cannot override, such as a `.gitignore` inside `.lore/data`, still hides row files, `lore sync` stops with `E_SYNC_DATA_IGNORED` (exit 23) and pushes nothing.

### General

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_NOT_FOUND` | 27 | no row with that id | check the id; when your branch is behind main it may have landed there (the hint names `lore sync peek`); when your branch already has main as last fetched, the row may belong to another repo (run the command there) or be newer than that fetch (the same `lore sync peek`); a row a teammate wrote to a store stays on their machine until their `lore sync` and reaches yours with your `lore sync` (the hint says so). A task or gate command whose `--id` is the task has already fetched once and retried before answering this ([team troubleshooting](../team/troubleshooting.md#e_not_found-on-a-task-id-a-teammate-just-added)) |
| `E_INTERNAL` | 1 | something failed inside lore | run `lore support-bundle` and file a bug report with it |
| `E_NOT_IMPLEMENTED` | 23 | this option exists but this build cannot do it yet | read the message; it says what to use instead |
| `E_PORT_IN_USE` | 23 | `lore api` or the setup page cannot listen on the `--port` you gave: another program (or another `lore api`) holds it | pick another `--port`, or `--port 0` for a free one (lore prints the port it picked) |
| `E_UNSUPPORTED` | 23 | the command does not work here, for example outside a git repo, or `lore migrate rollback` after a shared-database conversion | run it inside a git repo; a shared-database conversion has no rollback (see [the shared-database guide](../sync/shared-database-mode-b.md#pitfalls)) |
| `E_REFUSED` | 23 | a fixed lore rule refuses this; the message names the rule. On coordination commands: an agent with no person behind it (an agent actor whose roster row names no owner) doing a human override such as cancel, reassign or revoke (the `--json` error has `"needs_person": true`; a person's own coding agent acts for them and passes), the WIP limit, a poisoned task, or a task that is no longer open | do what the message says instead; do not retry the same command |
| `E_NEEDS_CONFIRM` | 26 | the command asks first, and ran with no terminal and no `--yes`; the error carries the question | read the question, then run the command again with `--yes` |
| `E_POLICY_DENIED` | 23 | a team policy refuses this | read the message for the policy key; `lore policy show` lists them |
| `E_STALE` | 23 | the row changed between lore's read and its write, and the two changes could not be merged | look at the row again and repeat your change |
| `E_UNAUTHORIZED` | 28 | a `lore api` request has no valid token (HTTP 401) | send `Authorization: Bearer <the token in ~/.lore/api-token>`; after `lore api token rotate`, send the new token ([Local REST API](rest-api.md)) |
| `E_SQL_REFUSED` | 20 | `lore sql` takes one read-only `SELECT`, `WITH` or `VALUES` statement, and this was not one, or it named a table that is off limits | rewrite it as one read-only query |
| `E_SQL_TIMEOUT` | 23 | `lore sql` ran past `--timeout` | narrow the query, or raise `--timeout` |
| `E_NAME_TAKEN` | 23 | that name is already used by a live row in this place | pick another name, or edit the existing row |
| `E_REPO_URL` | 20 | lore cannot read that repo address | give a full address such as `github.com/agency/website` or a git url |
| `E_URL_CREDENTIALS` | 20 | a url holds a password or token in its login part | remove the login part; git reads it from your credential helper |

`lore sql` takes one read-only query and nothing else:

<!-- example: reference#sql-refused -->
```console
# alice, in website
$ lore sql --query 'DELETE FROM rules'
ERROR: [E_SQL_REFUSED] only one SELECT, WITH or VALUES statement (optionally after EXPLAIN [QUERY PLAN]) is allowed
  hint: lore sql is read-only; change data with lore commands so it reaches git
(exit 20)
```
<!-- /example -->

### Tasks

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_OPEN_CHILDREN` | 23 | done or cancel of a parent task that still has open sub-tasks | finish them first, or pass `--children` |
| `E_HAS_OPEN_CHILDREN` | 23 | a claim of a parent task that still has open sub-tasks | claim a sub-task, or pass `--coordinate` |
| `E_OPEN_ITEMS` | 23 | done of a task with unticked checklist items | tick them, or pass `--tick-all` (or `--force`) |
| `E_PARENT_CLOSED` | 23 | a sub-task added or reopened under a closed parent | pass `--reopen-parent`, or pick another parent |
| `E_PARENT_MISSION` | 23 | a sub-task's mission differs from its parent's | give it the parent's mission, or no parent |
| `E_PARENT_PROJECT` | 23 | the parent task is in another project | pick a parent in the same project |
| `E_TREE_DEPTH` | 23 | the sub-task tree would be deeper than `tasks.max_depth` | attach the task higher up |
| `E_TREE_WIDTH` | 23 | a parent would have more direct sub-tasks than `tasks.max_children` | group them under a new sub-task |
| `E_TEMPLATE` | 23 | a task template is invalid, or its arguments are missing or unknown | read the message; it names the bad or missing argument |
| `E_DATE` | 20 | a date lore does not understand, or one that could mean two days | give for example `2026-11-13`, `tomorrow`, `next mon` or `in 3 days` |
| `E_RECUR` | 20 | a repeat rule lore does not understand | give for example `every week`, `every month on last` or `every 3 days until 2027-03-31` |
| `E_DURATION_BAD` | 20 | a duration lore does not take (0, negative, or in days) | give for example `45m`, `1h30m` or `1.5h` |
| `E_TIME_TOO_LONG` | 20 | a manual time entry over `time.max_entry` (24h by default) | split it into one entry per day |

### Trash and delete

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_IN_TRASH` | 23 | an edit or delete of a row that is already in the trash | `lore trash restore --id <id>` first |
| `E_PURGED` | 23 | the row was removed for good; only a deletion marker is left | nothing to restore; write it again if you need it |
| `E_PARENT_DELETED` | 23 | a restore of a child whose parent is in the trash | restore the parent first |
| `E_PARENT_PURGED` | 23 | a restore of a child whose parent was removed for good | restore it under another parent, or not at all |
| `E_HAS_DEPENDENTS` | 23 | a delete refused because other live rows still point at this one | delete or repoint those rows first; the message lists them |
| `E_REASON_REQUIRED` | 20 | a delete without `--reason` while the policy `delete.require_reason` is on | add `--reason '<why>'` |

A project that still has rows cannot be deleted; the message names every row that points at it:

<!-- example: reference#has-dependents -->
```console
# alice, in website
$ lore project list
prj_d431…313a  website  https://github.com/client/website
$ lore project delete --id $(lore project list | cut -d' ' -f1) --yes
ERROR: [E_HAS_DEPENDENTS] project delete: mem_28f8…03df (memories.project_id), rul_54fe…28f2 (rules.project_id), tsk_2ad3…f891 (tasks.project_id), tsk_162d…9666 (tasks.project_id), tsk_05e9…4603 (tasks.project_id) … and 1 more (`lore project delete --json` lists all) still point at prj_d431…313a
  hint: delete or move the pointing rows first
(exit 23)
```
<!-- /example -->

### Stores

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_STORE_SCHEME` | 20 | a store url uses a scheme lore 0.1.18 has no backend for (`s3://`, `postgres://`, `lore://`); nothing was written | use a git url, the path of a bare git repository on this machine (`/srv/git/notes.git`), or `file:///<absolute path>` for a plain folder |
| `E_STORE_UNREACHABLE` | 15 | a store with no copy on this machine could not be reached | go online once; after that lore keeps a copy |
| `E_STORE_READ_ONLY` | 23 | a write to a store this repo attaches read-only | write to another place with `--to`, or attach the store without read-only |
| `E_STORE_LOCKED` | 23 | another lore process kept the store lock past `store.lock_timeout` | wait and run the command again |
| `E_STORE_BACKEND` | 23 | a store's settings and its backend do not fit. Not raised by the 0.1.18 core | check the store entry in `.lore/stores.yaml` |

`lore store set` and `lore store compact` (push modes and history compaction, [How writes reach the remote](../team/identity-and-stores.md#how-writes-reach-the-remote)) answer with these codes:

| Code | Exit | When | What to do |
|---|---|---|---|
| `E_REFUSED` | 23 | the store is the code repo (`repo`), a folder store (`file://`), or neither your personal store nor a store of one of your orgs; or it is an org store and this machine holds no admin setup of that org; or (compact) another copy of the store on this machine holds work not pushed yet | push the code repo with your code; move a folder store to a git url; ask the org admin; run `lore sync` first |
| `E_INVALID_INPUT` | 20 | an unknown key or value, `push_every` under `1m`, `compact_after` under `1d`, `compact=on` without `compact_after`, or a `--before` that is not an age | use `push=now\|batch\|manual`, `push_every=10m`, `compact=on compact_after=30d` |
| `E_NEEDS_CONFIRM` | 26 | `lore store compact` ran without a terminal and without `--yes` | read the question, then rerun with `--yes` |
| `E_CONTENDED` | 11 | someone pushed to the store while it was being compacted; nothing changed | run it again |
| `E_OFFLINE` | 15 | the store's remote could not be reached for the compaction; nothing changed | run it again when online |
| `E_NOT_A_STORE` | 19 | the url does not hold a lore store | check the url |

### Metadata

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_META_KEY` | 20 | a key or allowed-value name is not snake_case ASCII, or is too long or too deep | rename it, for example `client_tier` |
| `E_META_RESERVED` | 20 | a key name lore keeps for itself (a column, a type or level word, `id`, `meta`, or one starting with `_`, `mky` or `val`) | pick another name |
| `E_META_TYPE` | 20 | a value does not read as the key's type | give a value of that type; `lore meta show --id <key>` says which |
| `E_META_VALUE` | 20 | an empty value, or a value outside the key's allowed values | give one of the allowed values |
| `E_META_SHAPE` | 20 | a list of objects, a list of lists, or a mixed list on a structured key | flatten the value |
| `E_META_SIZE` | 20 | the row's metadata would pass `meta.max_bytes` | keep less metadata on the row |
| `E_META_DEPTH` | 20 | a key has more dot-separated parts than `meta.max_depth` | use a flatter name |
| `E_META_OP` | 20 | a mistake in a metadata flag or filter | read the message; it points at the part it could not read |
| `E_META_TABLE` | 20 | this kind of row takes no metadata from the command line | set it on a row kind that takes metadata |
| `E_META_REQUIRED` | 20 | a required metadata key is missing on add | add it with `--meta key=value` |
| `E_META_UNKNOWN` | 20 | an unset of a key the row does not have, or an unknown key id | check the key with `lore meta get --id <id>` |
| `E_META_AMBIGUOUS` | 21 | a bare key name matches definitions at two or more levels | name the level, as the message shows |
| `E_META_UNREGISTERED` | 22 | a write to a key whose definition is not visible here | register the key here with `lore meta add`, or write where it is defined |
| `E_META_STALE` | 22 | a new key would be registered while the store's key lists are missing or out of date | run `lore sync`, then write again |
| `E_META_TAKEN` | 23 | that key or value name is already taken in this place | pick another name, or use the existing key |
| `E_META_IN_USE` | 23 | a key or allowed value that some row still uses | move those rows off it first (`--merge-into`) |
| `E_META_CONFLICT` | 23 | a write by a bare name while that name has an unsettled duplicate | settle the duplicate first, as the message says |
| `E_META_RETIRED` | 23 | `--merge-into` onto a retired key | merge into a live key |
| `E_META_QUARANTINED` | 23 | the row's metadata has an entry lore could not read cleanly, so the row is not moved; the row file is kept as it is | `lore meta show --id <id>` names the entry; fix it, then move again |
| `E_META_INTERNAL` | 19 | an internal key name or value would reach a client repo | keep the row internal, or drop that metadata |
| `E_META_FOREIGN_KEY` | 19 | a client-repo row would carry a key id or label from outside the client repo's own keys | place the key with `lore meta rehome`, `adopt` or `move` first |
| `E_META_STORE_UNREADABLE` | 19 | in a client repo, a write with metadata while an org store has no local copy and cannot be reached, so lore cannot check the keys; nothing written | go online once (or `lore sync`) so the store is kept locally, then write again |

Removing a key the row does not have is `E_META_UNKNOWN`; the message says which keys the row has:

<!-- example: reference#meta-unknown -->
```console
# alice, in website
$ lore rule add --body 'Keep the footer links in one list'
lore: wrote ~/work/website/.lore/data/rules/rul_3b62…e913.json
✓ rul_3b62…e913
  scope: [project] severity: must
$ lore meta unset --id rul_3b62…e913 --key client_tier
ERROR: [E_META_UNKNOWN] rul_3b62…e913 has no metadata key "client_tier"; it has no metadata
  hint: lore meta show --id rul_3b62…e913 lists its keys
(exit 20)
```
<!-- /example -->

### Resources and kinds

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_RESOURCE_NAME` | 20 | a kind, field or resource name breaks its format, is reserved, or looks like a credential | pick another name |
| `E_RESOURCE_INVALID` | 20 | a value or field does not fit the kind | read the message; it names the field that does not fit |
| `E_RESOURCE_TYPE` | 20 | changing a field's type found values that do not fit the new type | fix those values first; the message lists them |
| `E_RESOURCE_KIND_MISMATCH` | 20 | a merge of two resources of different kinds | merge resources of one kind only |
| `E_KIND_LIMIT` | 20 | over a cap on kinds per place, fields per kind or values per field | remove unused ones first |
| `E_RESOURCE_AMBIGUOUS` | 21 | a bare resource name matches rows at two levels or in two kinds | name the kind or level |
| `E_KIND_CONFLICT` | 22 | a kind definition clashes with another level's, or holds conflict markers (adopt, rehome and graph skip such a kind) | settle the definitions (`lore sync conflicts`) |
| `E_KIND_UNKNOWN` | 22 | a kind that is not defined in this place | define it, or write where it is defined |
| `E_RESOURCE_STALE` | 22 | the store's kind rows this write needs are not available | run `lore sync`, or write with `--to repo` |
| `E_RESOURCE_INTERNAL` | 19 | an internal resource field would be written in a client repo | write it in the store or an own repo, or move with `--drop-internal` |
| `E_RESOURCE_IN_USE` | 23 | a kind field or value that resources still use | move those resources off it first |
| `E_RESOURCE_TAKEN` | 23 | that resource name is already used at this level (archived and trashed rows count) | pick another name, or restore the old one |
| `E_RESOURCE_ARCHIVED` | 23 | a new reference to an archived resource | refer to its replacement, or another resource of that kind (archive is one way) |
| `E_RESOURCE_PLACE` | 23 | resources are not allowed in this place (`resources.allowed_in`, or `--to me`) | write them in an allowed place |
| `E_RESOURCE_QUARANTINED` | 23 | the resource has values lore could not read cleanly | fix them before moving it |
| `E_RESOURCE_BATCH` | 23 | an import failed its checks as a whole; nothing was written | fix the rows the message lists and import again |
| `E_RESOURCE_CONFLICT` | 23 | a push to the store hit a strict or kind/field conflict | settle it first, as the message says |

### References and secrets

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_REF_SYNTAX` | 20 | a `${…}` reference is malformed (an unknown option, a reference inside a path, more than 3 secret alternates) | fix the reference |
| `E_REF_SCHEME` | 20 | a secret reference names a password manager lore does not know | use one of the managers lore lists in the message |
| `E_REF_CYCLE` | 20 | references refer to each other in a loop; the message prints the chain | break the loop |
| `E_REF_DEPTH` | 20 | references nest deeper than 8 | flatten them |
| `E_REF_LIMIT` | 20 | expanding a text took more than 256 references or 64 KB | split the text |
| `E_REF_DANGLING` | 20 | the text refers to something not defined here | define it first, or pass `--allow-dangling` |
| `E_REF_REQUIRED` | 20 | a `${scheme:name:?message}` reference did not resolve | define what it names; the message says what |
| `E_SECRET_UNAVAILABLE` | 15 | a password manager could not give the entry: not installed, locked, not logged in, timed out, or no such entry on this machine | unlock or log in to the manager and run the command again |

A variable whose value holds a reference with an option that scheme does not take is refused before anything is written:

<!-- example: reference#ref-syntax -->
```console
# alice, in website
$ lore vars set --name host --value staging.client.example
lore: wrote ~/work/website/.lore/data/vars/var_4c59…6c38.json
Added var_…6c38 host = staging.client.example (text) in repo (not shareable: clients see ${var:<name>}).
$ lore vars set --name base_url --value 'https://${var:host?port=8080}/'
ERROR: [E_REF_SYNTAX] reference ${var:host?port=8080}: unknown option "port" for ${var:…}
(exit 20)
```
<!-- /example -->

### Upgrade and migration

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_SCHEMA_OLD` | 25 | the local database is older than this lore | run any lore command again; it updates the local layout on open |
| `E_UPGRADE_PERMISSION` | 25 | the folder of the lore program cannot be written | run `sudo lore upgrade`, or upgrade through your package manager |
| `E_UPGRADE_CHECKSUM` | 25 | the downloaded release is cut short or its checksum does not match | run `lore upgrade` again |
| `E_UPGRADE_PINNED` | 25 | an upgrade in CI without a pinned version, or `--version` against the team's pin | set `LORE_VERSION_PIN` or `lore_version_pin`, or pass `--ignore-pin` |
| `E_MIGRATE_LOCKED` | 24 | another teammate's `lore migrate run` holds the team migration lock | wait for it to finish |
| `E_MIGRATE_ROLLBACK_LOSES` | 24 | rolling back the migration would lose changes made after it; nothing was changed | keep the migration, or copy those changes out first |
| `E_FORMAT_OLD` | 24 | the project is on the lore 0.1.17 format: this lore reads it but writes nothing there (no row, no live row, no landing) until it is migrated | run `lore migrate run` once (teammates need lore 0.1.18 after it); a write to your personal store or a migrated org store still works |

### Teams and agents

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_NO_WORK` | 10 | `lore task next` found no ready task; the message says why each one is blocked | wait, or unblock a task |
| `E_CONTENDED` | 11 | ready work exists but every claim lost the race to another agent, or another writer won every attempt to push a coordination write | wait a moment and try again |
| `E_LOST_CLAIM` | 12 | your claim is no longer on the server (it ran out or was taken back); lore fetched once and retried before answering this, so it is not stale | stop working on this task |
| `E_CANCELLED` | 13 | a person cancelled the task while you held it | stop working on this task |
| `E_REASSIGNED` | 14 | a person reassigned the task while you held it | stop working on this task |
| `E_OFFLINE` | 15 | the server could not be reached to confirm a coordination write; nothing was queued | check the network and try again |
| `E_AMBIGUOUS_ID` | 16 | the id exists in two loaded places | name one with `--from` |
| `E_ORG_AMBIGUOUS` | 17 | two org entries match this repo equally well | choose one with `lore org use --entry <entry>` |
| `E_ORG_DISAGREES` | 17 | `LORE_ORG` or `lore org use` names another org than the store's link for this repo | name the place with `--to <org>`, or fix the choice with `lore org use` (or unset `LORE_ORG`) |
| `E_LINK_UNKNOWN` | 18 | the store cannot be reached and no saved link list exists, so lore cannot tell whether this is a client repo | pass `--to`, or try again online (`lore store doctor`) |
| `E_NOT_A_STORE` | 19 | the folder is not inside a git work tree; or the git repo has no lore data (`lore isn't set up in … (no .lore folder)`): every command but `lore init`, `lore setup` and the link commands refuses there and changes nothing, not even `.git/config`, `.git/hooks` or `.git/info`; or a repo under `stores:` has no `lore-store.yaml`, or holds files that are not lore notes | outside git: cd into a git checkout, or run `git init` first; a repo with no lore data: `lore init` (or the This folder step of `lore setup`) when you want lore there; for a store: make one with `lore store init` in a new, empty repo |
| `E_LEAK_BLOCKED` | 19 | the write would put internal notes where the client can read them, or (`cannot check (…)`) a store the check must read could not be read on this machine; the message names it and its cause, and nothing was written | see `lore check leaks --explain`; for `cannot check`, run `lore sync` once the store is reachable (`lore doctor` names it) and run the command again |
| `E_CYCLE` | 19 | the `--after` link would make tasks wait on each other in a circle | drop one `--after` link |
| `E_OLD_LORE` | 19 | this lore is older than the live rows here allow; reading still works | upgrade lore |
| `E_CONFIG_INVALID` | 20 | `~/.lore/config.yaml` has a mistake | run `lore config check`; it lists every problem |

Two of them for real: `lore task next` in a repo with no open task, and a write while `LORE_ORG` names another org than the repo's store link:

<!-- example: reference#no-work -->
```console
# alice, in landing
$ lore task next --push
ERROR: [E_NO_WORK] nothing ready: no open task matches
  hint: lore task add makes a task; lore task list shows the open ones
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

### Team profiles

These come from the people commands (`lore actor edit|contact|status|offboard|rejoin|period`, `lore contact-type`, `lore project member`, `lore role`); the guide is [docs/team/profiles.md](../team/profiles.md).

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_CONTACT_TAKEN` | 23 | an identity contact (an email, GitHub or GitLab login marked "use for identity") already belongs to another person, or an actor row already exists for that email | `lore actor show --id <type>:<value>` shows who has it; if it is the same person, run the add again with `--merge` |
| `E_CONTACT_TYPE` | 20 | an unknown or retired contact type, a value that fails the type's check (an email without a domain, a phone with too few digits), or `--identity` on a type that is not an identity type | `lore contact-type list`; add your own with `lore contact-type add --name <name> --kind …` |
| `E_ROLE_UNKNOWN` | 20 | a `--role` name that is not in the role list, or one that is retired | `lore role list`; `lore role add --name <name>` |
| `E_STATUS_CHANGE` | 23 | a status change the rules do not allow: offboarding someone who already left, rejoining someone who is active, adding a left person to a project, a person who left registering or starting an agent themselves, `status left` on an active person | the command that does it: `lore actor offboard`, `lore actor rejoin` or `lore actor status --actor <actor> --to active` |
| `E_PERIOD_OVERLAP` | 23 | a stint or membership date that overlaps another stint, ends before it starts, or (offboard) falls before the open stint's start | `lore actor period list --actor <actor>`, then pick a date after the last end |

A team profile write in a repo without an org answers `E_REFUSED` (profiles live in an org store; `lore org create --org <name>`), an agent running one where a team set `agents.may_manage_people` to false (it is true by default) answers `E_POLICY_DENIED`, and a contact, role, stint or membership row aimed at a client store answers `E_LEAK_BLOCKED`.

### Setup and invites

| Code | Exit | What it means | What to do |
|---|---|---|---|
| `E_SETUP_NOT_FOUND` | 27 | `--setup`, `LORE_SETUP` or a setup command names no setup on this machine | `lore setup list` ([more](../team/setup-errors.md#e_setup_not_found-exit-27)) |
| `E_SETUP_AMBIGUOUS` | 21 | two setups match this folder equally, or two setups of the repo's org | make one folder glob more specific (`lore setup edit`) or run with `--setup` |
| `E_SETUP_NAME_TAKEN` | 20 | a new setup was given an existing setup's name | pick another name, or `lore setup edit --name <name>` |
| `E_SETUP_ORG_MISMATCH` | 23 | the active setup's org is not the org this repo resolves to; writes refused | run with `--setup <a setup of that org>` or fix the org source |
| `E_SETUP_NO_PERSONAL_STORE` | 20 | `--to me` in a setup with `personal_store: none` | write elsewhere, or `lore setup edit --name <name> --personal-store <url>` |
| `E_SETUP_STEP_FAILED` | 23 | a setup step was saved while an error-level check fails | `lore setup check` shows each fix |
| `E_SETUP_STEP_ORDER` | 23 | a setup step was saved before the steps it needs | save the earlier steps first (`lore setup plan`) |
| `E_INVITE_INVALID` | 20 | the text is not a whole lore invite | copy the whole `lore-invite:v1:` line; the inviter can `lore team invite resend` |
| `E_JOIN_ORG_UNKNOWN` | 27 | `lore setup join --invite <name>` names an org this machine has no entry for yet (a new member's fresh machine) | `lore setup join --invite "<the org's store url>"`, the command `lore member add` printed for the admin ([more](../team/setup-errors.md#e_join_org_unknown-exit-27)) |
| `E_INVITE_NOT_FOUND` | 27 | no such invite in the org's store | ask the inviter to invite you again |
| `E_INVITE_EXPIRED` | 23 | the invite is past its last day | ask for a new one |
| `E_INVITE_REVOKED` | 23 | the invite was revoked | ask the inviter |
| `E_INVITE_USED` | 23 | the invite was accepted by another person | ask for a new one |
| `E_INVITE_EMAIL` | 20 | the invite is for another email | use that address, or ask for a new invite |
| `E_SKILL_LINKED` | 23 | `lore skill install`: the tool's skill folder (or a file in it) is a symbolic link whose target holds an older or hand-edited lore skill, another skill, or nothing; lore does not write through a link unasked | `--link replace` puts lore's own copy where the link was (its target is untouched); `--link update` writes this lore's skill into the target ([more](../knowledge/skill-install.md)) |
| `E_FIX_REFUSED` | 20 | a check's fix is not runnable, or a value in its command failed the safety check | follow the fix text by hand ([more](../team/setup-errors.md#e_fix_refused-exit-20)) |

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

Over the [local REST API](rest-api.md), an error is the same JSON object, sent with the HTTP status of its class. Every code with its exit status, class and HTTP status is in the generated [command and error reference](cli-commands.md#errors), written by `lore spec docs` from the code, so it always matches the program.

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
    "hint": "cd into a git checkout, or run git init here first",
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
  hint: lore rule list --help lists its flags
(exit 20)
```
<!-- /example -->

- `lore doctor` exits 1 for "needs attention" without printing an `E_` code. Check its exit status, or use `lore doctor --json`.

## Related

- [Environment variables](environment.md): `LORE_READ_ONLY`, `LORE_ALLOW_ROOT`, `LORE_ALLOW_NETWORK_FS` and the others named above.
- [Config files](config.md): what `lore config check` refuses.
- [Team troubleshooting](../team/troubleshooting.md): longer fixes for the team codes.
- [Command index](commands.md).
