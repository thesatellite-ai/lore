// Package errcodes is the canonical registry of stable error codes for
// lore. Every CLI error path produces a CLIError with a code from
// this registry. Codes are stable across minor versions and documented in
// docs/errors.md (TBD).
//
// Catches: R29 #48-50 (every error has registered code + actionable message + doc URL).
//
// Usage:
//
//	if errors.Is(err, sql.ErrNoRows) {
//	    return errcodes.New(errcodes.NotFound, "memory M-12 not found").
//	        WithHint("run `aicoder memory list` to see all memories")
//	}
//
// Output formats:
//
//	plain  → "[E_NOT_FOUND] memory M-12 not found
//	          Hint: run `aicoder memory list` to see all memories"
//	json   → {"error":{"code":"E_NOT_FOUND","message":"...","hint":"..."}}
package errcodes

// Code is a stable identifier for an error class. Format: E_<UPPER_SNAKE>.
// Codes are added freely (additive minor); removal = breaking major.
type Code string

// Registered error codes. Add new codes at the end; never reorder; never remove
// (deprecate via doc + retain).
const (
	// ── DB / storage ────────────────────────────────────────────────
	DBLocked              Code = "E_DB_LOCKED"
	DBCorrupt             Code = "E_DB_CORRUPT"
	DBNotFound            Code = "E_DB_NOT_FOUND"
	SchemaVersionMismatch Code = "E_SCHEMA_VERSION_MISMATCH"
	MigrationIncomplete   Code = "E_MIGRATION_INCOMPLETE"
	NetworkFS             Code = "E_NETWORK_FS"
	InodeMismatch         Code = "E_INODE_MISMATCH"
	DiskFull              Code = "E_DISK_FULL"
	ReadOnlyFS            Code = "E_READ_ONLY_FS"

	// ── Project / identity ──────────────────────────────────────────
	NotProjectRoot       Code = "E_NOT_PROJECT_ROOT"
	AmbiguousProject     Code = "E_AMBIGUOUS_PROJECT"
	ProjectNotFound      Code = "E_PROJECT_NOT_FOUND"
	ProjectNameCollision Code = "E_PROJECT_NAME_COLLISION"
	RepoNotFound         Code = "E_REPO_NOT_FOUND"
	MountNameTaken       Code = "E_MOUNT_NAME_TAKEN"
	IdentityFailed       Code = "E_IDENTITY_FAILED"
	BadPath              Code = "E_BAD_PATH"
	AlreadyInitialized   Code = "E_ALREADY_INITIALIZED"

	// ── Input / validation ──────────────────────────────────────────
	InvalidID         Code = "E_INVALID_ID"
	InvalidInput      Code = "E_INVALID_INPUT"
	EmptyBody         Code = "E_EMPTY_BODY"
	InvalidUTF8       Code = "E_INVALID_UTF8"
	BodyTooLarge      Code = "E_BODY_TOO_LARGE"
	InvalidIdentifier Code = "E_INVALID_IDENTIFIER"

	// ── Security / privilege ────────────────────────────────────────
	SecretDetected Code = "E_SECRET_DETECTED"
	RootRefused    Code = "E_ROOT_REFUSED"
	SymlinkDB      Code = "E_SYMLINK_DB"
	SymlinkLoop    Code = "E_SYMLINK_LOOP"
	UIDMismatch    Code = "E_UID_MISMATCH"

	// ── Concurrency ─────────────────────────────────────────────────
	LockHeld    Code = "E_LOCK_HELD"
	ReadOnly    Code = "E_READ_ONLY"
	BusyTimeout Code = "E_BUSY_TIMEOUT"

	// ── Audit ───────────────────────────────────────────────────────
	AuditChainBroken Code = "E_AUDIT_CHAIN_BROKEN"

	// ── Generic ─────────────────────────────────────────────────────
	NotFound       Code = "E_NOT_FOUND"
	Internal       Code = "E_INTERNAL"
	NotImplemented Code = "E_NOT_IMPLEMENTED"
	Unsupported    Code = "E_UNSUPPORTED"
)

// All returns every registered code. Used by `aicoder errors list` and tests.
func All() []Code {
	return []Code{
		DBLocked, DBCorrupt, DBNotFound, SchemaVersionMismatch, MigrationIncomplete,
		NetworkFS, InodeMismatch, DiskFull, ReadOnlyFS,
		NotProjectRoot, AmbiguousProject, ProjectNotFound, ProjectNameCollision,
		RepoNotFound, MountNameTaken, IdentityFailed, BadPath, AlreadyInitialized,
		InvalidID, InvalidInput, EmptyBody, InvalidUTF8, BodyTooLarge, InvalidIdentifier,
		SecretDetected, RootRefused, SymlinkDB, SymlinkLoop, UIDMismatch,
		LockHeld, ReadOnly, BusyTimeout,
		AuditChainBroken,
		NotFound, Internal, NotImplemented, Unsupported,
	}
}

// Description returns a one-line human description for documentation.
// Used by `aicoder errors list` output.
func Description(c Code) string {
	descs := map[Code]string{
		DBLocked:              "another aicoder process is holding the DB write lock",
		DBCorrupt:             "DB file failed quick_check; run `aicoder repair`",
		DBNotFound:            "DB file does not exist at the configured path",
		SchemaVersionMismatch: "DB was migrated by a newer aicoder; upgrade or use a different DB",
		MigrationIncomplete:   "a migration is in_progress; run `aicoder repair`",
		NetworkFS:             "DB path is on a network/cloud-sync filesystem (silent corruption risk)",
		InodeMismatch:         "DB file inode changed since last open; verify external mutation",
		DiskFull:              "no space left on device for write or backup",
		ReadOnlyFS:            "DB path is on a read-only filesystem",
		NotProjectRoot:        "current directory is not an aicoder project (no .lore/lore.db or lore.toml)",
		AmbiguousProject:      "both .lore/lore.db AND .lore/lore.toml present (Mode A vs Mode B ambiguity)",
		ProjectNotFound:       "project_id not registered in this DB",
		ProjectNameCollision:  "multiple projects share that name; specify by opaque ID",
		RepoNotFound:          "repo mount_name or rep_id not found in current project",
		MountNameTaken:        "another repo in this project already uses that mount_name",
		IdentityFailed:        "could not resolve actor identity; falling back to ephemeral",
		BadPath:               "path contains traversal sequence or control characters",
		AlreadyInitialized:    "this directory already contains a .lore/ — refusing to overwrite",
		InvalidID:             "id failed format validation (expected <prefix>_<32-hex-uuidv7>)",
		InvalidInput:          "input value failed validation",
		EmptyBody:             "body is empty after normalization",
		InvalidUTF8:           "input is not valid UTF-8",
		BodyTooLarge:          "body exceeds maximum allowed size",
		InvalidIdentifier:     "identifier contains disallowed character",
		SecretDetected:        "input contains a credential pattern; refusing to store. Use --allow-secrets to override (logged)",
		RootRefused:           "aicoder refuses to run as root; set MINI_ALLOW_ROOT=1 to override",
		SymlinkDB:             "DB path is a symlink; refusing for safety. Use --allow-symlink-db to override",
		SymlinkLoop:           "filesystem walk encountered a symlink loop",
		UIDMismatch:           "EUID does not match HOME owner (sudo PRESERVE_ENV detected); refusing",
		LockHeld:              "another aicoder process is holding the project flock",
		ReadOnly:              "command requires write access but read-only mode is active",
		BusyTimeout:           "DB busy timeout exceeded",
		AuditChainBroken:      "audit log hash chain integrity verification failed",
		NotFound:              "requested entity does not exist",
		Internal:              "internal error; please file a bug report with `aicoder support-bundle`",
		NotImplemented:        "feature not yet implemented (deferred to v0.2 or v1.0+)",
		Unsupported:           "operation not supported in current mode",
	}
	if d, ok := descs[c]; ok {
		return d
	}
	return string(c)
}
