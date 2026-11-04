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
// bad_input (20) or refused (23) by meaning; only E_INTERNAL stays generic
// (O7-O9: it marks real bugs only), so no input or state error can ever
// answer HTTP 500. E_NOT_FOUND is class not_found, exit 27 (RES-OQ5,
// BATCH 7).
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
	SQLRefused  Code = "E_SQL_REFUSED"
	SQLTimeout  Code = "E_SQL_TIMEOUT"

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
	// ResourceStale refuses a write that needs store kind rows that are not
	// available (store unreachable / never synced; RES 3.6).
	ResourceStale Code = "E_RESOURCE_STALE"
	// NameTaken refuses a create or rename onto a name a live settings row
	// of that place already uses (a var, a kind, a resource).
	NameTaken Code = "E_NAME_TAKEN"
	// MetaTaken refuses a metadata key or value create or rename onto a
	// name already taken in that place (CLI_REFERENCE 3.13).
	MetaTaken Code = "E_META_TAKEN"

	NoWork       Code = "E_NO_WORK"
	Contended    Code = "E_CONTENDED"
	LostClaim    Code = "E_LOST_CLAIM"
	Cancelled    Code = "E_CANCELLED"
	Reassigned   Code = "E_REASSIGNED"
	Offline      Code = "E_OFFLINE"
	AmbiguousID  Code = "E_AMBIGUOUS_ID"
	OrgAmbiguous Code = "E_ORG_AMBIGUOUS"
	OrgDisagrees Code = "E_ORG_DISAGREES"
	LinkUnknown  Code = "E_LINK_UNKNOWN"
	NotAStore    Code = "E_NOT_A_STORE"
	LeakBlocked  Code = "E_LEAK_BLOCKED"
	Cycle        Code = "E_CYCLE"
	// Task tree refusals (TASK_SYSTEM_DESIGN 6.4-6.7, 24; CLI_REFERENCE 5.6: exit 23, 409).
	OpenChildren    Code = "E_OPEN_CHILDREN"
	ParentClosed    Code = "E_PARENT_CLOSED"
	ParentMission   Code = "E_PARENT_MISSION"
	ParentProject   Code = "E_PARENT_PROJECT"
	TreeDepth       Code = "E_TREE_DEPTH"
	HasOpenChildren Code = "E_HAS_OPEN_CHILDREN"
	Date            Code = "E_DATE"
	Recur           Code = "E_RECUR"
	Template        Code = "E_TEMPLATE"
	OpenItems       Code = "E_OPEN_ITEMS"
	OldLore         Code = "E_OLD_LORE"
	ConfigInvalid   Code = "E_CONFIG_INVALID"
	// Time tracking input errors (TIME_TRACKING_DESIGN, CLI_REFERENCE 5: exit 20).
	DurationBad Code = "E_DURATION_BAD"
	TimeTooLong Code = "E_TIME_TOO_LONG"
)

// New codes of 0.1.18 (STORE_SERVER_SPEC 5.4 and 9.5, CLI_REFERENCE 5).
const (
	// lore upgrade refusals (FORMAT_EVOLUTION 8.10).
	UpgradePermission    Code = "E_UPGRADE_PERMISSION"
	UpgradeChecksum      Code = "E_UPGRADE_CHECKSUM"
	UpgradePinned        Code = "E_UPGRADE_PINNED"
	Stale                Code = "E_STALE"
	StoreScheme          Code = "E_STORE_SCHEME"
	StoreBackend         Code = "E_STORE_BACKEND"
	StoreUnreachable     Code = "E_STORE_UNREACHABLE"
	StoreReadOnly        Code = "E_STORE_READ_ONLY"
	StoreLocked          Code = "E_STORE_LOCKED"
	Unauthorized         Code = "E_UNAUTHORIZED"
	ResourceInvalid      Code = "E_RESOURCE_INVALID"
	ResourcePlace        Code = "E_RESOURCE_PLACE"
	ResourceName         Code = "E_RESOURCE_NAME"
	ResourceType         Code = "E_RESOURCE_TYPE"
	ResourceKindMismatch Code = "E_RESOURCE_KIND_MISMATCH"
	SchemaOld            Code = "E_SCHEMA_OLD"
	NeedsConfirm         Code = "E_NEEDS_CONFIRM"
	PolicyDenied         Code = "E_POLICY_DENIED"
	KindUnknown          Code = "E_KIND_UNKNOWN"
	MetaType             Code = "E_META_TYPE"
	MetaAmbiguous        Code = "E_META_AMBIGUOUS"
	// The rest of the metadata set (METADATA_DESIGN 6, CLI_REFERENCE 5).
	MetaKey             Code = "E_META_KEY"
	MetaReserved        Code = "E_META_RESERVED"
	MetaValue           Code = "E_META_VALUE"
	MetaShape           Code = "E_META_SHAPE"
	MetaSize            Code = "E_META_SIZE"
	MetaDepth           Code = "E_META_DEPTH"
	MetaOp              Code = "E_META_OP"
	MetaTable           Code = "E_META_TABLE"
	MetaRequired        Code = "E_META_REQUIRED"
	MetaUnknown         Code = "E_META_UNKNOWN"
	MetaUnregistered    Code = "E_META_UNREGISTERED"
	MetaStale           Code = "E_META_STALE"
	MetaInUse           Code = "E_META_IN_USE"
	MetaConflict        Code = "E_META_CONFLICT"
	MetaRetired         Code = "E_META_RETIRED"
	MetaQuarantined     Code = "E_META_QUARANTINED"
	MetaInternal        Code = "E_META_INTERNAL"
	MetaForeignKey      Code = "E_META_FOREIGN_KEY"
	MetaStoreUnreadable Code = "E_META_STORE_UNREADABLE"
	MigrateLocked       Code = "E_MIGRATE_LOCKED"
	// FORMAT_EVOLUTION rule 4, FQ5.
	MigrateRollbackLoses Code = "E_MIGRATE_ROLLBACK_LOSES"
	URLUserinfo          Code = "E_URL_CREDENTIALS"
	// Purged refuses any write that would bring a purged id back as a live
	// row (update, create with the id, move, merge, restore): purged rows
	// never come back (DELETION_DESIGN 3.5, DQ4; CLI_REFERENCE 5.5).
	Purged Code = "E_PURGED"
	// Refused is a refusal by a fixed lore rule that no other code names,
	// with the rule and the way out in the message. BATCH2 #8: `lore actor
	// delete` answers it naming `lore actor deactivate`, because actors are
	// never deleted (DELETION 4.1: every *_by field points at them).
	Refused Code = "E_REFUSED"
	// Trash refusals (DELETION_DESIGN 3.2-3.4, 4.2; CLI_REFERENCE 5.5: exit 23/20, 409/400).
	InTrash        Code = "E_IN_TRASH"
	ParentDeleted  Code = "E_PARENT_DELETED"
	ParentPurged   Code = "E_PARENT_PURGED"
	ReasonRequired Code = "E_REASON_REQUIRED"
	// Reference errors (SECRETS_AND_REFERENCES 5.1, 8.1-8.6).
	RefSyntax   Code = "E_REF_SYNTAX"
	RefScheme   Code = "E_REF_SCHEME"
	RefCycle    Code = "E_REF_CYCLE"
	RefDepth    Code = "E_REF_DEPTH"
	RefLimit    Code = "E_REF_LIMIT"
	RefDangling Code = "E_REF_DANGLING"
	RefRequired Code = "E_REF_REQUIRED"
	// SecretUnavailable: a secret manager could not give the entry (SQ14/D20).
	SecretUnavailable Code = "E_SECRET_UNAVAILABLE"
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
	SQLRefused:            {class: ClassBadInput, desc: "lore sql refused the statement: not one read-only SELECT/WITH/VALUES, or a denied name"},
	SQLTimeout:            {class: ClassRefused, desc: "lore sql ran past --timeout"},
	AuditChainBroken:      {class: ClassRefused, desc: "audit log hash chain integrity verification failed"},
	SyncDataIgnored:       {class: ClassRefused, desc: "git ignores files under .lore/data, so teammates would not receive them"},
	NotFound:              {class: ClassNotFound, desc: "no such row, resource, variable or other thing here"},
	Internal:              {class: ClassGeneric, desc: "internal error; please file a bug report"},
	NotImplemented:        {class: ClassRefused, desc: "feature not yet implemented"},
	Unsupported:           {class: ClassRefused, desc: "operation not supported in current mode"},
	HasDependents:         {class: ClassRefused, desc: "a delete refused while restrict relations still have live pointing rows"},
	ResourceInUse:         {class: ClassRefused, desc: "a kind field or value is still used by resources"},
	KindConflict:          {class: ClassDefinition, desc: "a kind definition clashes with another level's definition"},
	ResourceStale:         {class: ClassDefinition, desc: "store kind rows needed for this write are not available; run lore sync or write with --to repo"},
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
	OpenChildren:          {class: ClassRefused, desc: "done or cancel of a parent task with open sub-tasks without --children"},
	ParentClosed:          {class: ClassRefused, desc: "a sub-task added or reopened under a closed parent without --reopen-parent"},
	ParentMission:         {class: ClassRefused, desc: "a sub-task's mission differs from its parent's"},
	ParentProject:         {class: ClassRefused, desc: "the parent task is in another project"},
	TreeDepth:             {class: ClassRefused, desc: "the sub-task tree would be deeper than tasks.max_depth"},
	HasOpenChildren:       {class: ClassRefused, desc: "claim of a parent task with open sub-tasks without --coordinate"},
	Date:                  {class: ClassBadInput, desc: "a date phrase lore does not understand or that is ambiguous; give e.g. 2026-11-13, tomorrow, next mon, in 3 days"},
	Recur:                 {class: ClassBadInput, desc: "a recurrence rule lore does not understand; give e.g. every week, every month on last, every! 3 days, … until 2027-03-31"},
	Template:              {class: ClassRefused, desc: "a task template is invalid or its arguments are missing or unknown"},
	OpenItems:             {class: ClassRefused, desc: "done of a task with unticked checklist items without --tick-all or --force (TSQ14)"},
	DurationBad:           {class: ClassBadInput, desc: "a duration lore does not accept (0, negative, a day unit); give e.g. 45m, 1h30m, 1.5h"},
	TimeTooLong:           {class: ClassBadInput, desc: "a manual time entry over time.max_entry (default 24h); split it into one entry per day"},
	UpgradePermission:     {class: ClassUpgrade, desc: "the folder of the lore binary is not writable; run `sudo lore upgrade`"},
	UpgradeChecksum:       {class: ClassUpgrade, desc: "the downloaded release is short or its sha256 does not match checksums.txt; run `lore upgrade` again"},
	UpgradePinned:         {class: ClassUpgrade, desc: "CI without a pin, or --version against the team pin without --ignore-pin; set LORE_VERSION_PIN, lore_version_pin or pass --version"},
	OldLore:               {class: ClassGuard, desc: "this lore binary is below the project's min_lore_version or does not know one of its features; run lore upgrade (reads still work)"},
	ConfigInvalid:         {class: ClassBadInput, desc: "a config file failed validation"},

	Stale:                {class: ClassRefused, desc: "the row changed between read and write and could not be merged automatically"},
	StoreScheme:          {class: ClassBadInput, desc: "a store url uses a scheme no backend is registered for"},
	StoreBackend:         {class: ClassRefused, desc: "a store's filler and backend do not fit"},
	StoreUnreachable:     {class: ClassCoordination, exit: ExitOffline, desc: "a store with no local copy could not be reached"},
	StoreReadOnly:        {class: ClassRefused, desc: "a write to a store attached read-only"},
	StoreLocked:          {class: ClassRefused, desc: "the store lock stayed held past store.lock_timeout"},
	Unauthorized:         {class: ClassUnauthorized, desc: "the request has no valid token"},
	ResourceInvalid:      {class: ClassBadInput, desc: "a value or field does not fit the kind"},
	ResourcePlace:        {class: ClassRefused, desc: "the place is not allowed by resources.allowed_in, or --to me (RQ2)"},
	ResourceName:         {class: ClassBadInput, desc: "a kind, field or resource name breaks its format, is reserved, or looks like a credential (RES 3.1)"},
	ResourceType:         {class: ClassBadInput, desc: "field retype found values that do not fit the new type (RES 3.14)"},
	ResourceKindMismatch: {class: ClassBadInput, desc: "merge across resource kinds (RES 3.6)"},
	SchemaOld:            {class: ClassUpgrade, desc: "the local database is older than this binary"},
	NeedsConfirm:         {class: ClassNeedsConfirmation, desc: "the command asks first and ran with no terminal and no --yes"},
	PolicyDenied:         {class: ClassRefused, desc: "a team policy refuses this"},
	KindUnknown:          {class: ClassDefinition, desc: "a kind not in this place's view"},
	MetaType:             {class: ClassBadInput, desc: "a value does not parse as the key's type"},
	MetaAmbiguous:        {class: ClassAmbiguous, desc: "a bare name matches definitions at two or more levels"},
	MetaKey:              {class: ClassBadInput, desc: "a key or allowed-value name is not snake_case ASCII, too long or too deep"},
	MetaReserved:         {class: ClassBadInput, desc: "a key name is a column, type word, level word, id, meta, _*, mky* or val*"},
	MetaValue:            {class: ClassBadInput, desc: "an empty value, null, [] or {}, or a value outside a key's allowed values"},
	MetaShape:            {class: ClassBadInput, desc: "a list of objects, a list of lists or a mixed list on a structured type"},
	MetaSize:             {class: ClassBadInput, desc: "the row's metadata would pass meta.max_bytes"},
	MetaDepth:            {class: ClassBadInput, desc: "a key has more dot segments than meta.max_depth"},
	MetaOp:               {class: ClassBadInput, desc: "a metadata grammar or filter error"},
	MetaTable:            {class: ClassBadInput, desc: "metadata on a table that does not accept it from the CLI"},
	MetaRequired:         {class: ClassBadInput, desc: "a required metadata key is missing on add"},
	MetaUnknown:          {class: ClassBadInput, desc: "unset of a key the row does not have, or an unknown key id"},
	MetaUnregistered:     {class: ClassDefinition, desc: "a write to a key whose definition is not visible here"},
	MetaStale:            {class: ClassDefinition, desc: "auto-registration while the store registries are missing or stale"},
	MetaInUse:            {class: ClassRefused, desc: "a key or allowed value some row still uses"},
	MetaConflict:         {class: ClassRefused, desc: "a write by a bare name while that name has an unsettled duplicate"},
	MetaRetired:          {class: ClassRefused, desc: "--merge-into onto a retired key"},
	MetaQuarantined:      {class: ClassRefused, desc: "the row's metadata is quarantined"},
	MetaInternal:         {class: ClassGuard, desc: "an internal key name or value would reach a client repo"},
	MetaForeignKey:       {class: ClassGuard, desc: "a client-repo row carries a key id or scope label from outside its registry"},
	MetaStoreUnreadable:  {class: ClassGuard, desc: "a client-repo key registration while the store's key rows cannot be read"},
	MigrateLocked:        {class: ClassMigrate, desc: "the team migration lock is held elsewhere"},
	MigrateRollbackLoses: {class: ClassMigrate, desc: "rolling back the migration would lose changes made after it; nothing was changed"},
	URLUserinfo:          {class: ClassBadInput, desc: "a URL value holds credentials"},
	Purged:               {class: ClassRefused, desc: "the row was purged; only a tombstone is left"},
	Refused:              {class: ClassRefused, desc: "a fixed lore rule refuses this; the message names the rule and what to do instead"},
	InTrash:              {class: ClassRefused, desc: "an edit or delete of a row that is already in the trash"},
	ParentDeleted:        {class: ClassRefused, desc: "restore of a child whose parent is in the trash"},
	ParentPurged:         {class: ClassRefused, desc: "restore of a child whose parent was purged"},
	ReasonRequired:       {class: ClassBadInput, desc: "a delete without --reason while delete.require_reason is true"},
	RefSyntax:            {class: ClassBadInput, desc: "a reference is malformed: an unknown option, a reference inside a path, or more than 3 secret alternates"},
	RefScheme:            {class: ClassBadInput, desc: "a secret reference names a manager scheme lore does not know"},
	RefCycle:             {class: ClassBadInput, desc: "references refer to each other in a loop; the message prints the chain"},
	RefDepth:             {class: ClassBadInput, desc: "references nest deeper than 8"},
	RefLimit:             {class: ClassBadInput, desc: "expanding a text took more than 256 references or 64 KB of output"},
	RefDangling:          {class: ClassBadInput, desc: "the value refers to something that is not defined here; define it first or pass --allow-dangling"},
	RefRequired:          {class: ClassBadInput, desc: "a ${scheme:name:?message} reference did not resolve"},
	SecretUnavailable:    {class: ClassCoordination, exit: ExitOffline, desc: "a secret manager could not give the named entry: CLI missing, locked, not logged in, timed out, or no such entry on this machine"},
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
