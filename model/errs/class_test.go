package errs

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// allConstants lists every Code constant of this package, so a constant
// added without a table row fails the test below.
func allConstants() []Code {
	return []Code{
		DBLocked, DBCorrupt, DBNotFound, SchemaVersionMismatch, MigrationIncomplete,
		NetworkFS, InodeMismatch, DiskFull, ReadOnlyFS,
		NotProjectRoot, AmbiguousProject, ProjectNameCollision,
		MountNameTaken, IdentityFailed, BadPath, AlreadyInitialized,
		InvalidID, InvalidInput, EmptyBody, InvalidUTF8, BodyTooLarge, InvalidIdentifier,
		SecretDetected, RootRefused, SymlinkDB, SymlinkLoop, UIDMismatch,
		LockHeld, ReadOnly, BusyTimeout, SQLRefused, SQLTimeout, AuditChainBroken, SyncDataIgnored,
		NotFound, Internal, NotImplemented, Unsupported, HasDependents,
		NoWork, Contended, LostClaim, Cancelled, Reassigned, Offline, AmbiguousID,
		OrgAmbiguous, OrgDisagrees, LinkUnknown, NotAStore, LeakBlocked, Cycle, OldLore,
		ConfigInvalid,
		Stale, StoreScheme, StoreBackend, StoreUnreachable, StoreReadOnly, StoreLocked,
		Unauthorized, ResourceInvalid, ResourcePlace, ResourceName, ResourceType, ResourceKindMismatch, SchemaOld, NeedsConfirm, PolicyDenied, KindUnknown,
		MetaType, MetaAmbiguous, MigrateLocked, URLUserinfo, RepoURL,
		MetaKey, MetaReserved, MetaValue, MetaShape, MetaSize, MetaDepth, MetaOp, MetaTable, MetaRequired,
		MetaUnknown, MetaUnregistered, MetaStale, MetaInUse, MetaConflict, MetaRetired, MetaQuarantined,
		MetaInternal, MetaForeignKey, MetaStoreUnreadable,
		ResourceInUse, KindConflict, ResourceStale, ResourceInternal, ResourceQuarantined, NameTaken, MetaTaken, Purged, Refused,
		OpenChildren, ParentClosed, ParentMission, ParentProject, TreeDepth, TreeWidth, OpenItems, HasOpenChildren, Date, Recur, Template,
		DurationBad, TimeTooLong, UpgradePermission, UpgradeChecksum, UpgradePinned,
		RefSyntax, RefScheme, RefCycle, RefDepth, RefLimit, RefDangling, RefRequired, VaultEntryUnavailable,
		InTrash, ParentDeleted, ParentPurged, ReasonRequired, MigrateRollbackLoses,
		ResourceAmbiguous, ResourceTaken, ResourceArchived, ResourceBatch, ResourceConflict, KindLimit,
		ContactTaken, ContactType, RoleUnknown, StatusChange, PeriodOverlap,
		SetupNotFound, SetupAmbiguous, SetupNameTaken, SetupOrgMismatch, SetupNoPersonalStore,
		SetupStepFailed, SetupStepOrder, InviteInvalid, InviteNotFound, InviteExpired, InviteRevoked,
		InviteUsed, InviteEmail, StoreNoAccess, StoreNoWrite, FixRefused, SkillLinked,
	}
}

func TestEveryConstantRegistered(t *testing.T) {
	t.Parallel()
	consts := allConstants()
	if len(consts) != len(All()) {
		t.Fatalf("constants %d != table rows %d", len(consts), len(All()))
	}
	for _, c := range consts {
		if !c.Known() {
			t.Errorf("%s has no table row", c)
		}
	}
}

// TestExitAndHTTPMatchClass is the spec 9.5 table check: every code's exit
// and HTTP status are exactly what its class says.
func TestExitAndHTTPMatchClass(t *testing.T) {
	t.Parallel()
	wantHTTP := map[Class]int{
		ClassGeneric: 500, ClassCoordination: 409, ClassGuard: 403, ClassBadInput: 400,
		ClassAmbiguous: 409, ClassDefinition: 422, ClassRefused: 409, ClassMigrate: 409,
		ClassUpgrade: 426, ClassNeedsConfirmation: 428, ClassNotFound: 404, ClassUnauthorized: 401,
	}
	wantExit := map[Class]int{
		ClassGeneric: 1, ClassGuard: 19, ClassBadInput: 20, ClassAmbiguous: 21,
		ClassDefinition: 22, ClassRefused: 23, ClassMigrate: 24, ClassUpgrade: 25,
		ClassNeedsConfirmation: 26, ClassNotFound: 27, ClassUnauthorized: 28,
	}
	for _, c := range Classes() {
		if _, ok := wantHTTP[c]; !ok {
			t.Errorf("class %s missing from the test table", c)
		}
	}
	for _, d := range All() {
		if !d.Class.Valid() {
			t.Errorf("%s: invalid class %q", d.Code, d.Class)
			continue
		}
		if d.Class == ClassCoordination {
			if d.Exit < ExitCoordinationMin || d.Exit > ExitCoordinationMax {
				t.Errorf("%s: coordination exit %d outside 10-18", d.Code, d.Exit)
			}
			want := http.StatusConflict
			if d.Exit == ExitOffline {
				want = http.StatusServiceUnavailable
			}
			if d.HTTP != want {
				t.Errorf("%s: http %d want %d", d.Code, d.HTTP, want)
			}
			continue
		}
		if d.Exit != wantExit[d.Class] {
			t.Errorf("%s: exit %d want %d (class %s)", d.Code, d.Exit, wantExit[d.Class], d.Class)
		}
		if d.HTTP != wantHTTP[d.Class] {
			t.Errorf("%s: http %d want %d (class %s)", d.Code, d.HTTP, wantHTTP[d.Class], d.Class)
		}
	}
}

// TestPinnedNumbers pins the numbers other tools already depend on.
func TestPinnedNumbers(t *testing.T) {
	t.Parallel()
	for code, want := range map[Code][2]int{
		NotFound: {27, 404}, Offline: {15, 503}, StoreUnreachable: {15, 503},
		Unauthorized: {28, 401}, Stale: {23, 409}, StoreScheme: {20, 400},
		OldLore: {19, 403}, SchemaOld: {25, 426}, NeedsConfirm: {26, 428},
		NoWork: {10, 409}, LinkUnknown: {18, 409}, InvalidInput: {20, 400}, SecretDetected: {23, 409}, DBLocked: {23, 409}, Internal: {1, 500},
	} {
		d := DefOf(code)
		if d.Exit != want[0] || d.HTTP != want[1] {
			t.Errorf("%s: exit/http %d/%d want %d/%d", code, d.Exit, d.HTTP, want[0], want[1])
		}
	}
}

func TestUnknownCodeFailsClosed(t *testing.T) {
	t.Parallel()
	d := DefOf("E_NOPE")
	if d.Exit != ExitGeneric || d.HTTP != 500 || d.Class != ClassGeneric {
		t.Fatalf("unknown code = %+v", d)
	}
	if _, ok := Lookup("E_NOPE"); ok {
		t.Fatal("unknown code reported known")
	}
}

func TestIsByCodeAndChain(t *testing.T) {
	t.Parallel()
	sentinel := New(Stale, "store: row changed")
	wrapped := fmt.Errorf("write mem_x: %w", New(Stale, "other text"))
	if !errors.Is(wrapped, sentinel) {
		t.Fatal("errors.Is by code failed through fmt wrap")
	}
	if errors.Is(wrapped, New(NotFound, "")) {
		t.Fatal("different codes matched")
	}
	cause := errors.New("disk")
	e := Wrap(DiskFull, "write failed", cause)
	if !errors.Is(e, cause) {
		t.Fatal("cause lost from chain")
	}
	if CodeOf(wrapped) != Stale || CodeOf(cause) != Internal || CodeOf(nil) != "" {
		t.Fatal("CodeOf wrong")
	}
	if !Has(wrapped, Stale) || Has(cause, Stale) {
		t.Fatal("Has wrong")
	}
	h := sentinel.WithHint("rerun")
	if sentinel.Hint != "" || h.Hint != "rerun" {
		t.Fatal("WithHint mutated the original")
	}
}

func TestEnvelopeJSON(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("ctx: %w", New(NeedsConfirm, "asks first").WithHint("re-run with --yes"))
	b, jerr := json.Marshal(EnvelopeOf(err))
	if jerr != nil {
		t.Fatal(jerr)
	}
	want := `{"error":{"code":"E_NEEDS_CONFIRM","message":"asks first","hint":"re-run with --yes","exit":26,"class":"needs_confirmation","retryable":true,"needs_person":true}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	b, jerr = json.Marshal(EnvelopeOf(errors.New("boom /home/x")))
	if jerr != nil {
		t.Fatal(jerr)
	}
	if string(b) != `{"error":{"code":"E_INTERNAL","message":"internal error","exit":1,"class":"generic","retryable":false,"needs_person":false}}` {
		t.Fatalf("foreign error envelope = %s", b)
	}
}

// TestOnlyInternalIsGeneric enforces DOCS O3: no input or state error may
// answer HTTP 500, so the generic class holds E_INTERNAL alone.
func TestOnlyInternalIsGeneric(t *testing.T) {
	t.Parallel()
	for _, d := range All() {
		if d.Class == ClassGeneric && d.Code != Internal {
			t.Errorf("%s is in class generic; pick bad_input or refused", d.Code)
		}
	}
	if DefOf(Internal).Class != ClassGeneric {
		t.Error("E_INTERNAL must stay generic")
	}
}

// TestPurgedRow pins E_PURGED to its CLI_REFERENCE 5.5 row: refused, exit
// 23, HTTP 409 (DELETION_DESIGN 3.5: purged rows never come back).
func TestPurgedRow(t *testing.T) {
	t.Parallel()
	d, ok := Lookup(Purged)
	if !ok || d.Code != "E_PURGED" || d.Class != ClassRefused || d.Exit != 23 || d.HTTP != http.StatusConflict {
		t.Fatalf("E_PURGED = %+v (ok %v), want refused/23/409", d, ok)
	}
}
