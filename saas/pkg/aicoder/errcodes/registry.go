// Package errcodes is the canonical registry of stable error codes for
// lore. Every CLI error path produces a CLIError with a code from
// this registry. Codes are stable across minor versions and documented in
// docs/reference/errors.md.
//
// Catches: R29 #48-50 (every error has registered code + actionable message + doc URL).
//
// Usage:
//
//	if errors.Is(err, sql.ErrNoRows) {
//	    return errcodes.New(errcodes.NotFound, "memory M-12 not found").
//	        WithHint("run `lore memory list` to see all memories")
//	}
//
// Output formats:
//
//	plain  → "[E_NOT_FOUND] memory M-12 not found
//	          Hint: run `lore memory list` to see all memories"
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

	// ── Git sync ────────────────────────────────────────────────────
	SyncDataIgnored Code = "E_SYNC_DATA_IGNORED"

	// ── Generic ─────────────────────────────────────────────────────
	NotFound       Code = "E_NOT_FOUND"
	Internal       Code = "E_INTERNAL"
	NotImplemented Code = "E_NOT_IMPLEMENTED"
	Unsupported    Code = "E_UNSUPPORTED"

	// ── Lore for teams (LORE_TEAM_SPEC §1 "Error codes") ────────────
	// Coordination and org codes carry their own process exit status
	// (see ExitCode) so agents and scripts can branch without parsing text.
	NoWork        Code = "E_NO_WORK"
	Contended     Code = "E_CONTENDED"
	LostClaim     Code = "E_LOST_CLAIM"
	Cancelled     Code = "E_CANCELLED"
	Reassigned    Code = "E_REASSIGNED"
	Offline       Code = "E_OFFLINE"
	AmbiguousID   Code = "E_AMBIGUOUS_ID"
	OrgAmbiguous  Code = "E_ORG_AMBIGUOUS"
	OrgDisagrees  Code = "E_ORG_DISAGREES"
	LinkUnknown   Code = "E_LINK_UNKNOWN"
	NotAStore     Code = "E_NOT_A_STORE"
	LeakBlocked   Code = "E_LEAK_BLOCKED"
	Cycle         Code = "E_CYCLE"
	OldLore       Code = "E_OLD_LORE"
	ConfigInvalid Code = "E_CONFIG_INVALID"
)

// All returns every registered code. Used by `lore errors list` and tests.
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
		SyncDataIgnored,
		NotFound, Internal, NotImplemented, Unsupported,
		NoWork, Contended, LostClaim, Cancelled, Reassigned, Offline, AmbiguousID,
		OrgAmbiguous, OrgDisagrees, LinkUnknown, NotAStore, LeakBlocked, Cycle, OldLore,
		ConfigInvalid,
	}
}

// ExitGeneric is the process exit status of every error without a
// dedicated one: the CLI's historical "something failed".
const ExitGeneric = 1

// exitCodes maps the codes that have their own process exit status
// (LORE_TEAM_SPEC §1 "Error codes"). Agents loop on `lore task next` and
// must tell "nothing to do" (10) from "lost a race" (11) from "you were
// fenced out" (12–14) without parsing text, so these numbers are a
// contract: never renumber, only add. Codes absent here exit ExitGeneric.
// ds:def id=team-exit-codes-vt2j5m98 owner=@khanakia stability=stable desc="exit statuses of the team error codes"
var exitCodes = map[Code]int{
	NoWork:       10,
	Contended:    11,
	LostClaim:    12,
	Cancelled:    13,
	Reassigned:   14,
	Offline:      15,
	AmbiguousID:  16,
	OrgAmbiguous: 17,
	OrgDisagrees: 17,
	LinkUnknown:  18,
	NotAStore:    19,
	LeakBlocked:  19,
	Cycle:        19,
	OldLore:      19,
}

// ExitCode returns the process exit status for code c.
func ExitCode(c Code) int {
	if n, ok := exitCodes[c]; ok {
		return n
	}
	return ExitGeneric
}

// Description returns a one-line human description for documentation.
// Used by `lore errors list` output.
func Description(c Code) string {
	descs := map[Code]string{
		DBLocked:              "another lore process is holding the DB write lock",
		DBCorrupt:             "DB file failed quick_check; run `lore repair`",
		DBNotFound:            "DB file does not exist at the configured path",
		SchemaVersionMismatch: "DB was migrated by a newer lore; upgrade or use a different DB",
		MigrationIncomplete:   "a schema migration did not finish; run `lore setup`",
		NetworkFS:             "DB path is on a network/cloud-sync filesystem (silent corruption risk); set LORE_ALLOW_NETWORK_FS=1 to override",
		InodeMismatch:         "DB file inode changed since last open; verify external mutation",
		DiskFull:              "no space left on device for write or backup",
		ReadOnlyFS:            "DB path is on a read-only filesystem",
		NotProjectRoot:        "current directory is not a lore project (no .lore/lore.db or lore.yaml)",
		AmbiguousProject:      "both .lore/lore.db AND a .lore/lore.yaml (or lore.toml) pointer present (Mode A vs Mode B ambiguity)",
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
		RootRefused:           "lore refuses to run as root; set LORE_ALLOW_ROOT=1 to override",
		SymlinkDB:             "DB path is a symlink; refusing for safety. Delete the symlink and re-init, or restore lore.db from a backup",
		SymlinkLoop:           "filesystem walk encountered a symlink loop",
		UIDMismatch:           "EUID does not match HOME owner (sudo PRESERVE_ENV detected); refusing",
		LockHeld:              "another lore process is holding the project flock",
		ReadOnly:              "command requires write access but read-only mode is active",
		BusyTimeout:           "DB busy timeout exceeded",
		AuditChainBroken:      "audit log hash chain integrity verification failed",
		SyncDataIgnored:       "git ignores files under .lore/data, so teammates would not receive them; re-include the folder in .gitignore",
		NotFound:              "requested entity does not exist",
		Internal:              "internal error; please file a bug report with `lore support-bundle`",
		NotImplemented:        "feature not yet implemented (deferred to v0.2 or v1.0+)",
		Unsupported:           "operation not supported in current mode",
		NoWork:                "`lore task next` found no ready task; the message lists why each candidate is blocked",
		Contended:             "ready work exists but every claim attempt lost a race; retry after a short wait",
		LostClaim:             "your claim token is no longer on the signals ref (lease expired or reclaimed); stop work on this task",
		Cancelled:             "a human cancelled the task while you held it; stop work on this task",
		Reassigned:            "a human reassigned the task while you held it; stop work on this task",
		Offline:               "a coordination write (claim, renew, release, done, approve, hold) could not be confirmed by the remote; nothing was queued",
		AmbiguousID:           "the id exists in two loaded sources; name one with --from",
		OrgAmbiguous:          "two org entries match this repo at the same rule; choose one with `lore org use <entry>`",
		OrgDisagrees:          "LORE_ORG or `lore org use` names a different org than the store link; pass --force-org to override",
		LinkUnknown:           "lore cannot tell whether this repo is a client repo (store unreachable, no cached link index); pass --to or retry online",
		NotAStore:             "the repo is not a lore store (no lore-store.yaml marker, or it holds non-lore files)",
		LeakBlocked:           "the write would put internal content where the client can read it; see `lore check leaks --explain`",
		Cycle:                 "the dependency would create a cycle between tasks",
		OldLore:               "this lore binary is below the live ref's min_lore_version; upgrade lore (reads still work)",
		ConfigInvalid:         "~/.lore/config.yaml failed validation; run `lore config check`",
	}
	if d, ok := descs[c]; ok {
		return d
	}
	return string(c)
}
