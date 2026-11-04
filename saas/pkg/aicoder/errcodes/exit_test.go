package errcodes

import (
	"testing"

	coreerrs "github.com/thesatellite-ai/lore/model/errs"
)

// The exit statuses are a contract with agents and scripts (LORE_TEAM_SPEC
// §1 "Error codes"): this table is the spec's table, checked against the
// spec by hand, not copied from the implementation.
func TestExitCode_SpecTable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code Code
		want int
	}{
		{NoWork, 10}, {Contended, 11}, {LostClaim, 12}, {Cancelled, 13}, {Reassigned, 14},
		{Offline, 15}, {AmbiguousID, 16}, {OrgAmbiguous, 17}, {OrgDisagrees, 17},
		{LinkUnknown, 18}, {NotAStore, 19}, {LeakBlocked, 19}, {Cycle, 19}, {OldLore, 19},
		// Q-R2-5: every other code exits its core class exit (CLI_REFERENCE
		// 1.5); only E_INTERNAL and unregistered codes keep the generic 1.
		{ConfigInvalid, 20}, {InvalidInput, 20}, {NotFound, 27},
		{ProjectNotFound, 27}, {RepoNotFound, 27}, // folded into E_NOT_FOUND (DOCS O4)
		{Refused, 23}, {SecretDetected, 23}, {DBLocked, 23}, {NotImplemented, 23},
		{Internal, ExitGeneric}, {Code("E_NOT_REGISTERED"), ExitGeneric},
	} {
		if got := ExitCode(tc.code); got != tc.want {
			t.Errorf("ExitCode(%s) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

// Every dedicated status belongs to a registered code; a typo'd key in the
// map would otherwise silently never fire.
func TestExitCode_OnlyRegisteredCodes(t *testing.T) {
	t.Parallel()
	registered := map[Code]bool{}
	for _, c := range All() {
		registered[c] = true
	}
	for c := range exitCodes {
		if !registered[c] {
			t.Errorf("exit status mapped for unregistered code %s", c)
		}
	}
}

// TestExitCode_ParityWithCore proves the two registries agree (Q-R2-5): every
// old code exits exactly what the core registry gives it (or the code it was
// folded into), so an old command and a core command never exit different
// numbers for the same error, and no dedicated number in exitCodes drifts
// from the core's class exit.
func TestExitCode_ParityWithCore(t *testing.T) {
	t.Parallel()
	for _, c := range All() {
		core := coreerrs.Code(c)
		if f, ok := foldedCodes[c]; ok {
			core = f
		}
		d, ok := coreerrs.Lookup(core)
		if !ok {
			t.Errorf("old code %s is not in model/errs and not folded", c)
			continue
		}
		if got := ExitCode(c); got != d.Exit {
			t.Errorf("ExitCode(%s) = %d, core %s exits %d", c, got, d.Code, d.Exit)
		}
	}
	for c, n := range exitCodes {
		if d := coreerrs.DefOf(coreerrs.Code(c)); d.Exit != n {
			t.Errorf("exitCodes[%s] = %d, core class exit is %d", c, n, d.Exit)
		}
	}
}
