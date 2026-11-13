# Error codes

**In short:** every lore error has a fixed code like `E_STORE_LOCKED` and a fixed exit status. Find the code here to learn what went wrong and how to fix it; scripts and agents branch on the code (or the exit status), never on the message.

The message may change between versions; the code and its exit status will not.

## What a failing command gives you

On a terminal:

```
ERROR: [E_<CODE>] <message>
  hint: <recovery hint>
```

With `--json` (and every `lore api` error body), one object:

```json
{"error":{
  "code": "E_NEEDS_CONFIRM",
  "message": "...",
  "hint": "...",
  "exit": 26,
  "class": "needs_confirmation",
  "retryable": true,
  "needs_person": true,
  "question": "task done tsk_... --children?",
  "next": {"command": "...", "args": ["..."]},
  "findings": []
}}
```

| Field | Meaning for an agent |
|---|---|
| `code` | the stable code; switch on it |
| `exit` / `class` | the process exit status and its class (table below) |
| `retryable` | running the same command again later can succeed without a fix (a lost race, offline, a confirmation) |
| `needs_person` | an agent must not answer this itself: show the person the message (and `question`), and let the person run or confirm it |
| `question` | set on every `E_NEEDS_CONFIRM`: the exact question a terminal would ask. Show it to the person word for word |
| `next` | set only when lore can name the follow-up without repeating your arguments (they may hold sensitive values); otherwise rerun your own command with the answer flag |
| `findings` | on a detected secret: one masked finding per matched value |

`hint`, `next`, `question` and `findings` are left out when empty. There is no `doc_url` in 0.1.18. The full registry (code, exit, class, HTTP status, meaning) is the errors block of `docs/reference/cli-commands.md`, generated from the code.

---

## Exit statuses

| Exit | Class | Means | What an agent does |
|---|---|---|---|
| 1 | `generic` | `E_INTERNAL` only: a bug | capture `lore support-bundle` and report it; never retry in a loop |
| 10-18 | `coordination` | a team or network outcome (table below) | see the code |
| 19 | `guard` | a safety guard refused: not a store, internal content would reach a client, a cycle, lore too old | read the message; do not work around it |
| 20 | `bad_input` | the input is wrong (`E_INVALID_INPUT`, `E_INVALID_ID`, a bad date, a bad metadata value, …) | fix the input and run again |
| 21 | `ambiguous` | a bare name matches two things | name it by id or level |
| 22 | `definition` | a kind or metadata key is not defined or not visible here | define it, or sync first |
| 23 | `refused` | lore refuses in this state or by a fixed rule (`E_REFUSED`, `E_READ_ONLY`, `E_POLICY_DENIED`, `E_STALE`, …) | read the message and hint; some set `needs_person` |
| 24 | `migrate` | the team migration lock, a rollback that would lose work, or a project on the lore 0.1.17 format (`E_FORMAT_OLD`) | wait, or ask the person |
| 25 | `upgrade` | `lore upgrade` could not finish, or the local database is older than this binary | follow the hint |
| 26 | `needs_confirmation` | `E_NEEDS_CONFIRM`: the command asks first and ran without a terminal and without `--yes` | ask the person (below) |
| 27 | `not_found` | `E_NOT_FOUND`: a well-formed id or name that does not exist here | check the id with a `list` |
| 28 | `unauthorized` | `E_UNAUTHORIZED` (REST only): no valid token | `lore api` callers send the token |

### Coordination codes (exit 10-18)

| Code | Exit | What to do |
|---|---|---|
| `E_NO_WORK` | 10 | `lore task next` found nothing ready; the message lists why each candidate is blocked. Wait, or check `lore inbox` |
| `E_CONTENDED` | 11 | another writer won every attempt (a claim race, or a live push whose remote moved on every retry); retry after a short wait |
| `E_LOST_CLAIM` | 12 | your claim is gone (lease expired, reclaimed or revoked); stop work on this task |
| `E_CANCELLED` | 13 | a person cancelled the task while you held it; stop |
| `E_REASSIGNED` | 14 | a person reassigned the task while you held it; stop |
| `E_OFFLINE` / `E_STORE_UNREACHABLE` / `E_SECRET_UNAVAILABLE` | 15 | the remote (or a store with no local copy, or a secret manager) could not be reached; nothing was queued, retry when online |
| `E_AMBIGUOUS_ID` | 16 | the id exists in two loaded sources; pass `--from <source>` |
| `E_ORG_AMBIGUOUS` / `E_ORG_DISAGREES` | 17 | two org entries match, or your choice disagrees with a store link; `lore org use --entry <entry>` or `--force-org` |
| `E_LINK_UNKNOWN` | 18 | lore cannot tell whether this is a client repo (store unreachable); pass `--to` or retry online |

---

## Codes agents meet most

| Code | Exit | When | What to do |
|---|---|---|---|
| `E_NEEDS_CONFIRM` | 26 | the command asks first (a delete, a move, a bulk action, a secret finding) and has no terminal | show the person `error.question` word for word; only after they say yes, rerun your own command with `--yes` (row deletes say `--confirm` in their hint). A secret finding is different: `--yes` alone never stores it (next row) |
| `E_SECRET_DETECTED` / `E_NEEDS_CONFIRM` with `findings` | 23 / 26 | the text looks like it holds a password or key | strip the secret, or rerun with one of `error.next.add_args_one_of`: `--secret-ref "<manager>://<entry>"` (keep a reference), `--secret-unstored` (keep the row, drop the value), or `--not-a-secret --reason "<why>" --yes` only after the person confirms it is not a secret |
| `E_INVALID_ID` | 20 | an id is malformed (expected `<prefix>_<32 hex>`, for example `tsk_` + 32 hex). Never reported as not found | copy the id from `list` or `--json` output; do not guess or shorten it |
| `E_NOT_FOUND` | 27 | a well-formed id or name that does not exist here (also a missing project or repo) | `lore <kind> list`, or check you are in the right repo; it may be in the trash (`lore trash list`) |
| `E_POLICY_DENIED` | 23 | a team policy refuses this. With `needs_person: true` the policy `agents.person_only` lists this command, or a team set `agents.may_manage_people` to false for a people write, and you look like an agent | do not retry or work around it: tell the person the command and let them run it in their own terminal |
| `E_REFUSED` | 23 | a fixed lore rule refuses this; the message names the rule. Coordination refusals use it too: an agent with no person behind it doing a human override (cancel, reassign, revoke, finishing another's claim; `needs_person: true`; a person's own coding agent acts for them and passes), the WIP limit, a poisoned task, a task that is not open | follow the message; for `needs_person`, hand it to the person |
| `E_STALE` | 23 | the row changed between read and write and could not be merged | read the row again and redo the edit |
| `E_OLD_LORE` | 19 | this binary is below the project's `min_lore_version` or does not know one of its features (reads still work) | `lore upgrade` |
| `E_FORMAT_OLD` | 24 | the project (or a shared store) is on the lore 0.1.17 format: this lore reads it but writes nothing there (no row, live row, landing or render) | tell the person; only they decide to run `lore migrate run` (or `lore fix --migrate`) once, after every teammate has lore 0.1.18. A write to the personal store or a migrated org store still works |
| `E_LEAK_BLOCKED` | 19 | the write would put internal content where the client can read it (or lore cannot check it) | write it to the internal store (`--to`), or `lore check leaks --explain` |
| `E_META_QUARANTINED` | 23 | a move of a row whose metadata is quarantined (a value that no longer fits its key) | `lore meta show --id <id>` prints each problem and its fix; fix it, then move |
| `E_META_STORE_UNREADABLE` | 19 | a write carrying metadata (`--meta`, `meta set|unset`) in a client repo while an org store has no local copy and cannot be reached, so lore cannot check the keys | nothing was written: go online once (or `lore sync` in the store), then write again |
| `E_STORE_LOCKED` | 23 | the store lock stayed held past `store.lock_timeout` (another lore command is writing) | wait and run again |
| `E_STORE_SCHEME` | 20 | a store url uses a scheme 0.1.18 has no backend for (`s3://`, `postgres://`, `lore://`) | use a git url or `file:///<absolute path>` |
| `E_URL_CREDENTIALS` | 20 | a url holds a password or token | remove it; use your git credential helper or ssh |
| `E_HAS_DEPENDENTS` | 23 | a delete while other live rows still point at this one | delete or repoint them first (the message lists them) |
| `E_OPEN_CHILDREN` / `E_HAS_OPEN_CHILDREN` | 23 | done/cancel of a parent with open sub-tasks; a claim of a parent with open sub-tasks | ask the person, then `--children --yes`; claim a leaf, or `--coordinate` |
| `E_INVALID_INPUT` | 20 | a flag value or field failed validation; also "the agent … has no person behind it": an agent actor whose roster row names no owner tried a roster change (`needs_person: true`) | fix the value (the message names it); for no person behind it, the person runs it or `lore agent start --label <l> --owner @<person>` |

---

## Full registry

Every code of 0.1.18, grouped. "Not raised" marks codes kept for compatibility that the 0.1.18 core never answers.

### Local files and safety

| Code | Exit | Meaning and fix |
|---|---|---|
| `E_DB_CORRUPT` | 23 | the local cache holds a record lore cannot read. Delete the repo cache (`~/.lore/cache/repo/<repo folder>-*`, with its `-wal`/`-shm`): the next command rebuilds it from `.lore/data`. A cache file that cannot be opened at all is moved aside beside itself (`<file>.corrupt-<time>`) and rebuilt by itself |
| `E_DB_NOT_FOUND` | 23 | a store's cache could not be attached; run the command again (it rebuilds) or check the store with `lore store list` |
| `E_DISK_FULL` | 23 | no space left; nothing was written. Free space and run again |
| `E_NETWORK_FS` | 23 | the store is in a cloud-sync folder (iCloud, Dropbox, OneDrive, Google Drive) or on NFS/SMB. Move the project to a local disk (git shares the notes); `LORE_ALLOW_NETWORK_FS=1` overrides at your own risk |
| `E_ROOT_REFUSED` | 23 | lore refuses to run as root (files would be root-owned). Run as your own user; `LORE_ALLOW_ROOT=1` only if the person really means it |
| `E_SKILL_LINKED` | 23 | `lore skill install`: the tool's skill folder is a symbolic link (a shared-skills layout) whose target holds an older or edited lore skill, another skill, or nothing; lore does not write through it. Ask the person which they want: `--link replace` (lore's own copy replaces the link) or `--link update` (write into the link's target) |
| `E_SYMLINK_LOOP` | 23 | a chain of symbolic links loops (or is longer than 40); the message names the link. Fix or remove that link |
| `E_SYMLINK_DB` | 23 | a database file, the store lock (`.lore/lock`) or an audit file is a symbolic link; lore never writes through one. Delete the link; the next command recreates the file |
| `E_READ_ONLY` | 23 | a write in read-only mode. Drop `--read-only` / unset `LORE_READ_ONLY`. Also answered by `lore sync` / `lore land` while the remote live ref is deleted or rewritten (the hint names `lore live restore`) |
| `E_LOCK_HELD` | 23 | another `lore upgrade` holds the upgrade lock |
| `E_BUSY_TIMEOUT` | 23 | other writers kept moving the store branch; run the command again |
| `E_AUDIT_CHAIN_BROKEN` | 23 | `lore audit verify` found an edited or deleted audit entry. Inspect with `lore audit log`; tell the person |
| `E_SYNC_DATA_IGNORED` | 23 | git ignores files under `.lore/data`, so teammates would not receive them; nothing was pushed. Lore adds `!.lore/data/` exceptions itself when it can; this means a whole parent folder (usually `.lore/`) is ignored: change that rule |
| `E_NOT_PROJECT_ROOT` | 23 | this folder is not a lore project: `cd` to the repo, or `lore init` there |
| `E_AMBIGUOUS_PROJECT` | 23 | the repository holds several projects; pass `--project` |
| `E_ALREADY_INITIALIZED` | 23 | this folder already has a `.lore/` |
| `E_PROJECT_NAME_COLLISION` / `E_MOUNT_NAME_TAKEN` | 23 | a project or repo name is already used; pick another or use the id |
| `E_IDENTITY_FAILED` | 23 | lore could not work out who you are; `lore whoami` shows what it found |
| `E_DB_LOCKED`, `E_SCHEMA_VERSION_MISMATCH`, `E_MIGRATION_INCOMPLETE`, `E_INODE_MISMATCH`, `E_READ_ONLY_FS`, `E_UID_MISMATCH`, `E_STORE_BACKEND` | 23 | not raised by the 0.1.18 core (v0.1.17 database codes). There is no schema step to finish: the cache rebuilds itself and the machine store updates when opened |

### Input (exit 20)

`E_INVALID_INPUT`, `E_INVALID_ID`, `E_EMPTY_BODY`, `E_INVALID_UTF8`, `E_BODY_TOO_LARGE`, `E_INVALID_IDENTIFIER` (a project or repo name with a character that is not allowed), `E_BAD_PATH` (a path with `..` or control characters), `E_CONFIG_INVALID` (a config file failed validation; the message names the key), `E_DATE` (give e.g. `2026-11-13`, `tomorrow`, `next mon`, `in 3 days`), `E_RECUR`, `E_DURATION_BAD` (e.g. `45m`, `1h30m`), `E_TIME_TOO_LONG`, `E_SQL_REFUSED` (`lore sql` takes one read-only SELECT/WITH/VALUES), `E_REASON_REQUIRED` (a delete without `--reason` while `delete.require_reason` is on), `E_REPO_URL`, `E_URL_CREDENTIALS`, `E_STORE_SCHEME`, the metadata input codes (`E_META_TYPE`, `E_META_KEY`, `E_META_RESERVED`, `E_META_VALUE`, `E_META_SHAPE`, `E_META_SIZE`, `E_META_DEPTH`, `E_META_OP`, `E_META_TABLE`, `E_META_REQUIRED`, `E_META_UNKNOWN`), the resource input codes (`E_RESOURCE_INVALID`, `E_RESOURCE_NAME`, `E_RESOURCE_TYPE`, `E_RESOURCE_KIND_MISMATCH`, `E_KIND_LIMIT`) and the reference codes (`E_REF_SYNTAX`, `E_REF_SCHEME`, `E_REF_CYCLE`, `E_REF_DEPTH`, `E_REF_LIMIT`, `E_REF_DANGLING`, `E_REF_REQUIRED`). Fix the input the message names and run again.

### State refusals (exit 23)

Besides the ones above: `E_NOT_IMPLEMENTED` / `E_UNSUPPORTED` (not built in this release or not available in this mode: tell the person, do not retry), `E_SQL_TIMEOUT`, `E_NAME_TAKEN`, `E_META_TAKEN`, `E_IN_TRASH`, `E_PURGED`, `E_PARENT_DELETED`, `E_PARENT_PURGED` (restore the parent first), `E_PARENT_CLOSED` (`--reopen-parent`), `E_PARENT_MISSION`, `E_PARENT_PROJECT`, `E_TREE_DEPTH`, `E_TREE_WIDTH`, `E_OPEN_ITEMS` (tick the done items, `--tick-all` only when all really are done, `--force` only after the person says so), `E_TEMPLATE`, `E_STORE_READ_ONLY` (the store is attached read-only), `E_META_IN_USE`, `E_META_CONFLICT`, `E_META_RETIRED`, `E_RESOURCE_IN_USE`, `E_RESOURCE_QUARANTINED`, `E_RESOURCE_TAKEN`, `E_RESOURCE_ARCHIVED`, `E_RESOURCE_BATCH`, `E_RESOURCE_CONFLICT`, `E_RESOURCE_PLACE`.

### Guards (exit 19)

`E_NOT_A_STORE`, `E_LEAK_BLOCKED`, `E_CYCLE`, `E_OLD_LORE`, `E_META_INTERNAL` and `E_RESOURCE_INTERNAL` (an internal key, value or field would reach a client repo), `E_META_FOREIGN_KEY` (a client-repo row carries a key from outside its registry), `E_META_STORE_UNREADABLE`.

### Definitions (exit 22), ambiguity (21), migrate (24), upgrade (25)

`E_KIND_UNKNOWN`, `E_KIND_CONFLICT`, `E_RESOURCE_STALE` and `E_META_STALE` (run `lore sync`, or write with `--to repo`), `E_META_UNREGISTERED`; `E_META_AMBIGUOUS`, `E_RESOURCE_AMBIGUOUS`; `E_MIGRATE_LOCKED`, `E_MIGRATE_ROLLBACK_LOSES`; `E_UPGRADE_PERMISSION` (`sudo lore upgrade`), `E_UPGRADE_CHECKSUM` (run `lore upgrade` again), `E_UPGRADE_PINNED` (set the pin or pass `--version`), `E_SCHEMA_OLD`.

---

## Recovery decision tree

```
After upgrading lore
    → nothing to run: the cache and search index rebuild themselves from
      .lore/data and the machine store updates when it is opened.
      A repo with v0.1.17 data stays on that format (writes: E_FORMAT_OLD)
      until a person runs `lore migrate run` once (`--dry-run` first),
      after every teammate upgraded; `lore fix` never migrates without --migrate.
      `lore setup` is optional (safe to repeat; `--agent-hooks` installs the
      coding tools' hooks).

E_DB_CORRUPT
    → rm -f ~/.lore/cache/repo/<repo folder>-*
      the next command rebuilds it from .lore/data (nothing shared is lost;
      machine-local state lives in ~/.lore/state/machine.db and is untouched)
    → no .lore/data either: git checkout HEAD -- .lore/data first

E_NEEDS_CONFIRM
    → show the person error.question; on yes rerun your command with --yes
    → with error.findings (a secret): --yes alone is not an answer; pick one of
      error.next.add_args_one_of with the person

needs_person: true (E_POLICY_DENIED from a team policy; E_REFUSED or E_INVALID_INPUT "has no person behind it")
    → stop; tell the person which command to run in their own terminal

E_INVALID_ID
    → the id is malformed; copy it from `lore <kind> list --json`

E_STORE_LOCKED / E_LOCK_HELD / E_BUSY_TIMEOUT / E_CONTENDED
    → another lore command is writing; wait a few seconds and run again

E_OFFLINE / E_STORE_UNREACHABLE / E_LINK_UNKNOWN / E_META_STORE_UNREADABLE
    → the remote or an org store cannot be reached; nothing was written.
      Retry online (or pass --to where the hint says so)

E_READ_ONLY
    → unset LORE_READ_ONLY and drop --read-only
    → from lore sync / lore land: the remote live ref was deleted or
      rewritten; the hint names `lore live restore`

E_DISK_FULL
    → free space and run the command again; nothing was written

E_AUDIT_CHAIN_BROKEN
    → `lore audit log` to inspect; tell the person (someone edited the log)

E_SYNC_DATA_IGNORED
    → a .gitignore rule hides .lore/data; ignore .lore/lock and
      .lore/state/ instead of the whole .lore/

E_NOT_PROJECT_ROOT
    → cd to the repo root, or run `lore init` there

E_ROOT_REFUSED
    → run as your normal user; LORE_ALLOW_ROOT=1 only if the person means it

E_SYMLINK_DB
    → delete the link; the next command recreates the file

E_NETWORK_FS
    → move the project to a local disk; git (via .lore/data) shares the notes,
      not the sync client. LORE_ALLOW_NETWORK_FS=1 at your own risk

E_NOT_IMPLEMENTED / E_UNSUPPORTED
    → not available in this release or mode; tell the person, do not retry

E_INTERNAL
    → a bug. lore support-bundle --out=/tmp/bundle.tar.gz and file an issue
```

---

## Programmatic use

```bash
# Branch on the exit status, or on the code from --json
out=$(lore memory add --json --body="$body" 2>&1); rc=$?
if [ $rc -ne 0 ]; then
    code=$(printf '%s' "$out" | jq -r '.error.code // empty')
    case "$code" in
        E_NEEDS_CONFIRM) printf '%s\n' "$out" | jq -r '.error.question'; exit 1 ;;  # ask the person
        E_STORE_LOCKED)  sleep 2; exec "$0" "$@" ;;
        E_READ_ONLY)     echo "read-only mode"; exit 0 ;;
        *)               echo "unexpected: $code (exit $rc)"; exit 1 ;;
    esac
fi
```
