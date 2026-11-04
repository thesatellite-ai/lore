package errs

import (
	"cmp"
	"slices"
)

// Code is a stable error identifier, E_<UPPER_SNAKE>. Callers branch on it;
// the message wording may change between versions, the code never does.
type Code string

// Existing codes, copied with their meaning from v0.1.17
// (saas/pkg/aicoder/errcodes/registry.go). Never reorder, never remove.
//
// Folded in 0.1.18 (owner decision DOCS O4): E_PROJECT_NOT_FOUND and
// E_REPO_NOT_FOUND are gone; a missing project or repo is E_NOT_FOUND
// (exit 27) like every other noun.
//
// Reclassified in 0.1.18 (DOCS O3): every v0.1.17 code that exited 1 is now
// bad_input (20) or refused (23) by meaning; only E_INTERNAL stays generic,
// so no input or state error can ever answer HTTP 500.
const (
	DBLocked              Code = "E_DB_LOCKED"
	DBCorrupt             Code = "E_DB_CORRUPT"
	DBNotFound            Code = "E_DB_NOT_FOUND"
	SchemaVersionMismatch Code = "E_SCHEMA_VERSION_MISMATCH"
	MigrationIncomplete   Code = "E_MIGRATION_INCOMPLETE"
	NetworkFS             Code = "E_NETWORK_FS"
	InodeMismatch         Code = "E_INODE_MISMATCH"
	DiskFull              Code = "E_DISK_FULL"
	ReadOnlyFS            Code = "E_READ_ONLY_FS"

	NotProjectRoot       Code = "E_NOT_PROJECT_ROOT"
	AmbiguousProject     Code = "E_AMBIGUOUS_PROJECT"
	ProjectNameCollision Code = "E_PROJECT_NAME_COLLISION"
	MountNameTaken       Code = "E_MOUNT_NAME_TAKEN"
	IdentityFailed       Code = "E_IDENTITY_FAILED"
	BadPath              Code = "E_BAD_PATH"
	AlreadyInitialized   Code = "E_ALREADY_INITIALIZED"

	InvalidID         Code = "E_INVALID_ID"
	InvalidInput      Code = "E_INVALID_INPUT"
	EmptyBody         Code = "E_EMPTY_BODY"
	InvalidUTF8       Code = "E_INVALID_UTF8"
	BodyTooLarge      Code = "E_BODY_TOO_LARGE"
	InvalidIdentifier Code = "E_INVALID_IDENTIFIER"

	SecretDetected Code = "E_SECRET_DETECTED"
	RootRefused    Code = "E_ROOT_REFUSED"
	SymlinkDB      Code = "E_SYMLINK_DB"
	SymlinkLoop    Code = "E_SYMLINK_LOOP"
	UIDMismatch    Code = "E_UID_MISMATCH"

	LockHeld    Code = "E_LOCK_HELD"
	ReadOnly    Code = "E_READ_ONLY"
	BusyTimeout Code = "E_BUSY_TIMEOUT"

	AuditChainBroken Code = "E_AUDIT_CHAIN_BROKEN"

	SyncDataIgnored Code = "E_SYNC_DATA_IGNORED"

	NotFound       Code = "E_NOT_FOUND"
	Internal       Code = "E_INTERNAL"
	NotImplemented Code = "E_NOT_IMPLEMENTED"
	Unsupported    Code = "E_UNSUPPORTED"
	// HasDependents refuses a delete while a restrict relation still has
	// live rows pointing at the target (the engine applies OnDelete, O2).
	HasDependents Code = "E_HAS_DEPENDENTS"
	// ResourceInUse refuses removing a kind field (or a closed value of
	// one) while resources still hold values for it (CLI_REFERENCE 3.12).
	ResourceInUse Code = "E_RESOURCE_IN_USE"
	// KindConflict is a kind definition that clashes with another level's
	// definition of the same kind (CLI_REFERENCE 1.5 class definition).
	KindConflict Code = "E_KIND_CONFLICT"
	// NameTaken refuses a create or rename onto a name a live settings row
	// of that place already uses (a var, a kind, a resource).
	NameTaken Code = "E_NAME_TAKEN"
	// MetaTaken refuses a metadata key or value create or rename onto a
	// name already taken in that place (CLI_REFERENCE 3.13).
	MetaTaken Code = "E_META_TAKEN"

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

// New codes of 0.1.18 (STORE_SERVER_SPEC 5.4 and 9.5, CLI_REFERENCE 5).
const (
	Stale            Code = "E_STALE"
	StoreScheme      Code = "E_STORE_SCHEME"
	StoreBackend     Code = "E_STORE_BACKEND"
	StoreUnreachable Code = "E_STORE_UNREACHABLE"
	StoreReadOnly    Code = "E_STORE_READ_ONLY"
	StoreLocked      Code = "E_STORE_LOCKED"
	Unauthorized     Code = "E_UNAUTHORIZED"
	ResourceInvalid  Code = "E_RESOURCE_INVALID"
	SchemaOld        Code = "E_SCHEMA_OLD"
	NeedsConfirm     Code = "E_NEEDS_CONFIRM"
	PolicyDenied     Code = "E_POLICY_DENIED"
	KindUnknown      Code = "E_KIND_UNKNOWN"
	MetaType         Code = "E_META_TYPE"
	MetaAmbiguous    Code = "E_META_AMBIGUOUS"
	MigrateLocked    Code = "E_MIGRATE_LOCKED"
	URLUserinfo      Code = "E_URL_CREDENTIALS"
)

// Def is the registry entry of one code: everything a surface needs to print
// or return it.
type Def struct {
	Code  Code
	Class Class
	// Exit is the process exit status; it always falls in the class's
	// number (or range, for coordination).
	Exit int
	// HTTP is the REST status, derived from Class and Exit.
	HTTP int
	// Description is the one-line meaning used by `lore errors list` and the
	// written error table.
	Description string
}

// entry is one hand-written row of the table; Exit is filled from the class
// unless the class is coordination.
type entry struct {
	class Class
	exit  int // only for coordination codes
	desc  string
}

// table is the single definition of every code. Read-only after init; never
// mutated, so it is not shared mutable state.
var table = map[Code]entry{
	DBLocked:              {class: ClassRefused, desc: "another lore process is holding the DB write lock"},
	DBCorrupt:             {class: ClassRefused, desc: "DB file failed quick_check; run `lore repair`"},
	DBNotFound:            {class: ClassRefused, desc: "DB file does not exist at the configured path"},
	SchemaVersionMismatch: {class: ClassRefused, desc: "DB was migrated by a newer lore; upgrade or use a different DB"},
	MigrationIncomplete:   {class: ClassRefused, desc: "a schema migration did not finish; run `lore setup`"},
	NetworkFS:             {class: ClassRefused, desc: "DB path or store is on a network/cloud-sync filesystem (silent corruption risk); set LORE_ALLOW_NETWORK_FS=1 to override"},
	InodeMismatch:         {class: ClassRefused, desc: "DB file inode changed since last open"},
	DiskFull:              {class: ClassRefused, desc: "no space left on device for write or backup"},
	ReadOnlyFS:            {class: ClassRefused, desc: "DB path is on a read-only filesystem"},
	NotProjectRoot:        {class: ClassRefused, desc: "current directory is not a lore project"},
	AmbiguousProject:      {class: ClassRefused, desc: "both .lore/lore.db and a .lore/lore.yaml pointer present"},
	ProjectNameCollision:  {class: ClassRefused, desc: "multiple projects share that name; specify by opaque ID"},
	MountNameTaken:        {class: ClassRefused, desc: "another repo in this project already uses that mount_name"},
	IdentityFailed:        {class: ClassRefused, desc: "could not resolve actor identity"},
	BadPath:               {class: ClassBadInput, desc: "path contains traversal sequence or control characters"},
	AlreadyInitialized:    {class: ClassRefused, desc: "this directory already contains a .lore/"},
	InvalidID:             {class: ClassBadInput, desc: "id failed format validation (expected <prefix>_<32-hex-uuidv7>)"},
	InvalidInput:          {class: ClassBadInput, desc: "input value failed validation"},
	EmptyBody:             {class: ClassBadInput, desc: "body is empty after normalization"},
	InvalidUTF8:           {class: ClassBadInput, desc: "input is not valid UTF-8"},
	BodyTooLarge:          {class: ClassBadInput, desc: "body exceeds maximum allowed size"},
	InvalidIdentifier:     {class: ClassBadInput, desc: "identifier contains disallowed character"},
	SecretDetected:        {class: ClassRefused, desc: "input contains a credential pattern; refusing to store. When secrets.on_detect is ask, rerun with --yes once a person confirms it is not a secret (recorded in secret_allows)"},
	RootRefused:           {class: ClassRefused, desc: "lore refuses to run as root"},
	SymlinkDB:             {class: ClassRefused, desc: "DB path is a symlink; refusing for safety"},
	SymlinkLoop:           {class: ClassRefused, desc: "filesystem walk encountered a symlink loop"},
	UIDMismatch:           {class: ClassRefused, desc: "EUID does not match HOME owner; refusing"},
	LockHeld:              {class: ClassRefused, desc: "another lore process is holding the project flock"},
	ReadOnly:              {class: ClassRefused, desc: "command requires write access but read-only mode is active"},
	BusyTimeout:           {class: ClassRefused, desc: "DB busy timeout exceeded"},
	AuditChainBroken:      {class: ClassRefused, desc: "audit log hash chain integrity verification failed"},
	SyncDataIgnored:       {class: ClassRefused, desc: "git ignores files under .lore/data, so teammates would not receive them"},
	NotFound:              {class: ClassNotFound, desc: "no such row, resource, variable or other thing here"},
	Internal:              {class: ClassGeneric, desc: "internal error; please file a bug report"},
	NotImplemented:        {class: ClassRefused, desc: "feature not yet implemented"},
	Unsupported:           {class: ClassRefused, desc: "operation not supported in current mode"},
	HasDependents:         {class: ClassRefused, desc: "a delete refused while restrict relations still have live pointing rows"},
	ResourceInUse:         {class: ClassRefused, desc: "a kind field or value is still used by resources"},
	KindConflict:          {class: ClassDefinition, desc: "a kind definition clashes with another level's definition"},
	NameTaken:             {class: ClassRefused, desc: "that name is already used by a live row in this place"},
	MetaTaken:             {class: ClassRefused, desc: "that metadata key or value name is already taken in this place"},
	NoWork:                {class: ClassCoordination, exit: 10, desc: "no ready task"},
	Contended:             {class: ClassCoordination, exit: 11, desc: "every claim attempt lost a race; retry after a short wait"},
	LostClaim:             {class: ClassCoordination, exit: 12, desc: "your claim is no longer held; stop work on this task"},
	Cancelled:             {class: ClassCoordination, exit: 13, desc: "a person cancelled the task while you held it"},
	Reassigned:            {class: ClassCoordination, exit: 14, desc: "a person reassigned the task while you held it"},
	Offline:               {class: ClassCoordination, exit: ExitOffline, desc: "the remote could not be reached; nothing was queued"},
	AmbiguousID:           {class: ClassCoordination, exit: 16, desc: "the id exists in two loaded sources; name one with --from"},
	OrgAmbiguous:          {class: ClassCoordination, exit: 17, desc: "two org entries match this repo at the same rule"},
	OrgDisagrees:          {class: ClassCoordination, exit: 17, desc: "the selected org differs from the store link"},
	LinkUnknown:           {class: ClassCoordination, exit: 18, desc: "lore cannot tell whether this repo is a client repo"},
	NotAStore:             {class: ClassGuard, desc: "the repo or url is not a lore store"},
	LeakBlocked:           {class: ClassGuard, desc: "the write would put internal content where the client can read it"},
	Cycle:                 {class: ClassGuard, desc: "the write would create a cycle"},
	OldLore:               {class: ClassGuard, desc: "this lore binary is too old for the data; writes refused, reads work"},
	ConfigInvalid:         {class: ClassBadInput, desc: "a config file failed validation"},

	Stale:            {class: ClassRefused, desc: "the row changed between read and write and could not be merged automatically"},
	StoreScheme:      {class: ClassBadInput, desc: "a store url uses a scheme no backend is registered for"},
	StoreBackend:     {class: ClassRefused, desc: "a store's filler and backend do not fit"},
	StoreUnreachable: {class: ClassCoordination, exit: ExitOffline, desc: "a store with no local copy could not be reached"},
	StoreReadOnly:    {class: ClassRefused, desc: "a write to a store attached read-only"},
	StoreLocked:      {class: ClassRefused, desc: "the store lock stayed held past store.lock_timeout"},
	Unauthorized:     {class: ClassUnauthorized, desc: "the request has no valid token"},
	ResourceInvalid:  {class: ClassBadInput, desc: "a value or field does not fit the kind"},
	SchemaOld:        {class: ClassUpgrade, desc: "the local database is older than this binary"},
	NeedsConfirm:     {class: ClassNeedsConfirmation, desc: "the command asks first and ran with no terminal and no --yes"},
	PolicyDenied:     {class: ClassRefused, desc: "a team policy refuses this"},
	KindUnknown:      {class: ClassDefinition, desc: "a kind not in this place's view"},
	MetaType:         {class: ClassBadInput, desc: "a value does not parse as the key's type"},
	MetaAmbiguous:    {class: ClassAmbiguous, desc: "a bare name matches definitions at two or more levels"},
	MigrateLocked:    {class: ClassMigrate, desc: "the team migration lock is held elsewhere"},
	URLUserinfo:      {class: ClassBadInput, desc: "a URL value holds credentials"},
}

// Lookup returns the registry entry of c. An unregistered code is reported
// as not ok; callers that must still answer (printing a foreign error) use
// DefOf, which falls back to the generic class.
func Lookup(c Code) (Def, bool) {
	e, ok := table[c]
	if !ok {
		return Def{}, false
	}
	exit := e.exit
	if e.class != ClassCoordination {
		exit = classExit(e.class)
	}
	return Def{Code: c, Class: e.class, Exit: exit, HTTP: HTTPFor(e.class, exit), Description: e.desc}, true
}

// DefOf is Lookup with a fail-closed fallback: an unregistered code is
// treated as generic (exit 1, HTTP 500), so it can never look like success.
func DefOf(c Code) Def {
	if d, ok := Lookup(c); ok {
		return d
	}
	return Def{Code: c, Class: ClassGeneric, Exit: ExitGeneric, HTTP: HTTPFor(ClassGeneric, ExitGeneric), Description: string(c)}
}

// All returns every registered code's Def, sorted by code, for docs and tests.
func All() []Def {
	out := make([]Def, 0, len(table))
	for c := range table {
		d, _ := Lookup(c) // c comes from table, so Lookup always succeeds
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Def) int { return cmp.Compare(a.Code, b.Code) })
	return out
}

// Known reports whether c is registered.
func (c Code) Known() bool {
	_, ok := table[c]
	return ok
}
