# Error codes

Every lore error has a stable `E_*` code. When something fails, **match on the code, not the English message** — the message may change between versions, the code will not.

## Recover by inspecting the code

The CLI always exits non-zero on error AND prints either:

```
ERROR: [E_<CODE>] <message>
  hint: <recovery hint>
```

…or in `--json` mode:

```json
{"error":{"code":"E_<CODE>","message":"...","hint":"...","doc_url":"..."}}
```

Auto-discovery: `lore errors list --json` returns the full registry at runtime — your code should pull it once and switch on the result.

---

## Full registry (37 codes as of v0.1)

| Code | Description |
|---|---|
| `E_DB_LOCKED` | another lore process is holding the DB write lock |
| `E_DB_CORRUPT` | DB file failed quick_check; run `lore repair` |
| `E_DB_NOT_FOUND` | DB file does not exist at the configured path |
| `E_SCHEMA_VERSION_MISMATCH` | DB was migrated by a newer aicoder; upgrade or use a different DB |
| `E_MIGRATION_INCOMPLETE` | a migration is in_progress; run `lore repair` |
| `E_NETWORK_FS` | DB path is on a network/cloud-sync filesystem (silent corruption risk) |
| `E_INODE_MISMATCH` | DB file inode changed since last open; verify external mutation |
| `E_DISK_FULL` | no space left on device for write or backup |
| `E_READ_ONLY_FS` | DB path is on a read-only filesystem |
| `E_NOT_PROJECT_ROOT` | current directory is not an lore project (no .lore/lore.db or lore.toml) |
| `E_AMBIGUOUS_PROJECT` | both .lore/lore.db AND .lore/lore.toml present (Mode A vs Mode B ambiguity) |
| `E_PROJECT_NOT_FOUND` | project_id not registered in this DB |
| `E_PROJECT_NAME_COLLISION` | multiple projects share that name; specify by opaque ID |
| `E_REPO_NOT_FOUND` | repo mount_name or rep_id not found in current project |
| `E_MOUNT_NAME_TAKEN` | another repo in this project already uses that mount_name |
| `E_IDENTITY_FAILED` | could not resolve actor identity; falling back to ephemeral |
| `E_BAD_PATH` | path contains traversal sequence or control characters |
| `E_ALREADY_INITIALIZED` | this directory already contains a .lore/ — refusing to overwrite |
| `E_INVALID_ID` | id failed format validation (expected <prefix>_<32-hex-uuidv7>) |
| `E_INVALID_INPUT` | input value failed validation |
| `E_EMPTY_BODY` | body is empty after normalization |
| `E_INVALID_UTF8` | input is not valid UTF-8 |
| `E_BODY_TOO_LARGE` | body exceeds maximum allowed size |
| `E_INVALID_IDENTIFIER` | identifier contains disallowed character |
| `E_SECRET_DETECTED` | input contains a credential pattern; refusing to store. Use --allow-secrets to override (logged) |
| `E_ROOT_REFUSED` | lore refuses to run as root; set MINI_ALLOW_ROOT=1 to override |
| `E_SYMLINK_DB` | DB path is a symlink; refusing for safety. Use --allow-symlink-db to override |
| `E_SYMLINK_LOOP` | filesystem walk encountered a symlink loop |
| `E_UID_MISMATCH` | EUID does not match HOME owner (sudo PRESERVE_ENV detected); refusing |
| `E_LOCK_HELD` | another lore process is holding the project flock |
| `E_READ_ONLY` | command requires write access but read-only mode is active |
| `E_BUSY_TIMEOUT` | DB busy timeout exceeded |
| `E_AUDIT_CHAIN_BROKEN` | audit log hash chain integrity verification failed |
| `E_NOT_FOUND` | requested entity does not exist |
| `E_INTERNAL` | internal error; please file a bug report with `lore support-bundle` |
| `E_NOT_IMPLEMENTED` | feature not yet implemented (deferred to v0.2 or v1.0+) |
| `E_UNSUPPORTED` | operation not supported in current mode |

---

## Recovery decision tree

```
E_DB_CORRUPT / E_DB_NOT_FOUND / E_MIGRATION_INCOMPLETE
    → lore repair --tier=2 --confirm     (restore from backup)
    → if no backup:    --tier=3 --confirm   (bootstrap empty, then learn-from docs)

E_DB_LOCKED / E_LOCK_HELD
    → another lore process is running; wait or `pkill -f aicoder`
    → if the lock file is stale, lore auto-reclaims (PID validation)

E_NOT_PROJECT_ROOT
    → cwd has no .lore/. Either:
        cd to the project root, OR
        run with --db=<path> to override (no walk-up by design)

E_AMBIGUOUS_PROJECT
    → both lore.db and lore.toml exist in .lore/. Remove one.

E_SECRET_DETECTED
    → input matched a credential pattern. Strip the secret OR use
      --allow-secrets (the override is logged loudly).

E_ROOT_REFUSED
    → set MINI_ALLOW_ROOT=1 (only if you really mean it).

E_SYMLINK_DB
    → .lore/lore.db is a symlink. Delete the symlink and either
      restore a real backup or re-init.

E_READ_ONLY
    → unset LORE_READ_ONLY and drop --read-only flag.

E_INVALID_INPUT / E_EMPTY_BODY / E_INVALID_IDENTIFIER / E_BAD_PATH
    → user-correctable; show the hint to the user.

E_NETWORK_FS
    → DB path is on iCloud/Dropbox/NFS. Move it to a local disk
      (lore.db inside cloud-synced folders silently corrupts).

E_NOT_IMPLEMENTED / E_UNSUPPORTED
    → feature isn't built yet (v0.2). No recovery, just inform user.

E_INTERNAL
    → bug. Capture support bundle:
        lore support-bundle --out=/tmp/bundle.tar.gz
      and file an issue.
```

---

## Programmatic use

```bash
# Run a command, catch a specific code in JSON
if ! out=$(lore memory add "$body" 2>&1); then
    code=$(echo "$out" | grep -oE 'E_[A-Z_]+' | head -1)
    case "$code" in
        E_SECRET_DETECTED) echo "secret refused"; exit 1 ;;
        E_DB_LOCKED)       echo "retry in 2s"; sleep 2; exec "$0" "$@" ;;
        E_READ_ONLY)       echo "read-only mode"; exit 0 ;;
        *)                 echo "unexpected: $code"; exit 1 ;;
    esac
fi
```

The Go API also exposes typed errors via `errcodes.As(err, &cli.CLIError{})` and a `Code()` method — see `saas/pkg/aicoder/errcodes/error.go`.
