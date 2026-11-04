# Generated command and error reference

This file is generated from the operation registry of the 0.1.18 core (the `op.Define` specs in `ops/`). Do not edit the text between the `lore:spec` markers: run `lore spec docs` (and `lore spec openapi` for `openapi.json`) after changing an operation, and the drift test in `wire/docs_test.go` fails until you do. Only the commands already migrated to the new core appear here; the rest of the CLI is described in the hand-written reference.

## Commands

<!-- lore:spec:begin commands -->

| Command | Summary | Flags | REST | Permission | Errors |
|---|---|---|---|---|---|
| `lore memory add [text]` | Add a memory (free-form learned knowledge) | `--body` `--kind` `--source` `--source-ref` `--allow-secrets` `--to` `--repo` | `POST /v1/memories` | write | `E_INVALID_INPUT` `E_INVALID_ID` `E_EMPTY_BODY` `E_NOT_FOUND` `E_AMBIGUOUS_ID` `E_SECRET_DETECTED` `E_NEEDS_CONFIRM` `E_CANCELLED` `E_POLICY_DENIED` `E_CONFIG_INVALID` `E_STALE` `E_STORE_READ_ONLY` `E_STORE_LOCKED` `E_STORE_UNREACHABLE` `E_OLD_LORE` `E_INTERNAL` |
| `lore memory archive <id>` | Archive a memory (hidden from list and search; kept) |  | `POST /v1/memories/{id}/archive` | write | `E_INVALID_INPUT` `E_INVALID_ID` `E_EMPTY_BODY` `E_NOT_FOUND` `E_AMBIGUOUS_ID` `E_SECRET_DETECTED` `E_NEEDS_CONFIRM` `E_CANCELLED` `E_POLICY_DENIED` `E_CONFIG_INVALID` `E_STALE` `E_STORE_READ_ONLY` `E_STORE_LOCKED` `E_STORE_UNREACHABLE` `E_OLD_LORE` `E_INTERNAL` |
| `lore memory delete <id>` | Delete a memory (a tombstone is kept so old copies never return) |  | `DELETE /v1/memories/{id}` | write | `E_INVALID_INPUT` `E_INVALID_ID` `E_EMPTY_BODY` `E_NOT_FOUND` `E_AMBIGUOUS_ID` `E_SECRET_DETECTED` `E_NEEDS_CONFIRM` `E_CANCELLED` `E_POLICY_DENIED` `E_CONFIG_INVALID` `E_STALE` `E_STORE_READ_ONLY` `E_STORE_LOCKED` `E_STORE_UNREACHABLE` `E_OLD_LORE` `E_INTERNAL` `E_HAS_DEPENDENTS` |
| `lore memory edit <id>` | Edit memory fields (only the flags given change) | `--body` `--kind` `--source` `--source-ref` `--allow-secrets` | `PATCH /v1/memories/{id}` | write | `E_INVALID_INPUT` `E_INVALID_ID` `E_EMPTY_BODY` `E_NOT_FOUND` `E_AMBIGUOUS_ID` `E_SECRET_DETECTED` `E_NEEDS_CONFIRM` `E_CANCELLED` `E_POLICY_DENIED` `E_CONFIG_INVALID` `E_STALE` `E_STORE_READ_ONLY` `E_STORE_LOCKED` `E_STORE_UNREACHABLE` `E_OLD_LORE` `E_INTERNAL` |
| `lore memory invalidate <id>` | Mark a memory invalid as of now (sets valid_until) |  | `POST /v1/memories/{id}/invalidate` | write | `E_INVALID_INPUT` `E_INVALID_ID` `E_EMPTY_BODY` `E_NOT_FOUND` `E_AMBIGUOUS_ID` `E_SECRET_DETECTED` `E_NEEDS_CONFIRM` `E_CANCELLED` `E_POLICY_DENIED` `E_CONFIG_INVALID` `E_STALE` `E_STORE_READ_ONLY` `E_STORE_LOCKED` `E_STORE_UNREACHABLE` `E_OLD_LORE` `E_INTERNAL` |
| `lore memory list` | List memories of every attached store (active by default) | `--repo` `--no-inherit` `--master-only` `--all-repos` `--archived` `--kind` `--store` | `GET /v1/memories` | read | `E_INVALID_ID` `E_NOT_FOUND` `E_AMBIGUOUS_ID` `E_STORE_UNREACHABLE` `E_OLD_LORE` `E_INTERNAL` |
| `lore memory search <query>` | Full-text search over memories of every attached store | `--repo` `--no-inherit` `--master-only` `--all-repos` `--limit` `--include-archived` | `POST /v1/memories/search` | read | `E_INVALID_INPUT` `E_UNSUPPORTED` `E_DB_CORRUPT` `E_STORE_UNREACHABLE` `E_INTERNAL` |
| `lore memory show <id>` | Show one memory |  | `GET /v1/memories/{id}` | read | `E_INVALID_ID` `E_NOT_FOUND` `E_AMBIGUOUS_ID` `E_STORE_UNREACHABLE` `E_OLD_LORE` `E_INTERNAL` |
| `lore memory unarchive <id>` | Bring an archived memory back |  | `POST /v1/memories/{id}/unarchive` | write | `E_INVALID_INPUT` `E_INVALID_ID` `E_EMPTY_BODY` `E_NOT_FOUND` `E_AMBIGUOUS_ID` `E_SECRET_DETECTED` `E_NEEDS_CONFIRM` `E_CANCELLED` `E_POLICY_DENIED` `E_CONFIG_INVALID` `E_STALE` `E_STORE_READ_ONLY` `E_STORE_LOCKED` `E_STORE_UNREACHABLE` `E_OLD_LORE` `E_INTERNAL` |
| `lore migrate run` | Rewrite v0.1.17 row files (_v: 1) as _v: 2 (memories in this release) | `--dry-run` | `POST /v1/migrate/run` | admin | `E_STALE` `E_STORE_LOCKED` `E_STORE_READ_ONLY` `E_INVALID_INPUT` `E_OLD_LORE` `E_INTERNAL` |
| `lore stores sync` | Sync the attached stores (.lore/stores.yaml) with their remotes | `--store` | `POST /v1/stores/sync` | write | `E_NOT_FOUND` `E_STALE` `E_CONTENDED` `E_STORE_LOCKED` `E_STORE_UNREACHABLE` `E_POLICY_DENIED` `E_INTERNAL` |

<!-- lore:spec:end commands -->

## Errors

<!-- lore:spec:begin errors -->

| Code | Exit | Class | HTTP | Meaning |
|---|---|---|---|---|
| `E_ALREADY_INITIALIZED` | 23 | `refused` | 409 | this directory already contains a .lore/ |
| `E_AMBIGUOUS_ID` | 16 | `coordination` | 409 | the id exists in two loaded sources; name one with --from |
| `E_AMBIGUOUS_PROJECT` | 23 | `refused` | 409 | both .lore/lore.db and a .lore/lore.yaml pointer present |
| `E_AUDIT_CHAIN_BROKEN` | 23 | `refused` | 409 | audit log hash chain integrity verification failed |
| `E_BAD_PATH` | 20 | `bad_input` | 400 | path contains traversal sequence or control characters |
| `E_BODY_TOO_LARGE` | 20 | `bad_input` | 400 | body exceeds maximum allowed size |
| `E_BUSY_TIMEOUT` | 23 | `refused` | 409 | DB busy timeout exceeded |
| `E_CANCELLED` | 13 | `coordination` | 409 | a person cancelled the task while you held it |
| `E_CONFIG_INVALID` | 20 | `bad_input` | 400 | a config file failed validation |
| `E_CONTENDED` | 11 | `coordination` | 409 | every claim attempt lost a race; retry after a short wait |
| `E_CYCLE` | 19 | `guard` | 403 | the write would create a cycle |
| `E_DB_CORRUPT` | 23 | `refused` | 409 | DB file failed quick_check; run `lore repair` |
| `E_DB_LOCKED` | 23 | `refused` | 409 | another lore process is holding the DB write lock |
| `E_DB_NOT_FOUND` | 23 | `refused` | 409 | DB file does not exist at the configured path |
| `E_DISK_FULL` | 23 | `refused` | 409 | no space left on device for write or backup |
| `E_EMPTY_BODY` | 20 | `bad_input` | 400 | body is empty after normalization |
| `E_HAS_DEPENDENTS` | 23 | `refused` | 409 | a delete refused while restrict relations still have live pointing rows |
| `E_IDENTITY_FAILED` | 23 | `refused` | 409 | could not resolve actor identity |
| `E_INODE_MISMATCH` | 23 | `refused` | 409 | DB file inode changed since last open |
| `E_INTERNAL` | 1 | `generic` | 500 | internal error; please file a bug report |
| `E_INVALID_ID` | 20 | `bad_input` | 400 | id failed format validation (expected <prefix>_<32-hex-uuidv7>) |
| `E_INVALID_IDENTIFIER` | 20 | `bad_input` | 400 | identifier contains disallowed character |
| `E_INVALID_INPUT` | 20 | `bad_input` | 400 | input value failed validation |
| `E_INVALID_UTF8` | 20 | `bad_input` | 400 | input is not valid UTF-8 |
| `E_KIND_UNKNOWN` | 22 | `definition` | 422 | a kind not in this place's view |
| `E_LEAK_BLOCKED` | 19 | `guard` | 403 | the write would put internal content where the client can read it |
| `E_LINK_UNKNOWN` | 18 | `coordination` | 409 | lore cannot tell whether this repo is a client repo |
| `E_LOCK_HELD` | 23 | `refused` | 409 | another lore process is holding the project flock |
| `E_LOST_CLAIM` | 12 | `coordination` | 409 | your claim is no longer held; stop work on this task |
| `E_META_AMBIGUOUS` | 21 | `ambiguous` | 409 | a bare name matches definitions at two or more levels |
| `E_META_TYPE` | 20 | `bad_input` | 400 | a value does not parse as the key's type |
| `E_MIGRATE_LOCKED` | 24 | `migrate` | 409 | the team migration lock is held elsewhere |
| `E_MIGRATION_INCOMPLETE` | 23 | `refused` | 409 | a schema migration did not finish; run `lore setup` |
| `E_MOUNT_NAME_TAKEN` | 23 | `refused` | 409 | another repo in this project already uses that mount_name |
| `E_NEEDS_CONFIRM` | 26 | `needs_confirmation` | 428 | the command asks first and ran with no terminal and no --yes |
| `E_NETWORK_FS` | 23 | `refused` | 409 | DB path is on a network/cloud-sync filesystem |
| `E_NOT_A_STORE` | 19 | `guard` | 403 | the repo or url is not a lore store |
| `E_NOT_FOUND` | 27 | `not_found` | 404 | no such row, resource, variable or other thing here |
| `E_NOT_IMPLEMENTED` | 23 | `refused` | 409 | feature not yet implemented |
| `E_NOT_PROJECT_ROOT` | 23 | `refused` | 409 | current directory is not a lore project |
| `E_NO_WORK` | 10 | `coordination` | 409 | no ready task |
| `E_OFFLINE` | 15 | `coordination` | 503 | the remote could not be reached; nothing was queued |
| `E_OLD_LORE` | 19 | `guard` | 403 | this lore binary is too old for the data; writes refused, reads work |
| `E_ORG_AMBIGUOUS` | 17 | `coordination` | 409 | two org entries match this repo at the same rule |
| `E_ORG_DISAGREES` | 17 | `coordination` | 409 | the selected org differs from the store link |
| `E_POLICY_DENIED` | 23 | `refused` | 409 | a team policy refuses this |
| `E_PROJECT_NAME_COLLISION` | 23 | `refused` | 409 | multiple projects share that name; specify by opaque ID |
| `E_READ_ONLY` | 23 | `refused` | 409 | command requires write access but read-only mode is active |
| `E_READ_ONLY_FS` | 23 | `refused` | 409 | DB path is on a read-only filesystem |
| `E_REASSIGNED` | 14 | `coordination` | 409 | a person reassigned the task while you held it |
| `E_RESOURCE_INVALID` | 20 | `bad_input` | 400 | a value or field does not fit the kind |
| `E_ROOT_REFUSED` | 23 | `refused` | 409 | lore refuses to run as root |
| `E_SCHEMA_OLD` | 25 | `upgrade` | 426 | the local database is older than this binary |
| `E_SCHEMA_VERSION_MISMATCH` | 23 | `refused` | 409 | DB was migrated by a newer lore; upgrade or use a different DB |
| `E_SECRET_DETECTED` | 23 | `refused` | 409 | input contains a credential pattern; refusing to store |
| `E_STALE` | 23 | `refused` | 409 | the row changed between read and write and could not be merged automatically |
| `E_STORE_BACKEND` | 23 | `refused` | 409 | a store's filler and backend do not fit |
| `E_STORE_LOCKED` | 23 | `refused` | 409 | the store lock stayed held past store.lock_timeout |
| `E_STORE_READ_ONLY` | 23 | `refused` | 409 | a write to a store attached read-only |
| `E_STORE_SCHEME` | 20 | `bad_input` | 400 | a store url uses a scheme no backend is registered for |
| `E_STORE_UNREACHABLE` | 15 | `coordination` | 503 | a store with no local copy could not be reached |
| `E_SYMLINK_DB` | 23 | `refused` | 409 | DB path is a symlink; refusing for safety |
| `E_SYMLINK_LOOP` | 23 | `refused` | 409 | filesystem walk encountered a symlink loop |
| `E_SYNC_DATA_IGNORED` | 23 | `refused` | 409 | git ignores files under .lore/data, so teammates would not receive them |
| `E_UID_MISMATCH` | 23 | `refused` | 409 | EUID does not match HOME owner; refusing |
| `E_UNAUTHORIZED` | 28 | `unauthorized` | 401 | the request has no valid token |
| `E_UNSUPPORTED` | 23 | `refused` | 409 | operation not supported in current mode |
| `E_URL_CREDENTIALS` | 20 | `bad_input` | 400 | a URL value holds credentials |

<!-- lore:spec:end errors -->
